package provider

import (
	"context"
	"fmt"
)

// AuthMethod identifies an authentication flow supported by a provider.
type AuthMethod string

const (
	AuthDeviceCode          AuthMethod = "device_code"
	AuthAPIKey              AuthMethod = "api_key"
	AuthManualTokenRecovery AuthMethod = "manual_token_recovery"
	AuthAnonymous           AuthMethod = "anonymous"
)

// ConfigValueType is a descriptor-level field type. It intentionally stays
// small and provider-neutral so future clients do not need provider-specific
// protocols.
type ConfigValueType string

const (
	ConfigString  ConfigValueType = "string"
	ConfigBoolean ConfigValueType = "boolean"
	ConfigInteger ConfigValueType = "integer"
	ConfigStrings ConfigValueType = "strings"
)

// ConfigField describes one supported provider setting. Secret fields are
// metadata only; values must never be returned through descriptors.
type ConfigField struct {
	Name     string          `json:"name"`
	Type     ConfigValueType `json:"type"`
	Required bool            `json:"required"`
	Secret   bool            `json:"secret"`
}

// Descriptor describes management capabilities independently from a live
// request-serving provider instance.
type Descriptor struct {
	ID            ID            `json:"id"`
	Name          string        `json:"name"`
	Capabilities  []Capability  `json:"capabilities"`
	AuthMethods   []AuthMethod  `json:"auth_methods"`
	MultiAccount  bool          `json:"multi_account"`
	ConfigSchema  []ConfigField `json:"config_schema"`
	SchemaVersion int           `json:"schema_version"`
}

// Clone prevents callers from mutating descriptor slices owned by a factory.
func (descriptor Descriptor) Clone() Descriptor {
	cloned := descriptor
	cloned.Capabilities = append([]Capability(nil), descriptor.Capabilities...)
	cloned.AuthMethods = append([]AuthMethod(nil), descriptor.AuthMethods...)
	cloned.ConfigSchema = append([]ConfigField(nil), descriptor.ConfigSchema...)
	return cloned
}

// ValidateDescriptor rejects descriptors that cannot safely drive generic
// provider management clients.
func ValidateDescriptor(descriptor Descriptor) error {
	if descriptor.ID == "" {
		return fmt.Errorf("provider descriptor ID is required")
	}
	if descriptor.Name == "" {
		return fmt.Errorf("provider %q descriptor name is required", descriptor.ID)
	}
	if descriptor.SchemaVersion < 1 {
		return fmt.Errorf("provider %q descriptor schema version must be positive", descriptor.ID)
	}
	if len(descriptor.Capabilities) == 0 {
		return fmt.Errorf("provider %q descriptor needs at least one capability", descriptor.ID)
	}
	seenFields := make(map[string]struct{}, len(descriptor.ConfigSchema))
	for _, field := range descriptor.ConfigSchema {
		if field.Name == "" {
			return fmt.Errorf("provider %q descriptor contains an unnamed config field", descriptor.ID)
		}
		if _, exists := seenFields[field.Name]; exists {
			return fmt.Errorf("provider %q descriptor repeats config field %q", descriptor.ID, field.Name)
		}
		seenFields[field.Name] = struct{}{}
	}
	return nil
}

// Factory constructs a replacement without mutating any live provider.
// Validation is split so cheap configuration checks happen before resources
// are allocated and instance checks happen before publication.
type Factory interface {
	Descriptor() Descriptor
	ValidateConfig(config any) error
	Build(ctx context.Context, config any) (Provider, error)
	ValidateProvider(ctx context.Context, provider Provider) error
}

// FactoryFuncs makes compiled-in provider adapters concise while retaining the
// explicit Factory lifecycle contract.
type FactoryFuncs struct {
	ProviderDescriptor Descriptor
	ValidateConfigFunc func(config any) error
	BuildFunc          func(ctx context.Context, config any) (Provider, error)
	ValidateFunc       func(ctx context.Context, provider Provider) error
}

func (factory FactoryFuncs) Descriptor() Descriptor { return factory.ProviderDescriptor.Clone() }

func (factory FactoryFuncs) ValidateConfig(config any) error {
	if factory.ValidateConfigFunc == nil {
		return nil
	}
	return factory.ValidateConfigFunc(config)
}

func (factory FactoryFuncs) Build(ctx context.Context, config any) (Provider, error) {
	if factory.BuildFunc == nil {
		return nil, fmt.Errorf("provider %q factory has no builder", factory.ProviderDescriptor.ID)
	}
	return factory.BuildFunc(ctx, config)
}

func (factory FactoryFuncs) ValidateProvider(ctx context.Context, registered Provider) error {
	if factory.ValidateFunc == nil {
		return nil
	}
	return factory.ValidateFunc(ctx, registered)
}
