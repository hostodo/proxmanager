package powerstatus

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

const testBoot = "bbcf2358-904b-4d53-bcea-a7d83c861038"
const testEpoch = "d15b7b76-4869-4fb0-89a9-b560edc03167"
const testToken = "test-only-status-token-0123456789abcdef"

func fixture(t *testing.T) (string, Clock) {
	t.Helper()
	dir := t.TempDir()
	if err := os.Chmod(dir, 0750); err != nil {
		t.Fatal(err)
	}
	boot := filepath.Join(dir, "boot_id")
	uptime := filepath.Join(dir, "uptime")
	write(t, boot, testBoot)
	write(t, uptime, "100.00 90.00\n")
	return filepath.Join(dir, "power.json"), Clock{boot, uptime}
}
func write(t *testing.T, path, text string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
}
func sample() DiskSnapshot {
	return DiskSnapshot{Snapshot: Snapshot{SchemaVersion: 1, NodeID: 7, BootID: testBoot, CollectorEpoch: testEpoch, Sequence: 1, SampleStartedAt: time.Date(2026, 11, 23, 5, 0, 0, 0, time.UTC), SampleCompletedAt: time.Date(2026, 11, 23, 5, 0, 0, 250000000, time.UTC), SampleAgeMS: 250, VMs: []VM{{1001, "running"}, {1002, "stopped"}}}, SampleStartedUptimeMS: 99000}
}

func TestReadPreservesObservationIdentityAndAgesStaleSnapshot(t *testing.T) {
	path, clock := fixture(t)
	s := sample()
	if err := WriteSnapshot(path, s); err != nil {
		t.Fatal(err)
	}
	first, err := ReadSnapshot(path, 7, clock)
	if err != nil {
		t.Fatal(err)
	}
	if first.SampleAgeMS != 1010 {
		t.Fatalf("age = %d", first.SampleAgeMS)
	}
	write(t, clock.UptimeFile, "200.00 190.00")
	later, err := ReadSnapshot(path, 7, clock)
	if err != nil {
		t.Fatal(err)
	}
	if later.SampleAgeMS != 101010 || later.Sequence != first.Sequence || later.SampleStartedAt != first.SampleStartedAt {
		t.Fatalf("old observation was rejuvenated: %+v", later)
	}
}

func TestReadRejectsDifferentBootNodeAndBackwardsClock(t *testing.T) {
	for _, scenario := range []string{"boot", "node", "clock"} {
		t.Run(scenario, func(t *testing.T) {
			path, clock := fixture(t)
			if err := WriteSnapshot(path, sample()); err != nil {
				t.Fatal(err)
			}
			node := int64(7)
			switch scenario {
			case "boot":
				write(t, clock.BootIDFile, testEpoch)
			case "node":
				node = 8
			case "clock":
				write(t, clock.UptimeFile, "1.0 0.0")
			}
			if _, err := ReadSnapshot(path, node, clock); err == nil {
				t.Fatal("accepted mismatched snapshot")
			}
		})
	}
}

func TestHTTPRequiresBootRelativeStartButAcceptsZero(t *testing.T) {
	for _, scenario := range []string{"missing", "null", "zero"} {
		t.Run(scenario, func(t *testing.T) {
			path, clock := fixture(t)
			encoded, err := json.Marshal(sample())
			if err != nil {
				t.Fatal(err)
			}
			var fields map[string]any
			if err := json.Unmarshal(encoded, &fields); err != nil {
				t.Fatal(err)
			}
			switch scenario {
			case "missing":
				delete(fields, "sample_started_uptime_ms")
			case "null":
				fields["sample_started_uptime_ms"] = nil
			case "zero":
				fields["sample_started_uptime_ms"] = 0
			}
			encoded, err = json.Marshal(fields)
			if err != nil {
				t.Fatal(err)
			}
			write(t, path, string(encoded))
			request := httptest.NewRequest(http.MethodGet, "/v1/power-state", nil)
			request.Header.Set("Authorization", "Bearer "+testToken)
			response := httptest.NewRecorder()
			Handler(path, 7, clock, testToken).ServeHTTP(response, request)
			expected := http.StatusServiceUnavailable
			if scenario == "zero" {
				expected = http.StatusOK
			}
			if response.Code != expected {
				t.Fatalf("status = %d, want %d", response.Code, expected)
			}
		})
	}
}

