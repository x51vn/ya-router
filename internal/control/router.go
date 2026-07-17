package control

import (
	"net/http"
	"strings"
	"time"
)

type StateMeta struct {
	Revision        uint64
	RestartRequired bool
}

type APIOptions struct {
	ServiceVersion  string
	DeploymentMode  string
	Features        []string
	State           func() StateMeta
	DataPlaneAPIKey string
	Audit           AuditSink
	Idempotency     *IdempotencyStore
	Now             func() time.Time
}

type route struct {
	method                  string
	path                    string
	prefix                  bool
	requiredRole            Role
	idempotent              bool
	allowUnsupportedVersion bool
	handler                 http.Handler
}

// API is the isolated versioned management router. Exact routes win over
// prefix routes and control traffic never falls through to data-plane handlers.
type API struct {
	options APIOptions
	routes  []route
}

func NewAPI(options APIOptions) *API {
	if options.ServiceVersion == "" {
		options.ServiceVersion = "dev"
	}
	if options.DeploymentMode == "" {
		options.DeploymentMode = "local"
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	if options.Idempotency == nil {
		options.Idempotency = NewIdempotencyStore(0, 0)
	}
	api := &API{options: options}
	api.routes = append(api.routes, route{
		method:                  http.MethodGet,
		path:                    "/control/v1/meta",
		requiredRole:            RoleViewer,
		allowUnsupportedVersion: true,
		handler:                 http.HandlerFunc(api.metaHandler),
	})
	return api
}

// Handle registers an exact resource route while preserving common RBAC,
// version, request-ID, audit, and idempotency behavior.
func (api *API) Handle(method, path string, requiredRole Role, idempotent bool, handler http.Handler) {
	api.routes = append(api.routes, route{
		method:       strings.ToUpper(strings.TrimSpace(method)),
		path:         path,
		requiredRole: requiredRole,
		idempotent:   idempotent,
		handler:      handler,
	})
}

// HandlePrefix registers a resource-detail prefix such as
// /control/v1/operations/. Exact routes always take precedence.
func (api *API) HandlePrefix(method, path string, requiredRole Role, idempotent bool, handler http.Handler) {
	api.routes = append(api.routes, route{
		method:       strings.ToUpper(strings.TrimSpace(method)),
		path:         path,
		prefix:       true,
		requiredRole: requiredRole,
		idempotent:   idempotent,
		handler:      handler,
	})
}

func (api *API) Handler(authenticator Authenticator) http.Handler {
	core := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		api.serveAuthenticated(writer, request)
	})
	chain := api.authenticationMiddleware(authenticator, core)
	chain = auditMiddleware(api.options.Audit, api.options.Now, chain)
	return requestIDMiddleware(chain)
}

func (api *API) authenticationMiddleware(authenticator Authenticator, next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if dataKey := strings.TrimSpace(api.options.DataPlaneAPIKey); dataKey != "" {
			credential := suppliedCredential(request)
			if credential != "" && constantTimeEqual(credential, dataKey) {
				writeError(writer, request, http.StatusUnauthorized, "data_plane_credential_rejected", "Data-plane credentials do not authorize control access.", false, nil)
				return
			}
		}
		if authenticator == nil {
			writeError(writer, request, http.StatusUnauthorized, "control_authentication_required", "Control-plane authentication is required.", false, nil)
			return
		}
		identity, ok := authenticator.Authenticate(request)
		if !ok || identity.Subject == "" || !identity.Role.valid() {
			writeError(writer, request, http.StatusUnauthorized, "invalid_control_credential", "The control-plane credential is missing or invalid.", false, nil)
			return
		}
		authenticated := withIdentity(request, identity)
		*request = *authenticated
		next.ServeHTTP(writer, request)
	})
}

func (api *API) serveAuthenticated(writer http.ResponseWriter, request *http.Request) {
	candidate, pathMatched := api.matchRoute(request.Method, request.URL.Path)
	if candidate == nil {
		if pathMatched {
			writeError(writer, request, http.StatusMethodNotAllowed, "method_not_allowed", "The HTTP method is not supported for this control resource.", false, nil)
			return
		}
		writeError(writer, request, http.StatusNotFound, "control_resource_not_found", "The control resource was not found.", false, nil)
		return
	}
	compatibility := negotiateClientVersion(request.Header.Get(ClientVersionHeader))
	request = request.WithContext(withCompatibility(request.Context(), compatibility))
	if !compatibility.Compatible && !candidate.allowUnsupportedVersion {
		writeError(writer, request, http.StatusUpgradeRequired, "unsupported_client_version", "The client version is outside the supported compatibility window.", false, map[string]any{
			"minimum": compatibility.Minimum,
			"maximum": compatibility.Maximum,
		})
		return
	}
	identity, _ := IdentityFromContext(request.Context())
	if !identity.Role.permits(candidate.requiredRole) {
		writeError(writer, request, http.StatusForbidden, "control_forbidden", "The authenticated identity does not have the required role.", false, map[string]any{
			"required_role": candidate.requiredRole,
		})
		return
	}
	handler := candidate.handler
	if candidate.idempotent {
		handler = idempotencyMiddleware(api.options.Idempotency, handler)
	}
	handler.ServeHTTP(writer, request)
}

func (api *API) matchRoute(method, path string) (*route, bool) {
	method = strings.ToUpper(method)
	var best *route
	pathMatched := false
	for index := range api.routes {
		candidate := &api.routes[index]
		matched := path == candidate.path
		if candidate.prefix {
			matched = strings.HasPrefix(path, candidate.path) && len(path) > len(candidate.path)
		}
		if !matched {
			continue
		}
		pathMatched = true
		if method != candidate.method {
			continue
		}
		if best == nil || (!candidate.prefix && best.prefix) || (candidate.prefix == best.prefix && len(candidate.path) > len(best.path)) {
			best = candidate
		}
	}
	return best, pathMatched
}
