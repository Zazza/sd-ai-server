package main

import (
	"fmt"
	"os"
	"strconv"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Port      int                       `yaml:"port"`
	MDNS      bool                      `yaml:"mdns"`
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

var defaultConfig = Config{
	Port:     8080,
	MDNS:     true,
	ActiveSD: "forge",
	Processes: map[string]ProcessConfig{
		"sd": {
			Name:       "Stable Diffusion",
			HealthURL:  "http://localhost:7860/sdapi/v1/options",
			TargetURL:  "http://localhost:7860",
			ProxyPath:  "/api/sd/",
			AutoStart:  true,
			Restart:    true,
			MaxRestart: 5,
		},
		"ollama": {
			Name:       "Ollama",
			Binary:     "ollama",
			Args:       []string{"serve"},
			HealthURL:  "http://localhost:11434/api/tags",
			TargetURL:  "http://localhost:11434",
			ProxyPath:  "/api/llm/",
			AutoStart:  true,
			Restart:    true,
			MaxRestart: 5,
			Install: InstallConfig{
				Method: InstallBinary,
				URL:    "https://github.com/ollama/ollama/releases/download/v0.6.8/ollama-{os}-{arch}",
				Target: "/usr/local/bin/ollama",
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
			Binary:       "./stable-diffusion-webui-forge/webui.sh",
			Args:         []string{"--listen", "--api", "--xformers"},
			WorkDir:      "./stable-diffusion-webui-forge",
			ModelsDir:    "./stable-diffusion-webui-forge/models/Stable-diffusion",
			LoraDir:      "./stable-diffusion-webui-forge/models/Lora",
			VaeDir:       "./stable-diffusion-webui-forge/models/VAE",
			EmbeddingDir: "./stable-diffusion-webui-forge/embeddings",
			Install: InstallConfig{
				Method:  InstallZip,
				URL:     "https://github.com/lllyasviel/stable-diffusion-webui-forge/archive/refs/heads/main.zip",
				Target:  "./stable-diffusion-webui-forge",
				Version: "main",
			},
		},
		"a1111": {
			Name:         "Stable Diffusion A1111",
			ProcessKey:   "sd",
			Binary:       "./stable-diffusion-webui/webui.sh",
			Args:         []string{"--listen", "--api", "--xformers"},
			WorkDir:      "./stable-diffusion-webui",
			ModelsDir:    "./stable-diffusion-webui/models/Stable-diffusion",
			LoraDir:      "./stable-diffusion-webui/models/Lora",
			VaeDir:       "./stable-diffusion-webui/models/VAE",
			EmbeddingDir: "./stable-diffusion-webui/embeddings",
			Install: InstallConfig{
				Method:  InstallZip,
				URL:     "https://github.com/AUTOMATIC1111/stable-diffusion-webui/archive/refs/heads/master.zip",
				Target:  "./stable-diffusion-webui",
				Version: "master",
			},
		},
	},
}

var installDefaultsBackends map[string]InstallConfig
var installDefaultsProcesses map[string]InstallConfig

func init() {
	installDefaultsBackends = map[string]InstallConfig{
		"forge": {
			Method:  InstallZip,
			URL:     "https://github.com/lllyasviel/stable-diffusion-webui-forge/archive/refs/heads/main.zip",
			Target:  "./stable-diffusion-webui-forge",
			Version: "main",
		},
		"a1111": {
			Method:  InstallZip,
			URL:     "https://github.com/AUTOMATIC1111/stable-diffusion-webui/archive/refs/heads/master.zip",
			Target:  "./stable-diffusion-webui",
			Version: "master",
		},
	}
	installDefaultsProcesses = map[string]InstallConfig{
		"ollama": {
			Method: InstallBinary,
			URL:    "https://github.com/ollama/ollama/releases/download/v0.6.8/ollama-{os}-{arch}",
			Target: "/usr/local/bin/ollama",
		},
		"rembg": {
			Method: InstallPip,
			URL:    "rembg",
			Target: "rembg",
		},
	}
}

func Load(path string) (*Config, error) {
	cfg := defaultConfig

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			if writeErr := WriteTemplate(path); writeErr != nil {
				return nil, fmt.Errorf("create default config: %w", writeErr)
			}
			cfg.applyEnvOverrides()
			cfg.applyBackendToProcess()
			return &cfg, nil
		}
		return nil, fmt.Errorf("read config: %w", err)
	}

	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}

	cfg.applyEnvOverrides()
	cfg.applyBackendToProcess()
	cfg.applyInstallDefaults()
	return &cfg, nil
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
	data, err := yaml.Marshal(&defaultConfig)
	if err != nil {
		return err
	}
	header := []byte("# SD Studio Server Configuration\n# Auto-generated default config\n\n")
	return os.WriteFile(path, append(header, data...), 0644)
}
