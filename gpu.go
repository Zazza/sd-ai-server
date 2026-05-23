package main

import (
	"context"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

type GPUInfo struct {
	Name       string  `json:"name,omitempty"`
	MemoryTotal int    `json:"memory_total_mb,omitempty"`
	MemoryUsed  int    `json:"memory_used_mb,omitempty"`
	MemoryFree  int    `json:"memory_free_mb,omitempty"`
	Utilization int    `json:"utilization_percent,omitempty"`
	Available   bool    `json:"available"`
}

type GPUMonitor struct {
	mu       sync.RWMutex
	info     GPUInfo
	binary   string
	OnUpdate func(GPUInfo)
}

func NewGPUMonitor() *GPUMonitor {
	gm := &GPUMonitor{
		binary: "nvidia-smi",
	}

	// On Windows, nvidia-smi is typically in a specific path
	if runtime.GOOS == "windows" {
		if path := os.Getenv("ProgramFiles"); path != "" {
			candidate := path + `\NVIDIA Corporation\NVSMI\nvidia-smi.exe`
			if _, err := os.Stat(candidate); err == nil {
				gm.binary = candidate
			}
		}
	}

	return gm
}

func (gm *GPUMonitor) Start(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	// Initial check
	gm.poll()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			gm.poll()
		}
	}
}

func (gm *GPUMonitor) poll() {
	cmd := exec.Command(gm.binary,
		"--query-gpu=name,memory.total,memory.used,memory.free,utilization.gpu",
		"--format=csv,noheader,nounits",
	)

	output, err := cmd.Output()
	if err != nil {
		gm.mu.Lock()
		gm.info = GPUInfo{Available: false}
		gm.mu.Unlock()
		return
	}

	line := strings.TrimSpace(string(output))
	if line == "" {
		gm.mu.Lock()
		gm.info = GPUInfo{Available: false}
		gm.mu.Unlock()
		return
	}

	// Parse CSV: name, memory_total, memory_used, memory_free, utilization
	fields := strings.Split(line, ",")
	if len(fields) < 5 {
		return
	}

	info := GPUInfo{
		Name:        strings.TrimSpace(fields[0]),
		MemoryTotal: atoi(strings.TrimSpace(fields[1])),
		MemoryUsed:  atoi(strings.TrimSpace(fields[2])),
		MemoryFree:  atoi(strings.TrimSpace(fields[3])),
		Utilization: atoi(strings.TrimSpace(fields[4])),
		Available:   true,
	}

	gm.mu.Lock()
	gm.info = info
	gm.mu.Unlock()

	if gm.OnUpdate != nil {
		gm.OnUpdate(info)
	}
}

func (gm *GPUMonitor) Info() GPUInfo {
	gm.mu.RLock()
	defer gm.mu.RUnlock()
	return gm.info
}

func atoi(s string) int {
	n, _ := strconv.Atoi(s)
	return n
}

func (gm *GPUMonitor) Detect() GPUInfo {
	gm.poll()
	return gm.Info()
}

func checkXformersCompat(pythonBinary, workDir string) bool {
	if pythonBinary == "" {
		return false
	}
	cmd := exec.Command(pythonBinary, "-c",
		"import xformers; import xformers._C; print('ok')",
	)
	if workDir != "" {
		cmd.Dir = workDir
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(out)) == "ok"
}

func forgeArgsForVRAM(vramMB int, useXformers bool) []string {
	base := []string{"launch.py", "--listen", "--api"}
	if useXformers {
		base = append(base, "--xformers")
	}
	switch {
	case vramMB <= 0:
		return append(base, "--medvram-sdxl")
	case vramMB < 6000:
		return append(base, "--lowvram")
	case vramMB < 8192:
		return append(base, "--medvram")
	default:
		return base
	}
}
