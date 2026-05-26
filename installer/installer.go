package installer

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"sd-studio-server/config"
	"sd-studio-server/process"
)

type InstallStatus struct {
	Key        string `json:"key"`
	Installed  bool   `json:"installed"`
	Installing bool   `json:"installing"`
	Progress   string `json:"progress"`
	Error      string `json:"error,omitempty"`
	Version    string `json:"version,omitempty"`
}

type Installer struct {
	config     *config.Config
	mu         sync.RWMutex
	statuses   map[string]*InstallStatus
	logs       map[string]*process.RingBuffer
	OnProgress func(key, progress string)
}

var _ process.Installer = (*Installer)(nil)

func (inst *Installer) resolveTarget(target string) string {
	if filepath.IsAbs(target) {
		return target
	}
	return filepath.Join(inst.config.DataDir, target)
}

func NewInstaller(cfg *config.Config) *Installer {
	inst := &Installer{
		config:   cfg,
		statuses: make(map[string]*InstallStatus),
		logs:     make(map[string]*process.RingBuffer),
	}

	for key, bc := range cfg.Backends {
		if bc.Install.Method != "" {
			s := &InstallStatus{Key: key}
			s.Installed = inst.checkInstalled(bc.Install)
			if s.Installed {
				s.Version = bc.Install.Version
			}
			inst.statuses[key] = s
			inst.logs[key] = process.NewRingBuffer(process.RingBufferSize)
		}
	}

	for key, pc := range cfg.Processes {
		if pc.Install.Method != "" {
			if _, exists := inst.statuses[key]; exists {
				continue
			}
			s := &InstallStatus{Key: key}
			s.Installed = inst.checkInstalled(pc.Install)
			if s.Installed {
					s.Version = pc.Install.Version
				}
			inst.statuses[key] = s
			inst.logs[key] = process.NewRingBuffer(process.RingBufferSize)
		}
	}

	return inst
}

func (inst *Installer) IsInstalled(key string) bool {
	inst.mu.RLock()
	s, ok := inst.statuses[key]
	inst.mu.RUnlock()
	if !ok {
		return false
	}
	return s.Installed
}

var installOrder = []string{"python", "forge", "ollama", "rembg"}

func (inst *Installer) EnsureAllInstalled() {
	for _, key := range installOrder {
		ic := inst.getConfig(key)
		if ic == nil {
			continue
		}
		if inst.IsInstalled(key) {
			log.Printf("[%s] already installed, skipping", key)
			continue
		}
		log.Printf("[%s] installing...", key)
		if err := inst.Install(key); err != nil {
			log.Printf("[%s] install failed: %v (will retry on process start)", key, err)
		}
	}
}

func (inst *Installer) EnsureInstalled(key, binary string) error {
	if binary != "" {
		if strings.ContainsRune(binary, '/') {
			if _, err := os.Stat(binary); err == nil {
				return nil
			}
		} else {
			if _, err := exec.LookPath(binary); err == nil {
				return nil
			}
		}
	}
	ic := inst.getConfig(key)
	if ic == nil {
		return nil
	}
	return inst.Install(key)
}

func (inst *Installer) ensureStatus(key string) *process.RingBuffer {
	inst.mu.Lock()
	defer inst.mu.Unlock()

	s, ok := inst.statuses[key]
	if !ok {
		s = &InstallStatus{Key: key}
		ic := inst.getConfig(key)
		if ic != nil {
			s.Installed = inst.checkInstalled(*ic)
		}
		inst.statuses[key] = s
	}

	lb, ok := inst.logs[key]
	if !ok {
		lb = process.NewRingBuffer(process.RingBufferSize)
		inst.logs[key] = lb
	}
	return lb
}

func (inst *Installer) getConfig(key string) *config.InstallConfig {
	if bc, ok := inst.config.Backends[key]; ok && bc.Install.Method != "" {
		return &bc.Install
	}
	if pc, ok := inst.config.Processes[key]; ok && pc.Install.Method != "" {
		return &pc.Install
	}
	return nil
}

func (inst *Installer) Install(key string) error {
	lb := inst.ensureStatus(key)

	inst.mu.Lock()
	s := inst.statuses[key]
	if s.Installing {
		inst.mu.Unlock()
		return fmt.Errorf("already installing: %s", key)
	}
	if s.Installed {
		inst.mu.Unlock()
		return nil
	}

	ic := inst.getConfig(key)
	if ic == nil {
		inst.mu.Unlock()
		return fmt.Errorf("no install config for: %s", key)
	}

	s.Installing = true
	s.Progress = "starting"
	s.Error = ""
	inst.statuses[key] = s
	inst.mu.Unlock()

	err := inst.doInstall(key, *ic, lb)

	inst.mu.Lock()
	s = inst.statuses[key]
	s.Installing = false
	if err != nil {
		s.Error = err.Error()
		s.Progress = "failed"
	} else {
		s.Installed = true
		s.Progress = "done"
		s.Version = ic.Version
	}
	inst.statuses[key] = s
	inst.mu.Unlock()

	if inst.OnProgress != nil {
		inst.OnProgress(key, s.Progress)
	}

	return err
}

func (inst *Installer) Status() map[string]InstallStatus {
	inst.mu.RLock()
	defer inst.mu.RUnlock()

	result := make(map[string]InstallStatus, len(inst.statuses))
	for k, s := range inst.statuses {
		result[k] = *s
	}
	return result
}

func (inst *Installer) doInstall(key string, ic config.InstallConfig, lb *process.RingBuffer) error {
	switch ic.Method {
	case config.InstallZip:
		return inst.installZip(key, ic, lb)
	case config.InstallBinary:
		return inst.installBinary(key, ic, lb)
	case config.InstallPip:
		return inst.installPip(key, ic, lb)
	case config.InstallArchive:
		return inst.installArchive(key, ic, lb)
	case config.InstallTgz:
		return inst.installTgz(key, ic, lb)
	}
	return fmt.Errorf("unknown install method: %s", ic.Method)
}

func (inst *Installer) checkInstalled(ic config.InstallConfig) bool {
	switch ic.Method {
	case config.InstallZip, config.InstallTgz:
		if ic.Target == "" {
			return false
		}
		target := inst.resolveTarget(ic.Target)
		st, err := os.Stat(target)
		return err == nil && st.IsDir()
	case config.InstallBinary, config.InstallArchive:
		if ic.Target == "" {
			return false
		}
		target := inst.resolveTarget(ic.Target)
		if runtime.GOOS == "windows" && !strings.HasSuffix(target, ".exe") {
			target += ".exe"
		}
		_, err := os.Stat(target)
		return err == nil
	case config.InstallPip:
		if ic.Target == "" {
			return false
		}
		if process.FindBinary(inst.config.DataDir, ic.Target) == "" {
			return false
		}
		pythonPath := pythonBinPath(inst.config.DataDir)
		if _, err := os.Stat(pythonPath); err == nil {
			cmd := exec.Command(pythonPath, "-c", "import "+ic.Target)
			if err := cmd.Run(); err != nil {
				return false
			}
		}
		return true
	}
	return false
}

func (inst *Installer) setProgress(key string, progress string) {
	inst.mu.Lock()
	if s, ok := inst.statuses[key]; ok {
		s.Progress = progress
		inst.statuses[key] = s
	}
	inst.mu.Unlock()
	if inst.OnProgress != nil {
		inst.OnProgress(key, progress)
	}
}
