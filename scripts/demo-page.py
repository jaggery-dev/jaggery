#!/usr/bin/env python3
"""Serve the page (internal/dashboard/index.html) for a made-up house.

    python3 scripts/demo-page.py [--port 8765]

then open http://127.0.0.1:8765/. For screenshots in the README and docs:
the machines, apps, models and addresses are invented (addresses from
192.0.2.0/24), so nothing about a real house ends up in a picture. The page
code is the real one; only /v1/house and /v1/house/activity are faked.
"""
import argparse
import json
import os
from datetime import datetime, timedelta, timezone
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
PAGE = os.path.join(ROOT, "internal", "dashboard", "index.html")
GiB = 1 << 30


def iso(t):
    return t.isoformat().replace("+00:00", "Z")


def model(name, family, params, quant, size_gb, caps, loaded=False, vram_gb=0, days=10):
    m = {"name": name, "loaded": loaded, "size_bytes": int(size_gb * GiB), "digest": (name.encode().hex() * 8)[:64],
         "format": "gguf", "family": family, "parameters": params, "quantization": quant,
         "capabilities": caps, "first_seen": iso(NOW - timedelta(days=days))}
    if loaded:
        m["vram_bytes"] = int(vram_gb * GiB)
    return m


def machine(mid, name, os_, arch, cpu, cores, mem_gb, disk_total, disk_free, role, apps=(), gpus=None,
            runtimes=None, shares=None, serves_web=False, self_=False, reachable=True):
    inv = {
        "version": VERSION + " (abc1234, built 2026-10-24 12:00)", "build": VERSION, "commit": "abc1234" + "0" * 33,
        "protocol": 1, "min_protocol": 1, "running_since": iso(NOW - timedelta(hours=6)),
        "asset": f"jag-{os_}-{arch}", "hostname": name, "os": os_, "arch": arch, "cpu_model": cpu, "cpu_count": cores,
        "memory_bytes": mem_gb * GiB, "disk_total_bytes": disk_total * GiB, "disk_free_bytes": disk_free * GiB,
        "gpus": gpus, "runtimes": runtimes or [], "apps": list(apps), "role": role, "serves_web": serves_web,
        "collected_at": iso(NOW - timedelta(seconds=20)), "boot_time": iso(NOW - timedelta(days=3)),
        "update": {"policy": "auto", "channel": "prod", "checked_at": iso(NOW - timedelta(minutes=12))},
        "inbound": {"checked_by": "desktop", "checked_at": iso(NOW - timedelta(minutes=3)),
                    "checks": [{"port": p, "open": True} for p in (7373, 7374, 80)]},
    }
    if shares:
        inv["shares"] = shares
    return {"machine_id": mid, "name": name, "self": self_, "reachable": reachable,
            "fingerprint": "SHA256:" + (mid * 6)[:43], "inventory_at": iso(NOW - timedelta(seconds=20)), "inventory": inv}


def app(name, desc, port, hue, local_name=None):
    a = {"name": name, "description": desc, "port": port, "hue": hue}
    if local_name:
        a["local_name"] = local_name
    return a


