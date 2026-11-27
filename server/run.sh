#!/usr/bin/env bash
# Starts the lipstick server with server/config.yml.
#
# Usage (from the repo root):  ./server-run
#                          or  ./server/run
#
# Checks that PostgreSQL and NATS are reachable before starting, so the
# server does not fail halfway through startup.

set -euo pipefail

# Resolve symlinks so this works when called through ./server-run.
SCRIPT="$(readlink -f "${BASH_SOURCE[0]}")"
SERVER_DIR="$(dirname "$SCRIPT")"
ROOT="$(dirname "$SERVER_DIR")"
CONFIG="$SERVER_DIR/config.yml"

if [[ ! -f "$CONFIG" ]]; then
  echo "No existe $CONFIG" >&2
  echo "Crea server/config.yml con admin_secret_key, database, nats y tls (revisa los nombres de las claves en server/config/config.go)." >&2
  exit 1
fi

port_open() {
  # Probes a TCP port that belongs to PostgreSQL or NATS, never to lipstick.
  (exec 3<>"/dev/tcp/$1/$2") 2>/dev/null
}

check_service() {
  local name="$1" host="$2" port="$3"
  if port_open "$host" "$port"; then
    echo "[ok] $name responde en $host:$port"
  else
    echo "[error] $name no responde en $host:$port. Inícialo antes de arrancar el servidor." >&2
    return 1
  fi
}

# These are the defaults in server/config.yml. Change them here if your
# database or NATS address is different.
check_service "PostgreSQL" localhost 5432
check_service "NATS" localhost 4222

CLIENT_KEY_FILE="$ROOT/client_key.txt"
if [[ -f "$CLIENT_KEY_FILE" ]]; then
  CLIENT_KEY="$(cat "$CLIENT_KEY_FILE")"
else
  CLIENT_KEY="(falta $CLIENT_KEY_FILE, ver RUN.md)"
fi

cat <<EOF

Servidor: https://localhost:5050 (visitantes), https://localhost:5051 (clientes), https://127.0.0.1:5052 (admin, solo local)

Para conectar un cliente, en otra terminal:
  cd $ROOT
  go run ./client -s https://localhost:5051 -p 127.0.0.1:8082 -k "$CLIENT_KEY" -ca "$SERVER_DIR/certs/server.crt"

Clave del cliente (de client_key.txt): $CLIENT_KEY
Detalles en RUN.md.

EOF

cd "$SERVER_DIR"
echo "Arrancando lipstick con $CONFIG"
exec go run main.go -c config.yml "$@"
