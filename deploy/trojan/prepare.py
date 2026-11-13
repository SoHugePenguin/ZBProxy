#!/usr/bin/env python3
"""Generate a TCP-only Trojan/TLS entry for an existing ZBProxy deployment."""

import argparse
import ipaddress
import json
import os
from pathlib import Path
import re
import secrets


def port(value):
    number = int(value)
    if not 1 <= number <= 65535:
        raise argparse.ArgumentTypeError("port must be between 1 and 65535")
    return number


def generate(domain, server_ip, upstream_port, local_port, tunnel_port, password):
    server = {
        "log": {"level": "info", "timestamp": True},
        "inbounds": [{
            "type": "trojan", "tag": "minecraft-tls",
            "listen": "0.0.0.0", "listen_port": tunnel_port,
            "users": [{"name": "minecraft", "password": password}],
            "tls": {
                "enabled": True, "alpn": ["http/1.1"], "min_version": "1.2",
                "certificate_path": f"/certs/live/{domain}/fullchain.pem",
                "key_path": f"/certs/live/{domain}/privkey.pem",
            },
            "fallback": {"server": "127.0.0.1", "server_port": 18088},
        }],
        "outbounds": [{"type": "direct", "tag": "zbproxy-only"}],
        "route": {
            "rules": [
                {"inbound": ["minecraft-tls"], "network": "udp", "action": "reject"},
                {"inbound": ["minecraft-tls"], "auth_user": ["minecraft"], "action": "route",
                 "outbound": "zbproxy-only", "override_address": "127.0.0.1",
                 "override_port": upstream_port},
            ],
            "final": "zbproxy-only",
        },
    }
    client = {
        "log": {"level": "info", "timestamp": True},
        "inbounds": [{
            "type": "direct", "tag": "minecraft-local", "listen": "127.0.0.1",
            "listen_port": local_port, "network": "tcp",
            "override_address": "127.0.0.1", "override_port": upstream_port,
        }],
        "outbounds": [{
            "type": "trojan", "tag": "encrypted", "server": server_ip,
            "server_port": tunnel_port, "password": password, "network": "tcp",
            "tls": {
                "enabled": True, "server_name": domain,
                "alpn": ["http/1.1"], "min_version": "1.2",
                "utls": {"enabled": True, "fingerprint": "chrome"},
            },
        }],
        "route": {"final": "encrypted"},
    }
    return server, client


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--domain", required=True)
    parser.add_argument("--email", required=True)
    parser.add_argument("--server-ip", default="64.83.0.57")
    parser.add_argument("--upstream-port", type=port, default=55565,
                        help="Existing host-side ZBProxy game port, NOT its web port")
    parser.add_argument("--local-port", type=port, default=25565)
    parser.add_argument("--tunnel-port", type=port, default=443)
    parser.add_argument("--output-dir", type=Path, default=Path(__file__).resolve().parent)
    args = parser.parse_args()
    domain = args.domain.lower().rstrip(".")
    if len(domain) > 253 or "." not in domain or not all(
        re.fullmatch(r"[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?", label)
        for label in domain.split(".")
    ):
        parser.error("--domain must be a DNS hostname, such as game.example.com")
    if not re.fullmatch(r"[^\s@=$]+@[^\s@=$]+\.[^\s@=$]+", args.email):
        parser.error("--email must be a valid email address")
    try:
        ipaddress.ip_address(args.server_ip)
    except ValueError:
        parser.error("--server-ip must be an IP address")
    base = args.output_dir.resolve()
    runtime = base / "runtime"
    runtime.mkdir(parents=True, exist_ok=True, mode=0o700)
    if any((runtime / name).exists() for name in ["server.json", "client.json"]) or (base / ".env").exists():
        parser.error("configuration already exists; edit it in place to preserve the password")
    password = secrets.token_urlsafe(32)
    server, client = generate(domain, args.server_ip, args.upstream_port,
                              args.local_port, args.tunnel_port, password)
    files = {
        runtime / "server.json": json.dumps(server, indent=2) + "\n",
        runtime / "client.json": json.dumps(client, indent=2) + "\n",
        base / ".env": f"TUNNEL_DOMAIN={domain}\nACME_EMAIL={args.email}\n",
    }
    for path, content in files.items():
        fd = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
        with os.fdopen(fd, "w") as output:
            output.write(content)
    print("Created runtime/server.json, runtime/client.json and .env.")
    print(f"TLS endpoint: {args.server_ip}:{args.tunnel_port} (certificate name: {domain})")
    print(f"Existing ZBProxy target: 127.0.0.1:{args.upstream_port}")
    print(f"Minecraft client address: 127.0.0.1:{args.local_port}")
    print("Transfer runtime/client.json privately to the player computer; it contains the tunnel password.")


if __name__ == "__main__":
    main()
