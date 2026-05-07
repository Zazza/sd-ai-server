package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os/exec"
	"sync"
	"time"
)

const ringBufferSize = 100

type RingBuffer struct {
	mu     sync.RWMutex
	lines  []string
	size   int
	head   int
	count  int
}

func NewRingBuffer(size int) *RingBuffer {
	return &RingBuffer{
		lines: make([]string, size),
		size:  size,
	}
}

func (r *RingBuffer) Write(line string) {
	r.mu.Lock()
	r.lines[r.head] = line
	r.head = (r.head + 1) % r.size
	if r.count < r.size {
		r.count++
	}
	r.mu.Unlock()
}

func (r *RingBuffer) Lines(n int) []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if n > r.count {
		n = r.count
	}
	result := make([]string, 0, n)
	start := r.head - n
	if start < 0 {
		start += r.size
	}
	for i := 0; i < n; i++ {
		idx := (start + i) % r.size
		result = append(result, r.lines[idx])
	}
	return result
}

type ProcessStatus struct {
	Name      string    `json:"name"`
	Status    string    `json:"status"`
	PID       int       `json:"pid,omitempty"`
	StartedAt time.Time `json:"started_at,omitempty"`
	Restarts  int       `json:"restarts"`
	Uptime    string    `json:"uptime,omitempty"`
}

type ManagedProcess struct {
	Config     ProcessConfig
	Cmd        *exec.Cmd
	PID        int
	Status     string
	StartedAt  time.Time
	Restarts   int
	cancelFunc context.CancelFunc
	logBuf     *RingBuffer
}

type ProcessManager struct {
	mu        sync.RWMutex
	processes map[string]*ManagedProcess
}

func NewProcessManager(cfg *Config) *ProcessManager {
	pm := &ProcessManager{
		processes: make(map[string]*ManagedProcess),
	}
	for key, pc := range cfg.Processes {
		pm.processes[key] = &ManagedProcess{
			Config: pc,
			Status: "stopped",
			logBuf: NewRingBuffer(ringBufferSize),
		}
	}
	return pm
}

func (pm *ProcessManager) StartAll() {
	pm.mu.RLock()
	defer pm.mu.RUnlock()
	for name, mp := range pm.processes {
		if mp.Config.AutoStart {
			go pm.start(name)
		}
	}
}

func (pm *ProcessManager) Start(name string) error {
	pm.mu.RLock()
	_, ok := pm.processes[name]
	pm.mu.RUnlock()
	if !ok {
		return fmt.Errorf("process %q not found", name)
	}
	return pm.start(name)
}

func (pm *ProcessManager) start(name string) error {
	pm.mu.Lock()
	mp, ok := pm.processes[name]
	if !ok {
		pm.mu.Unlock()
		return fmt.Errorf("process %q not found", name)
	}
	if mp.Status == "running" || mp.Status == "starting" {
		pm.mu.Unlock()
		return fmt.Errorf("process %q is already %s", name, mp.Status)
	}

	mp.Status = "starting"
	pm.mu.Unlock()

	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, mp.Config.Binary, mp.Config.Args...)

	if mp.Config.WorkDir != "" {
		cmd.Dir = mp.Config.WorkDir
	}

	if len(mp.Config.Env) > 0 {
		cmd.Env = append(cmd.Environ(), envSlice(mp.Config.Env)...)
	}

	setPlatformProcAttr(cmd)

	stdout, _ := cmd.StdoutPipe()
	stderr, _ := cmd.StderrPipe()

	mp.Cmd = cmd
	mp.cancelFunc = cancel
	mp.logBuf = NewRingBuffer(ringBufferSize)

	pm.mu.Lock()
	pm.processes[name] = mp
	pm.mu.Unlock()

	if err := cmd.Start(); err != nil {
		pm.mu.Lock()
		mp.Status = "crashed"
		pm.processes[name] = mp
		pm.mu.Unlock()
		cancel()
		return fmt.Errorf("start %q: %w", name, err)
	}

	mp.PID = cmd.Process.Pid
	mp.StartedAt = time.Now()
	mp.Status = "running"

	pm.mu.Lock()
	pm.processes[name] = mp
	pm.mu.Unlock()

	// Pipe stdout/stderr to ring buffer
	go pipeLogs(stdout, mp.logBuf)
	go pipeLogs(stderr, mp.logBuf)

	// Wait for process exit
	go func() {
		err := cmd.Wait()
		pm.mu.Lock()
		mp.Status = "crashed"
		if err != nil {
			mp.logBuf.Write(fmt.Sprintf("process exited: %v", err))
		}
		pm.processes[name] = mp
		pm.mu.Unlock()
	}()

	return nil
}

func pipeLogs(r io.Reader, buf *RingBuffer) {
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		buf.Write(scanner.Text())
	}
}

func (pm *ProcessManager) Stop(name string) error {
	pm.mu.Lock()
	mp, ok := pm.processes[name]
	if !ok {
		pm.mu.Unlock()
		return fmt.Errorf("process %q not found", name)
	}
	if mp.Status != "running" && mp.Status != "starting" {
		pm.mu.Unlock()
		return nil
	}
	pm.mu.Unlock()

	return platformKill(mp)
}

func (pm *ProcessManager) Restart(name string) error {
	if err := pm.Stop(name); err != nil {
		return err
	}

	pm.mu.Lock()
	mp := pm.processes[name]
	mp.Restarts = 0
	pm.processes[name] = mp
	pm.mu.Unlock()

	return pm.Start(name)
}

func (pm *ProcessManager) Status() map[string]ProcessStatus {
	pm.mu.RLock()
	defer pm.mu.RUnlock()

	result := make(map[string]ProcessStatus, len(pm.processes))
	for name, mp := range pm.processes {
		ps := ProcessStatus{
			Name:     mp.Config.Name,
			Status:   mp.Status,
			PID:      mp.PID,
			StartedAt: mp.StartedAt,
			Restarts: mp.Restarts,
		}
		if !mp.StartedAt.IsZero() && mp.Status == "running" {
			ps.Uptime = time.Since(mp.StartedAt).Truncate(time.Second).String()
		}
		result[name] = ps
	}
	return result
}

func (pm *ProcessManager) Logs(name string, lines int) ([]string, error) {
	pm.mu.RLock()
	mp, ok := pm.processes[name]
	pm.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("process %q not found", name)
	}
	return mp.logBuf.Lines(lines), nil
}

func (pm *ProcessManager) Get(name string) (*ManagedProcess, bool) {
	pm.mu.RLock()
	defer pm.mu.RUnlock()
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
			pm.mu.RLock()
			for name, mp := range pm.processes {
				if mp.Status == "crashed" && mp.Config.Restart && mp.Restarts < mp.Config.MaxRestart {
					delay := backoffDuration(mp.Restarts)
					go func(n string, m *ManagedProcess) {
						time.Sleep(delay)
						pm.mu.Lock()
						m.Restarts++
						pm.processes[n] = m
						pm.mu.Unlock()
						pm.start(n)
					}(name, mp)
				}
			}
			pm.mu.RUnlock()
		}
	}
}

func (pm *ProcessManager) StopAll() {
	pm.mu.RLock()
	defer pm.mu.RUnlock()
	for _, mp := range pm.processes {
		if mp.Status == "running" || mp.Status == "starting" {
			platformKill(mp)
		}
	}
}

func (pm *ProcessManager) UpdateProcessConfig(name string, binary string, args []string, workdir string) {
	pm.mu.Lock()
	defer pm.mu.Unlock()
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
