package client

import (
	"context"
	"time"

	configschema "github.com/duvu/ya-router/internal/config"
	"github.com/duvu/ya-router/internal/control"
	providerpkg "github.com/duvu/ya-router/internal/provider"
)

// stubReadModel is a deterministic control.ReadModel for client tests.
type stubReadModel struct{}

func (stubReadModel) Providers(context.Context) ([]control.ProviderResource, error) {
	return []control.ProviderResource{{
		Descriptor: providerpkg.Descriptor{ID: providerpkg.Copilot, Name: "GitHub Copilot"},
		Enabled:    true,
		Generation: 1,
	}}, nil
}

func (stubReadModel) Accounts(context.Context) ([]control.AccountResource, error) {
	return []control.AccountResource{{ProviderID: providerpkg.Copilot, ID: "acct_1", Enabled: true}}, nil
}

func (stubReadModel) Models(context.Context, bool) (control.ModelCatalogResponse, error) {
	return control.ModelCatalogResponse{Catalogs: []control.ProviderCatalog{{
		ProviderID: providerpkg.Copilot,
		Enabled:    true,
		Available:  true,
		Models:     []control.ModelResource{{ID: "gpt-5-mini", ProviderID: providerpkg.Copilot, Available: true}},
	}}}, nil
}

func (stubReadModel) Configuration(context.Context) (control.ConfigResource, error) {
	return control.ConfigResource{Revision: 7, Digest: "abc", EffectiveDigest: "abc", Effective: &configschema.Config{}}, nil
}

func (stubReadModel) Operations(context.Context) ([]control.OperationResource, error) {
	return []control.OperationResource{}, nil
}

func (stubReadModel) Events(after uint64) []providerpkg.LifecycleEvent {
	events := []providerpkg.LifecycleEvent{{Sequence: 1, Type: "provider_published", ProviderID: providerpkg.Copilot, Timestamp: time.Unix(1000, 0)}}
	var result []providerpkg.LifecycleEvent
	for _, event := range events {
		if event.Sequence > after {
			result = append(result, event)
		}
	}
	return result
}

func (stubReadModel) SubscribeEvents(int) (<-chan providerpkg.LifecycleEvent, func()) {
	stream := make(chan providerpkg.LifecycleEvent)
	close(stream)
	return stream, func() {}
}
