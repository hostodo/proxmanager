#!/usr/bin/env python3
import concurrent.futures
import http.client
import json
import os
from pathlib import Path
import socket
import subprocess
import sys
import tempfile
import time

binary = str(Path(sys.argv[1]).resolve())
boot = "bbcf2358-904b-4d53-bcea-a7d83c861038"
processes = []


def wait_until(check):
    deadline = time.monotonic() + 5
    while time.monotonic() < deadline:
        try:
            result = check()
            if result:
                return result
        except (OSError, ValueError):
            pass
        time.sleep(0.02)
    raise AssertionError("local fixture did not become ready")


with tempfile.TemporaryDirectory(prefix="hos110-", dir="/tmp") as directory:
    root = Path(directory)
    os.chmod(root, 0o700)

    def write(name, data, mode=0o600):
        path = root / name
        path.write_text(data)
        path.chmod(mode)
        return path

    write("boot", boot)
    write("uptime", "100.00 200.00\n")
    write("rows", '[{"vmid":1001,"status":"running"},{"vmid":1002,"status":"stopped"},{"vmid":9000,"status":"stopped","template":1}]')
    token = "fixture-only-status-token-0123456789"
    write("token", token)
    source = write("pvesh", '#!/bin/sh\ncd "$(dirname "$0")" || exit 1\nprintf "called\\n" >> calls\nif [ -e fail ]; then exit 1; fi\ncat rows\n', 0o700)
    common = ["--node-id=7", f"--snapshot={root / 'power.json'}", f"--boot-id-file={root / 'boot'}", f"--uptime-file={root / 'uptime'}"]
    observe = [binary, "observe", *common, "--node-name=fixture", f"--source-command={source}"]

    def collect(success=True):
        result = subprocess.run([*observe, "--once"], capture_output=True, timeout=3)
        assert (result.returncode == 0) == success, result.stderr.decode()

    def launch(args):
        process = subprocess.Popen(args, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
        processes.append(process)
        return process

    def stop(process):
        process.terminate()
        process.wait(timeout=5)
        assert process.returncode == 0, process.returncode

    class UnixHTTPConnection(http.client.HTTPConnection):
        def connect(self):
            self.sock = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
            self.sock.settimeout(self.timeout)
            self.sock.connect(str(root / "status.sock"))

    def request(auth=True, method="GET", path="/v1/power-state"):
        connection = UnixHTTPConnection("localhost", timeout=2)
        headers = {"Authorization": f"Bearer {token}"} if auth else {}
        try:
            connection.request(method, path, headers=headers)
            response = connection.getresponse()
            data = response.read()
            assert response.getheader("Cache-Control") == "no-store"
            return response.status, json.loads(data) if data.startswith(b"{") else data
        finally:
            connection.close()

    server_args = [binary, "serve-status", *common, f"--token-file={root / 'token'}", f"--unix-socket={root / 'status.sock'}"]
    try:
        server = launch(server_args)
        wait_until(lambda: (root / "status.sock").exists())
        assert request(False)[0] == 401
        assert request()[0] == 503
        assert not (root / "calls").exists(), "HTTP executed a source command"
        collect()
        first = request()[1]
        assert first["vms"] == [{"vmid":1001,"state":"running"},{"vmid":1002,"state":"stopped"}]
        assert first["schema_version"] == 1 and first["sequence"] == 1
        assert "sample_started_uptime_ms" not in first
        assert request(method="POST")[0] == 405
        assert request(path="/v1/power-state/../token")[0] == 404
        with concurrent.futures.ThreadPoolExecutor(max_workers=12) as pool:
            replies = list(pool.map(lambda _: request(), range(120)))
        assert all(status == 200 and data == first for status, data in replies)
        assert (root / "calls").read_text().count("called") == 1
        write("uptime", "130.00 200.00\n")
        stale = request()[1]
        assert stale["sample_age_ms"] == 30010
        assert stale["sample_started_at"] == first["sample_started_at"]
        assert stale["collector_epoch"] == first["collector_epoch"]
        stop(server)
        server = launch(server_args)
        wait_until(lambda: (root / "status.sock").exists())
        assert request()[1] == stale, "server restart rejuvenated an observation"
        before = (root / "power.json").read_bytes()
        write("fail", "")
        collect(False)
        assert (root / "power.json").read_bytes() == before
        assert request()[1] == stale
        (root / "fail").unlink()
        collect()
        second = request()[1]
        assert second["collector_epoch"] != first["collector_epoch"] and second["sequence"] == 1
        assert second["sample_age_ms"] == 10
        observer = launch([*observe, "--interval=100ms", "--timeout=90ms"])
        wait_until(lambda: request()[1]["sequence"] >= 2)
        collect(False)
        stop(observer)
        write("boot", "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa")
        assert request()[0] == 503, "old boot accepted"
        write("boot", boot)
        write("power.json", "{bad")
        assert request()[0] == 503
        assert request(False)[0] == 401
        stop(server)
        print("PASS binary integration: Unix HTTP, auth, concurrency, zero request subprocesses, stale age, restarts, failed collection, writer exclusion and boot rejection")
    finally:
        for process in processes:
            if process.poll() is None:
                process.terminate()
                try:
                    process.wait(timeout=5)
                except subprocess.TimeoutExpired:
                    process.kill()
                    process.wait()
