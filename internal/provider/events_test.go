package provider

import "testing"

func TestEventBusSequencesAndBoundsHistory(t *testing.T) {
	bus := NewEventBus(2)
	first := bus.Publish(LifecycleEvent{Type: EventConstructing, ProviderID: Copilot})
	second := bus.Publish(LifecycleEvent{Type: EventValidating, ProviderID: Copilot})
	third := bus.Publish(LifecycleEvent{Type: EventPublished, ProviderID: Copilot})
	if first.Sequence != 1 || second.Sequence != 2 || third.Sequence != 3 {
		t.Fatalf("event sequences = %d, %d, %d", first.Sequence, second.Sequence, third.Sequence)
	}
	history := bus.History(0)
	if len(history) != 2 || history[0].Sequence != 2 || history[1].Sequence != 3 {
		t.Fatalf("bounded history = %#v", history)
	}
	if after := bus.History(2); len(after) != 1 || after[0].Sequence != 3 {
		t.Fatalf("history after sequence = %#v", after)
	}
}

func TestDescriptorCloneProtectsFactoryMetadata(t *testing.T) {
	descriptor := Descriptor{
		ID:            Copilot,
		Name:          "Copilot",
		Capabilities:  []Capability{CapabilityChat},
		AuthMethods:   []AuthMethod{AuthDeviceCode},
		SchemaVersion: 1,
		ConfigSchema:  []ConfigField{{Name: "allowed_models", Type: ConfigStrings}},
	}
	cloned := descriptor.Clone()
	descriptor.Capabilities[0] = CapabilityEmbeddings
	descriptor.AuthMethods[0] = AuthAPIKey
	descriptor.ConfigSchema[0].Name = "changed"
	if cloned.Capabilities[0] != CapabilityChat || cloned.AuthMethods[0] != AuthDeviceCode || cloned.ConfigSchema[0].Name != "allowed_models" {
		t.Fatal("descriptor clone shared mutable slices")
	}
}
