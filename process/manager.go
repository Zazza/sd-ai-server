package process

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"sd-studio-server/config"
)

type Installer interface {
	EnsureInstalled(key, binary string) error
	PreStartForge()
}

type ProcessManager struct {
	Mu        sync.RWMutex
	processes map[string]*ManagedProcess
	installer Installer
	dataDir   string
	OnChange  func()
}

func NewProcessManager(cfg *config.Config, inst Installer, dataDir string) *ProcessManager {
	pm := &ProcessManager{
		processes: make(map[string]*ManagedProcess),
		installer: inst,
		dataDir:   dataDir,
	}
	for key, pc := range cfg.Processes {
		pm.processes[key] = &ManagedProcess{
			Config: pc,
			Status: "stopped",
			LogBuf: NewRingBuffer(RingBufferSize),
		}
	}
	return pm
}

func (pm *ProcessManager) StartAll() {
	pm.Mu.RLock()
	defer pm.Mu.RUnlock()
	for name, mp := range pm.processes {
		if mp.Config.AutoStart {
			go pm.start(name)
		}
	}
}

func (pm *ProcessManager) Start(name string) error {
	pm.Mu.RLock()
	_, ok := pm.processes[name]
	pm.Mu.RUnlock()
	if !ok {
		return fmt.Errorf("process %q not found", name)
	}
	return pm.start(name)
}

