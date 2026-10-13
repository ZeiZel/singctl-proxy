#!/usr/bin/env python3
"""Read-only daemon identity and local PAC readiness check."""
import json
import socket
import sys
import urllib.request
from urllib.parse import urlparse

MAX_REPLY = 1024 * 1024


def exchange(sock_path, command):
    with socket.socket(socket.AF_UNIX, socket.SOCK_STREAM) as conn:
        conn.settimeout(1.0)
        conn.connect(sock_path)
        conn.sendall((command + "\n").encode())
        with conn.makefile("rb") as stream:
            reply = stream.readline(MAX_REPLY + 1)
    if not reply.endswith(b"\n") or len(reply) > MAX_REPLY or reply.startswith(b"ERR "):
        raise ValueError("invalid control reply")
    result = json.loads(reply)
    if not isinstance(result, dict):
        raise ValueError("invalid control object")
    return result


def valid_identity(value):
    return (isinstance(value, dict)
            and type(value.get("pid")) is int and value["pid"] > 0
            and isinstance(value.get("started_at"), str)
            and bool(value["started_at"].strip()))


class LocalOnlyRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, *_args, **_kwargs):
        raise ValueError("PAC redirects are not readiness")


def main():
    sock_path, instance_path = sys.argv[1:3]
    with open(instance_path, encoding="utf-8") as stream:
        instance = json.load(stream)
    if not valid_identity(instance):
        return 1
    status = exchange(sock_path, "STATUS")
    if not valid_identity(status):
        return 1
    if status["pid"] != instance["pid"] or status["started_at"] != instance["started_at"]:
        return 1
    sysproxy = exchange(sock_path, "SYSPROXY-STATUS")
    mode = sysproxy.get("mode")
    if mode == "off":
        print("OFF")
        return 0
    if mode not in ("include", "exclude"):
        return 1
    pac = sysproxy.get("pac_url") or sysproxy.get("pacURL")
    if not isinstance(pac, str):
        return 1
    parsed = urlparse(pac)
    if (parsed.scheme != "http" or parsed.hostname not in ("127.0.0.1", "localhost")
            or parsed.username or parsed.password or not parsed.path.endswith(".pac")):
        return 1
    if sysproxy.get("pac_server_up") is not True:
        return 1
    opener = urllib.request.build_opener(urllib.request.ProxyHandler({}), LocalOnlyRedirect())
    with opener.open(pac, timeout=2) as response:
        body = response.read(MAX_REPLY + 1)
    if len(body) > MAX_REPLY or b"FindProxyForURL" not in body:
        return 1
    print(pac)
    return 0


if __name__ == "__main__":
    try:
        sys.exit(main())
    except (OSError, ValueError, TypeError, KeyError):
        sys.exit(1)
