package powerstatus

import (
	"context"
	"os/exec"
	"syscall"
	"testing"
	"time"
)

func TestWaitWithoutReapingReservesPIDUntilCleanup(t *testing.T) {
	cmd := exec.Command("/bin/sh", "-c", "exit 0")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer cmd.Wait()
	if err := waitWithoutReaping(cmd.Process.Pid); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Kill(cmd.Process.Pid, 0); err != nil {
		t.Fatalf("child was reaped before cleanup: %v", err)
	}
	if err := cmd.Wait(); err != nil {
		t.Fatal(err)
	}
	if err := waitWithoutReaping(cmd.Process.Pid); err != syscall.ECHILD {
		t.Fatalf("child was not reaped by Wait: %v", err)
	}
}

func TestStoppedSourceStillHonorsDeadline(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	cmd := exec.Command("/bin/sh", "-c", "kill -STOP $$; sleep 30")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	started := time.Now()
	if err := runProcessGroup(ctx, cmd); err == nil {
		t.Fatal("stopped source succeeded")
	}
	if time.Since(started) > 2*time.Second {
		t.Fatal("stopped source exceeded deadline margin")
	}
}
