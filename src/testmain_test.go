package main

import (
	"os"
	"testing"

	authpkg "github.com/x51vn/github-copilot-svcs/internal/auth"
)

func TestMain(m *testing.M) {
	origIssuer := *authpkg.CodexAuthIssuer
	origDelay := *authpkg.BaseRetryDelay

	result := m.Run()

	*authpkg.CodexAuthIssuer = origIssuer
	*authpkg.BaseRetryDelay = origDelay

	os.Exit(result)
}
