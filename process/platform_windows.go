//go:build windows

package process

import (
	"os/exec"
	"strings"
	"syscall"
	"time"
)

func SetPlatformProcAttr(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP,
	}
}

func PlatformKill(mp *ManagedProcess) error {
	if mp.CancelFunc != nil {
		mp.CancelFunc()
	}

	if mp.PID > 0 {
		killProcessTree(mp.PID)
		time.Sleep(1500 * time.Millisecond)
	}

	mp.Status = "stopped"
	return nil
}

func killProcessTree(pid int) {
	exec.Command("taskkill", "/PID", itoa(pid), "/T", "/F").Run()

	out, _ := exec.Command("wmic", "process", "where",
		"ParentProcessId="+itoa(pid),
		"get", "ProcessId", "/format:csv").Output()
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "Node,") {
			continue
		}
		fields := strings.Split(line, ",")
		childPID := strings.TrimSpace(fields[len(fields)-1])
		if childPID != "" && childPID != itoa(pid) {
			exec.Command("taskkill", "/PID", childPID, "/T", "/F").Run()
		}
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	s := ""
	for n > 0 {
		s = string(rune('0'+n%10)) + s
		n /= 10
	}
	return s
}
