// Package daemon runs DarkServe as a detached background process.
//
// The AppImage launcher and start.sh both need to start the server without
// depending on lsof, curl or nohup being installed, and they need a reliable
// way to find the running process again in order to stop it. This package
// spawns a detached copy of the binary and waits until the child says it is
// listening.
package daemon

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"syscall"
	"time"
)

// EnvChild marks the detached child process. The parent sets it when
// spawning, so the child knows it must not fork a second time.
const EnvChild = "DARKSERVE_DAEMON_CHILD"

// EnvReadyFD tells the child which file descriptor it has to write to when
// it is up and serving. It is the end of a pipe the parent watches, so the
// parent learns that the server is really listening — and not some other
// program that happens to hold the same port.
const EnvReadyFD = "DARKSERVE_READY_FD"

// IsChild reports whether this process is the detached child.
func IsChild() bool { return os.Getenv(EnvChild) == "1" }

// Spawn starts a detached copy of self with the given arguments and waits
// until the child reports that it is serving. The child keeps running after
// this process exits because it lives in its own session.
//
// out receives the child's stdout and stderr — usually the log file. It is
// opened by the caller and closed there, not here.
func Spawn(self string, args []string, out *os.File, timeout time.Duration) (int, error) {
	// The pipe the child writes to when it is ready.
	readEnd, writeEnd, err := os.Pipe()
	if err != nil {
		return 0, fmt.Errorf("pipe: %w", err)
	}
	defer readEnd.Close()

	cmd := exec.Command(self, args...)
	cmd.Env = append(os.Environ(), EnvChild+"=1", EnvReadyFD+"=3")
	cmd.Stdout = out
	cmd.Stderr = out
	cmd.Stdin = nil
	cmd.ExtraFiles = []*os.File{writeEnd}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		writeEnd.Close()
		return 0, fmt.Errorf("could not start %s: %w", self, err)
	}
	pid := cmd.Process.Pid
	// The child has its own copy of the write end now.
	writeEnd.Close()

	// Reap the child and remember how it died, so a server that quits during
	// startup (port already taken, unreadable database) is reported at once
	// instead of after the full timeout.
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()

	// The child writes one byte as soon as it is listening.
	ready := make(chan error, 1)
	go func() {
		buf := make([]byte, 1)
		_, err := readEnd.Read(buf)
		ready <- err
	}()

	deadline := time.Now().Add(timeout)
	for {
		select {
		case err := <-ready:
			if err != nil {
				return pid, fmt.Errorf("the server did not report that it started: %w", err)
			}
			return pid, nil
		case err := <-exited:
			if err == nil {
				err = errors.New("it stopped right away")
			}
			return pid, fmt.Errorf("the server stopped while starting up: %w", err)
		case <-time.After(100 * time.Millisecond):
			if time.Now().After(deadline) {
				return pid, fmt.Errorf("the server did not answer within %s", timeout)
			}
		}
	}
}

// SignalReady tells the parent that the server is listening. It is a no-op
// when the process was not started by Spawn.
func SignalReady() {
	fdStr := os.Getenv(EnvReadyFD)
	if fdStr == "" {
		return
	}
	fd, err := strconv.Atoi(fdStr)
	if err != nil {
		return
	}
	f := os.NewFile(uintptr(fd), "ready")
	if f == nil {
		return
	}
	_, _ = f.Write([]byte{1})
	_ = f.Close()
}

// WritePID records the current process id so the launcher can stop the
// server later without guessing which process owns the port.
func WritePID(path string) error {
	return os.WriteFile(path, []byte(fmt.Sprintf("%d\n", os.Getpid())), 0o644)
}

// ReadPID returns the process id stored in path, or 0 when the file is
// missing or unreadable.
func ReadPID(path string) int {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	pid := 0
	for _, c := range b {
		if c < '0' || c > '9' {
			break
		}
		pid = pid*10 + int(c-'0')
	}
	return pid
}

// RemovePID deletes the pid file, ignoring a missing file.
func RemovePID(path string) {
	_ = os.Remove(path)
}
