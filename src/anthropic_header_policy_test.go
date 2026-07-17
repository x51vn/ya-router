package yarouter

import "testing"

func TestAnthropicHeaderDisposition_handlesProtocolEvolutionWithoutForwardingCredentials(t *testing.T) {
	// Given
	tests := []struct {
		name   string
		header string
		want   anthropicHeaderDisposition
	}{
		{name: "gateway authorization", header: "Authorization", want: anthropicHeaderConsumed},
		{name: "future anthropic beta", header: "Anthropic-Future-Beta", want: anthropicHeaderConsumed},
		{name: "future Claude Code header", header: "X-Claude-Code-Trace", want: anthropicHeaderConsumed},
		{name: "idempotency key", header: "Idempotency-Key", want: anthropicHeaderForwarded},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// When
			got := classifyAnthropicHeader(test.header)

			// Then
			if got != test.want {
				t.Fatalf("disposition=%q, want %q", got, test.want)
			}
		})
	}
}
