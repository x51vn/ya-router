// main.go — importable command dispatch used by the compatibility binaries.
package yarouter

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"strings"
)

var version = "dev"

func readSecretFromStdin(label string) (string, error) {
	fmt.Fprintf(os.Stderr, "%s: ", label)
	value, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil && strings.TrimSpace(value) == "" {
		return "", err
	}
	value = strings.TrimSpace(value)
	if value == "" {
		return "", fmt.Errorf("empty credential")
	}
	return value, nil
}

// Execute runs the historical ya-router command surface and returns a process
// exit code. Keeping process exit in cmd/ makes the service package importable.
func Execute(args []string) int {
	if len(args) < 2 {
		printUsage()
		return 0
	}

	switch args[1] {
	case "help", "--help", "-h":
		printUsage()
		return 0

	case "auth":
		authCmd := flag.NewFlagSet("auth", flag.ContinueOnError)
		authCmd.SetOutput(os.Stderr)
		modeFlag := authCmd.String("mode", "device_code", "Auth mode: device_code (default)")
		apiKeyStdin := authCmd.Bool("api-key-stdin", false, "Read a provider API key from stdin")
		tokenStdin := authCmd.Bool("token-stdin", false, "Read a ChatGPT access token from stdin (fallback only)")
		accountFlag := authCmd.String("account", "", "Account label for the provider account pool")
		commandArgs := args[2:]
		provider := "copilot"
		if len(commandArgs) > 0 && !strings.HasPrefix(commandArgs[0], "-") {
			provider = commandArgs[0]
			commandArgs = commandArgs[1:]
		}
		if err := authCmd.Parse(commandArgs); err != nil {
			fmt.Printf("Authentication arguments failed: %v\n", err)
			return 2
		}
		if *apiKeyStdin && *tokenStdin {
			fmt.Println("Authentication failed: choose only one of --api-key-stdin or --token-stdin")
			return 2
		}

		var err error
		switch provider {
		case "codex":
			switch {
			case *apiKeyStdin:
				var secret string
				secret, err = readSecretFromStdin("OpenAI API key")
				if err == nil {
					err = handleAuthCodexAPIKey(secret, *accountFlag)
				}
			case *tokenStdin:
				var secret string
				secret, err = readSecretFromStdin("ChatGPT access token")
				if err == nil {
					err = handleAuthCodexManualToken(secret, *accountFlag)
				}
			default:
				err = handleAuthCodex(*accountFlag)
			}
		case "copilot":
			err = handleAuthCopilot(*modeFlag, *accountFlag)
		case "kilo":
			if *tokenStdin {
				err = fmt.Errorf("--token-stdin is not supported for Kilo; use --api-key-stdin")
			} else if *apiKeyStdin {
				var secret string
				secret, err = readSecretFromStdin("Kilo API key")
				if err == nil {
					err = handleAuthKiloAPIKey(secret)
				}
			} else {
				err = handleAuthKiloAnonymous()
			}
		default:
			err = fmt.Errorf("unknown provider %q", provider)
		}
		if err != nil {
			fmt.Printf("Authentication failed: %v\n", err)
			return 1
		}
		return 0

	case "run", "start":
		runCmd := flag.NewFlagSet("run", flag.ContinueOnError)
		runCmd.SetOutput(os.Stderr)
		configMigrate := runCmd.String("config-migrate", "merge", "Config migration mode: none, merge, override")
		if err := runCmd.Parse(args[2:]); err != nil {
			fmt.Printf("Run arguments failed: %v\n", err)
			return 2
		}
		mode := ConfigMigrationMode(*configMigrate)
		if mode != ConfigMigrationNone && mode != ConfigMigrationMerge && mode != ConfigMigrationOverride {
			fmt.Printf("Invalid config-migrate mode: %s\n", mode)
			return 2
		}
		releaseState, err := acquireManagedConfigState("ya-routerd")
		if err != nil {
			fmt.Printf("Server failed: %v\n", err)
			return 1
		}
		defer func() {
			if err := releaseState(); err != nil {
				fmt.Printf("State shutdown warning: %v\n", err)
			}
		}()
		if err := handleRunWithMigration(mode); err != nil {
			fmt.Printf("Server failed: %v\n", err)
			return 1
		}
		return 0

	case "migrate-config":
		command := flag.NewFlagSet("migrate-config", flag.ContinueOnError)
		command.SetOutput(os.Stderr)
		modeValue := command.String("mode", "merge", "Migration mode: merge, override")
		if err := command.Parse(args[2:]); err != nil {
			fmt.Printf("Migration arguments failed: %v\n", err)
			return 2
		}
		mode := ConfigMigrationMode(*modeValue)
		if mode != ConfigMigrationMerge && mode != ConfigMigrationOverride {
			fmt.Printf("Invalid mode: %s\n", mode)
			return 2
		}
		if err := migrateConfig(mode); err != nil {
			fmt.Printf("Config migration failed: %v\n", err)
			return 1
		}
		return 0

	case "models":
		command := flag.NewFlagSet("models", flag.ContinueOnError)
		command.SetOutput(os.Stderr)
		providerFlag := command.String("provider", "", "Filter to a specific provider")
		refreshFlag := command.Bool("refresh", false, "Ignore model cache and re-fetch model list")
		if err := command.Parse(args[2:]); err != nil {
			fmt.Printf("Models arguments failed: %v\n", err)
			return 2
		}
		if err := handleModels(*providerFlag, *refreshFlag); err != nil {
			fmt.Printf("Models failed: %v\n", err)
			return 1
		}
		return 0

	case "config":
		if err := handleConfig(); err != nil {
			fmt.Printf("Config failed: %v\n", err)
			return 1
		}
		return 0

	case "status":
		if err := handleStatus(); err != nil {
			fmt.Printf("Status failed: %v\n", err)
			return 1
		}
		return 0

	case "refresh":
		command := flag.NewFlagSet("refresh", flag.ContinueOnError)
		command.SetOutput(os.Stderr)
		providerFlag := command.String("provider", "", "Filter to a specific provider")
		if err := command.Parse(args[2:]); err != nil {
			fmt.Printf("Refresh arguments failed: %v\n", err)
			return 2
		}
		if err := handleRefresh(*providerFlag); err != nil {
			fmt.Printf("Refresh failed: %v\n", err)
			return 1
		}
		return 0

	case "version":
		fmt.Printf("ya-router %s\n", version)
		return 0

	default:
		fmt.Printf("Unknown command: %s\n", args[1])
		return 2
	}
}

// ExecuteDaemon runs the service-only ya-routerd surface. With no command, or
// when the first argument is a flag, it starts the daemon.
func ExecuteDaemon(args []string) int {
	if len(args) == 0 {
		args = []string{"ya-routerd"}
	}
	if len(args) == 1 {
		return Execute([]string{args[0], "run"})
	}
	switch args[1] {
	case "run", "start":
		return Execute(args)
	case "version":
		fmt.Printf("ya-routerd %s\n", version)
		return 0
	case "help", "--help", "-h":
		fmt.Println("ya-routerd — ya-router service")
		fmt.Println()
		fmt.Println("Usage: ya-routerd [run|start] [--config-migrate=none|merge|override]")
		return 0
	default:
		if strings.HasPrefix(args[1], "-") {
			daemonArgs := append([]string{args[0], "run"}, args[1:]...)
			return Execute(daemonArgs)
		}
		fmt.Printf("Unknown ya-routerd command: %s\n", args[1])
		return 2
	}
}

// ExecuteClient provides the installable client binary boundary for both the
// interactive dashboard and scriptable control commands.
func ExecuteClient(args []string) int {
	return runClientCLI(args)
}