func TestReadRejectsMalformedOversizedAndNonRegularFiles(t *testing.T) {
	for _, kind := range []string{"invalid_json", "trailing", "unknown_field", "oversized", "symlink", "directory"} {
		t.Run(kind, func(t *testing.T) {
			path, clock := fixture(t)
			b, _ := json.Marshal(sample())
			switch kind {
			case "invalid_json":
				write(t, path, "{")
			case "trailing":
				write(t, path, string(b)+"{}")
			case "unknown_field":
				write(t, path, strings.TrimSuffix(string(b), "}")+`,"extra":1}`)
			case "oversized":
				write(t, path, strings.Repeat(" ", MaxSnapshotBytes+1))
			case "symlink":
				if err := os.Symlink(clock.UptimeFile, path); err != nil {
					t.Fatal(err)
				}
			case "directory":
				if err := os.Mkdir(path, 0700); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := ReadSnapshot(path, 7, clock); err == nil {
				t.Fatal("accepted unsafe snapshot")
			}
		})
	}
}

func TestAtomicPublicationWithConcurrentReaders(t *testing.T) {
	path, clock := fixture(t)
	s := sample()
	if err := WriteSnapshot(path, s); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				got, err := ReadSnapshot(path, 7, clock)
				if err != nil {
					errs <- err
					return
				}
				if len(got.VMs) != 2 || got.VMs[0].VMID != 1001 {
					errs <- fmt.Errorf("partial snapshot")
					return
				}
			}
		}()
	}
	for i := 0; i < 100; i++ {
		s.Sequence++
		if err := WriteSnapshot(path, s); err != nil {
			t.Fatal(err)
		}
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0640 {
		t.Fatalf("permissions %v", info.Mode())
	}
}

func TestObserverLockExcludesSecondWriter(t *testing.T) {
	path, _ := fixture(t)
	first, err := LockObserver(path)
	if err != nil {
		t.Fatal(err)
	}
	if second, err := LockObserver(path); err == nil {
		second.Close()
		t.Fatal("second writer acquired lock")
	}
	first.Close()
	next, err := LockObserver(path)
	if err != nil {
		t.Fatal(err)
	}
	next.Close()
}

func TestHTTPAuthenticatesBeforeSnapshotReads(t *testing.T) {
	path, clock := fixture(t)
	server := httptest.NewServer(Handler(path, 7, clock, testToken))
	defer server.Close()
	request := func(token string) (int, string) {
		req, _ := http.NewRequest("GET", server.URL+"/v1/power-state", nil)
		req.Header.Set("Authorization", token)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		if resp.Header.Get("Cache-Control") != "no-store" {
			t.Fatal("missing no-store")
		}
		return resp.StatusCode, string(b)
	}
	if code, body := request(""); code != 401 || strings.Contains(body, "snapshot") {
		t.Fatalf("unauthorized request learned snapshot state: %d %s", code, body)
	}
	if code, _ := request("Bearer " + testToken); code != 503 {
		t.Fatalf("missing snapshot: %d", code)
	}
	if err := WriteSnapshot(path, sample()); err != nil {
		t.Fatal(err)
	}
	if code, body := request("Bearer " + testToken); code != 200 || strings.Contains(body, "uptime") {
		t.Fatalf("public response: %d %s", code, body)
	}
	if code, _ := request("Bearer invalid"); code != 401 {
		t.Fatalf("wrong token: %d", code)
	}
}

func TestSnapshotValidation(t *testing.T) {
	for _, kind := range []string{"version", "node", "sequence", "duplicate", "state", "time", "nil_vms"} {
		t.Run(kind, func(t *testing.T) {
			s := sample()
			switch kind {
			case "version":
				s.SchemaVersion = 2
			case "node":
				s.NodeID = 0
			case "sequence":
				s.Sequence = 0
			case "duplicate":
				s.VMs[1].VMID = s.VMs[0].VMID
			case "state":
				s.VMs[0].State = "unknown"
			case "time":
				s.SampleCompletedAt = s.SampleStartedAt.Add(-time.Second)
			case "nil_vms":
				s.VMs = nil
			}
			path, _ := fixture(t)
			if err := WriteSnapshot(path, s); err == nil {
				t.Fatal("accepted invalid snapshot")
			}
		})
	}
}

