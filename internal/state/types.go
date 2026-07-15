// Package state owns the daemon's revisioned desired/effective configuration
// and crash-safe persistence contract.
package state

import (
	"fmt"
	"sync"
	"time"

	configschema "github.com/duvu/ya-router/internal/config"
)

const metadataSchemaVersion = 1

type FaultStage string

const (
	FaultBeforeBackup         FaultStage = "before_backup"
	FaultBeforeConfigRename   FaultStage = "before_config_rename"
	FaultAfterConfigRename    FaultStage = "after_config_rename"
	FaultBeforeMetadataRename FaultStage = "before_metadata_rename"
	FaultAfterMetadataRename  FaultStage = "after_metadata_rename"
)

type Loader func(path string) (*configschema.Config, error)
type Validator func(config *configschema.Config) error

type Options struct {
	Path      string
	Initial   *configschema.Config
	Loader    Loader
	Validator Validator
	Actor     string
	Now       func() time.Time
	FaultHook func(FaultStage) error
}

type metadata struct {
	SchemaVersion   int       `json:"schema_version"`
	Revision        uint64    `json:"revision"`
	Digest          string    `json:"digest"`
	EffectiveDigest string    `json:"effective_digest"`
	RestartRequired []string  `json:"restart_required,omitempty"`
	UpdatedAt       time.Time `json:"updated_at"`
	Actor           string    `json:"actor"`
}

type Snapshot struct {
	SchemaVersion   int                  `json:"schema_version"`
	Revision        uint64               `json:"revision"`
	Digest          string               `json:"digest"`
	EffectiveDigest string               `json:"effective_digest"`
	UpdatedAt       time.Time            `json:"updated_at"`
	Actor           string               `json:"actor"`
	Desired         *configschema.Config `json:"desired"`
	Effective       *configschema.Config `json:"effective"`
	RestartRequired []string             `json:"restart_required,omitempty"`
}

type Preview struct {
	CurrentRevision uint64   `json:"current_revision"`
	NextRevision    uint64   `json:"next_revision"`
	Digest          string   `json:"digest"`
	Changed         bool     `json:"changed"`
	ChangedPaths    []string `json:"changed_paths,omitempty"`
	RestartRequired []string `json:"restart_required,omitempty"`
}

type ConflictError struct {
	Expected uint64
	Current  uint64
}

func (err *ConflictError) Error() string {
	return fmt.Sprintf("configuration revision conflict: expected %d, current %d", err.Expected, err.Current)
}

type Manager struct {
	mu       sync.RWMutex
	options  Options
	lock     *processLock
	snapshot Snapshot
	closed   bool
}
