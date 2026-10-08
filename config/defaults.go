package config

import (
	"os"
	"path/filepath"
	"runtime"
)

var InstallDefaultsBackends map[string]InstallConfig
var InstallDefaultsProcesses map[string]InstallConfig

func init() {
	InstallDefaultsBackends = map[string]InstallConfig{
		"forge": {
			Method:  InstallZip,
			URL:     "https://github.com/lllyasviel/stable-diffusion-webui-forge/archive/refs/heads/main.zip",
			Target:  "stable-diffusion-webui-forge",
			Version: "main",
		},
	}
	InstallDefaultsProcesses = map[string]InstallConfig{
		"ollama": {},
		"python": {
			Method: InstallTgz,
			URL:    pythonArchiveURL(),
			Target: "python",
		},
	}
}

func PythonArchiveURL() string {
	return pythonArchiveURL()
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

func DefaultForgeBinary() string {
	if runtime.GOOS == "windows" {
		return "python/python.exe"
	}
	return "python/bin/python3"
}

func DefaultDataDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "sd-studio-server"
	}
	return filepath.Join(home, "sd-studio-server")
}

func NewDefault() Config {
	return Config{
		Port:     8080,
		MDNS:     true,
		DataDir:  "",
		ActiveSD: "forge",
		GPU: GPUConfig{
			TotalBudgetMB:     0,
			ReserveMB:         500,
			MaxWaitSeconds:    120,
			LeaseTTLSeconds:   90,
			SDDefaultWeightMB: 11000,
			SDOverheadMB:      4500,
			LLMWeightMB:       11000,
		},
		Processes: map[string]ProcessConfig{
			"python": {
				Name:      "Python 3.10",
				AutoStart: false,
				Category:  "utility",
			},
			"sd": {
				Name:       "Stable Diffusion",
				HealthURL:  "http://localhost:7860/sdapi/v1/options",
				TargetURL:  "http://localhost:7860",
				ProxyPath:  "/api/sd/",
				AutoStart:  true,
				Restart:    true,
				MaxRestart: 5,
				Env: map[string]string{
					"PYTORCH_CUDA_ALLOC_CONF":         "expandable_segments:True",
					"GIT_DISCOVERY_ACROSS_FILESYSTEM": "1",
					"GIT_CONFIG_COUNT":                "1",
					"GIT_CONFIG_KEY_0":                "safe.directory",
					"GIT_CONFIG_VALUE_0":              "*",
				},
			},
			"ollama": {
				Name:   "Ollama",
				Binary: "ollama",
				Args:   []string{"serve"},
				Env: map[string]string{
					"OLLAMA_HOST":         "0.0.0.0:11434",
					"OLLAMA_MODELS":       "models/ollama",
					"OLLAMA_KEEP_ALIVE":   "24h",
					"OLLAMA_NUM_PARALLEL": "2",
					"OLLAMA_NUM_GPU":      "-1",
					"OLLAMA_DEBUG":        "1",
				},
				HealthURL:  "http://localhost:11434/api/tags",
				TargetURL:  "http://localhost:11434",
				ProxyPath:  "/api/llm/",
				AutoStart:  true,
				Restart:    true,
				MaxRestart: 5,
				Install:    InstallConfig{},
			},
		},
		Backends: map[string]BackendConfig{
			"forge": {
				Name:         "Stable Diffusion Forge",
				ProcessKey:   "sd",
				Binary:       DefaultForgeBinary(),
				Args:         []string{"launch.py", "--listen", "--api", "--xformers", "--medvram-sdxl"},
				AutoOptimize: true,
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
