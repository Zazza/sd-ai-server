//go:build !windows

package process

import (
	"os/exec"
	"syscall"
	"time"
)

func SetPlatformProcAttr(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func PlatformKill(mp *ManagedProcess) error {
	if mp.CancelFunc != nil {
		mp.CancelFunc()
	}

	if mp.PID > 0 {
		syscall.Kill(-mp.PID, syscall.SIGTERM)

		done := make(chan struct{})
		go func() {
			for {
				if syscall.Kill(-mp.PID, 0) != nil {
					close(done)
					return
				}
				time.Sleep(100 * time.Millisecond)
			}
		}()

		select {
		case <-done:
		case <-time.After(10 * time.Second):
			syscall.Kill(-mp.PID, syscall.SIGKILL)
		}
	}

	mp.Status = "stopped"
	return nil
}
