package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/fijimunkii/cozysoc/internal/controller/capability"
)

const (
	CurrentSchemaVersion = 2
	Filename             = "config.json"
	MaxConfigBytes       = 256 * 1024
)

type Config struct {
	SchemaVersion int                        `json:"schema_version"`
	LogLevel      string                     `json:"log_level"`
	Capabilities  []capability.Configuration `json:"capabilities,omitempty"`
}

type configV1 struct {
	SchemaVersion int    `json:"schema_version"`
	LogLevel      string `json:"log_level"`
}

func Default() Config {
	return Config{
		SchemaVersion: CurrentSchemaVersion,
		LogLevel:      "info",
	}
}

func LoadOrCreate(stateDir string) (Config, error) {
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		return Config{}, fmt.Errorf("create state directory: %w", err)
	}
	if err := os.Chmod(stateDir, 0o700); err != nil {
		return Config{}, fmt.Errorf("secure state directory: %w", err)
	}

	path := filepath.Join(stateDir, Filename)
	data, err := readBounded(path)
	if errors.Is(err, os.ErrNotExist) {
		cfg := Default()
		if err := writeAtomic(path, cfg); err != nil {
			return Config{}, err
		}
		return cfg, nil
	}
	if err != nil {
		return Config{}, err
	}

	var header struct {
		SchemaVersion int `json:"schema_version"`
	}
	if err := json.Unmarshal(data, &header); err != nil {
		return Config{}, fmt.Errorf("parse config header: %w", err)
	}

	switch header.SchemaVersion {
	case 1:
		var legacy configV1
		if err := decodeStrict(data, &legacy); err != nil {
			return Config{}, err
		}
		if err := validateLogLevel(legacy.LogLevel); err != nil {
			return Config{}, err
		}
		cfg := Config{
			SchemaVersion: CurrentSchemaVersion,
			LogLevel:      legacy.LogLevel,
		}
		if err := writeAtomic(path, cfg); err != nil {
			return Config{}, fmt.Errorf("migrate config schema 1 to %d: %w", CurrentSchemaVersion, err)
		}
		return cfg, nil
	case CurrentSchemaVersion:
		var cfg Config
		if err := decodeStrict(data, &cfg); err != nil {
			return Config{}, err
		}
		if err := validate(cfg); err != nil {
			return Config{}, err
		}
		return cfg, nil
	default:
		return Config{}, fmt.Errorf("unsupported config schema version %d; expected %d", header.SchemaVersion, CurrentSchemaVersion)
	}
}

func readBounded(path string) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, os.ErrNotExist
		}
		return nil, fmt.Errorf("open config: %w", err)
	}
	defer file.Close()

	data, err := io.ReadAll(io.LimitReader(file, MaxConfigBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	if len(data) > MaxConfigBytes {
		return nil, fmt.Errorf("config exceeds %d bytes", MaxConfigBytes)
	}
	return data, nil
}

func decodeStrict(data []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("parse config: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return fmt.Errorf("parse config: trailing JSON value")
		}
		return fmt.Errorf("parse config trailer: %w", err)
	}
	return nil
}

func validate(cfg Config) error {
	if cfg.SchemaVersion != CurrentSchemaVersion {
		return fmt.Errorf("unsupported config schema version %d; expected %d", cfg.SchemaVersion, CurrentSchemaVersion)
	}
	if err := validateLogLevel(cfg.LogLevel); err != nil {
		return err
	}
	seen := make(map[string]bool, len(cfg.Capabilities))
	for _, configuration := range cfg.Capabilities {
		if configuration.ID == "" {
			return fmt.Errorf("capability configuration id is required")
		}
		if seen[configuration.ID] {
			return fmt.Errorf("duplicate capability configuration %q", configuration.ID)
		}
		seen[configuration.ID] = true
	}
	return nil
}

func validateLogLevel(level string) error {
	switch level {
	case "debug", "info", "warn", "error":
		return nil
	default:
		return fmt.Errorf("invalid log_level %q", level)
	}
}

func writeAtomic(path string, cfg Config) error {
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("encode config: %w", err)
	}
	data = append(data, '\n')
	if len(data) > MaxConfigBytes {
		return fmt.Errorf("encoded config exceeds %d bytes", MaxConfigBytes)
	}

	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".config-*.tmp")
	if err != nil {
		return fmt.Errorf("create config temp file: %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)

	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("secure config temp file: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write config temp file: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("sync config temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close config temp file: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("replace config: %w", err)
	}
	return nil
}
