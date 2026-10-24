#!/usr/bin/env python3
"""A stand-in for GitHub's releases API, for the test house (phase 4 Q3).

    fake-releases.py --dir DIR --listen 192.0.2.1:8080

Each folder in DIR is a release named by its tag (v0.9.0-beta.2), holding its
assets (jag-linux-amd64, SHA256SUMS, release.json, ...). A tag with "-beta."
is a prerelease. It answers the three calls `jag` makes:

    GET /repos/<owner>/<repo>/releases?per_page=50
    GET /repos/<owner>/<repo>/releases/tags/<tag>
    GET /assets/<tag>/<name>

If DIR/allow exists, only the addresses listed in it (one per line) get an
answer; the rest get 404, as a private repo gives a machine without a token.
The file is read on every request, so a test can change it as it goes.
"""
import argparse
import datetime
import json
import os
import re
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--dir", required=True)
    ap.add_argument("--listen", required=True)
    args = ap.parse_args()
    root = os.path.abspath(args.dir)
    host, port = args.listen.rsplit(":", 1)

    def allowed(addr):
        try:
            with open(os.path.join(root, "allow")) as f:
                return addr in {line.strip() for line in f if line.strip()}
        except FileNotFoundError:
            return True

    def asset(d, tag, name, base):
        st = os.stat(os.path.join(d, name))
        updated = datetime.datetime.fromtimestamp(st.st_mtime_ns / 1e9, datetime.timezone.utc)
        return {"name": name, "url": f"{base}/assets/{tag}/{name}", "size": st.st_size,
                "updated_at": updated.strftime("%Y-%m-%dT%H:%M:%S.%fZ")}

    def release(tag, base):
        d = os.path.join(root, tag)
        manifest = os.path.join(d, "release.json")
        created = datetime.datetime.fromtimestamp(os.path.getmtime(manifest), datetime.timezone.utc)
        return {
            "tag_name": tag,
            "draft": False,
            "prerelease": "-beta." in tag,
            "created_at": created.strftime("%Y-%m-%dT%H:%M:%S.%fZ"),
            # size and updated_at change when a file is replaced, as a
            # re-uploaded asset's do on GitHub (agents key their cache on them).
            "assets": [asset(d, tag, n, base) for n in sorted(os.listdir(d))],
        }

    def tags():
        return [t for t in os.listdir(root)
                if os.path.isdir(os.path.join(root, t)) and os.path.exists(os.path.join(root, t, "release.json"))]

    class Handler(BaseHTTPRequestHandler):
        def log_message(self, fmt, *a):
            pass

        def send(self, code, body, ctype="application/json"):
            if isinstance(body, (dict, list)):
                body = json.dumps(body).encode()
            self.send_response(code)
            self.send_header("Content-Type", ctype)
            self.send_header("Content-Length", str(len(body)))
            self.end_headers()
            self.wfile.write(body)

        def do_GET(self):
            if not allowed(self.client_address[0]):
                return self.send(404, {"message": "Not Found"})
            base = f"http://{self.headers.get('Host', args.listen)}"
            path = self.path.split("?", 1)[0]
            if re.fullmatch(r"/repos/[^/]+/[^/]+/releases", path):
                return self.send(200, [release(t, base) for t in tags()])
            m = re.fullmatch(r"/repos/[^/]+/[^/]+/releases/tags/([^/]+)", path)
            if m:
                if m.group(1) in tags():
                    return self.send(200, release(m.group(1), base))
                return self.send(404, {"message": "Not Found"})
            m = re.fullmatch(r"/assets/([^/]+)/([^/]+)", path)
            if m and m.group(1) in tags():
                f = os.path.join(root, m.group(1), m.group(2))
                if os.path.isfile(f):
                    with open(f, "rb") as fh:
                        return self.send(200, fh.read(), "application/octet-stream")
            self.send(404, {"message": "Not Found"})

    ThreadingHTTPServer((host, int(port)), Handler).serve_forever()


if __name__ == "__main__":
    main()
