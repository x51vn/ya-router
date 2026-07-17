package config

import (
	"strings"
	"testing"
)

var testKnownPrefixes = []string{"github/", "codex/", "kilo/"}

func TestValidateVirtualModels(t *testing.T) {
	cases := []struct {
		name        string
		routing     Routing
		wantErr     bool
		errContains string
	}{
		{
			name:    "no virtual models",
			routing: Routing{},
		},
		{
			name: "valid single umbrella",
			routing: Routing{
				VirtualModels: map[string]VirtualModel{
					"router/auto": {Strategy: "priority", Targets: []string{"github/gpt-5-mini", "codex/gpt-5.4-mini"}},
				},
			},
		},
		{
			name: "empty targets",
			routing: Routing{
				VirtualModels: map[string]VirtualModel{
					"router/auto": {Strategy: "priority", Targets: nil},
				},
			},
			wantErr:     true,
			errContains: "at least one target",
		},
		{
			name: "invalid strategy",
			routing: Routing{
				VirtualModels: map[string]VirtualModel{
					"router/auto": {Strategy: "weighted", Targets: []string{"github/gpt-5-mini"}},
				},
			},
			wantErr:     true,
			errContains: "strategy",
		},
		{
			name: "empty strategy",
			routing: Routing{
				VirtualModels: map[string]VirtualModel{
					"router/auto": {Targets: []string{"github/gpt-5-mini"}},
				},
			},
			wantErr:     true,
			errContains: "strategy",
		},
		{
			name: "duplicate target",
			routing: Routing{
				VirtualModels: map[string]VirtualModel{
					"router/auto": {Strategy: "priority", Targets: []string{"github/gpt-5-mini", "github/gpt-5-mini"}},
				},
			},
			wantErr:     true,
			errContains: "more than once",
		},
		{
			name: "unknown prefix target",
			routing: Routing{
				VirtualModels: map[string]VirtualModel{
					"router/auto": {Strategy: "priority", Targets: []string{"bogus/model"}},
				},
			},
			wantErr:     true,
			errContains: "known provider prefix",
		},
		{
			name: "bare target without prefix",
			routing: Routing{
				VirtualModels: map[string]VirtualModel{
					"router/auto": {Strategy: "priority", Targets: []string{"gpt-5-mini"}},
				},
			},
			wantErr:     true,
			errContains: "known provider prefix",
		},
		{
			name: "empty target string",
			routing: Routing{
				VirtualModels: map[string]VirtualModel{
					"router/auto": {Strategy: "priority", Targets: []string{"   "}},
				},
			},
			wantErr:     true,
			errContains: "must not be empty",
		},
		{
			name: "id collides with provider prefix",
			routing: Routing{
				VirtualModels: map[string]VirtualModel{
					"github/auto": {Strategy: "priority", Targets: []string{"github/gpt-5-mini"}},
				},
			},
			wantErr:     true,
			errContains: "reserved provider prefix",
		},
		{
			name: "umbrella to umbrella reference forbidden",
			routing: Routing{
				VirtualModels: map[string]VirtualModel{
					"router/auto":  {Strategy: "priority", Targets: []string{"router/inner"}},
					"router/inner": {Strategy: "priority", Targets: []string{"github/gpt-5-mini"}},
				},
			},
			wantErr:     true,
			errContains: "another virtual model",
		},
		{
			name: "shadows model_map entry",
			routing: Routing{
				ModelMap: map[string]ModelMapEntry{
					"router/auto": {Provider: "codex"},
				},
				VirtualModels: map[string]VirtualModel{
					"router/auto": {Strategy: "priority", Targets: []string{"github/gpt-5-mini"}},
				},
			},
			wantErr:     true,
			errContains: "shadows an explicit routing.model_map",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.routing.ValidateVirtualModels(testKnownPrefixes)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				if tc.errContains != "" && !strings.Contains(err.Error(), tc.errContains) {
					t.Fatalf("error %q does not contain %q", err.Error(), tc.errContains)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

// TestValidateVirtualModelsDeterministic proves the first reported error is
// stable across runs regardless of Go map iteration order.
func TestValidateVirtualModelsDeterministic(t *testing.T) {
	routing := Routing{
		VirtualModels: map[string]VirtualModel{
			"router/aaa": {Strategy: "priority", Targets: []string{"github/ok"}},
			"router/bbb": {Strategy: "bad", Targets: []string{"github/ok"}},
			"router/ccc": {Strategy: "priority", Targets: nil},
		},
	}
	var first string
	for i := 0; i < 50; i++ {
		err := routing.ValidateVirtualModels(testKnownPrefixes)
		if err == nil {
			t.Fatalf("expected error")
		}
		if first == "" {
			first = err.Error()
			continue
		}
		if err.Error() != first {
			t.Fatalf("non-deterministic error: %q != %q", err.Error(), first)
		}
	}
	// router/bbb sorts before router/ccc, so the strategy error is reported first.
	if !strings.Contains(first, `router/bbb`) {
		t.Fatalf("expected router/bbb reported first, got %q", first)
	}
}
