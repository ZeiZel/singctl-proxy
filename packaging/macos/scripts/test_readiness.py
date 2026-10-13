#!/usr/bin/env python3
"""Hermetic readiness regressions: only temporary state and loopback fixtures."""
import contextlib
import http.server
import json
import pathlib
import socket
import subprocess
import sys
import tempfile
import threading
import time
import unittest

HELPER = pathlib.Path(__file__).with_name("check-readiness.py")
IDENTITY = {"pid": 4242, "started_at": "fixture-start"}
PAC_BODY = b'function FindProxyForURL(url, host) { return "DIRECT"; }'


@contextlib.contextmanager
def daemon_fixture(path, status, sysproxy, fragmented=False):
    listener = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
    listener.bind(str(path))
    listener.listen()
    listener.settimeout(0.05)
    stop = threading.Event()

    def serve():
        while not stop.is_set():
            try:
                conn, _ = listener.accept()
            except socket.timeout:
                continue
            except OSError:
                return
            with conn:
                conn.settimeout(1)
                command = conn.makefile("rb").readline().strip()
                reply = status if command == b"STATUS" else sysproxy
                wire = (reply if isinstance(reply, bytes) else json.dumps(reply).encode()) + b"\n"
                try:
                    if fragmented:
                        midpoint = max(1, len(wire) // 2)
                        conn.sendall(wire[:midpoint])
                        time.sleep(0.02)
                        conn.sendall(wire[midpoint:])
                    else:
                        conn.sendall(wire)
                except (BrokenPipeError, ConnectionResetError):
                    pass

    thread = threading.Thread(target=serve, daemon=True)
    thread.start()
    try:
        yield
    finally:
        stop.set()
        thread.join(2)
        listener.close()


@contextlib.contextmanager
def pac_fixture(body=PAC_BODY, status=200):
    class Handler(http.server.BaseHTTPRequestHandler):
        def do_GET(self):
            self.send_response(status)
            self.send_header("Content-Length", str(len(body)))
            self.send_header("Connection", "close")
            self.end_headers()
            self.wfile.write(body)

        def log_message(self, *_):
            pass

    server = http.server.ThreadingHTTPServer(("127.0.0.1", 0), Handler)
    thread = threading.Thread(target=server.serve_forever, daemon=True)
    thread.start()
    try:
        yield f"http://127.0.0.1:{server.server_port}/proxy.pac"
    finally:
        server.shutdown()
        server.server_close()
        thread.join(2)


class ReadinessTests(unittest.TestCase):
    def probe(self, instance=IDENTITY, status=IDENTITY, sysproxy=None, fragmented=False):
        if sysproxy is None:
            sysproxy = {"mode": "off"}
        # Short macOS pathname avoids sockaddr_un's 104-byte length limit.
        with tempfile.TemporaryDirectory(prefix="readiness-", dir="/tmp") as directory:
            root = pathlib.Path(directory)
            state = {"profile.txt": b"fixture-profile", "sysproxy.ini": b"fixture-settings", "autostart-mode.txt": b"off"}
            for name, data in state.items():
                (root / name).write_bytes(data)
            (root / "instance.json").write_text(instance if isinstance(instance, str) else json.dumps(instance))
            snapshot = {name: path.read_bytes() for name, path in ((p.name, p) for p in root.iterdir())}
            with daemon_fixture(root / "control.sock", status, sysproxy, fragmented):
                result = subprocess.run([sys.executable, str(HELPER), str(root / "control.sock"), str(root / "instance.json")], capture_output=True, timeout=30)
            self.assertEqual(snapshot, {name: (root / name).read_bytes() for name in snapshot}, "readiness mutated persisted state")
            return result

    def test_off_and_fragmented_reply(self):
        result = self.probe(fragmented=True)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(result.stdout.strip(), b"OFF")

    def test_reject_invalid_advertisement_and_status(self):
        for invalid in ("{broken", {}, {"pid": 0, "started_at": "x"}, {"pid": 4242}, {"pid": "4242", "started_at": "x"}, {"pid": True, "started_at": "x"}, {"pid": 4242, "started_at": 42}):
            with self.subTest(instance=invalid):
                self.assertNotEqual(self.probe(instance=invalid).returncode, 0)
        for invalid in ({"pid": True, "started_at": "x"}, {"pid": 4242, "started_at": 42}):
            with self.subTest(matching_invalid=invalid):
                self.assertNotEqual(self.probe(instance=invalid, status=invalid).returncode, 0)
        for invalid in ({}, {"pid": 4242}, {"pid": 4243, "started_at": "fixture-start"}, {"pid": 4242, "started_at": "previous-start"}):
            with self.subTest(status=invalid):
                self.assertNotEqual(self.probe(status=invalid).returncode, 0)

    def test_reject_error_or_missing_sysproxy_mode(self):
        for invalid in (b"ERR unavailable", {}, {"mode": "unknown"}, {"mode": "include", "pac_server_up": True}):
            with self.subTest(sysproxy=invalid):
                self.assertNotEqual(self.probe(sysproxy=invalid).returncode, 0)

    def test_active_pac_response_required(self):
        with pac_fixture() as url:
            for mode in ("include", "exclude"):
                result = self.probe(sysproxy={"mode": mode, "pac_url": url, "pac_server_up": True})
                self.assertEqual(result.returncode, 0, result.stderr)
            self.assertNotEqual(self.probe(sysproxy={"mode": "include", "pac_url": url, "pac_server_up": False}).returncode, 0)
            self.assertNotEqual(self.probe(sysproxy={"mode": "include", "pac_url": url}).returncode, 0)
        # Closed HTTP listener, error response and wrong body must not prove ready.
        self.assertNotEqual(self.probe(sysproxy={"mode": "include", "pac_url": url, "pac_server_up": True}).returncode, 0)
        for body, status in ((PAC_BODY, 503), (b"not a PAC file", 200)):
            with pac_fixture(body, status) as url:
                self.assertNotEqual(self.probe(sysproxy={"mode": "include", "pac_url": url, "pac_server_up": True}).returncode, 0)


if __name__ == "__main__":
    unittest.main()
