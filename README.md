# Lipstick

**Lipstick** exposes a local service on a public server through a reverse tunnel, without a VPN. The client runs next to your service and connects out to the server. Visitors then reach your service through the server.

Status: experimental. Read `revision.md` for the open security and stability items before using it on anything public.

## Requirements

- Go 1.25 or later, to build from source.
- PostgreSQL. The schema is created on startup.
- NATS. The server connects to it at startup.

## Quick start

See [RUN.md](RUN.md) for the full steps: start the server, create a domain, connect a client, and test the tunnel.

```bash
./server-run                 # starts the server, prints the client command
```

## Ports

| Port | Who uses it | Notes |
|------|-------------|-------|
| `5050` | Visitors | `proxy.address`. HTTPS only. |
| `5051` | Tunnel clients | `manager.address`. HTTPS only. |
| `5052` | Admin API | `admin.address`. Defaults to `127.0.0.1`, so only the local machine can reach it. HTTPS. |

TLS is always required. The server refuses to start without a certificate and key. If the files do not exist, it generates a self-signed certificate at the configured paths.

## Server

### Configuration file

The server reads a YAML file (`-c`, default `/etc/lipstick/config.yml`). Start from [`server/config.yml.example`](server/config.yml.example). Unknown keys are an error, so a misspelled key cannot be ignored.

| Key | Default | Purpose |
|-----|---------|---------|
| `admin_secret_key` | none, required | Key for the admin API. At least 16 characters. |
| `proxy.address` | `:5050` | Visitor listener. |
| `manager.address` | `:5051` | Tunnel client listener. |
| `admin.address` | `127.0.0.1:5052` | Admin API listener. |
| `tls.certificate_path` | `certs/server.crt` | Certificate. Generated if missing. Relative paths are relative to the configuration file. |
| `tls.key_path` | `certs/server.key` | Private key. Generated if missing. |
| `database.host` | `localhost` | PostgreSQL host. |
| `database.port` | `5432` | PostgreSQL port. |
| `database.user` | `postgres` | PostgreSQL user. |
| `database.password` | empty | PostgreSQL password. |
| `database.database` | `app_db` | PostgreSQL database. |
| `database.ssl_mode` | `disable` | One of `disable`, `allow`, `prefer`, `require`, `verify-ca`, `verify-full`. |
| `nats.url` | `nats://localhost:4222` | NATS server. |

### Command-line flags

Flags override the file for the values they name. They are validated the same way.

```text
-c                  path to the configuration file (default /etc/lipstick/config.yml)
-admin-addr         admin.address
-manager-addr       manager.address
-proxy-addr         proxy.address
-admin-secret       admin_secret_key
-tls-cert           tls.certificate_path
-tls-key            tls.key_path
-db-host            database.host
-db-port            database.port
-db-user            database.user
-db-password        database.password
-db-name            database.database
-db-ssl-mode        database.ssl_mode
-nats-url           nats.url
-redis-host, -redis-port, -redis-password, -redis-db,
-redis-pool-size, -redis-min-idle-conns, -redis-pool-timeout
                    redis.* (the server does not use Redis yet)
```

### Admin API

All endpoints except `/health` need the admin key in the `Authorization` header, sent as-is (no `Bearer` prefix).

| Method and path | Purpose |
|-----------------|---------|
| `GET /health` | Liveness. No authentication. |
| `GET /domains` | List domains. The tunnel keys are not returned. |
| `GET /domains/:name` | One domain. The key is not returned. |
| `POST /domains` | Create a domain: `{"name", "apiKey", "allowMultipleConnections"}`. |
| `PATCH /domains/:name` | Change `apiKey` or `allowMultipleConnections`. Disconnects the domain's clients. |
| `DELETE /domains/:name` | Delete a domain. Disconnects its clients. |
| `GET /domains/:name/traffic?from=YYYY-MM-DD&to=YYYY-MM-DD` | Daily traffic. |

## Client

### Command-line flags

```text
-s   server URL. Must start with https://. Default https://localhost:5051
-p   local services to expose, host:port, separated by spaces: -p "127.0.0.1:8082 127.0.0.1:9000". Default 127.0.0.1:8082
-k   domain key, the one created in the admin API
-ca  PEM file with the server certificate or its CA. Needed for a self-signed server
-c   configuration file. Default config.client.yml next to the binary
```

### Configuration file

Start from [`client/config.example.yml`](client/config.example.yml).

| Key | Purpose |
|-----|---------|
| `api_secret` | Domain key. Required. |
| `server_url` | Server URL, `https://`. |
| `proxy_pass` | List of local services, `host:port`. |
| `ca_file` | PEM file to trust, for a self-signed server. |

Flags override the file. Unknown keys are an error.

### Behaviour

- The client verifies the server certificate. There is no option to skip verification.
- If the server refuses the connection, for example with a wrong key, the client prints the status and retries with a wait that doubles up to 60 seconds.
- Local services can be `host:port` (plain TCP) or `https://host:port` (verified TLS). The tunnel to the server is always TLS.

## Notes

- The tunnel carries API keys and visitor traffic. Keep `config.yml`, `client_key.txt` and the `certs/` directory out of version control. They are already in `.gitignore`.
- Lipstick is experimental. Use a firewall in front of ports `5050` and `5051`, and keep `5052` local.
- `DEBUG=true` in the environment starts the Go profiler on `:6060` and turns on extra logging.
