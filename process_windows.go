//go:build windows

package main

import (
	"os/exec"
	"syscall"
	"time"
)

func setPlatformProcAttr(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP,
	}
}

func platformKill(mp *ManagedProcess) error {
	if mp.cancelFunc != nil {
		mp.cancelFunc()
	}

	if mp.PID > 0 {
		exec.Command("taskkill", "/PID", itoa(mp.PID), "/T", "/F").Run()

		// Give process time to exit
		time.Sleep(2 * time.Second)
	}

	mp.Status = "stopped"
	return nil
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
