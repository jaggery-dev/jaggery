#!/usr/bin/env python3
"""A fake web app and its checks, for the test house (track W2, app names).

  fake-app.py serve <address> <port>       the app: / says hello, /stream sends
                                            two lines a second apart, /ws upgrades
                                            and echoes one line
  fake-app.py get <host:port> <Host>        print / as the app answers it
  fake-app.py stream <host:port> <Host>     succeed if the first line arrives
                                            before the second is sent
  fake-app.py ws <host:port> <Host>         succeed if the upgrade and echo work
  fake-app.py mdns <name>                   print the A record a responder gives
                                            for name (a .local name), or fail
  fake-app.py claim <name> <addr> <from>    a rogue device: answer for name with
                                            addr, sent from the interface with
                                            address from, every second (#161)

Standard library only, so it runs wherever the test house does.
"""
import socket
import socketserver
import struct
import sys
import time


class App(socketserver.StreamRequestHandler):
    def handle(self):
        line = self.rfile.readline().decode()
        headers = {}
        while True:
            h = self.rfile.readline().decode().strip()
            if not h:
                break
            k, _, v = h.partition(":")
            headers[k.strip().lower()] = v.strip()
        path = line.split(" ")[1] if " " in line else "/"
        host = headers.get("host", "")
        if path == "/ws" and headers.get("upgrade", "").lower() == "websocket":
            self.wfile.write(b"HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n\r\n")
            self.wfile.flush()
            msg = self.rfile.readline()
            self.wfile.write(b"echo " + msg)
            return
        if path == "/stream":
            self.wfile.write(b"HTTP/1.1 200 OK\r\nContent-Type: application/x-ndjson\r\nTransfer-Encoding: chunked\r\n\r\n")
            for i, word in enumerate((b"one\n", b"two\n")):
                if i:
                    time.sleep(2)
                self.wfile.write(b"%x\r\n%s\r\n" % (len(word), word))
                self.wfile.flush()
            self.wfile.write(b"0\r\n\r\n")
            return
        body = ("hello from the fake app; Host %s; forwarded host %s\n" % (host, headers.get("x-forwarded-host", "-"))).encode()
        self.wfile.write(b"HTTP/1.1 200 OK\r\nContent-Type: text/plain\r\nX-Frame-Options: SAMEORIGIN\r\nContent-Length: %d\r\nConnection: close\r\n\r\n%s" % (len(body), body))


class Server(socketserver.ThreadingTCPServer):
    allow_reuse_address = True
    daemon_threads = True


def connect(addr):
    host, _, port = addr.rpartition(":")
    return socket.create_connection((host, int(port)), timeout=5)


def request(addr, host, path, extra=""):
    s = connect(addr)
    s.sendall(("GET %s HTTP/1.1\r\nHost: %s\r\n%sConnection: close\r\n\r\n" % (path, host, extra)).encode())
    return s


def get(addr, host):
    s = request(addr, host, "/")
    data = b""
    while True:
        b = s.recv(4096)
        if not b:
            break
        data += b
    head, _, body = data.partition(b"\r\n\r\n")
    print(head.decode().split("\r\n")[0])
    for h in head.decode().split("\r\n")[1:]:
        print(h)
    print()
    print(body.decode(), end="")


def stream(addr, host):
    s = request(addr, host, "/stream")
    start = time.time()
    data = b""
    while b"one" not in data:
        b = s.recv(4096)
        if not b:
            sys.exit("the stream ended before its first line")
        data += b
    took = time.time() - start
    if took > 1.5:
        sys.exit("the first line took %.1fs: it was held back until the answer ended" % took)
    while b"two" not in data:
        b = s.recv(4096)
        if not b:
            sys.exit("the stream ended before its second line")
        data += b


def ws(addr, host):
    s = request(addr, host, "/ws", "Upgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Version: 13\r\nSec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\n")
    data = b""
    while b"\r\n\r\n" not in data:
        b = s.recv(4096)
        if not b:
            sys.exit("no answer to the upgrade")
        data += b
    if b" 101 " not in data.split(b"\r\n")[0]:
        sys.exit("upgrade refused: %r" % data.split(b"\r\n")[0])
    s.sendall(b"ping\n")
    data = data.partition(b"\r\n\r\n")[2]
    while b"\n" not in data:
        b = s.recv(4096)
        if not b:
            break
        data += b
    if data != b"echo ping\n":
        sys.exit("echo was %r" % data)


def read_name(msg, off):
    labels = []
    while True:
        n = msg[off]
        if n & 0xC0 == 0xC0:
            ptr = struct.unpack(">H", msg[off:off + 2])[0] & 0x3FFF
            rest, _ = read_name(msg, ptr)
            labels.append(rest)
            return ".".join(l for l in labels if l), off + 2
        off += 1
        if n == 0:
            return ".".join(labels), off
        labels.append(msg[off:off + n].decode())
        off += n


def claim(name, addr, src):
    s = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
    s.setsockopt(socket.IPPROTO_IP, socket.IP_MULTICAST_IF, socket.inet_aton(src))
    s.setsockopt(socket.IPPROTO_IP, socket.IP_MULTICAST_TTL, 255)
    rname = b"".join(bytes([len(p)]) + p.encode() for p in name.rstrip(".").split(".")) + b"\0"
    # A response with one A record, cache-flush class, TTL 30.
    msg = struct.pack(">HHHHHH", 0, 0x8400, 0, 1, 0, 0)
    msg += rname + struct.pack(">HHIH", 1, 0x8001, 30, 4) + socket.inet_aton(addr)
    while True:
        s.sendto(msg, ("224.0.0.251", 5353))
        time.sleep(1)


def mdns(name):
    name = name.rstrip(".").lower()
    s = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
    s.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
    s.bind(("", 5353))
    s.setsockopt(socket.IPPROTO_IP, socket.IP_ADD_MEMBERSHIP, socket.inet_aton("224.0.0.251") + socket.inet_aton("0.0.0.0"))
    q = struct.pack(">HHHHHH", 0, 0, 1, 0, 0, 0)
    q += b"".join(bytes([len(p)]) + p.encode() for p in name.split(".")) + b"\0" + struct.pack(">HH", 1, 1)
    s.sendto(q, ("224.0.0.251", 5353))
    s.settimeout(0.5)
    deadline = time.time() + 3
    while time.time() < deadline:
        try:
            msg, _ = s.recvfrom(9000)
        except socket.timeout:
            continue
        flags, qd, an, ns, ar = struct.unpack(">HHHHH", msg[2:12])
        if not flags & 0x8000:
            continue
        off = 12
        for _ in range(qd):
            _, off = read_name(msg, off)
            off += 4
        for _ in range(an + ns + ar):
            rname, off = read_name(msg, off)
            rtype, _, ttl, rdlen = struct.unpack(">HHIH", msg[off:off + 10])
            off += 10
            if rtype == 1 and ttl > 0 and rname.lower() == name:
                print(socket.inet_ntoa(msg[off:off + 4]))
                return
            off += rdlen
    sys.exit("no answer for " + name)


if __name__ == "__main__":
    cmd, args = sys.argv[1], sys.argv[2:]
    if cmd == "serve":
        Server((args[0], int(args[1])), App).serve_forever()
    elif cmd in ("get", "stream", "ws"):
        globals()[cmd](args[0], args[1])
    elif cmd == "mdns":
        mdns(args[0])
    elif cmd == "claim":
        claim(*args)
    else:
        sys.exit(__doc__)
