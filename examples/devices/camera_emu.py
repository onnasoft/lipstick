"""Emulates an IP camera over plain HTTP.

GET /snapshot.jpg  one JPEG frame (the usual still-image URL of IP cameras)
GET /mjpeg         multipart/x-mixed-replace stream, about 5 frames per second
Each frame is a JPEG with the frame number and time drawn on it."""
import io, sys, time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from PIL import Image, ImageDraw

PORT = int(sys.argv[1]) if len(sys.argv) > 1 else 8090

def frame(n):
    img = Image.new("RGB", (640, 360), (20 + (n * 7) % 200, 60, 120))
    d = ImageDraw.Draw(img)
    d.text((20, 20), f"CAM-EMU frame {n}", fill=(255, 255, 255))
    d.text((20, 50), time.strftime("%H:%M:%S"), fill=(255, 255, 0))
    buf = io.BytesIO()
    img.save(buf, "JPEG", quality=80)
    return buf.getvalue()

class H(BaseHTTPRequestHandler):
    def log_message(self, *a):
        pass

    def do_GET(self):
        if self.path.startswith("/snapshot.jpg"):
            data = frame(int(time.time()))
            self.send_response(200)
            self.send_header("Content-Type", "image/jpeg")
            self.send_header("Content-Length", str(len(data)))
            self.end_headers()
            self.wfile.write(data)
        elif self.path.startswith("/mjpeg"):
            self.send_response(200)
            self.send_header("Content-Type", "multipart/x-mixed-replace; boundary=frame")
            self.send_header("Cache-Control", "no-cache")
            self.end_headers()
            try:
                for n in range(25):  # about 5 seconds, then the stream ends
                    data = frame(n)
                    self.wfile.write(b"--frame\r\nContent-Type: image/jpeg\r\n"
                                     + f"Content-Length: {len(data)}\r\n\r\n".encode() + data + b"\r\n")
                    self.wfile.flush()
                    time.sleep(0.2)
            except (BrokenPipeError, ConnectionResetError):
                pass
        else:
            self.send_response(404)
            self.send_header("Content-Length", "0")
            self.end_headers()

print(f"camera listening on 127.0.0.1:{PORT}", flush=True)
ThreadingHTTPServer(("127.0.0.1", PORT), H).serve_forever()
