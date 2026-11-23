# Node power observations

This additive Go binary collects one bulk QEMU status list per node every two
seconds and publishes a local snapshot. Its separate HTTP process reads that
file. An HTTP request never starts `pvesh` or probes a VM. Existing Python
snippet routes and their service configuration are unchanged.

The component is not installed or enabled by the existing Python setup. The
sample service and nginx files require an explicit operator rollout. Do not
replace the deployed snippet service with this binary.

## Build and verify

Go 1.23 or newer and Python 3 are required for development. The binary has no
external Go dependencies. Linux is the deployment target; macOS supports local
fixture verification.

```sh
make check cross-build
./build/proxmanager version
./build/proxmanager observe --help
./build/proxmanager serve-status --help
```

`make check` runs the Go race detector, behavioral tests, vet and a real binary
integration test. The integration test uses a private temporary directory, fake
`pvesh` output and a Unix socket. It verifies missing and invalid observations,
authentication, concurrent reads, stale source age, collector/server restarts,
writer exclusion, boot rejection and the absence of request-triggered commands.
`make cross-build` produces static Linux amd64 and arm64 binaries in `build/`.

These checks do not establish live Proxmox source freshness, host overhead or
production end-to-end p99 latency. Those require a separately approved canary.

## Collection and publication

The observer invokes exactly this command with a one-second deadline:

```text
/usr/bin/pvesh --noproxy get /nodes/<PVE_NODE_NAME>/qemu --full 0 --output-format json
```

The leading `--noproxy` makes PVE reject a target other than its local node or
`localhost`, so a misconfigured node name cannot fetch a peer's observations.
This uses PVE's own local-node check in
[`pvesh check_proxyto`](https://github.com/proxmox/pve-manager/blob/master/PVE/CLI/pvesh.pm).

It accepts unique positive VMIDs with `running` or `stopped` status and excludes
rows marked `template: 1`. An unsupported state, malformed list, failed command,
timeout or oversized output rejects the whole sample and preserves the previous
file. No failure fabricates `stopped`. Verify this bulk endpoint's status and
template semantics on the target PVE release before rollout; collection frequency
does not prove how recently PVE itself sampled the state.

Output is capped at 2 MiB and 10,000 rows, with no per-VM subprocesses. Child
processes run in their own process group and are killed after completion or
cancellation, before reaping the group leader. The leader's unreaped PID keeps
the process-group identity reserved until cleanup finishes. The observer waits
at most another 100 ms for command pipes. Stderr
is discarded and failure logs contain no source output. Collection is serial;
a slow or failed query cannot create an overlapping collector within the process.
A nonblocking file lock prevents a second observer using the same directory.
The `.observer.lock` filename is reserved and cannot be a snapshot destination.

Each successful sample gets a sequence number. Each observer process starts a
new random UUID epoch. The boot ID comes from `/proc/sys/kernel/random/boot_id`.
The observer records UTC start/completion timestamps and boot-relative sample
start from `/proc/uptime`, including source query duration in the reported age.
Age includes a conservative 10 ms allowance because Linux exposes uptime at
centisecond precision. UTC timestamps are explanatory; clock corrections must
not be used to replace the boot-relative age.

The private disk envelope extends the public response with
`sample_started_uptime_ms`. This field never appears in HTTP responses. The
file is written to a temporary file in the same directory, closed and atomically
renamed to `/run/hostodo-node/power.json`. It is mode 0640 in a mode 0750 directory.
The runtime directory must be on a local filesystem, with trusted ancestors and
no other writers. Final symlinks and nonregular files are rejected. Publication
does not fsync because `/run` is ephemeral and snapshots are rebuilt after boot;
atomicity protects concurrent readers, not persistence across a host crash.

## HTTP contract

`GET /v1/power-state` requires a separate status-only token in
`Authorization: Bearer <token>`. The token must contain at least 32 non-whitespace
characters, loaded from a regular file without world permissions or group write
access. Tokens are never logged. Rotate by replacing the credential file and
restarting only the HTTP service. Existing Proxmox mutation credentials are not
accepted by this service.

Authentication runs before path validation or any snapshot/clock reads. Missing,
invalid, wrong-node or wrong-boot files return 503. Authentication failure returns
401. Other methods return 405 and other paths return 404. Every response has
`Cache-Control: no-store`. A valid old file returns 200 with its original identity
and timestamps; source age increases using boot-relative time. The central
consumer decides freshness. A server restart cannot rejuvenate an observation.

```json
{"schema_version":1,"node_id":7,"boot_id":"bbcf2358-904b-4d53-bcea-a7d83c861038","collector_epoch":"d15b7b76-4869-4fb0-89a9-b560edc03167","sequence":1,"sample_started_at":"2026-11-23T05:00:00Z","sample_completed_at":"2026-11-23T05:00:00.250Z","sample_age_ms":300,"vms":[{"vmid":1001,"state":"running"},{"vmid":1002,"state":"stopped"}]}
```

`node_id` is the positive OdoPanel database ID, distinct from the PVE node name.
A missing VM is absent from the list, never inferred stopped. The endpoint has no
VM selectors, file path parameters or mutation methods.

## Service separation and ingress

The sample files in `packaging/` are reviewable deployment inputs, not an
installer. Before enabling them, the operator must:

1. Create a dedicated system user and group named `proxmanager-status`, without
   a login shell. Verify the nginx worker group is `www-data`, or adjust the HTTP
   service's primary group to the actual nginx group.
2. Install the binary as root-owned mode 0755 at `/usr/local/libexec/proxmanager`.
   Create root-owned `/etc/proxmanager-status` mode 0700. Copy the node environment
   example there as `node.env` and set the real database ID and PVE name. Generate
   a separate random status-only credential in `status-token`, root-owned mode
   0600. Do not put tokens in environment files, command lines or Git.
3. Review the systemd units on the target Linux/systemd/PVE release. The observer
   is privileged to run the local bulk query and writes root:`proxmanager-status`
   files. Its runtime directory survives process restarts but disappears on boot.
   The HTTP service runs as `proxmanager-status`, has supplementary read access
   to those files, an empty capability set, and cannot write the snapshot directory.
   `LoadCredential` supplies its private token. It listens only on a native Unix
   socket with nginx group access. The HTTP runtime directory is recreated when
   the service stops, removing a socket left by a crash.
4. Add the exact nginx location inside the existing private TLS server block.
   Keep existing snippet locations intact. Restrict ingress to the control plane
   using the existing network/access policy. The Go endpoint also checks the
   status token; nginx must forward Authorization. Do not expose the Unix socket
   through an unauthenticated file alias or disable token checks.
5. Validate unit/nginx configuration and permissions, then canary collection,
   template exclusion, API state parity, timeout behavior and recovery on one
   approved node. Confirm p95/p99 transition detection including source-internal
   delay, central polling and browser refresh before expanding.

The server refuses to unlink an existing socket, so a manual invocation after a
crash may require the operator to verify no server is running and remove its
stale socket. The systemd runtime-directory lifecycle handles this for the sample
service. The server can alternatively bind a numeric loopback address for local
integration. Public addresses and hostnames are rejected:

```sh
./build/proxmanager serve-status --node-id=7 --token-file=/private/path/token \
  --snapshot=/private/path/power.json --unix-socket= --listen=127.0.0.1:8181
```

`--boot-id-file`, `--uptime-file` and `--source-command` exist for local fixture
integration. Deployment should keep their defaults. Stop/disable these additive
units and remove their exact nginx location to roll back. The old snippet API
continues running independently; central readers should show unknown/stale when
observations disappear rather than falling back to live per-VM calls.
