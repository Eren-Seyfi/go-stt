package backend

import (
	"os/exec"
	"syscall"
)

// createNoWindow keeps a console program (llama-server.exe) from opening a
// console window when started from a GUI app.
const createNoWindow = 0x08000000 // CREATE_NO_WINDOW

func setServerProcAttrs(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow}
}

// terminateServer stops the server. Windows has no SIGTERM for console-less
// processes, so this is a hard stop, like killServer.
func terminateServer(cmd *exec.Cmd) {
	_ = cmd.Process.Kill()
}

func killServer(cmd *exec.Cmd) {
	_ = cmd.Process.Kill()
}

// killOrphanServers is a no-op on Windows: there is no /proc to scan, and
// Windows does not kill a child when its parent dies. Leftover servers after
// a crash have to be closed by hand (Task Manager) for now.
func killOrphanServers(bin string) {}
