package yarouter

import (
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/natefinch/lumberjack.v2"
)

func setupLogging(config LoggingConfig) func() {
	return configureLogger(log.Default(), config, os.Stderr)
}

func configureLogger(logger *log.Logger, config LoggingConfig, console io.Writer) func() {
	logger.SetFlags(log.LstdFlags | log.Lshortfile)
	logger.SetPrefix("[ya-router] ")
	logger.SetOutput(console)

	file, err := newRotatingLogFile(config)
	if err != nil {
		fmt.Fprintf(console, "[ya-router] file logging disabled: %v\n", err)
		return func() {}
	}

	logger.SetOutput(io.MultiWriter(console, file))
	return func() {
		if err := file.Close(); err != nil {
			fmt.Fprintf(console, "[ya-router] file logging close failed: %v\n", err)
		}
	}
}

func newRotatingLogFile(config LoggingConfig) (*lumberjack.Logger, error) {
	path := strings.TrimSpace(config.FilePath)
	if path == "" {
		return nil, fmt.Errorf("log file path is empty")
	}
	if config.MaxFileSizeMiB <= 0 {
		return nil, fmt.Errorf("max file size must be positive")
	}
	if config.RetainedFiles < 2 {
		return nil, fmt.Errorf("retained files must include the active log and one backup")
	}
	if config.RetainedFiles > defaultRetainedLogFiles {
		return nil, fmt.Errorf("retained files must not exceed %d", defaultRetainedLogFiles)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create log directory: %w", err)
	}
	created, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, fmt.Errorf("create log file: %w", err)
	}
	if err := created.Close(); err != nil {
		return nil, fmt.Errorf("close initialized log file: %w", err)
	}
	return &lumberjack.Logger{
		Filename:   path,
		MaxSize:    config.MaxFileSizeMiB,
		MaxBackups: config.RetainedFiles - 1,
	}, nil
}
