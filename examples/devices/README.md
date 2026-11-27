# Dispositivos emulados

Programas que imitan un dispositivo de red, para probar Lipstick sin hardware real.
Cada uno escucha en `127.0.0.1` y debe exponerse con el cliente de Lipstick.

| Archivo | Imita | Puerto por defecto | Cómo se expone |
|---------|-------|--------------------|----------------|
| `printer_emu.py` | Impresora de red (JetDirect, impresión cruda) | 9100 | TCP por puerto de intranet |
| `camera_emu.py` | Cámara IP: foto fija `/snapshot.jpg` y stream `/mjpeg` | 8090 | HTTP por puerto de intranet |
| `modbus_emu.py` | Controlador PLC o robot con Modbus TCP | 502 | TCP por puerto de intranet |

Ejemplo, con el servidor de intranet configurado:

```bash
python3 examples/devices/camera_emu.py 8090 &
go run ./client -s https://localhost:5051 -p 127.0.0.1:8090 -k "$(cat client_key.txt)" \
  -ca server/certs/server.crt -intranet-port 5105
curl http://192.168.40.12:5105/snapshot.jpg -o foto.jpg
```

`printer_emu.py` guarda cada trabajo en `jobs/`. `modbus_emu.py` usa el puerto 502, que requiere privilegios; para pruebas, pasa otro puerto como argumento.
Los emuladores no son dispositivos reales: sirven para comprobar el túnel, no el comportamiento exacto de un fabricante.
