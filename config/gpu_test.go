package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func writeConfigFile(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "server-config.yaml")
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
	return path
}

func TestGPUConfig_ResolveBudget(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		gpu      GPUConfig
		detected int
		want     int
	}{
		{"explicit total wins", GPUConfig{TotalBudgetMB: 8000, ReserveMB: 500}, 16000, 8000},
		{"auto detected minus reserve", GPUConfig{ReserveMB: 500}, 16000, 15500},
		{"auto zero detected", GPUConfig{ReserveMB: 500}, 0, 0},
		{"auto negative detected", GPUConfig{ReserveMB: 500}, -100, 0},
		{"auto below reserve disables", GPUConfig{ReserveMB: 500}, 300, -200},
		{"auto no reserve", GPUConfig{}, 16000, 16000},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, tt.gpu.ResolveBudget(tt.detected))
		})
	}
}

func TestConfig_ValidateGPUNegativeFields(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		mutate func(*GPUConfig)
	}{
		{"total_budget_mb", func(g *GPUConfig) { g.TotalBudgetMB = -1 }},
		{"reserve_mb", func(g *GPUConfig) { g.ReserveMB = -1 }},
		{"max_wait_seconds", func(g *GPUConfig) { g.MaxWaitSeconds = -1 }},
		{"lease_ttl_seconds", func(g *GPUConfig) { g.LeaseTTLSeconds = -1 }},
		{"sd_default_weight_mb", func(g *GPUConfig) { g.SDDefaultWeightMB = -1 }},
		{"sd_overhead_mb", func(g *GPUConfig) { g.SDOverheadMB = -1 }},
		{"llm_weight_mb", func(g *GPUConfig) { g.LLMWeightMB = -1 }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			c := NewDefault()
			tt.mutate(&c.GPU)
			err := c.Validate()
			require.Error(t, err)
			assert.Contains(t, err.Error(), "gpu.")
		})
	}
}

func TestConfig_ValidateGPUDefaults(t *testing.T) {
	t.Parallel()
	c := NewDefault()
	assert.NoError(t, c.Validate())
}

func TestLoad_GPUValidSectionKeepsDefaults(t *testing.T) {
	t.Parallel()
	path := writeConfigFile(t, "gpu:\n  reserve_mb: 256\n")
	cfg, err := Load(path)
	require.NoError(t, err)

	assert.Equal(t, 256, cfg.GPU.ReserveMB)
	assert.Equal(t, 0, cfg.GPU.TotalBudgetMB)
	assert.Equal(t, 120, cfg.GPU.MaxWaitSeconds)
	assert.Equal(t, 90, cfg.GPU.LeaseTTLSeconds)
	assert.Equal(t, 11000, cfg.GPU.SDDefaultWeightMB)
	assert.Equal(t, 4500, cfg.GPU.SDOverheadMB)
	assert.Equal(t, 11000, cfg.GPU.LLMWeightMB)
}

func TestLoad_GPUFullOverride(t *testing.T) {
	t.Parallel()
	yaml := "gpu:\n" +
		"  total_budget_mb: 12000\n" +
		"  reserve_mb: 256\n" +
		"  max_wait_seconds: 30\n" +
		"  lease_ttl_seconds: 45\n" +
		"  sd_default_weight_mb: 6500\n" +
		"  sd_overhead_mb: 500\n" +
		"  llm_weight_mb: 8000\n"
	cfg, err := Load(writeConfigFile(t, yaml))
	require.NoError(t, err)

	assert.Equal(t, 12000, cfg.GPU.TotalBudgetMB)
	assert.Equal(t, 256, cfg.GPU.ReserveMB)
	assert.Equal(t, 30, cfg.GPU.MaxWaitSeconds)
	assert.Equal(t, 45, cfg.GPU.LeaseTTLSeconds)
	assert.Equal(t, 6500, cfg.GPU.SDDefaultWeightMB)
	assert.Equal(t, 500, cfg.GPU.SDOverheadMB)
	assert.Equal(t, 8000, cfg.GPU.LLMWeightMB)
	assert.Equal(t, 12000, cfg.GPU.ResolveBudget(16000))
}

func TestLoad_GPUNegativeFieldsRejected(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		yaml string
		key  string
	}{
		{"total_budget_mb", "gpu:\n  total_budget_mb: -1\n", "gpu.total_budget_mb"},
		{"reserve_mb", "gpu:\n  reserve_mb: -2\n", "gpu.reserve_mb"},
		{"max_wait_seconds", "gpu:\n  max_wait_seconds: -3\n", "gpu.max_wait_seconds"},
		{"lease_ttl_seconds", "gpu:\n  lease_ttl_seconds: -4\n", "gpu.lease_ttl_seconds"},
		{"sd_default_weight_mb", "gpu:\n  sd_default_weight_mb: -5\n", "gpu.sd_default_weight_mb"},
		{"sd_overhead_mb", "gpu:\n  sd_overhead_mb: -6\n", "gpu.sd_overhead_mb"},
		{"llm_weight_mb", "gpu:\n  llm_weight_mb: -7\n", "gpu.llm_weight_mb"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cfg, err := Load(writeConfigFile(t, tt.yaml))
			require.Error(t, err)
			assert.Nil(t, cfg)
			assert.Contains(t, err.Error(), tt.key)
		})
	}
}
