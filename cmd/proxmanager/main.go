package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/hostodo/proxmanager/internal/powerstatus"
)

var version = "development"

func main() {
	log.SetFlags(log.LstdFlags | log.LUTC)
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer cancel()
	if err := run(ctx, os.Args[1:]); err != nil {
		log.Print(err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return errors.New("usage: proxmanager observe|serve-status|version")
	}
	if args[0] == "version" {
		fmt.Println(version)
		return nil
	}
	if args[0] != "observe" && args[0] != "serve-status" {
		return errors.New("unknown command; use observe, serve-status or version")
	}
	fs := flag.NewFlagSet(args[0], flag.ContinueOnError)
	path := fs.String("snapshot", "/run/hostodo-node/power.json", "absolute snapshot path")
	nodeID := fs.Int64("node-id", 0, "positive OdoPanel node ID")
	boot := fs.String("boot-id-file", "/proc/sys/kernel/random/boot_id", "node boot identity file")
	uptime := fs.String("uptime-file", "/proc/uptime", "boot-relative uptime file")
	var command, nodeName, tokenFile, socket, tcp *string
	var interval, timeout *time.Duration
	var once *bool
	if args[0] == "observe" {
		command = fs.String("source-command", "/usr/bin/pvesh", "absolute source command path")
		nodeName = fs.String("node-name", "", "Proxmox node name")
		interval = fs.Duration("interval", 2*time.Second, "start-to-start sample interval")
		timeout = fs.Duration("timeout", time.Second, "source query deadline")
		once = fs.Bool("once", false, "collect once and exit")
	} else {
		tokenFile = fs.String("token-file", "", "private status-only token file")
		socket = fs.String("unix-socket", "/run/proxmanager-status/status.sock", "Unix socket path; empty for loopback TCP")
		tcp = fs.String("listen", "", "loopback IP:port for local integration")
	}
	if err := fs.Parse(args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if fs.NArg() != 0 || *nodeID <= 0 {
		return errors.New("a positive --node-id is required and positional arguments are not allowed")
	}
	clock := powerstatus.Clock{BootIDFile: *boot, UptimeFile: *uptime}
	if args[0] == "observe" {
		if *interval < 100*time.Millisecond || *interval > time.Minute || *timeout > *interval {
			return errors.New("interval must be 100ms..1m and timeout must not exceed interval")
		}
		source := powerstatus.Source{Command: *command, NodeName: *nodeName, Timeout: *timeout}
		if err := source.Validate(); err != nil {
			return err
		}
		lock, err := powerstatus.LockObserver(*path)
		if err != nil {
			return err
		}
		defer lock.Close()
		epoch, err := powerstatus.NewEpoch()
		if err != nil {
			return err
		}
		observer := powerstatus.Observer{Source: source, Clock: clock, Path: *path, NodeID: *nodeID, Epoch: epoch}
		collect := func() error { return observer.Collect(ctx) }
		if *once {
			return collect()
		}
		ticker := time.NewTicker(*interval)
		defer ticker.Stop()
		for {
			if err := collect(); err != nil && ctx.Err() == nil {
				log.Print("power observation failed; previous snapshot retained")
			}
			select {
			case <-ctx.Done():
				return nil
			case <-ticker.C:
			}
		}
	}
	token, err := powerstatus.ReadToken(*tokenFile)
	if err != nil {
		return err
	}
	listener, err := powerstatus.Listen(*socket, *tcp)
	if err != nil {
		return err
	}
	defer listener.Close()
	server := &http.Server{Handler: powerstatus.Handler(*path, *nodeID, clock, token), ReadHeaderTimeout: 2 * time.Second, ReadTimeout: 3 * time.Second, WriteTimeout: 3 * time.Second, IdleTimeout: 15 * time.Second, MaxHeaderBytes: 8192}
	log.Printf("status server listening on %s", listener.Addr())
	return serveStatus(ctx, server, listener)
}

func serveStatus(ctx context.Context, server *http.Server, listener net.Listener) error {
	served := make(chan error, 1)
	go func() { served <- server.Serve(listener) }()
	select {
	case err := <-served:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		stop, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := server.Shutdown(stop); err != nil {
			_ = server.Close()
			return err
		}
		return nil
	}
}
