package yarouter

import (
	"bytes"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func Test_configureLogger_creates_parent_and_writes_console_and_file(t *testing.T) {
	// Given
	logPath := filepath.Join(t.TempDir(), "nested", "logs", "ya-router.log")
	var console bytes.Buffer
	logger := log.New(io.Discard, "", 0)

	// When
	closeLog := configureLogger(logger, LoggingConfig{
		FilePath:       logPath,
		MaxFileSizeMiB: 5,
		RetainedFiles:  2,
	}, &console)
	t.Cleanup(closeLog)
	logger.Print("request completed")

	// Then
	fileLog, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read log file: %v", err)
	}
	if !strings.Contains(console.String(), "request completed") {
		t.Fatalf("console log = %q, want request entry", console.String())
	}
	if !strings.Contains(string(fileLog), "request completed") {
		t.Fatalf("file log = %q, want request entry", fileLog)
	}
}

func Test_configureLogger_rotates_and_limits_retained_storage(t *testing.T) {
	// Given
	directory := t.TempDir()
	logPath := filepath.Join(directory, "ya-router.log")
	logger := log.New(io.Discard, "", 0)
	closeLog := configureLogger(logger, LoggingConfig{
		FilePath:       logPath,
		MaxFileSizeMiB: 1,
		RetainedFiles:  2,
	}, io.Discard)
	t.Cleanup(closeLog)
	payload := strings.Repeat("x", 512*1024)

	// When
	for index := 0; index < 8; index++ {
		logger.Print(payload)
	}

	// Then
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatalf("read log directory: %v", err)
	}
	var files int
	var size int64
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".log") {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			t.Fatalf("log entry info: %v", err)
		}
		files++
		size += info.Size()
	}
	if files != 2 {
		t.Fatalf("retained log files = %d, want active file plus one backup", files)
	}
	if size > 2<<20 {
		t.Fatalf("retained log bytes = %d, want at most %d", size, 2<<20)
	}
}

func Test_configureLogger_keeps_console_logging_when_file_initialization_fails(t *testing.T) {
	// Given
	path := t.TempDir()
	var console bytes.Buffer
	logger := log.New(io.Discard, "", 0)

	// When
	closeLog := configureLogger(logger, LoggingConfig{
		FilePath:       path,
		MaxFileSizeMiB: 5,
		RetainedFiles:  2,
	}, &console)
	t.Cleanup(closeLog)
	logger.Print("service remains available")

	// Then
	output := console.String()
	if !strings.Contains(output, "file logging disabled") {
		t.Fatalf("console output = %q, want file logging failure", output)
	}
	if !strings.Contains(output, "service remains available") {
		t.Fatalf("console output = %q, want application log entry", output)
	}
}

func Test_newRotatingLogFile_rejects_retention_above_two_files(t *testing.T) {
	// Given
	config := LoggingConfig{
		FilePath:       filepath.Join(t.TempDir(), "ya-router.log"),
		MaxFileSizeMiB: 5,
		RetainedFiles:  3,
	}

	// When
	_, err := newRotatingLogFile(config)

	// Then
	if err == nil || !strings.Contains(err.Error(), "must not exceed 2") {
		t.Fatalf("error = %v, want retention limit error", err)
	}
}

func Test_configureLogger_keeps_each_concurrent_entry_once(t *testing.T) {
	// Given
	logPath := filepath.Join(t.TempDir(), "ya-router.log")
	logger := log.New(io.Discard, "", 0)
	closeLog := configureLogger(logger, LoggingConfig{
		FilePath:       logPath,
		MaxFileSizeMiB: 5,
		RetainedFiles:  2,
	}, io.Discard)
	t.Cleanup(closeLog)
	const entries = 64

	// When
	var group sync.WaitGroup
	group.Add(entries)
	for index := 0; index < entries; index++ {
		go func(index int) {
			defer group.Done()
			logger.Print(fmt.Sprintf("concurrent-entry-%02d", index))
		}(index)
	}
	group.Wait()

	// Then
	fileLog, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read log file: %v", err)
	}
	for index := 0; index < entries; index++ {
		entry := fmt.Sprintf("concurrent-entry-%02d", index)
		if count := strings.Count(string(fileLog), entry); count != 1 {
			t.Fatalf("entry %q appears %d times, want once", entry, count)
		}
	}
}
