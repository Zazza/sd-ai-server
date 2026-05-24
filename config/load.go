package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

func Load(path string) (*Config, error) {
	cfg := NewDefault()

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			if writeErr := WriteTemplate(path); writeErr != nil {
				return nil, fmt.Errorf("create default config: %w", writeErr)
			}
			cfg.DataDir = DefaultDataDir()
			cfg.ApplyEnvOverrides()
			cfg.ApplyInstallDefaults()
			cfg.ResolvePaths()
			return &cfg, nil
		}
		return nil, fmt.Errorf("read config: %w", err)
	}

	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}

	if cfg.DataDir == "" {
		cfg.DataDir = DefaultDataDir()
	}

	cfg.MergeProcessEnvDefaults()
	cfg.ApplyEnvOverrides()
	cfg.ApplyInstallDefaults()
	cfg.ResolvePaths()
	return &cfg, nil
}

func LoadWithDir(path, dataDir string) (*Config, error) {
	cfg := NewDefault()
	cfg.DataDir = dataDir

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			if writeErr := WriteTemplate(path); writeErr != nil {
				return nil, fmt.Errorf("create default config: %w", writeErr)
			}
			cfg.ApplyEnvOverrides()
			cfg.ApplyInstallDefaults()
			cfg.ResolvePaths()
			return &cfg, nil
		}
		return nil, fmt.Errorf("read config: %w", err)
	}

	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}

	if cfg.DataDir == "" {
		cfg.DataDir = dataDir
	}

	cfg.MergeProcessEnvDefaults()
	cfg.ApplyEnvOverrides()
	cfg.ApplyInstallDefaults()
	cfg.ResolvePaths()
	return &cfg, nil
}

func WriteTemplate(path string) error {
	cfg := NewDefault()
	cfg.DataDir = DefaultDataDir()
	data, err := yaml.Marshal(&cfg)
	if err != nil {
		return err
	}
	header := []byte("# SD Studio Server Configuration\n# Auto-generated default config\n\n")
	return os.WriteFile(path, append(header, data...), 0644)
}
