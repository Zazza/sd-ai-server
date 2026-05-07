package main

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"time"
)

type HealthResult struct {
	Healthy   bool      `json:"healthy"`
	LatencyMs int64     `json:"latency_ms"`
	CheckedAt time.Time `json:"checked_at"`
	Error     string    `json:"error,omitempty"`
}

type HealthMonitor struct {
	mu      sync.RWMutex
	results map[string]HealthResult
	config  *Config
	client  *http.Client
}

func NewHealthMonitor(cfg *Config) *HealthMonitor {
	return &HealthMonitor{
		results: make(map[string]HealthResult),
		config:  cfg,
		client:  &http.Client{Timeout: 5 * time.Second},
	}
}

func (hm *HealthMonitor) Start(ctx context.Context) {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	// Run initial check immediately
	hm.checkAll()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			hm.checkAll()
		}
	}
}

func (hm *HealthMonitor) checkAll() {
	for name, pc := range hm.config.Processes {
		if pc.HealthURL == "" {
			continue
		}
		go hm.check(name, pc.HealthURL)
	}
}

func (hm *HealthMonitor) check(name, url string) {
	start := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		hm.setResult(name, HealthResult{
			Healthy:   false,
			LatencyMs: 0,
			CheckedAt: time.Now(),
			Error:     err.Error(),
		})
		return
	}

	resp, err := hm.client.Do(req)
	latency := time.Since(start).Milliseconds()

	if err != nil {
		hm.setResult(name, HealthResult{
			Healthy:   false,
			LatencyMs: latency,
			CheckedAt: time.Now(),
			Error:     err.Error(),
		})
		return
	}
	resp.Body.Close()

	healthy := resp.StatusCode == http.StatusOK
	result := HealthResult{
		Healthy:   healthy,
		LatencyMs: latency,
		CheckedAt: time.Now(),
	}
	if !healthy {
		result.Error = fmt.Sprintf("status %d", resp.StatusCode)
	}
	hm.setResult(name, result)
}

func (hm *HealthMonitor) setResult(name string, r HealthResult) {
	hm.mu.Lock()
	hm.results[name] = r
	hm.mu.Unlock()
}

func (hm *HealthMonitor) Results() map[string]HealthResult {
	hm.mu.RLock()
	defer hm.mu.RUnlock()

	// Return a copy
	result := make(map[string]HealthResult, len(hm.results))
	for k, v := range hm.results {
		result[k] = v
	}
	return result
}
