package yarouter

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	controlpkg "github.com/duvu/ya-router/internal/control"
	operationpkg "github.com/duvu/ya-router/internal/operation"
	providerpkg "github.com/duvu/ya-router/internal/provider"
	runtimepkg "github.com/duvu/ya-router/internal/runtime"
	secretpkg "github.com/duvu/ya-router/internal/secret"
)

const (
	controlSocketEnv           = "YA_ROUTER_CONTROL_SOCKET"
	controlSocketModeEnv       = "YA_ROUTER_CONTROL_SOCKET_MODE"
	controlSocketGroupEnv      = "YA_ROUTER_CONTROL_SOCKET_GROUP"
	controlRemoteAddressEnv    = "YA_ROUTER_CONTROL_LISTEN_ADDRESS"
	controlTLSCertEnv          = "YA_ROUTER_CONTROL_TLS_CERT"
	controlTLSKeyEnv           = "YA_ROUTER_CONTROL_TLS_KEY"
	controlClientCAEnv         = "YA_ROUTER_CONTROL_CLIENT_CA"
	controlRequireMTLSEnv      = "YA_ROUTER_CONTROL_REQUIRE_MTLS"
	controlViewerTokenEnv      = "YA_ROUTER_CONTROL_VIEWER_TOKEN"
	controlOperatorTokenEnv    = "YA_ROUTER_CONTROL_OPERATOR_TOKEN"
	controlAdminTokenEnv       = "YA_ROUTER_CONTROL_ADMIN_TOKEN"
	controlViewerSubjectsEnv   = "YA_ROUTER_CONTROL_VIEWER_SUBJECTS"
	controlOperatorSubjectsEnv = "YA_ROUTER_CONTROL_OPERATOR_SUBJECTS"
	controlAdminSubjectsEnv    = "YA_ROUTER_CONTROL_ADMIN_SUBJECTS"
	operationStatePathEnv      = "YA_ROUTER_OPERATIONS_PATH"
)

type managedControlRuntime struct {
	service       *controlpkg.Service
	audit         *controlpkg.MemoryAuditSink
	operations    *operationpkg.Manager
	unixSocket    string
	remoteAddress string
}

func newManagedControlRuntime(config *Config, runtimeManager *runtimepkg.Manager, providerManager *providerpkg.Manager) (*managedControlRuntime, error) {
	return newManagedControlRuntimeWithSecretStore(config, runtimeManager, providerManager, nil)
}

func newManagedControlRuntimeWithSecretStore(config *Config, runtimeManager *runtimepkg.Manager, providerManager *providerpkg.Manager, secretStore *secretpkg.MemoryStore) (*managedControlRuntime, error) {
	listenerConfig, tokens, subjectRoles, err := configuredControlListener(config)
	if err != nil {
		return nil, err
	}
	operationPath, err := configuredOperationStatePath()
	if err != nil {
		return nil, err
	}
	operationManager, err := operationpkg.OpenManager(operationpkg.Options{
		Path:          operationPath,
		MaxOperations: 2048,
		MaxEvents:     8192,
		Retention:     7 * 24 * time.Hour,
		DefaultTTL:    15 * time.Minute,
	})
	if err != nil {
		return nil, fmt.Errorf("open operation manager: %w", err)
	}
	closeOperations := true
	defer func() {
		if closeOperations {
			shutdownContext, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			_ = operationManager.Close(shutdownContext)
		}
	}()
	deploymentMode := "local"
	if listenerConfig.UnixSocket == "" {
		deploymentMode = "remote"
	} else if listenerConfig.RemoteAddress != "" {
		deploymentMode = "local+remote"
	}
	audit := controlpkg.NewMemoryAuditSink(2048)
	api := controlpkg.NewAPI(controlpkg.APIOptions{
		ServiceVersion: version,
		DeploymentMode: deploymentMode,
		Features: []string{
			"auth_sessions",
			"catalog_last_known_good",
			"control_meta",
			"control_read_models",
			"idempotency",
			"local_unix_socket",
			"operation_events",
			"persistent_operations",
			"request_id",
			"restart_recovery",
			"resumable_sse",
			"role_based_access",
			"typed_errors",
			"remote_tls",
		},
		DataPlaneAPIKey: strings.TrimSpace(os.Getenv(inboundAPIKeyEnv)),
		Audit:           controlpkg.MultiAuditSink{audit, controlpkg.LogAuditSink{}},
		State: func() controlpkg.StateMeta {
			manager := currentConfigState()
			if manager == nil {
				return controlpkg.StateMeta{}
			}
			snapshot := manager.Snapshot()
			return controlpkg.StateMeta{
				Revision:        snapshot.Revision,
				RestartRequired: len(snapshot.RestartRequired) > 0,
			}
		},
	})
	if secretStore == nil {
		secretStore, err = newDaemonSecretStore(config, audit)
		if err != nil {
			return nil, err
		}
	}
	readModel := newControlReadModel(runtimeManager, providerManager, operationManager, secretStore)
	controlpkg.RegisterReadRoutes(api, readModel)
	controlpkg.RegisterRoutingStatusRoutes(api, readModel)
	controlpkg.RegisterOperationRoutes(api, operationManager, authSessionRunner{operations: operationManager, reloader: providerManager, secrets: secretStore})
	controlpkg.RegisterSecretRoutes(api, secretStore)
	controlpkg.RegisterMutationRoutes(api, mutationExecutor{reloader: providerManager})
	localIdentity := controlpkg.Identity{Subject: "local:unix-socket", Role: controlpkg.RoleAdmin, Source: "unix_socket"}
	localHandler := api.Handler(controlpkg.FixedAuthenticator(localIdentity))

	var remoteHandler http.Handler
	if listenerConfig.RemoteAddress != "" {
		authenticators := make(controlpkg.ChainAuthenticator, 0, 2)
		if len(subjectRoles) > 0 {
			authenticators = append(authenticators, controlpkg.CertificateAuthenticator{SubjectRoles: subjectRoles})
		}
		if len(tokens) > 0 && !listenerConfig.RequireMTLS {
			authenticators = append(authenticators, controlpkg.NewTokenAuthenticator(tokens))
		}
		remoteHandler = api.Handler(authenticators)
	}
	service, err := controlpkg.NewService(listenerConfig, localHandler, remoteHandler)
	if err != nil {
		return nil, err
	}
	closeOperations = false
	return &managedControlRuntime{
		service:       service,
		audit:         audit,
		operations:    operationManager,
		unixSocket:    listenerConfig.UnixSocket,
		remoteAddress: listenerConfig.RemoteAddress,
	}, nil
}

