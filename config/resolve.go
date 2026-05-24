package config

import (
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

func (c *Config) MergeProcessEnvDefaults() {
	defaults := NewDefault()
	for key, defProc := range defaults.Processes {
		curProc, ok := c.Processes[key]
		if !ok {
			continue
		}
		if curProc.Env == nil {
			curProc.Env = make(map[string]string)
		}
		for k, v := range defProc.Env {
			if _, exists := curProc.Env[k]; !exists {
				curProc.Env[k] = v
			}
		}
		if curProc.Category == "" && defProc.Category != "" {
			curProc.Category = defProc.Category
		}
		c.Processes[key] = curProc
	}
}

func (c *Config) ResolvePaths() {
	for key, pc := range c.Processes {
		pc.Binary = resolveRelPath(c.DataDir, pc.Binary)
		pc.WorkDir = resolveDirPath(c.DataDir, pc.WorkDir)
		pc.Install.Target = resolveInstallTarget(c.DataDir, pc.Install)
		for ek, ev := range pc.Env {
			pc.Env[ek] = resolveRelPath(c.DataDir, ev)
		}
		c.Processes[key] = pc
	}
	for key, bc := range c.Backends {
		bc.Binary = resolveRelPath(c.DataDir, bc.Binary)
		bc.WorkDir = resolveDirPath(c.DataDir, bc.WorkDir)
		bc.ModelsDir = resolveDirPath(c.DataDir, bc.ModelsDir)
		bc.LoraDir = resolveDirPath(c.DataDir, bc.LoraDir)
		bc.VaeDir = resolveDirPath(c.DataDir, bc.VaeDir)
		bc.EmbeddingDir = resolveDirPath(c.DataDir, bc.EmbeddingDir)
		bc.Install.Target = resolveInstallTarget(c.DataDir, bc.Install)
		c.Backends[key] = bc
	}
}

func (c *Config) ApplyEnvOverrides() {
	if v := os.Getenv("SD_PORT"); v != "" {
		if p, err := strconv.Atoi(v); err == nil {
			c.Port = p
		}
	}
	if v := os.Getenv("SD_ACTIVE_BACKEND"); v != "" {
		c.ActiveSD = v
	}
	if v := os.Getenv("SD_DATA_DIR"); v != "" {
		c.DataDir = v
	}
}

func (c *Config) ApplyInstallDefaults() {
	for key, def := range InstallDefaultsBackends {
		if bc, ok := c.Backends[key]; ok && bc.Install.Method == "" {
			bc.Install = def
			c.Backends[key] = bc
		}
	}
	for key, def := range InstallDefaultsProcesses {
		if pc, ok := c.Processes[key]; ok && pc.Install.Method == "" {
			pc.Install = def
			c.Processes[key] = pc
		}
	}
}

type GPUOptimizer interface {
	DetectGPU() int
	CheckXformersCompat(binary, workDir string) bool
	ForgeArgsForVRAM(vramMB int, xformers bool) []string
}

func (c *Config) ApplyBackendToProcess(gpu GPUOptimizer) {
	backend, ok := c.Backends[c.ActiveSD]
	if !ok {
		return
	}
	proc, ok := c.Processes[backend.ProcessKey]
	if !ok {
		return
	}
	proc.Binary = backend.Binary
	if backend.AutoOptimize {
		vram := gpu.DetectGPU()
		c.DetectedVRAMMB = vram
		useXformers := gpu.CheckXformersCompat(proc.Binary, backend.WorkDir)
		proc.Args = gpu.ForgeArgsForVRAM(vram, useXformers)
	} else {
		proc.Args = backend.Args
	}
	proc.WorkDir = backend.WorkDir
	if proc.Install.Method == "" && backend.Install.Method != "" {
		proc.Install = backend.Install
	}
	c.Processes[backend.ProcessKey] = proc
}

func (c *Config) ApplyProxyPorts(logf func(string, ...interface{})) {
	if !c.Proxy.Enabled {
		return
	}

	for endpointName, epCfg := range c.Proxy.Endpoints {
		realBackend, err := url.Parse(epCfg.TargetURL)
		if err != nil {
			continue
		}

		_, portStr, err := net.SplitHostPort(realBackend.Host)
		if err != nil {
			continue
		}

		proxyAddr := "http://localhost" + epCfg.ListenAddr

		for procKey, pc := range c.Processes {
			if pc.ProxyPath == "" {
				continue
			}

			matched := false
			switch {
			case endpointName == "ollama" && pc.ProxyPath == "/api/llm/":
				matched = true
			case endpointName == "sd" && pc.ProxyPath == "/api/sd/":
				matched = true
			}
			if !matched {
				continue
			}

			pc.TargetURL = proxyAddr
			pc.HealthURL = "http://" + realBackend.Host

			switch endpointName {
			case "ollama":
				pc.HealthURL = pc.HealthURL + "/api/tags"
				if pc.Env == nil {
					pc.Env = make(map[string]string)
				}
				pc.Env["OLLAMA_HOST"] = realBackend.Host
			case "sd":
				pc.HealthURL = pc.HealthURL + "/sdapi/v1/options"
				hasPort := false
				for _, a := range pc.Args {
					if a == "--port" {
						hasPort = true
						break
					}
				}
				if !hasPort {
					pc.Args = append(pc.Args, "--port", portStr)
				}
			}

			c.Processes[procKey] = pc
		}
	}
}

func resolveInstallTarget(baseDir string, ic InstallConfig) string {
	if ic.Method == InstallPip {
		return ic.Target
	}
	if ic.Target == "" || filepath.IsAbs(ic.Target) {
		return ic.Target
	}
	return filepath.Join(baseDir, ic.Target)
}

func resolveRelPath(baseDir, p string) string {
	if p == "" || filepath.IsAbs(p) || !strings.ContainsRune(p, '/') {
		return p
	}
	return filepath.Join(baseDir, p)
}

func resolveDirPath(baseDir, p string) string {
	if p == "" || filepath.IsAbs(p) {
		return p
	}
	return filepath.Join(baseDir, p)
}
