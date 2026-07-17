package yarouter

import "strings"

type anthropicHeaderDisposition string

const (
	anthropicHeaderTranslated anthropicHeaderDisposition = "translate"
	anthropicHeaderConsumed   anthropicHeaderDisposition = "consume"
	anthropicHeaderForwarded  anthropicHeaderDisposition = "forward"
	anthropicHeaderRejected   anthropicHeaderDisposition = "reject"
)

func classifyAnthropicHeader(header string) anthropicHeaderDisposition {
	switch strings.ToLower(strings.TrimSpace(header)) {
	case "idempotency-key":
		return anthropicHeaderForwarded
	case "authorization", "x-api-key", "content-type", "accept":
		return anthropicHeaderConsumed
	}
	normalized := strings.ToLower(strings.TrimSpace(header))
	if strings.HasPrefix(normalized, "anthropic-") || strings.HasPrefix(normalized, "x-claude-code-") {
		return anthropicHeaderConsumed
	}
	return anthropicHeaderConsumed
}
