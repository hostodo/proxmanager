//go:build linux || darwin

package powerstatus

import (
	"context"
	"os/exec"
	"syscall"
	"time"
	"unsafe"
)

const (
	waitIDProcess = 1
	childExited   = 1
	childKilled   = 2
	childDumped   = 3
)

type childSignalInfo struct {
	Signo   int32
	Errno   int32
	Code    int32
	Payload [116]byte
}

func waitWithoutReaping(pid int) error {
	for {
		var info childSignalInfo
		_, _, errno := syscall.Syscall6(syscall.SYS_WAITID, waitIDProcess, uintptr(pid), uintptr(unsafe.Pointer(&info)), syscall.WEXITED|syscall.WNOWAIT, 0, 0)
		if errno == syscall.EINTR {
			continue
		}
		if errno != 0 {
			return errno
		}
		switch info.Code {
		case childExited, childKilled, childDumped:
			return nil
		}
		// Darwin waitid can report stopped children even with WEXITED (Go issue 19314).
		time.Sleep(time.Millisecond)
	}
}

func runProcessGroup(ctx context.Context, cmd *exec.Cmd) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	exited := make(chan error, 1)
	go func() { exited <- waitWithoutReaping(cmd.Process.Pid) }()
	var waitErr error
	select {
	case waitErr = <-exited:
	case <-ctx.Done():
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		waitErr = <-exited
	}
	if waitErr != syscall.ECHILD {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	err := cmd.Wait()
	if waitErr != nil {
		return waitErr
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return err
}
