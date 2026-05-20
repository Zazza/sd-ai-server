package gpuproxy

type EndpointConfig struct {
	ListenAddr string `yaml:"listen_addr" json:"listen_addr"`
	TargetURL  string `yaml:"target_url" json:"target_url"`
}

type Config struct {
	Enabled      bool                      `yaml:"enabled" json:"enabled"`
	GPUSlots     int                       `yaml:"gpu_slots" json:"gpu_slots"`
	Endpoints    map[string]EndpointConfig `yaml:"endpoints" json:"endpoints"`
	StudioHeader string                    `yaml:"studio_header" json:"studio_header"`
}

func DefaultConfig() Config {
	return Config{
		Enabled:  false,
		GPUSlots: 1,
		Endpoints: map[string]EndpointConfig{
			"ollama": {
				ListenAddr: ":11434",
				TargetURL:  "http://localhost:11435",
			},
			"sd": {
				ListenAddr: ":7860",
				TargetURL:  "http://localhost:7861",
			},
		},
		StudioHeader: "X-SD-Studio",
	}
}
