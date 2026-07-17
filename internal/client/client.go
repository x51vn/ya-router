package client

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/duvu/ya-router/internal/control"
)

// Transport selects how the client reaches the daemon.
type Transport string

const (
	// TransportUnix uses a local Unix domain socket (the default).
	TransportUnix Transport = "unix"
	// TransportHTTPS uses a remote TLS listener, optionally with mTLS.
	TransportHTTPS Transport = "https"
)

// Profile is a named, self-contained connection configuration. Credential
// fields are references to files or independent control tokens — never provider
// secrets.
type Profile struct {
	Name      string    `json:"name"`
	Transport Transport `json:"transport"`
	// Socket is the Unix socket path for TransportUnix.
	Socket string `json:"socket,omitempty"`
	// Address is host:port for TransportHTTPS.
	Address string `json:"address,omitempty"`
	// Token is an independent control bearer token (write-only from the client
	// perspective; never logged).
	Token string `json:"token,omitempty"`
	// TLS client/CA material for HTTPS/mTLS.
	CACertFile     string `json:"ca_cert_file,omitempty"`
	ClientCertFile string `json:"client_cert_file,omitempty"`
	ClientKeyFile  string `json:"client_key_file,omitempty"`
	// ServerName overrides the TLS SNI/verification name when set.
	ServerName string `json:"server_name,omitempty"`
	// TimeoutSeconds bounds a single request; 0 uses DefaultTimeout.
	TimeoutSeconds int `json:"timeout_seconds,omitempty"`
	// MaxRetries bounds idempotent-request retries; 0 uses DefaultMaxRetries.
	MaxRetries int `json:"max_retries,omitempty"`
}

const (
	// DefaultTimeout bounds a single control request.
	DefaultTimeout = 30 * time.Second
	// DefaultMaxRetries bounds automatic retries of safe/idempotent requests.
	DefaultMaxRetries = 2
	// ClientVersion is the client protocol version advertised to the daemon.
	ClientVersion = control.CurrentClientVersion
)

// APIError is a typed control-plane error mapped from the daemon's stable error
// envelope. It carries the HTTP status and machine-readable code so callers can
// map to stable process exit codes.
type APIError struct {
	Status    int
	Code      string
	Message   string
	Retryable bool
	RequestID string
	Details   map[string]any
}

func (e *APIError) Error() string {
	if e == nil {
		return "control API error"
	}
	if e.Message != "" {
		return fmt.Sprintf("%s (%s)", e.Message, e.Code)
	}
	return fmt.Sprintf("control API error %d (%s)", e.Status, e.Code)
}

// Client is a typed Control API client. It is safe for sequential use; the
// underlying http.Client is safe for concurrent use.
type Client struct {
	profile    Profile
	baseURL    string
	http       *http.Client
	maxRetries int
	timeout    time.Duration
	now        func() time.Time
}

// New builds a client for a validated profile. It performs no network I/O.
func New(profile Profile) (*Client, error) {
	if profile.Transport == "" {
		profile.Transport = TransportUnix
	}
	timeout := DefaultTimeout
	if profile.TimeoutSeconds > 0 {
		timeout = time.Duration(profile.TimeoutSeconds) * time.Second
	}
	maxRetries := DefaultMaxRetries
	if profile.MaxRetries > 0 {
		maxRetries = profile.MaxRetries
	}

	transport := &http.Transport{
		MaxIdleConns:          4,
		IdleConnTimeout:       60 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: time.Second,
	}
	var baseURL string
	switch profile.Transport {
	case TransportUnix:
		socket := strings.TrimSpace(profile.Socket)
		if socket == "" {
			return nil, fmt.Errorf("unix transport requires a socket path")
		}
		dialer := &net.Dialer{Timeout: 10 * time.Second}
		transport.DialContext = func(ctx context.Context, _, _ string) (net.Conn, error) {
			return dialer.DialContext(ctx, "unix", socket)
		}
		// The host is ignored for a Unix socket but must be a valid URL host.
		baseURL = "http://ya-router-control"
	case TransportHTTPS:
		address := strings.TrimSpace(profile.Address)
		if address == "" {
			return nil, fmt.Errorf("https transport requires an address")
		}
		tlsConfig, err := buildTLSConfig(profile)
		if err != nil {
			return nil, err
		}
		transport.TLSClientConfig = tlsConfig
		baseURL = "https://" + address
	default:
		return nil, fmt.Errorf("unsupported transport %q", profile.Transport)
	}

	return &Client{
		profile:    profile,
		baseURL:    baseURL,
		http:       &http.Client{Transport: transport, Timeout: timeout},
		maxRetries: maxRetries,
		timeout:    timeout,
		now:        time.Now,
	}, nil
}

