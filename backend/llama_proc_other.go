//go:build !linux && !windows

package backend

import (
	"os/exec"
	"syscall"
)

// macOS and the BSDs: a process group like on Linux, but no Pdeathsig and no
// /proc, so a crashed app can leave the server running.
func setServerProcAttrs(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func terminateServer(cmd *exec.Cmd) {
	_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
}

func killServer(cmd *exec.Cmd) {
	_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
}

func killOrphanServers(bin string) {}