func configuredOperationStatePath() (string, error) {
	if custom := strings.TrimSpace(os.Getenv(operationStatePathEnv)); custom != "" {
		if err := os.MkdirAll(filepath.Dir(custom), 0o700); err != nil {
			return "", fmt.Errorf("create operation state directory: %w", err)
		}
		return custom, nil
	}
	configPath, err := getConfigPath()
	if err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(configPath), "operations.json"), nil
}

func configuredControlListener(config *Config) (controlpkg.ListenerConfig, map[controlpkg.Role]string, map[string]controlpkg.Role, error) {
	configPath, err := getConfigPath()
	if err != nil {
		return controlpkg.ListenerConfig{}, nil, nil, err
	}
	socket := strings.TrimSpace(os.Getenv(controlSocketEnv))
	if socket == "" {
		socket = controlSocketNextTo(configPath)
	} else if strings.EqualFold(socket, "off") || strings.EqualFold(socket, "disabled") {
		socket = ""
	}
	remoteAddress := strings.TrimSpace(os.Getenv(controlRemoteAddressEnv))
	tokens := map[controlpkg.Role]string{
		controlpkg.RoleViewer:   strings.TrimSpace(os.Getenv(controlViewerTokenEnv)),
		controlpkg.RoleOperator: strings.TrimSpace(os.Getenv(controlOperatorTokenEnv)),
		controlpkg.RoleAdmin:    strings.TrimSpace(os.Getenv(controlAdminTokenEnv)),
	}
	for role, token := range tokens {
		if token == "" {
			delete(tokens, role)
		}
	}
	dataKey := strings.TrimSpace(os.Getenv(inboundAPIKeyEnv))
	for role, token := range tokens {
		if dataKey != "" && constantTimeStringEqual(dataKey, token) {
			return controlpkg.ListenerConfig{}, nil, nil, fmt.Errorf("control %s token must differ from %s", role, inboundAPIKeyEnv)
		}
	}
	subjectRoles := make(map[string]controlpkg.Role)
	addSubjectRoles(subjectRoles, controlpkg.RoleViewer, os.Getenv(controlViewerSubjectsEnv))
	addSubjectRoles(subjectRoles, controlpkg.RoleOperator, os.Getenv(controlOperatorSubjectsEnv))
	addSubjectRoles(subjectRoles, controlpkg.RoleAdmin, os.Getenv(controlAdminSubjectsEnv))

	requireMTLS, err := parseOptionalBool(os.Getenv(controlRequireMTLSEnv))
	if err != nil {
		return controlpkg.ListenerConfig{}, nil, nil, fmt.Errorf("%s: %w", controlRequireMTLSEnv, err)
	}
	if remoteAddress != "" {
		host, _, splitErr := net.SplitHostPort(remoteAddress)
		if splitErr == nil && !isLoopbackControlHost(host) {
			requireMTLS = true
		}
	}
	socketMode := os.FileMode(0o600)
	if raw := strings.TrimSpace(os.Getenv(controlSocketModeEnv)); raw != "" {
		parsed, parseErr := strconv.ParseUint(raw, 8, 32)
		if parseErr != nil {
			return controlpkg.ListenerConfig{}, nil, nil, fmt.Errorf("%s: invalid octal mode %q: %w", controlSocketModeEnv, raw, parseErr)
		}
		socketMode = os.FileMode(parsed)
	}
	socketGroup := strings.TrimSpace(os.Getenv(controlSocketGroupEnv))
	timeouts := config.Timeouts
	listener := controlpkg.ListenerConfig{
		UnixSocket:               socket,
		UnixMode:                 socketMode,
		UnixGroup:                socketGroup,
		RemoteAddress:            remoteAddress,
		TLSCertFile:              strings.TrimSpace(os.Getenv(controlTLSCertEnv)),
		TLSKeyFile:               strings.TrimSpace(os.Getenv(controlTLSKeyEnv)),
		ClientCAFile:             strings.TrimSpace(os.Getenv(controlClientCAEnv)),
		RemoteIdentityConfigured: len(tokens) > 0 || len(subjectRoles) > 0,
		RequireMTLS:              requireMTLS,
		ReadTimeout:              durationSeconds(timeouts.ServerRead, 30),
		WriteTimeout:             durationSeconds(timeouts.ServerWrite, 300),
		IdleTimeout:              durationSeconds(timeouts.ServerIdle, 120),
	}
	if err := listener.Validate(); err != nil {
		return controlpkg.ListenerConfig{}, nil, nil, err
	}
	if listener.RequireMTLS && len(subjectRoles) == 0 {
		return controlpkg.ListenerConfig{}, nil, nil, fmt.Errorf("mTLS control listener requires at least one certificate subject-to-role mapping")
	}
	return listener, tokens, subjectRoles, nil
}

