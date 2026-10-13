//go:build !windows

package main

import (
	"os/exec"
	"syscall"
	"time"
)

// setupProc puts cmd in its own process group so killTree kills any
// children too (e.g. wrapper scripts that exec the real ffmpeg).
func setupProc(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.WaitDelay = 2 * time.Second // never hang on a pipe held by a stray grandchild
}

func killTree(cmd *exec.Cmd) error {
	return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
}
