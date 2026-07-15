package provider

import "context"

// Retirement represents all pre-publication snapshots that may still hold a
// provider reference.
type Retirement interface {
	Wait(ctx context.Context) error
	Pending() int
}

// Publication reports the generation made visible by an atomic swap.
type Publication struct {
	Generation uint64
	Retirement Retirement
}

// SnapshotPublisher is implemented by runtime.Manager without introducing a
// provider -> runtime package dependency.
type SnapshotPublisher interface {
	PublishProviders(providers []Provider) (Publication, error)
}

// Lifecycle is optional. It is invoked only after snapshots that could refer
// to the instance have drained.
type Lifecycle interface {
	Close(ctx context.Context) error
}
