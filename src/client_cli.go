// client_cli.go implements the scriptable `ya` control client (YA-TUI-09).
//
// Every command works without a TTY, supports `--json` for machine-readable
// output, and returns a stable exit code so automation can branch on outcome.
// The client only reads redacted control resources; it never handles provider
// secrets.
package yarouter

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	clientpkg "github.com/duvu/ya-router/internal/client"
)

// Stable client exit codes. Automation may depend on these values.
const (
	clientExitOK             = 0
	clientExitUsage          = 2
	clientExitConnection     = 3
	clientExitAuth           = 4
	clientExitForbidden      = 5
	clientExitNotFound       = 6
	clientExitIncompatible   = 7
	clientExitConflict       = 8
	clientExitServer         = 9
	clientExitRuntimeFailure = 1
)

// Control client environment variables. These mirror the daemon's control
// listener configuration so a local operator needs no flags for the default
// Unix-socket deployment.
const (
	clientSocketEnv  = "YA_ROUTER_CONTROL_SOCKET"
	clientAddressEnv = "YA_ROUTER_CONTROL_ADDRESS"
	clientTokenEnv   = "YA_ROUTER_CONTROL_TOKEN"
	clientCACertEnv  = "YA_ROUTER_CONTROL_CA_CERT"
	clientCertEnv    = "YA_ROUTER_CONTROL_CLIENT_CERT"
	clientKeyEnv     = "YA_ROUTER_CONTROL_CLIENT_KEY"
	clientServerName = "YA_ROUTER_CONTROL_SERVER_NAME"
)

func runClientCLI(args []string) int {
	if len(args) < 2 {
		return clientDashboardRunner(clientCommonFlags{})
	}
	switch args[1] {
	case "help", "--help", "-h":
		printClientUsage()
		return clientExitOK
	case "version":
		fmt.Printf("ya %s\n", version)
		return clientExitOK
	}

	command := args[1]
	rest := args[2:]

	switch command {
	case "dashboard":
		return runClientDashboardCommand(rest)
	case "meta", "providers", "accounts", "models", "config", "routing", "operations", "operation", "events", "secrets":
		return runClientReadCommand(command, rest)
	case "provider-enable", "provider-disable", "default-model", "default-provider",
		"allowed-models", "model-map-set", "model-map-delete":
		return runClientMutationCommand(command, rest)
	case "auth-start", "auth-cancel":
		return runClientAuthCommand(command, rest)
	case "secret-set", "secret-delete":
		return runClientSecretCommand(command, rest)
	default:
		fmt.Fprintf(os.Stderr, "ya: unknown command %q\n", command)
		printClientUsage()
		return clientExitUsage
	}
}

// clientCommonFlags holds flags shared by all read commands.
type clientCommonFlags struct {
	jsonOut bool
	socket  string
	address string
	timeout int
	refresh bool
	after   uint64
	id      string
}

func registerClientFlags(set *flag.FlagSet, common *clientCommonFlags, withRefresh, withAfter, withID bool) {
	set.BoolVar(&common.jsonOut, "json", false, "Emit machine-readable JSON output")
	set.StringVar(&common.socket, "socket", "", "Control Unix socket path (overrides env)")
	set.StringVar(&common.address, "address", "", "Control HTTPS address host:port (overrides env)")
	set.IntVar(&common.timeout, "timeout", 0, "Per-request timeout in seconds")
	if withRefresh {
		set.BoolVar(&common.refresh, "refresh", false, "Ask the daemon to refresh provider catalogs")
	}
	if withAfter {
		var after uint64Flag
		after.target = &common.after
		set.Var(&after, "after", "Return events after this cursor")
	}
	if withID {
		set.StringVar(&common.id, "id", "", "Operation ID")
	}
}

type uint64Flag struct{ target *uint64 }

func (f *uint64Flag) String() string {
	if f.target == nil {
		return "0"
	}
	return strconv.FormatUint(*f.target, 10)
}
func (f *uint64Flag) Set(value string) error {
	parsed, err := strconv.ParseUint(strings.TrimSpace(value), 10, 64)
	if err != nil {
		return fmt.Errorf("must be an unsigned integer")
	}
	*f.target = parsed
	return nil
}

