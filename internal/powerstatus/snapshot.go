package powerstatus

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const MaxSnapshotBytes = 2 * 1024 * 1024
const MaxVMs = 10000

// Linux /proc/uptime truncates to centiseconds. Add one bucket to bound age.
const uptimeResolutionMS = 10

var uuidPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

type VM struct {
	VMID  int64  `json:"vmid"`
	State string `json:"state"`
}

type Snapshot struct {
	SchemaVersion     int       `json:"schema_version"`
	NodeID            int64     `json:"node_id"`
	BootID            string    `json:"boot_id"`
	CollectorEpoch    string    `json:"collector_epoch"`
	Sequence          uint64    `json:"sequence"`
	SampleStartedAt   time.Time `json:"sample_started_at"`
	SampleCompletedAt time.Time `json:"sample_completed_at"`
	SampleAgeMS       int64     `json:"sample_age_ms"`
	VMs               []VM      `json:"vms"`
}

type DiskSnapshot struct {
	Snapshot
	SampleStartedUptimeMS int64 `json:"sample_started_uptime_ms"`
}

type Clock struct {
	BootIDFile string
	UptimeFile string
}

func (c Clock) BootID() (string, error) {
	b, err := readBounded(c.BootIDFile, 128)
	if err != nil {
		return "", err
	}
	id := strings.TrimSpace(string(b))
	if !uuidPattern.MatchString(id) {
		return "", errors.New("invalid boot identity")
	}
	return id, nil
}

func (c Clock) UptimeMS() (int64, error) {
	b, err := readBounded(c.UptimeFile, 256)
	if err != nil {
		return 0, err
	}
	fields := strings.Fields(string(b))
	if len(fields) != 2 {
		return 0, errors.New("invalid boot-relative clock")
	}
	parts := strings.Split(fields[0], ".")
	if len(parts) != 2 || len(parts[1]) != 2 {
		return 0, errors.New("invalid boot-relative clock precision")
	}
	seconds, secondsErr := strconv.ParseInt(parts[0], 10, 64)
	centiseconds, fractionErr := strconv.ParseInt(parts[1], 10, 64)
	if secondsErr != nil || fractionErr != nil || seconds < 0 || centiseconds < 0 || centiseconds > 99 || seconds > (math.MaxInt64-1000)/1000 {
		return 0, errors.New("invalid boot-relative clock")
	}
	return seconds*1000 + centiseconds*uptimeResolutionMS, nil
}

func NewEpoch() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	h := hex.EncodeToString(b)
	return h[:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:], nil
}

func validate(s DiskSnapshot) error {
	if s.SchemaVersion != 1 || s.NodeID <= 0 || s.Sequence == 0 || !uuidPattern.MatchString(s.BootID) || !uuidPattern.MatchString(s.CollectorEpoch) {
		return errors.New("invalid snapshot identity")
	}
	if s.SampleStartedAt.IsZero() || s.SampleCompletedAt.IsZero() || s.SampleCompletedAt.Before(s.SampleStartedAt) || s.SampleStartedUptimeMS < 0 || s.SampleAgeMS < 0 {
		return errors.New("invalid snapshot observation time")
	}
	if s.VMs == nil || len(s.VMs) > MaxVMs {
		return errors.New("invalid VM collection")
	}
	seen := make(map[int64]bool, len(s.VMs))
	for _, vm := range s.VMs {
		if vm.VMID <= 0 || seen[vm.VMID] || (vm.State != "running" && vm.State != "stopped") {
			return errors.New("invalid VM observation")
		}
		seen[vm.VMID] = true
	}
	return nil
}

func readBounded(path string, limit int64) ([]byte, error) {
	fd, err := syscall.Open(path, syscall.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK|syscall.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fd), path)
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("expected regular file")
	}
	b, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > limit {
		return nil, errors.New("file exceeds size limit")
	}
	return b, nil
}

func ReadSnapshot(path string, nodeID int64, clock Clock) (Snapshot, error) {
	b, err := readBounded(path, MaxSnapshotBytes)
	if err != nil {
		return Snapshot{}, err
	}
	var disk DiskSnapshot
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&disk); err != nil {
		return Snapshot{}, err
	}
	if err := dec.Decode(new(any)); err != io.EOF {
		return Snapshot{}, errors.New("trailing snapshot data")
	}
	if err := validate(disk); err != nil {
		return Snapshot{}, err
	}
	bootID, err := clock.BootID()
	if err != nil {
		return Snapshot{}, err
	}
	uptime, err := clock.UptimeMS()
	if err != nil {
		return Snapshot{}, err
	}
	if disk.NodeID != nodeID || disk.BootID != bootID || uptime < disk.SampleStartedUptimeMS {
		return Snapshot{}, errors.New("snapshot does not belong to this node boot")
	}
	disk.SampleAgeMS = uptime - disk.SampleStartedUptimeMS + uptimeResolutionMS
	return disk.Snapshot, nil
}

func prepareDirectory(path string) error {
	if !filepath.IsAbs(path) {
		return errors.New("snapshot path must be absolute")
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0750); err != nil {
		return err
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode().Perm()&0022 != 0 {
		return errors.New("snapshot directory must not be a symlink or writable by group/others")
	}
	return nil
}

func WriteSnapshot(path string, snapshot DiskSnapshot) error {
	if err := validate(snapshot); err != nil {
		return err
	}
	b, err := json.Marshal(snapshot)
	if err != nil {
		return err
	}
	if len(b) > MaxSnapshotBytes {
		return errors.New("snapshot exceeds size limit")
	}
	if err := prepareDirectory(path); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".power-*.json")
	if err != nil {
		return err
	}
	temp := f.Name()
	defer os.Remove(temp)
	defer f.Close()
	if err := f.Chmod(0640); err != nil {
		return err
	}
	if _, err := f.Write(b); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(temp, path)
}

func LockObserver(path string) (*os.File, error) {
	if err := prepareDirectory(path); err != nil {
		return nil, err
	}
	lockPath := filepath.Join(filepath.Dir(path), ".observer.lock")
	fd, err := syscall.Open(lockPath, syscall.O_CREAT|syscall.O_RDWR|syscall.O_NOFOLLOW|syscall.O_NONBLOCK|syscall.O_CLOEXEC, 0640)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fd), lockPath)
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		f.Close()
		return nil, errors.New("invalid observer lock file")
	}
	if err := syscall.Flock(fd, syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return nil, fmt.Errorf("observer already running: %w", err)
	}
	return f, nil
}
