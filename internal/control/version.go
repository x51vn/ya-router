package control

import (
	"context"
	"fmt"
	"strconv"
	"strings"
)

const (
	ControlAPIVersion           = "v1"
	CurrentClientVersion        = "1.1.0"
	PreviousSupportedClient     = "1.0.0"
	MaximumSupportedClientLabel = "1.1.x"
)

type ClientCompatibility struct {
	Requested  string `json:"requested,omitempty"`
	Minimum    string `json:"minimum"`
	Maximum    string `json:"maximum"`
	Current    string `json:"current"`
	Compatible bool   `json:"compatible"`
}

func negotiateClientVersion(value string) ClientCompatibility {
	compatibility := ClientCompatibility{
		Requested:  strings.TrimSpace(value),
		Minimum:    PreviousSupportedClient,
		Maximum:    MaximumSupportedClientLabel,
		Current:    CurrentClientVersion,
		Compatible: true,
	}
	if compatibility.Requested == "" {
		return compatibility
	}
	major, minor, _, err := parseSemanticVersion(compatibility.Requested)
	compatibility.Compatible = err == nil && major == 1 && (minor == 0 || minor == 1)
	return compatibility
}

func parseSemanticVersion(value string) (int, int, int, error) {
	value = strings.TrimPrefix(strings.TrimSpace(value), "v")
	if index := strings.IndexAny(value, "+-"); index >= 0 {
		value = value[:index]
	}
	parts := strings.Split(value, ".")
	if len(parts) < 2 || len(parts) > 3 {
		return 0, 0, 0, fmt.Errorf("version must contain major.minor[.patch]")
	}
	parsed := []int{0, 0, 0}
	for index, part := range parts {
		value, err := strconv.Atoi(part)
		if err != nil || value < 0 {
			return 0, 0, 0, fmt.Errorf("invalid semantic version")
		}
		parsed[index] = value
	}
	return parsed[0], parsed[1], parsed[2], nil
}

func withCompatibility(ctx context.Context, compatibility ClientCompatibility) context.Context {
	return context.WithValue(ctx, versionContextKey, compatibility)
}

func compatibilityFromContext(ctx context.Context) ClientCompatibility {
	compatibility, ok := ctx.Value(versionContextKey).(ClientCompatibility)
	if !ok {
		return negotiateClientVersion("")
	}
	return compatibility
}
