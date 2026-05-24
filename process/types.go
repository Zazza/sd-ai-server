package process

import (
	"context"
	"os"
	"os/exec"
	"time"

	"sd-studio-server/config"
)

type ProcessStatus struct {
	Name      string    `json:"name"`
	Status    string    `json:"status"`
	PID       int       `json:"pid,omitempty"`
	StartedAt time.Time `json:"started_at,omitempty"`
	Restarts  int       `json:"restarts"`
	Uptime    string    `json:"uptime,omitempty"`
	Category  string    `json:"category,omitempty"`
}

type ManagedProcess struct {
	Config        config.ProcessConfig
	Cmd           *exec.Cmd
	PID           int
	Status        string
	StartedAt     time.Time
	Restarts      int
	InstallFailed bool
	Managed       bool
	CancelFunc    context.CancelFunc
	LogBuf        *RingBuffer
	LogFile       *os.File
}
