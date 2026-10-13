package main

import (
	"os/exec"
	"strconv"
	"time"
)

// setupProc prepares cmd so killTree can kill its whole process tree. Package-manager
// shims (chocolatey, scoop) launch the real ffmpeg as a child, and killing only
// the shim would leave the real one holding the capture device.
func setupProc(cmd *exec.Cmd) {
	cmd.WaitDelay = 2 * time.Second // never hang on a pipe held by a stray grandchild
}

func killTree(cmd *exec.Cmd) error {
	return exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(cmd.Process.Pid)).Run()
}
