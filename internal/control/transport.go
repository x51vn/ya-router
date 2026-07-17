package control

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type ListenerConfig struct {
	UnixSocket               string
	UnixMode                 os.FileMode
	UnixGroup                string
	RemoteAddress            string
	TLSCertFile              string
	TLSKeyFile               string
	ClientCAFile             string
	RemoteIdentityConfigured bool
	RequireMTLS              bool
	ReadTimeout              time.Duration
	WriteTimeout             time.Duration
	IdleTimeout              time.Duration
}

func (config ListenerConfig) Validate() error {
	if strings.TrimSpace(config.UnixSocket) == "" && strings.TrimSpace(config.RemoteAddress) == "" {
		return fmt.Errorf("at least one control listener must be configured")
	}
	if config.RemoteAddress == "" {
		return nil
	}
	host, port, err := net.SplitHostPort(config.RemoteAddress)
	if err != nil {
		return fmt.Errorf("invalid control remote address %q: %w", config.RemoteAddress, err)
	}
	if port == "" {
		return fmt.Errorf("control remote address requires a port")
	}
	if _, err := strconv.Atoi(port); err != nil {
		return fmt.Errorf("invalid control remote port %q", port)
	}
	if strings.TrimSpace(config.TLSCertFile) == "" || strings.TrimSpace(config.TLSKeyFile) == "" {
		return fmt.Errorf("remote control listener requires TLS certificate and key")
	}
	if !config.RemoteIdentityConfigured {
		return fmt.Errorf("remote control listener requires an independent control identity mechanism")
	}
	if !isLoopbackHost(host) && (!config.RequireMTLS || strings.TrimSpace(config.ClientCAFile) == "") {
		return fmt.Errorf("non-loopback remote control requires mTLS with a client CA")
	}
	return nil
}

func isLoopbackHost(host string) bool {
	host = strings.Trim(strings.TrimSpace(host), "[]")
	if strings.EqualFold(host, "localhost") {
		return true
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback()
	}
	return false
}

func (config ListenerConfig) tlsConfig() (*tls.Config, error) {
	certificate, err := tls.LoadX509KeyPair(config.TLSCertFile, config.TLSKeyFile)
	if err != nil {
		return nil, fmt.Errorf("load control TLS key pair: %w", err)
	}
	tlsConfig := &tls.Config{
		MinVersion:   tls.VersionTLS12,
		Certificates: []tls.Certificate{certificate},
	}
	if config.ClientCAFile != "" {
		pem, err := os.ReadFile(config.ClientCAFile)
		if err != nil {
			return nil, fmt.Errorf("read control client CA: %w", err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("control client CA contains no valid certificates")
		}
		tlsConfig.ClientCAs = pool
		if config.RequireMTLS {
			tlsConfig.ClientAuth = tls.RequireAndVerifyClientCert
		} else {
			tlsConfig.ClientAuth = tls.VerifyClientCertIfGiven
		}
	}
	return tlsConfig, nil
}

func prepareUnixSocket(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create control socket directory: %w", err)
	}
	info, err := os.Lstat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if info.Mode()&os.ModeSocket == 0 {
		return fmt.Errorf("control socket path %q exists and is not a Unix socket", path)
	}
	connection, dialErr := net.DialTimeout("unix", path, 100*time.Millisecond)
	if dialErr == nil {
		_ = connection.Close()
		return fmt.Errorf("control socket %q is already accepting connections", path)
	}
	if err := os.Remove(path); err != nil {
		return fmt.Errorf("remove stale control socket: %w", err)
	}
	return nil
}
