//go:build !windows

package main

import (
	"syscall"
	"time"
	"os/exec"
)

func setPlatformProcAttr(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func platformKill(mp *ManagedProcess) error {
	if mp.cancelFunc != nil {
		mp.cancelFunc()
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
