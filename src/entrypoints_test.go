package yarouter

import "testing"

func TestCompatibilityAndDaemonEntrypointsShareValidation(t *testing.T) {
	compatibility := Execute([]string{"ya-router", "run", "--config-migrate=invalid"})
	daemon := ExecuteDaemon([]string{"ya-routerd", "--config-migrate=invalid"})
	if compatibility != 2 || daemon != compatibility {
		t.Fatalf("compatibility=%d daemon=%d", compatibility, daemon)
	}
}

func TestClientUnknownCommandIsUsageError(t *testing.T) {
	if got := ExecuteClient([]string{"ya", "not-a-command"}); got != clientExitUsage {
		t.Fatalf("client exit=%d, want %d", got, clientExitUsage)
	}
}

// A read command against an explicit, unreachable endpoint must fail cleanly
// with the stable connection exit code — never a mutation or a panic.
func TestClientReadAgainstUnreachableEndpointFailsCleanly(t *testing.T) {
	got := ExecuteClient([]string{"ya", "models", "--socket", "/nonexistent/ya-control.sock"})
	if got != clientExitConnection {
		t.Fatalf("client exit=%d, want %d (connection)", got, clientExitConnection)
	}
}