func runClientReadCommand(command string, args []string) int {
	set := flag.NewFlagSet(command, flag.ContinueOnError)
	set.SetOutput(os.Stderr)
	var common clientCommonFlags
	registerClientFlags(set, &common, command == "models", command == "events", command == "operation")
	if err := set.Parse(args); err != nil {
		return clientExitUsage
	}

	if command == "operation" && strings.TrimSpace(common.id) == "" {
		fmt.Fprintln(os.Stderr, "ya: operation requires --id")
		return clientExitUsage
	}

	profile, err := resolveClientProfile(common)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ya: %v\n", err)
		return clientExitUsage
	}
	cl, err := clientpkg.New(profile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ya: %v\n", err)
		return clientExitUsage
	}

	ctx, cancel := context.WithTimeout(context.Background(), clientRequestTimeout(profile))
	defer cancel()

	result, err := dispatchClientRead(ctx, cl, command, common)
	if err != nil {
		return reportClientError(err)
	}
	if common.jsonOut {
		return emitJSON(result)
	}
	return emitText(command, result)
}

func clientRequestTimeout(profile clientpkg.Profile) time.Duration {
	if profile.TimeoutSeconds > 0 {
		// Allow a small margin over the per-request timeout for retries.
		return time.Duration(profile.TimeoutSeconds+5) * time.Second
	}
	return clientpkg.DefaultTimeout + 5*time.Second
}

func dispatchClientRead(ctx context.Context, cl *clientpkg.Client, command string, common clientCommonFlags) (any, error) {
	switch command {
	case "meta":
		return cl.Meta(ctx)
	case "providers":
		return cl.Providers(ctx)
	case "accounts":
		return cl.Accounts(ctx)
	case "models":
		return cl.Models(ctx, common.refresh)
	case "config":
		return cl.Configuration(ctx)
	case "routing":
		return cl.RoutingStatus(ctx)
	case "operations":
		return cl.Operations(ctx)
	case "operation":
		if strings.TrimSpace(common.id) == "" {
			return nil, fmt.Errorf("operation requires --id")
		}
		return cl.Operation(ctx, common.id)
	case "events":
		return cl.Events(ctx, common.after)
	case "secrets":
		return cl.Secrets(ctx)
	default:
		return nil, fmt.Errorf("unknown command %q", command)
	}
}

func resolveClientProfile(common clientCommonFlags) (clientpkg.Profile, error) {
	socket := firstNonEmpty(common.socket, os.Getenv(clientSocketEnv))
	address := firstNonEmpty(common.address, os.Getenv(clientAddressEnv))

	profile := clientpkg.Profile{
		Token:          strings.TrimSpace(os.Getenv(clientTokenEnv)),
		CACertFile:     strings.TrimSpace(os.Getenv(clientCACertEnv)),
		ClientCertFile: strings.TrimSpace(os.Getenv(clientCertEnv)),
		ClientKeyFile:  strings.TrimSpace(os.Getenv(clientKeyEnv)),
		ServerName:     strings.TrimSpace(os.Getenv(clientServerName)),
		TimeoutSeconds: common.timeout,
	}

	switch {
	case address != "":
		profile.Transport = clientpkg.TransportHTTPS
		profile.Address = address
	case socket != "":
		profile.Transport = clientpkg.TransportUnix
		profile.Socket = socket
	default:
		// Default to the daemon's default local socket next to the config file.
		defaultSocket, err := defaultControlSocketPath()
		if err != nil {
			return clientpkg.Profile{}, fmt.Errorf("no control endpoint configured: set --socket, --address, %s, or %s", clientSocketEnv, clientAddressEnv)
		}
		profile.Transport = clientpkg.TransportUnix
		profile.Socket = defaultSocket
	}
	return profile, nil
}

