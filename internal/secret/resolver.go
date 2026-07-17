package secret

import "strings"

// Candidate is one possible credential value with its source. The resolver
// picks the highest-precedence configured candidate.
type Candidate struct {
	Value  string
	Source Source
}

// Resolve returns the highest-precedence non-empty candidate by SourceRank.
// Ties are broken by input order (stable). It returns ok=false when no
// candidate carries a value, so callers can report "unconfigured" without
// guessing. This centralizes the credential-source precedence rule: an
// environment value can never be shadowed by a lower-precedence managed or
// legacy value.
func Resolve(candidates ...Candidate) (Candidate, bool) {
	best := Candidate{}
	found := false
	bestRank := -1
	for _, candidate := range candidates {
		if strings.TrimSpace(candidate.Value) == "" {
			continue
		}
		rank := SourceRank(candidate.Source)
		if rank > bestRank {
			best = candidate
			bestRank = rank
			found = true
		}
	}
	return best, found
}

// AuthController centralizes provider authentication and credential storage in
// the daemon. Implementations must never return stored secret values through a
// control response: callers receive redacted CredentialPosture only.
//
// The data plane obtains usable credentials through ResolveCredential, which is
// internal to the daemon process and not exposed on any control route.
type AuthController interface {
	// ResolveCredential returns the effective credential for a provider account
	// slot (for example "codex/access_token"), applying source precedence.
	ResolveCredential(slot string) (Candidate, bool)
	// Posture returns the redacted posture for a slot for control clients.
	Posture(slot string) CredentialPosture
	// SetManaged stores a managed credential for a slot. Read-only sources are
	// refused so environment values are never shadowed.
	SetManaged(actor, slot, value string) error
	// Rotate replaces a managed credential and records an audit event.
	Rotate(actor, slot, value string) error
}

// CredentialPosture is the redacted credential state for one slot.
type CredentialPosture struct {
	Slot        string `json:"slot"`
	Configured  bool   `json:"configured"`
	Source      Source `json:"source"`
	Refreshable bool   `json:"refreshable"`
	ReadOnly    bool   `json:"read_only"`
}

// StoreController implements AuthController over a SecretStore plus injected
// environment/official/legacy candidates supplied per slot by the daemon.
type StoreController struct {
	store     SecretStore
	providers map[string]func() []Candidate
}

// NewStoreController builds a controller. slotCandidates maps a slot to a
// function returning its read-only candidates (environment, official store,
// legacy config) in addition to the managed store value.
func NewStoreController(store SecretStore, slotCandidates map[string]func() []Candidate) *StoreController {
	if slotCandidates == nil {
		slotCandidates = map[string]func() []Candidate{}
	}
	return &StoreController{store: store, providers: slotCandidates}
}

func (controller *StoreController) candidatesFor(slot string) []Candidate {
	candidates := []Candidate{}
	if managed, source, ok := controller.store.Resolve(slot); ok && source == SourceManaged {
		candidates = append(candidates, Candidate{Value: managed, Source: SourceManaged})
	}
	if provider, ok := controller.providers[slot]; ok && provider != nil {
		candidates = append(candidates, provider()...)
	}
	return candidates
}

func (controller *StoreController) ResolveCredential(slot string) (Candidate, bool) {
	return Resolve(controller.candidatesFor(slot)...)
}

func (controller *StoreController) Posture(slot string) CredentialPosture {
	candidate, ok := controller.ResolveCredential(slot)
	posture := CredentialPosture{Slot: slot, Configured: ok, Source: candidate.Source}
	if ok {
		posture.ReadOnly = candidate.Source == SourceEnvironment || candidate.Source == SourceOfficialStore
	} else {
		posture.Source = "unconfigured"
	}
	return posture
}

func (controller *StoreController) SetManaged(actor, slot, value string) error {
	_, err := controller.store.Set(actor, slot, value)
	return err
}

func (controller *StoreController) Rotate(actor, slot, value string) error {
	// Rotation is a managed write; the store increments version and audits it.
	_, err := controller.store.Set(actor, slot, value)
	return err
}
