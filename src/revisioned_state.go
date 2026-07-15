package yarouter

import (
	"fmt"
	"sync"

	configschema "github.com/duvu/ya-router/internal/config"
	statepkg "github.com/duvu/ya-router/internal/state"
)

var managedConfigState struct {
	sync.RWMutex
	manager *statepkg.Manager
}

func currentConfigState() *statepkg.Manager {
	managedConfigState.RLock()
	defer managedConfigState.RUnlock()
	return managedConfigState.manager
}

func openConfigState(path, actor string) (*statepkg.Manager, error) {
	return statepkg.Open(statepkg.Options{
		Path:      path,
		Initial:   defaultConfig(),
		Loader:    loadConfigFromPath,
		Validator: validateManagedConfig,
		Actor:     actor,
	})
}

func acquireManagedConfigState(actor string) (func() error, error) {
	path, err := getConfigPath()
	if err != nil {
		return nil, err
	}
	manager, err := openConfigState(path, actor)
	if err != nil {
		return nil, err
	}
	managedConfigState.Lock()
	if managedConfigState.manager != nil {
		managedConfigState.Unlock()
		_ = manager.Close()
		return nil, fmt.Errorf("managed configuration state is already active")
	}
	managedConfigState.manager = manager
	managedConfigState.Unlock()

	return func() error {
		managedConfigState.Lock()
		if managedConfigState.manager == manager {
			managedConfigState.manager = nil
		}
		managedConfigState.Unlock()
		return manager.Close()
	}, nil
}

func validateManagedConfig(config *configschema.Config) error {
	if config == nil {
		return fmt.Errorf("configuration is required")
	}
	applyConfigDefaults(config)
	if config.ConfigVersion == 0 {
		config.ConfigVersion = currentConfigVersion
	}
	if config.ConfigVersion != currentConfigVersion {
		return fmt.Errorf("unsupported config version %d", config.ConfigVersion)
	}
	if config.Port < 1 || config.Port > 65535 {
		return fmt.Errorf("port must be between 1 and 65535")
	}
	return nil
}