def house():
    desktop = machine(
        "a1b2c3d4e5f6", "desktop", "linux", "amd64", "AMD Ryzen 7 5700X 8-Core Processor", 16, 32, 1863, 1210, "ai",
        apps=[app("Open WebUI", "Chat with the house's AI", 3000, 265, "chat")],
        gpus=[{"vendor": "nvidia", "model": "NVIDIA GeForce RTX 3060", "memory_bytes": 12 * GiB, "free_bytes": 7 * GiB}],
        runtimes=[{"kind": "ollama", "endpoint": "http://127.0.0.1:11434", "models": [
            model("qwen2.5-coder:7b", "qwen2", "7.6B", "Q4_K_M", 4.7, ["completion", "tools"], loaded=True, vram_gb=5.1),
            model("llama3.1:8b", "llama", "8.0B", "Q4_K_M", 4.9, ["completion", "tools"], days=20),
            model("nomic-embed-text", "nomic-bert", "137M", "F16", 0.3, ["embedding"], days=20),
        ]}],
        serves_web=True, self_=True)
    laptop = machine(
        "b2c3d4e5f6a1", "laptop", "darwin", "arm64", "Apple M2", 8, 16, 460, 210, "ai",
        gpus=[{"vendor": "apple", "model": "Apple M2 GPU", "memory_bytes": int(10.7 * GiB), "unified": True}],
        runtimes=[{"kind": "lmstudio", "endpoint": "http://127.0.0.1:1234", "models": [
            model("gemma-3-4b-it", "gemma3", "4B", "Q4_K_M", 2.5, ["completion", "vision"], days=5),
        ]}])
    pi = machine(
        "c3d4e5f6a1b2", "living-room-pi", "linux", "arm64", "Raspberry Pi 4 Model B Rev 1.4", 4, 4, 58, 41, "server",
        apps=[app("Home Assistant", "Lights, heating, the doorbell", 8123, 220, "home"),
              app("Uptime Kuma", "Is everything up?", 3001, 150)])
    nas = machine(
        "d4e5f6a1b2c3", "nas", "linux", "amd64", "Intel(R) Celeron(R) J4125 CPU @ 2.00GHz", 4, 8, 7450, 3020, "storage",
        apps=[app("Jellyfin", "Films and music for the house", 8096, 280, "movies"),
              app("Immich", "Everyone's photos", 2283, 25, "photos"),
              app("Paperless-ngx", "Scanned letters and bills", 8000, 95)],
        shares=[{"name": "notes", "writable": True, "use": {"reads": 14, "writes": 3, "apps": ["Open WebUI"]}},
                {"name": "recipes", "use": {"reads": 5, "apps": ["Open WebUI"]}}])
    return {"self": desktop, "peers": [laptop, pi, nas], "name_holder": "a1b2c3d4e5f6", "updated_at": iso(NOW)}


def activity():
    rows = [
        (4, "kitchen-ipad.local", "192.0.2.41", "Open WebUI", "Robin", "llama3.1:8b", "desktop", 412, 1800),
        (18, "laptop", "192.0.2.12", "Zed", None, "qwen2.5-coder:7b", "desktop", 960, 2400),
        (35, "laptop", "192.0.2.12", "Zed", None, "qwen2.5-coder:7b", "desktop", 1210, 3100),
        (52, "alex-phone.local", "192.0.2.57", "Open WebUI", "Alex", "gemma-3-4b-it", "laptop", 230, 2700),
        (90, "desktop", "192.0.2.10", "curl", None, "llama3.1:8b", "desktop", 64, 900),
    ]
    entries = [{"at": iso(NOW - timedelta(minutes=m)), "device": d, "addr": a, "app": ap, **({"user": u} if u else {}),
                "model": mo, "served_by": s, "status": 200, "stream": True, "completion_tokens": t,
                "prompt_tokens": t // 3, "duration_ms": ms, "routed_by": "desktop"}
               for m, d, a, ap, u, mo, s, t, ms in rows]
    return {"day": NOW.strftime("%Y-%m-%d"), "entries": entries}


class Handler(BaseHTTPRequestHandler):
    def log_message(self, *a):
        pass

    def send(self, body, ctype):
        self.send_response(200)
        self.send_header("Content-Type", ctype)
        self.end_headers()
        self.wfile.write(body)

    def do_GET(self):
        path = self.path.split("?", 1)[0]
        if path == "/v1/house":
            return self.send(json.dumps(house()).encode(), "application/json")
        if path.startswith("/v1/house/activity"):
            return self.send(json.dumps(activity()).encode(), "application/json")
        if path in ("/", "/index.html"):
            return self.send(open(PAGE, "rb").read(), "text/html; charset=utf-8")
        self.send_response(404)
        self.end_headers()


if __name__ == "__main__":
    p = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    p.add_argument("--port", type=int, default=8765)
    args = p.parse_args()
    NOW = datetime.now(timezone.utc).replace(microsecond=0)
    VERSION = "0.6.0"
    print(f"the page for a made-up house: http://127.0.0.1:{args.port}/")
    ThreadingHTTPServer(("127.0.0.1", args.port), Handler).serve_forever()
