"""Emulates a PLC or robot controller with Modbus TCP on port 502 (or the port given).

Holding registers 0..99 start with known values. Function 3 reads registers and
function 6 writes one. Register 0 is the joint 1 position in tenths of a degree,
so the client can check a value it wrote."""
import socket, struct, sys, threading

PORT = int(sys.argv[1]) if len(sys.argv) > 1 else 502
regs = [1000 + i for i in range(100)]  # register i holds 1000 + i
lock = threading.Lock()

def recv_exact(c, n):
    buf = b""
    while len(buf) < n:
        part = c.recv(n - len(buf))
        if not part:
            raise ConnectionError
        buf += part
    return buf

def handle(c):
    try:
        while True:
            head = recv_exact(c, 7)
            tid, proto, length, unit = struct.unpack(">HHHB", head)
            pdu = recv_exact(c, length - 1)
            fc = pdu[0]
            if fc == 3:
                addr, qty = struct.unpack(">HH", pdu[1:5])
                with lock:
                    if addr + qty > len(regs):
                        resp = bytes([fc | 0x80, 2])
                    else:
                        vals = regs[addr:addr + qty]
                        resp = bytes([fc, qty * 2]) + b"".join(struct.pack(">H", v) for v in vals)
            elif fc == 6:
                addr, value = struct.unpack(">HH", pdu[1:5])
                with lock:
                    if addr >= len(regs):
                        resp = bytes([fc | 0x80, 2])
                    else:
                        regs[addr] = value
                        resp = pdu  # echo, as the standard requires
            else:
                resp = bytes([fc | 0x80, 1])  # illegal function
            c.sendall(struct.pack(">HHHB", tid, 0, len(resp) + 1, unit) + resp)
    except (ConnectionError, OSError):
        pass
    finally:
        c.close()

srv = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
srv.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
srv.bind(("127.0.0.1", PORT))
srv.listen(8)
print(f"modbus listening on 127.0.0.1:{PORT}", flush=True)
while True:
    c, _ = srv.accept()
    threading.Thread(target=handle, args=(c,), daemon=True).start()
