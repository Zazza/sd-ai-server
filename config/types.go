package config

import (
	"sd-studio-server/gpuproxy"
)

type InstallMethod string

const (
	InstallZip     InstallMethod = "zip"
	InstallBinary  InstallMethod = "binary"
	InstallPip     InstallMethod = "pip"
	InstallArchive InstallMethod = "archive"
	InstallTgz     InstallMethod = "tgz"
)

type Config struct {
	Port           int                       `yaml:"port"`
	MDNS           bool                      `yaml:"mdns"`
	DataDir        string                    `yaml:"data_dir"`
	ActiveSD       string                    `yaml:"active_sd"`
	DetectedVRAMMB int                       `yaml:"detected_vram_mb"`
	Processes      map[string]ProcessConfig  `yaml:"processes"`
	Backends       map[string]BackendConfig  `yaml:"backends"`
	Proxy          gpuproxy.Config           `yaml:"proxy"`
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
	Category   string            `yaml:"category"`
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
	AutoOptimize bool          `yaml:"auto_optimize"`
	Install      InstallConfig `yaml:"install"`
}

func (c *Config) GetActiveBackend() *BackendConfig {
	b, ok := c.Backends[c.ActiveSD]
	if !ok {
		return nil
	}
	return &b
}