func buildTLSConfig(profile Profile) (*tls.Config, error) {
	config := &tls.Config{MinVersion: tls.VersionTLS12}
	if name := strings.TrimSpace(profile.ServerName); name != "" {
		config.ServerName = name
	}
	if ca := strings.TrimSpace(profile.CACertFile); ca != "" {
		pem, err := os.ReadFile(ca)
		if err != nil {
			return nil, fmt.Errorf("read control CA certificate: %w", err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("control CA certificate %q contains no valid certificates", ca)
		}
		config.RootCAs = pool
	}
	certFile := strings.TrimSpace(profile.ClientCertFile)
	keyFile := strings.TrimSpace(profile.ClientKeyFile)
	if (certFile == "") != (keyFile == "") {
		return nil, fmt.Errorf("mTLS requires both a client certificate and key")
	}
	if certFile != "" {
		pair, err := tls.LoadX509KeyPair(certFile, keyFile)
		if err != nil {
			return nil, fmt.Errorf("load control client certificate: %w", err)
		}
		config.Certificates = []tls.Certificate{pair}
	}
	return config, nil
}

// doJSON performs one request with retry for safe/idempotent operations and
// decodes a JSON response into out (which may be nil to discard the body).
func (c *Client) doJSON(ctx context.Context, method, path string, body any, out any) error {
	var encoded []byte
	if body != nil {
		var err error
		encoded, err = json.Marshal(body)
		if err != nil {
			return fmt.Errorf("encode request body: %w", err)
		}
	}

	// A non-GET (mutating) request carries a single idempotency key generated
	// once here and reused across every retry, so a retried mutation is deduped
	// by the daemon rather than applied twice. Because the key makes a retry
	// safe, mutations are retried on transport/5xx-retryable errors just like
	// reads; the daemon's idempotency store guarantees at-most-once application.
	var idempotencyKey string
	if method != http.MethodGet {
		key, err := newIdempotencyKey()
		if err != nil {
			return err
		}
		idempotencyKey = key
	}

	attempts := c.maxRetries + 1

	var lastErr error
	for attempt := 0; attempt < attempts; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(backoff(attempt)):
			}
		}
		err := c.doOnce(ctx, method, path, encoded, idempotencyKey, out)
		if err == nil {
			return nil
		}
		lastErr = err
		if !shouldRetry(err) {
			return err
		}
	}
	return lastErr
}

func (c *Client) doOnce(ctx context.Context, method, path string, encoded []byte, idempotencyKey string, out any) error {
	var reader io.Reader
	if encoded != nil {
		reader = bytes.NewReader(encoded)
	}
	request, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reader)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set(control.ClientVersionHeader, ClientVersion)
	if encoded != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if idempotencyKey != "" {
		request.Header.Set(control.IdempotencyKeyHeader, idempotencyKey)
	}
	if token := strings.TrimSpace(c.profile.Token); token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}

	response, err := c.http.Do(request)
	if err != nil {
		return &transportError{err: err}
	}
	defer response.Body.Close()

	payload, readErr := io.ReadAll(io.LimitReader(response.Body, 8<<20))
	if readErr != nil {
		return &transportError{err: readErr}
	}

	if response.StatusCode >= 400 {
		return decodeAPIError(response.StatusCode, payload)
	}
	if out == nil || len(payload) == 0 {
		return nil
	}
	if err := json.Unmarshal(payload, out); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	return nil
}

func decodeAPIError(status int, payload []byte) error {
	apiErr := &APIError{Status: status, Code: "control_error"}
	var envelope control.ErrorEnvelope
	if err := json.Unmarshal(payload, &envelope); err == nil && envelope.Error.Code != "" {
		apiErr.Code = envelope.Error.Code
		apiErr.Message = envelope.Error.Message
		apiErr.Retryable = envelope.Error.Retryable
		apiErr.RequestID = envelope.Error.RequestID
		apiErr.Details = envelope.Error.Details
	}
	return apiErr
}

// transportError marks a connection-level failure eligible for retry.
type transportError struct{ err error }

func (e *transportError) Error() string { return e.err.Error() }
func (e *transportError) Unwrap() error { return e.err }

func shouldRetry(err error) bool {
	var transport *transportError
	if errors.As(err, &transport) {
		return true
	}
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		// Retry only when the daemon explicitly marks the error retryable, and
		// never on a 4xx client/authorization/version failure.
		return apiErr.Retryable && apiErr.Status >= 500
	}
	return false
}

func backoff(attempt int) time.Duration {
	base := 100 * time.Millisecond
	d := base << (attempt - 1)
	if d > 2*time.Second {
		d = 2 * time.Second
	}
	return d
}

// escapePath escapes a single URL path segment (e.g. a resource ID).
func escapePath(value string) string { return url.PathEscape(value) }

// newIdempotencyKey returns a random key for one logical mutation. The same key
// is reused across retries so the daemon deduplicates rather than re-applies.
func newIdempotencyKey() (string, error) {
	buffer := make([]byte, 16)
	if _, err := rand.Read(buffer); err != nil {
		return "", fmt.Errorf("generate idempotency key: %w", err)
	}
	return "ya-" + hex.EncodeToString(buffer), nil
}