func (pm *ProcessManager) start(name string) error {
	pm.Mu.Lock()
	mp, ok := pm.processes[name]
	if !ok {
		pm.Mu.Unlock()
		return fmt.Errorf("process %q not found", name)
	}
	if mp.Status == "running" || mp.Status == "starting" {
		pm.Mu.Unlock()
		return fmt.Errorf("process %q is already %s", name, mp.Status)
	}

	mp.Status = "starting"
	pm.Mu.Unlock()

	if mp.Config.HealthURL != "" {
		hctx, hcancel := context.WithTimeout(context.Background(), 2*time.Second)
		hreq, _ := http.NewRequestWithContext(hctx, http.MethodGet, mp.Config.HealthURL, nil)
		hresp, herr := http.DefaultClient.Do(hreq)
		hcancel()
		if herr == nil {
			hresp.Body.Close()
			if hresp.StatusCode == http.StatusOK {
				pm.Mu.Lock()
				mp.Status = "running"
				mp.StartedAt = time.Now()
				mp.LogBuf.Write("already running (health check passed, skipped start)")
				pm.processes[name] = mp
				pm.Mu.Unlock()
				log.Printf("[%s] already running (health check passed)", name)
				pm.notifyChange()
				return nil
			}
		}
	}

	if name == "sd" && pm.installer != nil {
		pm.installer.PreStartForge()
	}

	if pm.installer != nil && !mp.InstallFailed {
		if err := pm.installer.EnsureInstalled(name, mp.Config.Binary); err != nil {
			pm.Mu.Lock()
			mp.Status = "crashed"
			mp.InstallFailed = true
			mp.LogBuf.Write(fmt.Sprintf("auto-install failed: %v", err))
			pm.processes[name] = mp
			pm.Mu.Unlock()
			log.Printf("[%s] auto-install failed: %v", name, err)
			return fmt.Errorf("auto-install %q: %w", name, err)
		}
	}

	if mp.Config.Binary != "" && !strings.ContainsRune(mp.Config.Binary, '/') && !strings.ContainsRune(mp.Config.Binary, os.PathSeparator) {
		resolved := FindBinary(pm.dataDir, mp.Config.Binary)
		if resolved != "" {
			mp.Config.Binary = resolved
		}
	}

	if runtime.GOOS == "windows" && mp.Config.Binary != "" && !strings.HasSuffix(mp.Config.Binary, ".exe") {
		exePath := mp.Config.Binary + ".exe"
		if _, err := os.Stat(exePath); err == nil {
			mp.Config.Binary = exePath
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, mp.Config.Binary, mp.Config.Args...)

	if mp.Config.WorkDir != "" {
		cmd.Dir = mp.Config.WorkDir
	}

	if len(mp.Config.Env) > 0 {
		cmd.Env = append(cmd.Environ(), envSlice(mp.Config.Env)...)
	}

	SetPlatformProcAttr(cmd)

	stdout, _ := cmd.StdoutPipe()
	stderr, _ := cmd.StderrPipe()

	mp.Cmd = cmd
	mp.CancelFunc = cancel
	mp.LogBuf = NewRingBuffer(RingBufferSize)

	pm.Mu.Lock()
	pm.processes[name] = mp
	pm.Mu.Unlock()

	if err := cmd.Start(); err != nil {
		pm.Mu.Lock()
		mp.Status = "crashed"
		mp.LogBuf.Write(fmt.Sprintf("failed to start: %v (binary=%s workdir=%s)", err, mp.Config.Binary, mp.Config.WorkDir))
		pm.processes[name] = mp
		pm.Mu.Unlock()
		cancel()
		return fmt.Errorf("start %q: %w (binary=%s)", name, err, mp.Config.Binary)
	}

	mp.PID = cmd.Process.Pid
	mp.StartedAt = time.Now()
	mp.Status = "starting"
	mp.Managed = true

	pm.Mu.Lock()
	pm.processes[name] = mp
	pm.Mu.Unlock()

	log.Printf("[%s] started (pid=%d binary=%s workdir=%s)", name, mp.PID, mp.Config.Binary, mp.Config.WorkDir)
	pm.notifyChange()

	go pipeLogs(stdout, mp.LogBuf, name)
	go pipeLogs(stderr, mp.LogBuf, name)

	go func() {
		if mp.Config.HealthURL != "" {
			log.Printf("[%s] waiting for healthy response from %s ...", name, mp.Config.HealthURL)
			for i := 0; i < 120; i++ {
				hctx, hcancel := context.WithTimeout(context.Background(), 2*time.Second)
				hreq, _ := http.NewRequestWithContext(hctx, http.MethodGet, mp.Config.HealthURL, nil)
				hresp, herr := http.DefaultClient.Do(hreq)
				hcancel()
				if herr == nil {
					hresp.Body.Close()
					if hresp.StatusCode == http.StatusOK {
						break
					}
				}
				time.Sleep(2 * time.Second)
			}
		}

		pm.Mu.Lock()
		mp.Status = "running"
		pm.processes[name] = mp
		pm.Mu.Unlock()
		log.Printf("[%s] healthy", name)
		pm.notifyChange()
	}()

	go func() {
		err := cmd.Wait()
		pm.Mu.Lock()
		mp.Status = "crashed"
		if err != nil {
			mp.LogBuf.Write(fmt.Sprintf("process exited: %v", err))
		}
		pm.processes[name] = mp
		pm.Mu.Unlock()
		pm.notifyChange()
	}()

	return nil
}

func pipeLogs(r io.Reader, buf *RingBuffer, name string) {
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := scanner.Text()
		buf.Write(line)
		log.Printf("[%s] %s", name, line)
	}
}

func (pm *ProcessManager) Stop(name string) error {
	pm.Mu.Lock()
	mp, ok := pm.processes[name]
	if !ok {
		pm.Mu.Unlock()
		return fmt.Errorf("process %q not found", name)
	}
	if mp.Status != "running" && mp.Status != "starting" {
		pm.Mu.Unlock()
		return nil
	}
	pm.Mu.Unlock()

	err := PlatformKill(mp)
	pm.notifyChange()
	return err
}

func (pm *ProcessManager) Restart(name string) error {
	if err := pm.Stop(name); err != nil {
		return err
	}

	pm.Mu.Lock()
	mp := pm.processes[name]
	mp.Restarts = 0
	pm.processes[name] = mp
	pm.Mu.Unlock()

	return pm.Start(name)
}