// defaultControlSocketPath mirrors configuredControlListener's default: a
// control.sock next to the resolved config path.
func defaultControlSocketPath() (string, error) {
	configPath, err := getConfigPath()
	if err != nil {
		return "", err
	}
	return controlSocketNextTo(configPath), nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func reportClientError(err error) int {
	var apiErr *clientpkg.APIError
	if errors.As(err, &apiErr) {
		fmt.Fprintf(os.Stderr, "ya: %s\n", apiErr.Error())
		if apiErr.RequestID != "" {
			fmt.Fprintf(os.Stderr, "     request_id=%s\n", apiErr.RequestID)
		}
		switch {
		case apiErr.Status == 401:
			return clientExitAuth
		case apiErr.Status == 403:
			return clientExitForbidden
		case apiErr.Status == 404:
			return clientExitNotFound
		case apiErr.Status == 409:
			return clientExitConflict
		case apiErr.Status == 426:
			return clientExitIncompatible
		case apiErr.Status >= 500:
			return clientExitServer
		default:
			return clientExitRuntimeFailure
		}
	}
	fmt.Fprintf(os.Stderr, "ya: %v\n", err)
	return clientExitConnection
}

func emitJSON(result any) int {
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(result); err != nil {
		fmt.Fprintf(os.Stderr, "ya: encode output: %v\n", err)
		return clientExitRuntimeFailure
	}
	return clientExitOK
}

func printClientUsage() {
	fmt.Println("ya — ya-router control client")
	fmt.Println()
	fmt.Println("Usage: ya [dashboard] [flags] | ya <command> [flags]")
	fmt.Println()
	fmt.Println("Read commands:")
	fmt.Println("  dashboard    Open the keyboard-driven daemon dashboard (default)")
	fmt.Println("  meta         Show daemon control metadata and version compatibility")
	fmt.Println("  providers    List providers and health")
	fmt.Println("  accounts     List provider accounts (redacted credential posture)")
	fmt.Println("  models       Show the model catalog (--refresh to re-fetch)")
	fmt.Println("  config       Show the revisioned configuration")
	fmt.Println("  routing      Show `thiendu` automatic-routing status")
	fmt.Println("  operations   List async operations")
	fmt.Println("  operation    Show one operation (--id)")
	fmt.Println("  events       List lifecycle events (--after cursor)")
	fmt.Println("  secrets      Show redacted credential metadata (never values)")
	fmt.Println()
	fmt.Println("Mutation commands (require --revision; support --dry-run):")
	fmt.Println("  provider-enable   --provider ID")
	fmt.Println("  provider-disable  --provider ID")
	fmt.Println("  default-model     --model ID")
	fmt.Println("  default-provider  --value ID")
	fmt.Println("  allowed-models    --provider ID --models a,b,c")
	fmt.Println("  model-map-set     --model ID --provider ID [--upstream-model ID]")
	fmt.Println("  model-map-delete  --model ID")
	fmt.Println("  secret-set        --slot ID --stdin")
	fmt.Println("  secret-delete     --slot ID")
	fmt.Println("  auth-start        --provider ID --method ID [--account ID]")
	fmt.Println("  auth-cancel       --id ID")
	fmt.Println()
	fmt.Println("Common flags:")
	fmt.Println("  --json             Machine-readable JSON output")
	fmt.Println("  --socket PATH      Control Unix socket (default: next to config)")
	fmt.Println("  --address HOST:PORT Control HTTPS endpoint")
	fmt.Println("  --timeout SECONDS  Per-request timeout")
	fmt.Println()
	fmt.Println("Environment: " + clientSocketEnv + ", " + clientAddressEnv + ", " + clientTokenEnv +
		", " + clientCACertEnv + ", " + clientCertEnv + ", " + clientKeyEnv)
	fmt.Println()
	fmt.Println("Exit codes: 0 ok, 2 usage, 3 connection, 4 auth, 5 forbidden, 6 not-found, 7 incompatible, 8 conflict, 9 server")
}

// tabWriter returns a configured tabwriter to stdout for aligned text output.
func tabWriter() *tabwriter.Writer {
	return tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
}
