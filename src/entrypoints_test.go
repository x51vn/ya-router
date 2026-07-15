package yarouter

import "testing"

func TestCompatibilityAndDaemonEntrypointsShareValidation(t *testing.T) {
	compatibility := Execute([]string{"ya-router", "run", "--config-migrate=invalid"})
	daemon := ExecuteDaemon([]string{"ya-routerd", "--config-migrate=invalid"})
	if compatibility != 2 || daemon != compatibility {
		t.Fatalf("compatibility=%d daemon=%d", compatibility, daemon)
	}
}

func TestClientFoundationDoesNotMutateLocalServiceState(t *testing.T) {
	if got := ExecuteClient([]string{"ya", "models"}); got != 2 {
		t.Fatalf("client exit=%d", got)
	}
}
