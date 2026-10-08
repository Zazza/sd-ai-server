package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"sd-studio-server/config"
	sdgpu "sd-studio-server/gpu"
	sdhealth "sd-studio-server/health"
	"sd-studio-server/installer"
	"sd-studio-server/process"
)

func newTestHandlers() *Handlers {
	cfg := &config.Config{}
	inst := installer.NewInstaller(cfg)
	return NewHandlers(
		process.NewProcessManager(cfg, inst, ""),
		sdhealth.NewHealthMonitor(cfg),
		sdgpu.NewGPUMonitor(),
		cfg,
		inst,
	)
}

func TestHostSysStats(t *testing.T) {
	s := hostSysStats()

	assert.Greater(t, s.RAMTotal, uint64(0), "RAMTotal must be reported by gopsutil")
	assert.GreaterOrEqual(t, s.RAMUsed, uint64(0))
	assert.LessOrEqual(t, s.RAMUsed, s.RAMTotal)
	assert.GreaterOrEqual(t, s.RAMUsage, float64(0))
	assert.LessOrEqual(t, s.RAMUsage, float64(100))
	assert.GreaterOrEqual(t, s.CPUPercent, float64(0))
}

func TestSysJSON_FieldNames(t *testing.T) {
	b, err := json.Marshal(sysJSON{CPUPercent: 11.5, RAMUsage: 40.5, RAMUsed: 100, RAMTotal: 200})
	require.NoError(t, err)

	s := string(b)
	for _, key := range []string{"cpu_percent", "ram_usage", "ram_used", "ram_total"} {
		assert.Contains(t, s, key)
	}
}

func TestHandleStatus_SysBlock(t *testing.T) {
	h := newTestHandlers()
	srv := httptest.NewServer(http.HandlerFunc(h.handleStatus))
	t.Cleanup(srv.Close)

	resp, err := http.Get(srv.URL)
	require.NoError(t, err)
	t.Cleanup(func() { resp.Body.Close() })
	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "application/json", resp.Header.Get("Content-Type"))

	var body struct {
		Sys struct {
			CPUPercent float64 `json:"cpu_percent"`
			RAMUsage   float64 `json:"ram_usage"`
			RAMUsed    uint64  `json:"ram_used"`
			RAMTotal   uint64  `json:"ram_total"`
		} `json:"sys"`
		Processes map[string]interface{} `json:"processes"`
		Health    map[string]interface{} `json:"health"`
		GPU       map[string]interface{} `json:"gpu"`
		Installs  map[string]interface{} `json:"installs"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))

	assert.Greater(t, body.Sys.RAMTotal, uint64(0), "sys.ram_total must be present and positive")
	assert.Greater(t, body.Sys.RAMUsed, uint64(0))
	assert.LessOrEqual(t, body.Sys.RAMUsed, body.Sys.RAMTotal)
	assert.GreaterOrEqual(t, body.Sys.CPUPercent, float64(0))
	assert.NotNil(t, body.Processes)
	assert.NotNil(t, body.Health)
	assert.NotNil(t, body.GPU)
	assert.NotNil(t, body.Installs)
}

func TestHandleStatus_MethodNotAllowed(t *testing.T) {
	h := newTestHandlers()
	req := httptest.NewRequest(http.MethodPost, "/api/server/status", nil)
	rec := httptest.NewRecorder()

	h.handleStatus(rec, req)

	assert.Equal(t, http.StatusMethodNotAllowed, rec.Code)
}
