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

	if err := cfg.Validate(); err != nil {
		return nil, err
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

	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	cfg.MergeProcessEnvDefaults()
	cfg.ApplyEnvOverrides()
	cfg.ApplyInstallDefaults()
	cfg.ResolvePaths()
	return &cfg, nil
}

func (c *Config) Validate() error {
	g := c.GPU
	if g.TotalBudgetMB < 0 {
		return fmt.Errorf("gpu.total_budget_mb must not be negative")
	}
	if g.ReserveMB < 0 {
		return fmt.Errorf("gpu.reserve_mb must not be negative")
	}
	if g.MaxWaitSeconds < 0 {
		return fmt.Errorf("gpu.max_wait_seconds must not be negative")
	}
	if g.LeaseTTLSeconds < 0 {
		return fmt.Errorf("gpu.lease_ttl_seconds must not be negative")
	}
	if g.SDDefaultWeightMB < 0 {
		return fmt.Errorf("gpu.sd_default_weight_mb must not be negative")
	}
	if g.SDOverheadMB < 0 {
		return fmt.Errorf("gpu.sd_overhead_mb must not be negative")
	}
	if g.LLMWeightMB < 0 {
		return fmt.Errorf("gpu.llm_weight_mb must not be negative")
	}
	return nil
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