func (pm *ProcessManager) Status() map[string]ProcessStatus {
	pm.Mu.RLock()
	defer pm.Mu.RUnlock()

	result := make(map[string]ProcessStatus, len(pm.processes))
	for name, mp := range pm.processes {
		ps := ProcessStatus{
			Name:      mp.Config.Name,
			Status:    mp.Status,
			PID:       mp.PID,
			StartedAt: mp.StartedAt,
			Restarts:  mp.Restarts,
		}
		if !mp.StartedAt.IsZero() && mp.Status == "running" {
			ps.Uptime = time.Since(mp.StartedAt).Truncate(time.Second).String()
		}
		result[name] = ps
	}
	return result
}

func (pm *ProcessManager) Logs(name string, lines int) ([]string, error) {
	pm.Mu.RLock()
	mp, ok := pm.processes[name]
	pm.Mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("process %q not found", name)
	}
	return mp.LogBuf.Lines(lines), nil
}

func (pm *ProcessManager) Get(name string) (*ManagedProcess, bool) {
	pm.Mu.RLock()
	defer pm.Mu.RUnlock()
	mp, ok := pm.processes[name]
	return mp, ok
}

func (pm *ProcessManager) Watch(ctx context.Context) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			pm.Mu.RLock()
			for name, mp := range pm.processes {
				if mp.Status == "crashed" && mp.Config.Restart && mp.Restarts < mp.Config.MaxRestart {
					if !mp.StartedAt.IsZero() && time.Since(mp.StartedAt) < 5*time.Second {
						log.Printf("[%s] crashed too fast (%v), skipping restart", name, time.Since(mp.StartedAt))
						continue
					}
					delay := backoffDuration(mp.Restarts)
					go func(n string, m *ManagedProcess) {
						time.Sleep(delay)
						pm.Mu.Lock()
						m.Restarts++
						pm.processes[n] = m
						pm.Mu.Unlock()
						pm.start(n)
						pm.notifyChange()
					}(name, mp)
				}
			}
			pm.Mu.RUnlock()
		}
	}
}

func (pm *ProcessManager) StopAll() {
	pm.Mu.RLock()
	defer pm.Mu.RUnlock()
	for name, mp := range pm.processes {
		if name == "ollama" {
			continue
		}
		if mp.Status == "running" || mp.Status == "starting" {
			PlatformKill(mp)
		}
	}
}

func (pm *ProcessManager) UpdateProcessConfig(name string, binary string, args []string, workdir string) {
	pm.Mu.Lock()
	defer pm.Mu.Unlock()
	mp, ok := pm.processes[name]
	if !ok {
		return
	}
	mp.Config.Binary = binary
	mp.Config.Args = args
	mp.Config.WorkDir = workdir
	pm.processes[name] = mp
}

func backoffDuration(restarts int) time.Duration {
	secs := 2 << uint(restarts)
	if secs > 32 {
		secs = 32
	}
	return time.Duration(secs) * time.Second
}

func envSlice(env map[string]string) []string {
	result := make([]string, 0, len(env))
	for k, v := range env {
		result = append(result, k+"="+v)
	}
	return result
}

func (pm *ProcessManager) notifyChange() {
	if pm.OnChange != nil {
		pm.OnChange()
	}
}

func FindBinary(dataDir, name string) string {
	if runtime.GOOS == "windows" {
		for _, dir := range []string{
			filepath.Join(dataDir, "python", "Scripts"),
			filepath.Join(dataDir, "python"),
			filepath.Join(dataDir, "bin"),
		} {
			candidate := filepath.Join(dir, name+".exe")
			if _, err := os.Stat(candidate); err == nil {
				return candidate
			}
		}
	} else {
		for _, dir := range []string{
			filepath.Join(dataDir, "python", "bin"),
			filepath.Join(dataDir, "bin"),
		} {
			candidate := filepath.Join(dir, name)
			if _, err := os.Stat(candidate); err == nil {
				return candidate
			}
		}
	}
	path, err := exec.LookPath(name)
	if err == nil {
		return path
	}
	return ""
}