func TestTokenPermissionsAndListenerBoundaries(t *testing.T) {
	path, _ := fixture(t)
	write(t, path, testToken)
	if token, err := ReadToken(path); err != nil || token != testToken {
		t.Fatal("private token rejected", err)
	}
	if err := os.Chmod(path, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadToken(path); err == nil {
		t.Fatal("world-readable token accepted")
	}
	if _, err := Listen("", "0.0.0.0:0"); err == nil {
		t.Fatal("public TCP bind accepted")
	}
	listener, err := Listen("", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	listener.Close()
	socketDir, err := os.MkdirTemp("/tmp", "hos110-sock-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(socketDir) })
	socket := filepath.Join(socketDir, "status.sock")
	first, err := Listen(socket, "")
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	if next, err := Listen(socket, ""); err == nil {
		next.Close()
		t.Fatal("unlinked active socket")
	}
}

func script(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "pvesh")
	write(t, path, "#!/bin/sh\n"+body+"\n")
	if err := os.Chmod(path, 0700); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestSourceUsesBulkArgsAndExcludesTemplates(t *testing.T) {
	command := script(t, `[ "$*" = "get /nodes/fixture/qemu --full 0 --output-format json" ] || exit 8
printf '%s' '[{"vmid":1002,"status":"stopped"},{"vmid":9000,"template":1,"status":"stopped"},{"vmid":1001,"status":"running","ignored":"field"}]'`)
	got, err := (Source{command, "fixture", time.Second}).Read(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].VMID != 1001 || got[1].State != "stopped" {
		t.Fatalf("wrong normalized observations: %+v", got)
	}
}

func TestSourceFailsClosed(t *testing.T) {
	for _, body := range []string{"exit 1", `echo null`, `echo '[] {}'`, `echo '[{"vmid":1,"status":"paused"}]'`, `echo '[{"vmid":1,"status":"running"},{"vmid":1,"status":"stopped"}]'`, `echo '[{"vmid":0,"status":"running"}]'`} {
		command := script(t, body)
		if _, err := (Source{command, "fixture", time.Second}).Read(context.Background()); err == nil {
			t.Fatalf("accepted %s", body)
		}
	}
}

func TestCommandDeadlineKillsProcessGroup(t *testing.T) {
	command := script(t, "sleep 30 &\nwait")
	started := time.Now()
	if _, err := (Source{command, "fixture", 100 * time.Millisecond}).Read(context.Background()); err == nil {
		t.Fatal("unbounded command succeeded")
	}
	if time.Since(started) > 2*time.Second {
		t.Fatal("command exceeded deadline margin")
	}
}

func TestCommandOutputIsBounded(t *testing.T) {
	command := script(t, "head -c 3000000 /dev/zero")
	if _, err := (Source{command, "fixture", time.Second}).Read(context.Background()); err == nil {
		t.Fatal("oversized output accepted")
	}
}

func TestFailedCollectionPreservesFileAndSequence(t *testing.T) {
	path, clock := fixture(t)
	command := script(t, `echo '[{"vmid":1001,"status":"running"}]'`)
	o := Observer{Source: Source{command, "fixture", time.Second}, Clock: clock, Path: path, NodeID: 7, Epoch: testEpoch}
	if err := o.Collect(context.Background()); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	o.Source.Command = script(t, "exit 1")
	if err := o.Collect(context.Background()); err == nil {
		t.Fatal("failed source accepted")
	}
	after, _ := os.ReadFile(path)
	if string(before) != string(after) || o.Sequence != 1 {
		t.Fatal("failure replaced prior observation")
	}
}

func TestCommandDoesNotLeaveDescendants(t *testing.T) {
	for _, exitEarly := range []bool{false, true} {
		t.Run(fmt.Sprint(exitEarly), func(t *testing.T) {
			marker := filepath.Join(t.TempDir(), "survived")
			body := fmt.Sprintf("(sleep 0.4; echo survived > '%s') &\n", marker)
			timeout := 100 * time.Millisecond
			if exitEarly {
				body += "exit 0"
				timeout = time.Second
			} else {
				body += "wait"
			}
			_, err := (Source{script(t, body), "fixture", timeout}).Read(context.Background())
			if err == nil {
				t.Fatal("invalid source accepted")
			}
			time.Sleep(500 * time.Millisecond)
			if _, err := os.Stat(marker); !os.IsNotExist(err) {
				t.Fatal("source descendant survived collection")
			}
		})
	}
}

func TestPublicationRejectsUnsafeDirectory(t *testing.T) {
	root := t.TempDir()
	unsafe := filepath.Join(root, "unsafe")
	if err := os.Mkdir(unsafe, 0777); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(unsafe, 0777); err != nil {
		t.Fatal(err)
	}
	if err := WriteSnapshot(filepath.Join(unsafe, "power.json"), sample()); err == nil {
		t.Fatal("writable directory accepted")
	}
	if err := os.Symlink(root, filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	if err := WriteSnapshot(filepath.Join(root, "link", "power.json"), sample()); err == nil {
		t.Fatal("symlink directory accepted")
	}
}

func TestCollectionAgeIncludesSourceDuration(t *testing.T) {
	path, clock := fixture(t)
	command := script(t, fmt.Sprintf("printf '100.75 200.0' > '%s'\necho '[{\"vmid\":1001,\"status\":\"running\"}]'", clock.UptimeFile))
	o := Observer{Source: Source{command, "fixture", time.Second}, Clock: clock, Path: path, NodeID: 7, Epoch: testEpoch}
	if err := o.Collect(context.Background()); err != nil {
		t.Fatal(err)
	}
	s, err := ReadSnapshot(path, 7, clock)
	if err != nil || s.SampleAgeMS != 760 {
		t.Fatalf("collection duration lost: %+v %v", s, err)
	}
}

func TestCollectionRejectsBootOrClockChangeDuringQuery(t *testing.T) {
	for _, changeBoot := range []bool{false, true} {
		t.Run(fmt.Sprint(changeBoot), func(t *testing.T) {
			path, clock := fixture(t)
			if err := WriteSnapshot(path, sample()); err != nil {
				t.Fatal(err)
			}
			before, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			changedPath, value := clock.UptimeFile, "1.00 200.0"
			if changeBoot {
				changedPath, value = clock.BootIDFile, "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
			}
			command := script(t, fmt.Sprintf("printf '%s' > '%s'\necho '[]'", value, changedPath))
			o := Observer{Source: Source{command, "fixture", time.Second}, Clock: clock, Path: path, NodeID: 7, Epoch: testEpoch}
			if err := o.Collect(context.Background()); err == nil {
				t.Fatal("clock discontinuity accepted")
			}
			after, _ := os.ReadFile(path)
			if string(before) != string(after) || o.Sequence != 0 {
				t.Fatal("clock failure published a new observation")
			}
		})
	}
}

func TestOutputLimitAppliesToStreamCopies(t *testing.T) {
	var buffer boundedBuffer
	source := io.LimitReader(strings.NewReader(strings.Repeat("x", MaxSnapshotBytes+1000)), MaxSnapshotBytes+1000)
	_, err := io.Copy(&buffer, source)
	if err == nil || buffer.data.Len() > MaxSnapshotBytes {
		t.Fatalf("stream copy escaped output bound: %d bytes, %v", buffer.data.Len(), err)
	}
}

func TestAgeIncludesClockResolutionWithinSameUptimeBucket(t *testing.T) {
	path, clock := fixture(t)
	s := sample()
	s.SampleStartedUptimeMS = 100000
	s.SampleCompletedAt = s.SampleStartedAt.Add(5 * time.Millisecond)
	s.SampleAgeMS = uptimeResolutionMS
	if err := WriteSnapshot(path, s); err != nil {
		t.Fatal(err)
	}
	got, err := ReadSnapshot(path, 7, clock)
	if err != nil || got.SampleAgeMS != 10 || got.SampleAgeMS < s.SampleCompletedAt.Sub(s.SampleStartedAt).Milliseconds() {
		t.Fatalf("same clock bucket underestimated age: %+v, %v", got, err)
	}
}

func TestBootClockParsingRejectsInvalidOrOverflowValues(t *testing.T) {
	_, clock := fixture(t)
	for _, value := range []string{"NaN 0", "Inf 0", "-1.00 0", "100.123 0", "9223372036854775807.99 0", "100.00"} {
		write(t, clock.UptimeFile, value)
		if _, err := clock.UptimeMS(); err == nil {
			t.Fatalf("invalid clock accepted: %s", value)
		}
	}
	write(t, clock.UptimeFile, "100.01 0.00")
	if got, err := clock.UptimeMS(); err != nil || got != 100010 {
		t.Fatalf("centisecond parsing lost precision: %d %v", got, err)
	}
}
