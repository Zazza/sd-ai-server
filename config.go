package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Port      int                       `yaml:"port"`
	MDNS      bool                      `yaml:"mdns"`
	DataDir   string                    `yaml:"data_dir"`
	ActiveSD  string                    `yaml:"active_sd"`
	Processes map[string]ProcessConfig  `yaml:"processes"`
	Backends  map[string]BackendConfig  `yaml:"backends"`
}

type InstallConfig struct {
	Method  InstallMethod `yaml:"method"`
	URL     string        `yaml:"url"`
	Target  string        `yaml:"target"`
	Version string        `yaml:"version"`
}

type ProcessConfig struct {
	Name       string            `yaml:"name"`
	Binary     string            `yaml:"binary"`
	Args       []string          `yaml:"args"`
	Env        map[string]string `yaml:"env"`
	WorkDir    string            `yaml:"workdir"`
	HealthURL  string            `yaml:"health_url"`
	TargetURL  string            `yaml:"target_url"`
	ProxyPath  string            `yaml:"proxy_path"`
	AutoStart  bool              `yaml:"autostart"`
	Restart    bool              `yaml:"restart"`
	MaxRestart int               `yaml:"max_restart"`
	Install    InstallConfig     `yaml:"install"`
}

type BackendConfig struct {
	Name         string        `yaml:"name"`
	ProcessKey   string        `yaml:"process_key"`
	Binary       string        `yaml:"binary"`
	Args         []string      `yaml:"args"`
	WorkDir      string        `yaml:"workdir"`
	ModelsDir    string        `yaml:"models_dir"`
	LoraDir      string        `yaml:"lora_dir"`
	VaeDir       string        `yaml:"vae_dir"`
	EmbeddingDir string        `yaml:"embedding_dir"`
	Install      InstallConfig `yaml:"install"`
}

func pythonArchiveURL() string {
	switch runtime.GOOS + "/" + runtime.GOARCH {
	case "darwin/arm64":
		return "https://github.com/astral-sh/python-build-standalone/releases/download/20241016/cpython-3.10.15%2B20241016-aarch64-apple-darwin-install_only.tar.gz"
	case "darwin/amd64":
		return "https://github.com/astral-sh/python-build-standalone/releases/download/20241016/cpython-3.10.15%2B20241016-x86_64-apple-darwin-install_only.tar.gz"
	case "linux/amd64":
		return "https://github.com/astral-sh/python-build-standalone/releases/download/20241016/cpython-3.10.15%2B20241016-x86_64-unknown-linux-gnu-install_only.tar.gz"
	case "linux/arm64":
		return "https://github.com/astral-sh/python-build-standalone/releases/download/20241016/cpython-3.10.15%2B20241016-aarch64-unknown-linux-gnu-install_only.tar.gz"
	case "windows/amd64":
		return "https://github.com/astral-sh/python-build-standalone/releases/download/20241016/cpython-3.10.15%2B20241016-x86_64-pc-windows-msvc-shared-install_only.tar.gz"
	case "windows/arm64":
		return "https://github.com/astral-sh/python-build-standalone/releases/download/20241016/cpython-3.10.15%2B20241016-aarch64-pc-windows-msvc-shared-install_only.tar.gz"
	}
	return ""
}

func defaultForgeBinary() string {
	if runtime.GOOS == "windows" {
		return "python/python.exe"
	}
	return "python/bin/python3"
}

func ollamaArchiveURL() string {
	switch runtime.GOOS {
	case "darwin":
		return "https://github.com/ollama/ollama/releases/download/v0.23.1/ollama-darwin.tgz"
	case "linux":
		return fmt.Sprintf("https://github.com/ollama/ollama/releases/download/v0.23.1/ollama-linux-%s.tar.zst", runtime.GOARCH)
	case "windows":
		return fmt.Sprintf("https://github.com/ollama/ollama/releases/download/v0.23.1/ollama-windows-%s.zip", runtime.GOARCH)
	}
	return ""
}

func defaultDataDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "sd-studio-server"
	}
	return filepath.Join(home, "sd-studio-server")
}

