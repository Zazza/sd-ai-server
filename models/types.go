package models

type ModelInfo struct {
	Name      string `json:"name"`
	Size      int64  `json:"size"`
	Extension string `json:"extension"`
}

type LLMModelInfo struct {
	Name string `json:"name"`
	Size string `json:"size"`
}
