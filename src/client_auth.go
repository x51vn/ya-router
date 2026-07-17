package yarouter

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"

	clientpkg "github.com/duvu/ya-router/internal/client"
	controlpkg "github.com/duvu/ya-router/internal/control"
)

func runClientAuthCommand(command string, args []string) int {
	set := flag.NewFlagSet(command, flag.ContinueOnError)
	set.SetOutput(os.Stderr)
	var common clientCommonFlags
	registerClientFlags(set, &common, false, false, false)
	var provider, method, account, id string
	var expires int
	set.StringVar(&provider, "provider", "", "Provider ID")
	set.StringVar(&method, "method", "", "Authentication method")
	set.StringVar(&account, "account", "", "Provider account label")
	set.StringVar(&id, "id", "", "Authentication session ID")
	set.IntVar(&expires, "expires-in", 0, "Session lifetime in seconds")
	if err := set.Parse(args); err != nil || set.NArg() != 0 {
		return clientExitUsage
	}
	if command == "auth-start" && (strings.TrimSpace(provider) == "" || strings.TrimSpace(method) == "") {
		return clientExitUsage
	}
	if command == "auth-cancel" && strings.TrimSpace(id) == "" {
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
	var operation controlpkg.OperationResource
	if command == "auth-start" {
		operation, err = client.CreateAuthSession(ctx, clientpkg.AuthSessionRequest{
			ProviderID: provider, AccountID: account, Method: method, ExpiresInSeconds: expires,
		})
	} else {
		operation, err = client.CancelAuthSession(ctx, id)
	}
	if err != nil {
		return reportClientError(err)
	}
	if common.jsonOut {
		return emitJSON(operation)
	}
	renderOperations([]controlpkg.OperationResource{operation})
	return clientExitOK
}