// controlSocketNextTo returns the default control Unix socket path colocated
// with the config file. The daemon listener and the ya client both derive the
// default endpoint through this helper so they stay in sync.
func controlSocketNextTo(configPath string) string {
	return filepath.Join(filepath.Dir(configPath), "control.sock")
}

func addSubjectRoles(destination map[string]controlpkg.Role, role controlpkg.Role, values string) {
	for _, value := range strings.Split(values, ",") {
		if subject := strings.TrimSpace(value); subject != "" {
			destination[subject] = role
		}
	}
}

func parseOptionalBool(value string) (bool, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return false, nil
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return false, fmt.Errorf("invalid boolean %q", value)
	}
	return parsed, nil
}

func durationSeconds(value, fallback int) time.Duration {
	if value <= 0 {
		value = fallback
	}
	return time.Duration(value) * time.Second
}

func isLoopbackControlHost(host string) bool {
	host = strings.Trim(strings.TrimSpace(host), "[]")
	if strings.EqualFold(host, "localhost") {
		return true
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback()
	}
	return false
}

func constantTimeStringEqual(left, right string) bool {
	if len(left) != len(right) {
		return false
	}
	var difference byte
	for index := range left {
		difference |= left[index] ^ right[index]
	}
	return difference == 0
}

func serveManagedServers(dataServer *http.Server, controlRuntime *managedControlRuntime) error {
	if controlRuntime == nil || controlRuntime.service == nil {
		return fmt.Errorf("control runtime is required")
	}
	if err := controlRuntime.service.Start(); err != nil {
		if controlRuntime.operations != nil {
			shutdownContext, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			_ = controlRuntime.operations.Close(shutdownContext)
		}
		return fmt.Errorf("start control service: %w", err)
	}
	dataErrors := make(chan error, 1)
	go func() {
		err := dataServer.ListenAndServe()
		if err == http.ErrServerClosed {
			err = nil
		}
		dataErrors <- err
	}()

	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(signals)

	var result error
	select {
	case <-signals:
		fmt.Println("\nGracefully shutting down...")
	case err := <-dataErrors:
		if err != nil {
			result = fmt.Errorf("data server: %w", err)
		}
	case err := <-controlRuntime.service.Errors():
		result = err
	}

	shutdownContext, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := dataServer.Shutdown(shutdownContext); err != nil && result == nil {
		result = fmt.Errorf("shutdown data server: %w", err)
	}
	if err := controlRuntime.service.Shutdown(shutdownContext); err != nil && result == nil {
		result = fmt.Errorf("shutdown control service: %w", err)
	}
	if controlRuntime.operations != nil {
		if err := controlRuntime.operations.Close(shutdownContext); err != nil && result == nil {
			result = fmt.Errorf("shutdown operation manager: %w", err)
		}
	}
	return result
}
