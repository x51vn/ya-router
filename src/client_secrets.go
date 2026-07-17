package yarouter

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"

	clientpkg "github.com/duvu/ya-router/internal/client"
	secretpkg "github.com/duvu/ya-router/internal/secret"
)

func runClientSecretCommand(command string, args []string) int {
	set := flag.NewFlagSet(command, flag.ContinueOnError)
	set.SetOutput(os.Stderr)
	var common clientCommonFlags
	registerClientFlags(set, &common, false, false, false)
	var slot string
	var stdin bool
	set.StringVar(&slot, "slot", "", "Daemon credential slot")
	set.BoolVar(&stdin, "stdin", false, "Read credential value from stdin")
	if err := set.Parse(args); err != nil || set.NArg() != 0 || strings.TrimSpace(slot) == "" {
		return clientExitUsage
	}
	if command == "secret-set" && !stdin {
		fmt.Fprintln(os.Stderr, "ya: secret-set requires --stdin; credentials must not be supplied as command arguments")
		return clientExitUsage
	}
	if command == "secret-delete" && stdin {
		fmt.Fprintln(os.Stderr, "ya: secret-delete does not accept --stdin")
		return clientExitUsage
	}
	profile, err := resolveClientProfile(common)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ya: %v\n", err)
		return clientExitUsage
	}
	client, err := clientpkg.New(profile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ya: %v\n", err)
		return clientExitUsage
	}
	ctx, cancel := context.WithTimeout(context.Background(), clientRequestTimeout(profile))
	defer cancel()
	if command == "secret-delete" {
		if err := client.DeleteSecret(ctx, slot); err != nil {
			return reportClientError(err)
		}
		fmt.Println("Secret deleted.")
		return clientExitOK
	}
	value, err := readSecretFromStdin("Credential")
	if err != nil {
		fmt.Fprintf(os.Stderr, "ya: %v\n", err)
		return clientExitUsage
	}
	metadata, err := client.SetSecret(ctx, slot, value)
	if err != nil {
		return reportClientError(err)
	}
	if common.jsonOut {
		return emitJSON(metadata)
	}
	renderSecrets([]secretpkg.Metadata{metadata})
	return clientExitOK
}