func newDefaultConfig() Config {
	return Config{
		Port:     8080,
		MDNS:     true,
		DataDir:  "",
		ActiveSD: "forge",
		Processes: map[string]ProcessConfig{
			"python": {
				Name:      "Python 3.10",
				AutoStart: false,
			},
			"sd": {
				Name:       "Stable Diffusion",
				HealthURL:  "http://localhost:7860/sdapi/v1/options",
				TargetURL:  "http://localhost:7860",
				ProxyPath:  "/api/sd/",
				AutoStart:  true,
				Restart:    true,
				MaxRestart: 5,
				Env:        map[string]string{},
			},
			"ollama": {
				Name:       "Ollama",
				Binary:     "bin/ollama",
				Args:       []string{"serve"},
				Env: map[string]string{
					"OLLAMA_MODELS":       "models/ollama",
					"OLLAMA_KEEP_ALIVE":   "24h",
					"OLLAMA_NUM_PARALLEL": "1",
				},
				HealthURL:  "http://localhost:11434/api/tags",
				TargetURL:  "http://localhost:11434",
				ProxyPath:  "/api/llm/",
				AutoStart:  true,
				Restart:    true,
				MaxRestart: 5,
				Install: InstallConfig{
					Method: InstallArchive,
					URL:    ollamaArchiveURL(),
					Target: "bin/ollama",
				},
			},
			"rembg": {
				Name:       "Rembg",
				Binary:     "rembg",
				Args:       []string{"s", "--host", "0.0.0.0", "--port", "7000"},
				HealthURL:  "http://localhost:7000/api",
				TargetURL:  "http://localhost:7000",
				ProxyPath:  "/api/rembg/",
				AutoStart:  false,
				Restart:    true,
				MaxRestart: 3,
				Install: InstallConfig{
					Method: InstallPip,
					URL:    "rembg",
					Target: "rembg",
				},
			},
		},
		Backends: map[string]BackendConfig{
			"forge": {
				Name:         "Stable Diffusion Forge",
				ProcessKey:   "sd",
				Binary:       defaultForgeBinary(),
				Args:         []string{"launch.py", "--listen", "--api", "--xformers", "--medvram-sdxl"},
				WorkDir:      "stable-diffusion-webui-forge",
				ModelsDir:    "stable-diffusion-webui-forge/models/Stable-diffusion",
				LoraDir:      "stable-diffusion-webui-forge/models/Lora",
				VaeDir:       "stable-diffusion-webui-forge/models/VAE",
				EmbeddingDir: "stable-diffusion-webui-forge/embeddings",
				Install: InstallConfig{
					Method:  InstallZip,
					URL:     "https://github.com/lllyasviel/stable-diffusion-webui-forge/archive/refs/heads/main.zip",
					Target:  "stable-diffusion-webui-forge",
					Version: "main",
				},
			},
		},
	}
}

var installDefaultsBackends map[string]InstallConfig
var installDefaultsProcesses map[string]InstallConfig

func init() {
	installDefaultsBackends = map[string]InstallConfig{
		"forge": {
			Method:  InstallZip,
			URL:     "https://github.com/lllyasviel/stable-diffusion-webui-forge/archive/refs/heads/main.zip",
			Target:  "stable-diffusion-webui-forge",
			Version: "main",
		},
	}
	installDefaultsProcesses = map[string]InstallConfig{
		"ollama": {
			Method: InstallArchive,
			URL:    ollamaArchiveURL(),
			Target: "bin/ollama",
		},
		"rembg": {
			Method: InstallPip,
			URL:    "rembg",
			Target: "rembg",
		},
		"python": {
			Method: InstallTgz,
			URL:    pythonArchiveURL(),
			Target: "python",
		},
	}
}

func Load(path string) (*Config, error) {
	cfg := newDefaultConfig()

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			if writeErr := WriteTemplate(path); writeErr != nil {
				return nil, fmt.Errorf("create default config: %w", writeErr)
			}
			cfg.DataDir = defaultDataDir()
			cfg.applyEnvOverrides()
			cfg.applyBackendToProcess()
			cfg.applyInstallDefaults()
			cfg.resolvePaths()
			return &cfg, nil
		}
		return nil, fmt.Errorf("read config: %w", err)
	}

	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}

	if cfg.DataDir == "" {
		cfg.DataDir = defaultDataDir()
	}

	cfg.applyEnvOverrides()
	cfg.applyBackendToProcess()
	cfg.applyInstallDefaults()
	cfg.resolvePaths()
	return &cfg, nil
}

func LoadWithDir(path, dataDir string) (*Config, error) {
	cfg := newDefaultConfig()
	cfg.DataDir = dataDir

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			if writeErr := WriteTemplate(path); writeErr != nil {
				return nil, fmt.Errorf("create default config: %w", writeErr)
			}
			cfg.applyEnvOverrides()
			cfg.applyBackendToProcess()
			cfg.applyInstallDefaults()
			cfg.resolvePaths()
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

	cfg.applyEnvOverrides()
	cfg.applyBackendToProcess()
	cfg.applyInstallDefaults()
	cfg.resolvePaths()
	return &cfg, nil
}

func (c *Config) resolvePaths() {
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

func (c *Config) applyEnvOverrides() {
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

func (c *Config) applyBackendToProcess() {
	backend, ok := c.Backends[c.ActiveSD]
	if !ok {
		return
	}
	proc, ok := c.Processes[backend.ProcessKey]
	if !ok {
		return
	}
	proc.Binary = backend.Binary
	proc.Args = backend.Args
	proc.WorkDir = backend.WorkDir
	if proc.Install.Method == "" && backend.Install.Method != "" {
		proc.Install = backend.Install
	}
	c.Processes[backend.ProcessKey] = proc
}

func (c *Config) applyInstallDefaults() {
	for key, def := range installDefaultsBackends {
		if bc, ok := c.Backends[key]; ok && bc.Install.Method == "" {
			bc.Install = def
			c.Backends[key] = bc
		}
	}
	for key, def := range installDefaultsProcesses {
		if pc, ok := c.Processes[key]; ok && pc.Install.Method == "" {
			pc.Install = def
			c.Processes[key] = pc
		}
	}
}

func (c *Config) GetActiveBackend() *BackendConfig {
	b, ok := c.Backends[c.ActiveSD]
	if !ok {
		return nil
	}
	return &b
}

func WriteTemplate(path string) error {
	cfg := newDefaultConfig()
	cfg.DataDir = defaultDataDir()
	data, err := yaml.Marshal(&cfg)
	if err != nil {
		return err
	}
	header := []byte("# SD Studio Server Configuration\n# Auto-generated default config\n\n")
	return os.WriteFile(path, append(header, data...), 0644)
}
