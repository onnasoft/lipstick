"""Emulates a network printer on raw port 9100 (JetDirect style).

The printer accepts a connection, stores everything sent until the client closes
its side, and saves it as one job file. It never answers, like real raw printers."""
import hashlib, os, socket, threading, time, sys

PORT = int(sys.argv[1]) if len(sys.argv) > 1 else 9100
OUT = os.path.join(os.path.dirname(os.path.abspath(__file__)), "jobs")
counter = 0
lock = threading.Lock()

def handle(conn):
    global counter
    with lock:
        counter += 1
        n = counter
    chunks = []
    conn.settimeout(20)
    try:
        while True:
            data = conn.recv(65536)
            if not data:
                break
            chunks.append(data)
    except socket.timeout:
        pass
    finally:
        conn.close()
    body = b"".join(chunks)
    path = os.path.join(OUT, f"job-{n:03d}.bin")
    with open(path, "wb") as f:
        f.write(body)
    print(f"job {n}: {len(body)} bytes sha256={hashlib.sha256(body).hexdigest()[:16]}", flush=True)

srv = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
srv.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
srv.bind(("127.0.0.1", PORT))
srv.listen(16)
print(f"printer listening on 127.0.0.1:{PORT}", flush=True)
while True:
    c, _ = srv.accept()
    threading.Thread(target=handle, args=(c,), daemon=True).start()
