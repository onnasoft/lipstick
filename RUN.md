# Cómo correr Lipstick

Lipstick tiene dos partes: el **servidor**, que recibe el tráfico público, y el **cliente**, que corre donde está tu servicio local.

## Requisitos

- Go instalado.
- PostgreSQL en `localhost:5432` con el usuario y la base de datos de `server/config.yml`.
- NATS en `localhost:4222`. Si no lo tienes, instálalo y arráncalo, por ejemplo con `nats-server -p 4222`.

## 1. Servidor

Desde la raíz del repo:

```bash
./server-run
```

`server-run` es un enlace a `server/run.sh`. Ese script comprueba que PostgreSQL y NATS respondan, imprime cómo conectar el cliente con la clave incluida y después ejecuta `go run main.go -c config.yml` dentro de `server/`. También puedes ejecutar `./server/run.sh` directamente.

La clave aparece en la salida del script. Si el script se ejecuta en una terminal compartida o en un log, trátala como un secreto.

La primera vez se genera un certificado TLS autofirmado en `server/certs/`. El servidor escucha en:

| Puerto | Uso |
|--------|-----|
| 5050 | Proxy: aquí entran los visitantes (`https://`) |
| 5051 | Manager: aquí se conecta el cliente (`https://`) |
| 5052 | Admin API (`https://`, solo en `127.0.0.1`, para crear dominios) |

Si el servidor no arranca, el mensaje indica qué falta: la configuración, un puerto ocupado o una base de datos o NATS sin responder.

### Crear el dominio

Cada dominio necesita un registro con una clave que el cliente usará. Esa clave no es la clave de admin. Créala con la admin API, usando la clave de `admin_secret_key` de `server/config.yml`:

```bash
ADMIN=$(sed -n 's/^admin_secret_key: "\(.*\)"/\1/p' server/config.yml)
CLIENT_KEY=$(openssl rand -hex 24)
echo "$CLIENT_KEY" > client_key.txt   # guarda la clave del cliente

curl -sk -X POST https://localhost:5052/domains \
  -H "Authorization: $ADMIN" \
  -H 'Content-Type: application/json' \
  --data "{\"name\":\"localhost\",\"apiKey\":\"$CLIENT_KEY\",\"allowMultipleConnections\":true}"
```

Para ver los dominios registrados: `curl -sk https://localhost:5052/domains -H "Authorization: $ADMIN"`. La respuesta no incluye la clave del cliente; la clave solo se puede escribir, no leer.

La admin API solo escucha en `127.0.0.1`, así que no es accesible desde otras máquinas. Usa `-k` porque el certificado es autofirmado.

`client_key.txt` contiene la clave del cliente. Ya está en `.gitignore`, así que no se sube al repo.

## 2. Cliente

El cliente corre en la máquina donde está el servicio que quieres exponer. Necesitas:

1. El servidor en marcha (paso 1).
2. El dominio creado y el archivo `client_key.txt` con su clave.
3. Tu servicio local escuchando en un puerto, por ejemplo `127.0.0.1:8082`.

En otra terminal, desde la raíz del repo:

```bash
go run ./client \
  -s https://localhost:5051 \
  -p 127.0.0.1:8082 \
  -k "$(cat client_key.txt)" \
  -ca server/certs/server.crt
```

`-ca` le dice al cliente qué certificado del servidor confiar. Sin él, el cliente rechaza el certificado autofirmado y muestra el error `server certificate could not be verified`. Si el servidor usa un certificado firmado por una CA pública, no necesitas `-ca`.

Si prefieres un binario, compílalo una vez y ejecútalo con los mismos flags:

```bash
go build -o lipstick-client ./client
./lipstick-client -s https://localhost:5051 -p 127.0.0.1:8082 -k "$(cat client_key.txt)" -ca server/certs/server.crt
```

Para no escribir `-ca` cada vez, puedes poner en un YAML del cliente:

```yaml
ca_file: /ruta/absoluta/a/lipstick/server/certs/server.crt
```

Úsalo con `-c ruta/al/config.yml`. No uses `-insecure` salvo en desarrollo: desactiva la verificación y tu clave puede ser robada.

Salida esperada:

```
https://localhost:5051 [127.0.0.1:8082]
Presiona Ctrl+C para salir.
Connecting to https://localhost:5051
Connected to server at https://localhost:5051
```

Para desconectarlo, presiona `Ctrl+C`. Si el cliente se desconecta, intenta reconectarse cada 3 segundos.

Los flags:

| Flag | Qué es | Ejemplo |
|------|--------|---------|
| `-s` | URL del manager del servidor. Debe usar `https://` porque el servidor tiene TLS | `https://localhost:5051` |
| `-p` | Servicio local que quieres exponer, `host:puerto`. Puedes pasar varios separados por espacio | `127.0.0.1:8082` |
| `-k` | Clave del dominio, la que creaste con la admin API | contenido de `client_key.txt` |
| `-ca` | Archivo PEM del certificado del servidor, para confiar en un certificado autofirmado | `server/certs/server.crt` |
| `-insecure` | Desactiva la verificación del certificado del servidor. Solo para desarrollo | (omitir) |

El nombre del dominio sale de la URL de `-s`. Con `localhost:5051` el dominio es `localhost`.

Si el cliente imprime `Connected to server at https://localhost:5051`, la conexión está activa. Si la clave es incorrecta, el servidor la rechaza, pero el cliente todavía dice `Connected` (punto 31 de `revision.md`); revisa el log del servidor para confirmar.

## 3. Probar el túnel

Antes de probar, levanta un servicio local en el puerto que pasaste a `-p`. Por ejemplo:

```bash
mkdir -p /tmp/demo && echo "hola desde mi servicio local" > /tmp/demo/archivo.txt
cd /tmp/demo && python3 -m http.server 8082 --bind 127.0.0.1
```

Luego, desde otra terminal, pide el archivo a través del servidor:

```bash
curl -sk -H "Host: localhost" https://localhost:5050/archivo.txt
```

`-k` acepta el certificado autofirmado. Si devuelve `hola desde mi servicio local`, el túnel funciona.

## Problemas conocidos

- **Peticiones que fallan de forma intermitente:** si reiniciaste un cliente, el servidor puede conservar su registro anterior y enviarle peticiones (punto 16). Reinicia el servidor para limpiar los registros.
- **Sin verificación de certificado:** el cliente acepta cualquier certificado del servidor (punto 3). No uses esta configuración en producción.
