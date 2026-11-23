package backend

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

// setServerProcAttrs puts llama-server in its own process group (so Stop can
// signal it and its children at once) and ties its life to ours. If the app
// dies, the server must die too. SIGKILL, not SIGTERM: on SIGTERM
// llama-server tries a graceful shutdown that can hang, and with the app gone
// nothing would kill it afterwards.
func setServerProcAttrs(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true, Pdeathsig: syscall.SIGKILL}
}

// terminateServer asks the server's process group to shut down gracefully.
func terminateServer(cmd *exec.Cmd) {
	_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
}

// killServer kills the server's process group outright.
func killServer(cmd *exec.Cmd) {
	_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
}

// killOrphanServers stops llama-servers an earlier run of the app left
// behind (e.g. after a crash): our binary, started with our "--alias yerel",
// and no longer owned by a running stt-app.
func killOrphanServers(bin string) {
	if bin == "" {
		return
	}
	want, err := filepath.EvalSymlinks(bin)
	if err != nil {
		return
	}
	self, _ := os.Executable()
	selfName := filepath.Base(self)
	procs, _ := os.ReadDir("/proc")
	for _, p := range procs {
		pid, err := strconv.Atoi(p.Name())
		if err != nil || pid == os.Getpid() {
			continue
		}
		exe, err := os.Readlink(fmt.Sprintf("/proc/%d/exe", pid))
		if err != nil || exe != want {
			continue
		}
		cmdline, _ := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", pid))
		if !strings.Contains(string(cmdline), "--alias\x00yerel\x00") {
			continue
		}
		stat, _ := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
		// stat: "pid (comm) state ppid …" — comm may contain spaces, so split after ')'
		fields := strings.Fields(string(stat[strings.LastIndexByte(string(stat), ')')+1:]))
		if len(fields) > 1 {
			if ppid, _ := strconv.Atoi(fields[1]); ppid > 1 {
				if parent, err := os.Readlink(fmt.Sprintf("/proc/%d/exe", ppid)); err == nil && filepath.Base(parent) == selfName {
					continue // belongs to another running copy of the app
				}
			}
		}
		log.Printf("llama: önceki oturumdan kalan llama-server kapatılıyor (pid %d)", pid)
		_ = syscall.Kill(pid, syscall.SIGKILL)
	}
}
