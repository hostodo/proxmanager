package powerstatus

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"syscall"
	"time"
)

var nodeNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,63}$`)

type Source struct {
	Command  string
	NodeName string
	Timeout  time.Duration
}

type boundedBuffer struct{ data bytes.Buffer }

func (b *boundedBuffer) Write(p []byte) (int, error) {
	if len(p) > MaxSnapshotBytes-b.data.Len() {
		return 0, errors.New("source output exceeds limit")
	}
	return b.data.Write(p)
}

func (s Source) Validate() error {
	if !filepath.IsAbs(s.Command) || !nodeNamePattern.MatchString(s.NodeName) || s.Timeout <= 0 || s.Timeout > time.Minute {
		return errors.New("invalid source configuration")
	}
	return nil
}

func (s Source) Read(ctx context.Context) ([]VM, error) {
	if err := s.Validate(); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, s.Timeout)
	defer cancel()
	cmd := exec.Command(s.Command, "--noproxy", "get", "/nodes/"+s.NodeName+"/qemu", "--full", "0", "--output-format", "json")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.WaitDelay = 100 * time.Millisecond
	var output boundedBuffer
	cmd.Stdout = &output
	cmd.Stderr = io.Discard
	runErr := runProcessGroup(ctx, cmd)
	if runErr != nil {
		return nil, errors.New("source command failed or exceeded its bounds")
	}
	var rows []struct {
		VMID     int64  `json:"vmid"`
		Status   string `json:"status"`
		Template int    `json:"template"`
	}
	dec := json.NewDecoder(bytes.NewReader(output.data.Bytes()))
	if err := dec.Decode(&rows); err != nil || rows == nil || len(rows) > MaxVMs {
		return nil, errors.New("invalid source VM list")
	}
	if err := dec.Decode(new(any)); err != io.EOF {
		return nil, errors.New("trailing source data")
	}
	vms := make([]VM, 0, len(rows))
	seen := make(map[int64]bool, len(rows))
	for _, row := range rows {
		if row.VMID <= 0 || seen[row.VMID] || (row.Template != 0 && row.Template != 1) {
			return nil, errors.New("invalid source VM identity")
		}
		seen[row.VMID] = true
		if row.Template == 1 {
			continue
		}
		if row.Status != "running" && row.Status != "stopped" {
			return nil, errors.New("unsupported source power state")
		}
		vms = append(vms, VM{VMID: row.VMID, State: row.Status})
	}
	sort.Slice(vms, func(i, j int) bool { return vms[i].VMID < vms[j].VMID })
	return vms, nil
}

type Observer struct {
	Source   Source
	Clock    Clock
	Path     string
	NodeID   int64
	Epoch    string
	Sequence uint64
}

func (o *Observer) Collect(ctx context.Context) error {
	boot, err := o.Clock.BootID()
	if err != nil {
		return err
	}
	startedUptime, err := o.Clock.UptimeMS()
	if err != nil {
		return err
	}
	started := time.Now().UTC()
	vms, err := o.Source.Read(ctx)
	if err != nil {
		return err
	}
	finishedUptime, err := o.Clock.UptimeMS()
	if err != nil {
		return err
	}
	finishedBoot, err := o.Clock.BootID()
	if err != nil {
		return err
	}
	if boot != finishedBoot || finishedUptime < startedUptime {
		return errors.New("node boot changed during collection")
	}
	completed := time.Now().UTC()
	if completed.Before(started) {
		return errors.New("wall clock moved backwards during collection")
	}
	if o.Sequence == ^uint64(0) {
		return errors.New("observation sequence exhausted")
	}
	next := o.Sequence + 1
	snapshot := DiskSnapshot{Snapshot: Snapshot{
		SchemaVersion: 1, NodeID: o.NodeID, BootID: boot, CollectorEpoch: o.Epoch, Sequence: next,
		SampleStartedAt: started, SampleCompletedAt: completed, SampleAgeMS: finishedUptime - startedUptime + uptimeResolutionMS, VMs: vms,
	}, SampleStartedUptimeMS: startedUptime}
	if err := WriteSnapshot(o.Path, snapshot); err != nil {
		return err
	}
	o.Sequence = next
	return nil
}
