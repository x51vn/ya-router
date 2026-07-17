package control

import (
	"context"
	"crypto/subtle"
	"crypto/x509"
	"net/http"
	"sort"
	"strings"
)

// Role is an ordered control-plane authorization level.
type Role string

const (
	RoleViewer   Role = "viewer"
	RoleOperator Role = "operator"
	RoleAdmin    Role = "admin"
)

func (role Role) valid() bool {
	switch role {
	case RoleViewer, RoleOperator, RoleAdmin:
		return true
	default:
		return false
	}
}

func (role Role) permits(required Role) bool {
	return roleRank(role) >= roleRank(required)
}

func roleRank(role Role) int {
	switch role {
	case RoleViewer:
		return 1
	case RoleOperator:
		return 2
	case RoleAdmin:
		return 3
	default:
		return 0
	}
}

// Identity is the redacted authenticated control-plane principal.
type Identity struct {
	Subject string `json:"subject"`
	Role    Role   `json:"role"`
	Source  string `json:"source"`
}

// Authenticator authenticates a control request independently from the data
// plane. Implementations must never return credential material in Identity.
type Authenticator interface {
	Authenticate(*http.Request) (Identity, bool)
}

type AuthenticatorFunc func(*http.Request) (Identity, bool)

func (function AuthenticatorFunc) Authenticate(request *http.Request) (Identity, bool) {
	return function(request)
}

// FixedAuthenticator trusts the transport boundary, for example a protected
// local Unix socket.
func FixedAuthenticator(identity Identity) Authenticator {
	return AuthenticatorFunc(func(*http.Request) (Identity, bool) {
		return identity, identity.Subject != "" && identity.Role.valid()
	})
}

// TokenAuthenticator authenticates independent control bearer tokens. Tokens
// are write-only configuration and are not retained in audit events.
type TokenAuthenticator struct {
	tokens []tokenIdentity
}

type tokenIdentity struct {
	token    string
	identity Identity
}

func NewTokenAuthenticator(tokens map[Role]string) *TokenAuthenticator {
	roles := make([]Role, 0, len(tokens))
	for role := range tokens {
		roles = append(roles, role)
	}
	sort.Slice(roles, func(left, right int) bool { return roleRank(roles[left]) > roleRank(roles[right]) })
	authenticator := &TokenAuthenticator{}
	for _, role := range roles {
		token := strings.TrimSpace(tokens[role])
		if token == "" || !role.valid() {
			continue
		}
		authenticator.tokens = append(authenticator.tokens, tokenIdentity{
			token: token,
			identity: Identity{
				Subject: "control-token:" + string(role),
				Role:    role,
				Source:  "control_bearer",
			},
		})
	}
	return authenticator
}

func (authenticator *TokenAuthenticator) Authenticate(request *http.Request) (Identity, bool) {
	if authenticator == nil {
		return Identity{}, false
	}
	token := bearerToken(request.Header.Get("Authorization"))
	if token == "" {
		return Identity{}, false
	}
	for _, candidate := range authenticator.tokens {
		if constantTimeEqual(token, candidate.token) {
			return candidate.identity, true
		}
	}
	return Identity{}, false
}

// CertificateAuthenticator maps verified mTLS identities to roles. Mapping
// keys can be a URI SAN, the complete X.509 subject, or the Common Name.
type CertificateAuthenticator struct {
	SubjectRoles map[string]Role
}

func (authenticator CertificateAuthenticator) Authenticate(request *http.Request) (Identity, bool) {
	if request.TLS == nil || len(request.TLS.VerifiedChains) == 0 || len(request.TLS.PeerCertificates) == 0 {
		return Identity{}, false
	}
	certificate := request.TLS.PeerCertificates[0]
	for _, subject := range certificateSubjects(certificate) {
		if role, exists := authenticator.SubjectRoles[subject]; exists && role.valid() {
			return Identity{Subject: subject, Role: role, Source: "mtls"}, true
		}
	}
	return Identity{}, false
}

func certificateSubjects(certificate *x509.Certificate) []string {
	if certificate == nil {
		return nil
	}
	result := make([]string, 0, len(certificate.URIs)+2)
	for _, uri := range certificate.URIs {
		result = append(result, uri.String())
	}
	if subject := strings.TrimSpace(certificate.Subject.String()); subject != "" {
		result = append(result, subject)
	}
	if commonName := strings.TrimSpace(certificate.Subject.CommonName); commonName != "" {
		result = append(result, commonName)
	}
	return result
}

// ChainAuthenticator accepts the first successful independent authenticator.
type ChainAuthenticator []Authenticator

func (chain ChainAuthenticator) Authenticate(request *http.Request) (Identity, bool) {
	for _, authenticator := range chain {
		if authenticator == nil {
			continue
		}
		if identity, ok := authenticator.Authenticate(request); ok {
			return identity, true
		}
	}
	return Identity{}, false
}

type contextKey string

const (
	identityContextKey  contextKey = "control-identity"
	requestIDContextKey contextKey = "control-request-id"
	versionContextKey   contextKey = "control-client-version"
)

func withIdentity(request *http.Request, identity Identity) *http.Request {
	return request.WithContext(context.WithValue(request.Context(), identityContextKey, identity))
}

func IdentityFromContext(ctx context.Context) (Identity, bool) {
	identity, ok := ctx.Value(identityContextKey).(Identity)
	return identity, ok
}

func bearerToken(value string) string {
	value = strings.TrimSpace(value)
	if len(value) <= len("Bearer ") || !strings.EqualFold(value[:len("Bearer ")], "Bearer ") {
		return ""
	}
	return strings.TrimSpace(value[len("Bearer "):])
}

func constantTimeEqual(left, right string) bool {
	if len(left) != len(right) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(left), []byte(right)) == 1
}
