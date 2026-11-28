# Lipstick Code Review

Scope: all 49 tracked files (Go server and client, deployment scripts, config examples).
Method: static reading of the code plus `go vet ./server/... ./client/...`. The code compiles. No tunnel was run and no attack was reproduced live. Items marked **(verify)** are likely but need a test to confirm.

## Summary

Lipstick exposes local ports to the internet through a reverse tunnel. The main problems are:

- Authentication of tunnel clients is missing on the server.
- Ticket values are predictable, which allows connection hijacking.
- The client skips TLS certificate verification.
- A single malformed request can stop the manager from accepting clients.
- Visitor connections have no timeout or limit.
- There are data races that can crash the process.
- The reverse-connection path for TLS does not work as written.

---

## Critical: security

### 1. Tunnel clients are not authenticated
- **Where:** `server/manager/routes.go` (`upgrade`), `client/main.go:46`
- **What:** `upgrade` looks up the domain from the `Host` header and never reads the `Authorization` header or compares it with `ApiKey`. The client sends `APISecret` in that header, and the server ignores it.
- **Impact:** Anyone who knows a domain name can register as its tunnel client and receive traffic meant for that domain.
- **Fix:** Read `Authorization` in `upgrade` before hijacking, compare it to `domain.ApiKey` with `crypto/subtle.ConstantTimeCompare`, and reject mismatches.
- **Status:** Fixed. `upgrade` checks the key before hijacking and returns 401 on a missing or wrong key, and 404 for an unknown domain. A domain with an empty `ApiKey` is always rejected. Covered by `server/manager/routes_test.go`.
- **Verified:** End to end over TLS, a client with the correct key connected and served traffic. A client with a wrong key was rejected (`Unauthorized tunnel client for domain:localhost`).

### 2. Predictable ticket values
- **Where:** `server/manager/ticket.go`, `server/manager/hub.go`
- **What:** Tickets are a counter starting at 1 for each hub. The manager port accepts `GET /<ticket>` from any client.
- **Impact:** An attacker can guess a pending visitor's ticket, attach to that connection, and read its traffic, including cookies and tokens.
- **Fix:** Generate tickets with `crypto/rand` (at least 128 bits). Also bind each ticket to the client connection that was issued it.
- **Status:** Fixed. Tickets are 128 random bits from `crypto/rand`, encoded as 32 hex characters (`server/manager/ticket.go`). A generation error closes the visitor connection instead of issuing a predictable value. Binding a ticket to one client connection is not done: the ticket is only sent over the authenticated client connection, so an unguessable ticket is the main protection.
- **Verified:** `server/manager/ticket_test.go` checks 10,000 unique tickets and that the guessed values `1`, `2` and `3` do not claim a pending visitor connection. End to end, after restarting the server on this build, 8 of 8 visitor requests over TLS returned the backend response.

### 3. Client accepts any TLS certificate
- **Where:** `client/manager/http.go` (`Connect`, `ConnectByAddress`), `client/handlers/http.go`, `client/handlers/tcp.go`
- **What:** `InsecureSkipVerify: true` is set unconditionally. The `ENV=development` check in `client/main.go` only changes `websocket.DefaultDialer`, which is not used.
- **Impact:** A man-in-the-middle can impersonate the server and capture `APISecret`.
- **Fix:** Verify certificates by default. Make the insecure option explicit and only for development.
- **Status:** Fixed. There is no option to skip verification.
  - `client/tlsconf` verifies every client TLS connection, both to the server and to local `https://` or TLS targets. Verification uses the system roots, or the CA given with `-ca` or `ca_file`.
  - `-insecure`, `insecure_skip_verify` and the `ENV=development` block were removed.
  - `server_url` must start with `https://`, and the client refuses to start otherwise.
  - Verification errors say which flag fixes them.
  - Local plain-text targets (`tcp://`, `http://`) are still allowed. They never leave the machine and the tunnel itself is always TLS. This can be tightened if you want.
- **Verified:** Without `-ca`, the client refused the server certificate and printed the fix. With `-ca server/certs/server.crt`, it connected and 8 of 8 visitor requests returned the backend response. Unit tests in `client/tlsconf` cover the default, CA, MinVersion and bad-CA cases.
- **Outside the client:** `performance/main.go`, a load-test tool, still sets `InsecureSkipVerify: true` and defaults to `http://localhost:8080`, which no longer matches the server. It should be updated to `https://localhost:5050` with the same CA handling. Not changed yet.

### 4. Admin API uses plain HTTP and a weak check
- **Where:** `server/admin/admin.go` (`engine.Run(addr)`), `server/admin/routes.go` (`isAuthorized`)
- **What:**
  - Served over plain HTTP on `:5052`, which listens on all interfaces by default.
  - The admin key is compared with `==` against the whole `Authorization` header, which is not constant-time.
  - `GET /domains` and `GET /domains/:name` return every domain's `apiKey`.
  - There is no rate limiting.
- **Fix:** Bind to localhost or a private interface, serve over TLS, compare keys in constant time, and stop returning `apiKey` in responses.
- **Status:** Fixed, except for rate limiting.
  - The admin API listens on `127.0.0.1:5052` by default, in both the code and `server/config.yml`.
  - It is served over TLS whenever TLS is configured (`SetupAdmin` takes the TLS config).
  - The key is compared with `crypto/subtle.ConstantTimeCompare`. An empty key never authorizes.
  - `GET /domains` and `GET /domains/:name` return a view without `apiKey`. The key can be written but not read back.
  - Not done: rate limiting on failed admin requests (still open).
- **Verified:** `server/admin/admin_test.go` covers wrong, empty and prefixed keys, and checks that no list or get response contains the tunnel key. End to end, `https://localhost:5052/domains` returned the domains without the key, a wrong key got 401, and plain HTTP was rejected.

### 5. Unauthenticated endpoints on the manager port
- **Where:** `server/manager/routes.go` (`/traffic`, `/health`)
- **What:**
  - `/traffic` returns usage data for any domain, selected by `Host`, with no authentication.
  - `/health` returns 200 for an existing domain and 404 otherwise, so domain names can be enumerated.
- **Fix:** Require the admin key for `/traffic`. Return the same response for unknown domains on `/health`, or drop the domain-specific detail.
- **Status:** Fixed.
  - Traffic statistics moved to the admin API: `GET /domains/:domainName/traffic?from=&to=`. It requires the admin key and validates the dates before querying the database. The manager port no longer serves it.
  - `/health` on the manager port returns `{"status":"ok"}` for every host, with no domain details.
- **Verified:** `server/manager/health_test.go` checks that health is identical for known and unknown domains, and that `/traffic` is not served on the manager. `server/admin/admin_test.go` checks the key and date validation. End to end, the admin traffic endpoint returned the stored usage with the key and 401 without it, and the tunnel kept working (8 of 8).
- **Note:** a request to `/traffic` on the manager port now gets 502 instead of 404. The listener treats any single-segment path as a tunnel ticket. It reveals nothing, but it is worth tidying up with the ticket handling.

### 6. Private key committed to the repository
- **Where:** `certs/lipstick.key`, `certs/lipstick.csr`, `certs/lipstick.crt`; `.gitignore`
- **What:** The private key (`-----BEGIN PRIVATE KEY-----`) is tracked by git, and `.gitignore` does not exclude `certs/`.
- **Fix:** Treat the key as compromised and rotate it. Remove it from history (for example with `git filter-repo`), add `certs/` to `.gitignore`, and load certificates from outside the repo.
- **Status:** Partly fixed. The key is no longer tracked.
  - `certs/lipstick.key`, `certs/lipstick.csr` and `certs/lipstick.crt` were removed from the index with `git rm --cached`. The files are still on disk, and this change is staged, not committed.
  - `.gitignore` now excludes `certs/` and `*.key`, so the files cannot be added again by accident.
  - Nothing references the root `certs/` folder. The server uses `server/certs/`, which is generated at startup and ignored.
  - No other real secrets are tracked. The matches in `README.md` and `server/db/manager.go` are placeholders or code.
- **Done on GitHub (force push, confirmed by you):**
  - `main`, `develop`, the Dependabot branch and the tags `v0.0.3-alpha` to `v0.0.5-alpha` were rewritten. A fresh clone shows no commit that touches `certs/` and no copy of the key in any branch or tag.
  - Commits in these branches were rewritten, so their hashes changed. Local clones still hold the old history and must be updated with `git fetch` and a reset to `origin/main`.
- **Still open:**
  - Three pull request refs still hold the old history, with the key: `refs/pull/1/head`, `refs/pull/2/head` and `refs/pull/3/merge`. GitHub manages these refs, and a normal push cannot change them. To remove them, contact GitHub Support and ask them to purge the sensitive data from the repository, or close and recreate the pull requests and ask support to purge the old refs.
  - Anyone who cloned or forked the repository before the rewrite still has the key. Treat it as leaked.
  - The rewritten mirror is in the scratch directory, `rewrite.git`. It can be deleted.
  - Delete `certs/lipstick.key` from disk. Nothing uses it.
  - The running server uses `server/certs/`, not `certs/`, so no current service depends on this key. If it was used somewhere else, that deployment needs a new certificate.

### 7. TLS fails open to plain text
- **Where:** `server/config/config.go` (`GetTLSConfig`), `helper/listener.go` (`NewListenerManagerTCP`), `server/main.go`
- **What:**
  - `GetTLSConfig` returns `nil` when the certificate cannot be loaded. It only logs the error, and the server starts without TLS.
  - `NewListenerManagerTCP` returns `nil` on error, and `main.go` then calls methods on that `nil` value.
- **Fix:** Fail startup when TLS is configured but cannot be loaded. Return and check errors from the listener constructor.
- **Status:** Fixed.
  - `TLSConfig.Prepare` returns an error and no configuration when the certificate or key cannot be loaded. It never returns `nil` without an error.
  - TLS is mandatory, so there is no plain-text branch left. The proxy, manager and admin API call `tls.Listen` only. `NewListenerManagerTCP` refuses a nil configuration.
  - `server/main.go` exits with code 1 on a TLS error, before opening any port.
- **Verified:** `server/config/tls_failure_test.go` checks that a broken certificate gives an error and no config. End to end, starting the server with a broken certificate printed `Error de TLS: ... failed to find any PEM data` and exited with code 1. Ports 5150, 5151 and 5152 stayed closed.

---

## High: stability and denial of service

### 8. One malformed request stops the manager from accepting clients
- **Where:** `server/manager/listener.go` (`CustomListener.handle`, `Accept`)
- **What:** Errors from parsing are sent through `cl.conn` and returned from `Accept`. `http.Server.Serve` returns on a non-temporary `Accept` error, so the server stops accepting connections until restarted.
- **Impact:** Sending garbage to the manager port takes the service down.
- **Observed:** Reproduced locally. Opening and closing empty TCP connections to port 5051 logged `Error running listener: invalid request`, and the port stopped answering until the server was restarted.
- **Fix:** Close the bad connection inside `handle` and never pass parse errors to `Accept`.
- **Status:** Fixed.
  - `handle` closes a bad connection and nothing reaches `Accept` except good connections.
  - Found while testing: the accept loop kept retrying forever once the listener was closed from outside. It now stops on `net.ErrClosed`, and it pauses 100 ms after transient errors.
- **Verified:** `server/manager/listener_test.go` sends five malformed connections and one empty one, then gets `/health` with 200. Earlier, the same probe from a shell took the manager down.

### 9. Visitor connections have no timeout or limit
- **Where:** `server/manager/hub.go` (`incomingClientConns`, `handleIncomingClientConn`)
- **What:** Pending visitor connections are stored until a client responds. There is no deadline, no cap, and no cleanup.
- **Impact:** Opening many connections to the proxy port exhausts memory and file descriptors.
- **Fix:** Add a read or response deadline, a per-hub maximum of pending connections, and cleanup on timeout.
- **Status:** Fixed.
  - Each pending visitor expires after `pendingVisitorTimeout` (30 s). The hub checks every 5 s, answers 502 and removes the entry.
  - Each hub holds at most `maxPendingVisitors` (1024) pending visitors. Above that, new visitors get 502.
  - Writing the 502 happens in its own goroutine, with a 5 s deadline, so a slow or dead visitor cannot block the hub loop.
- **Verified:** `server/manager/hub_test.go` covers expiry, a fresh visitor being kept, and the cap.

### 10. Visitors can hang without a response
- **Where:** `server/manager/hub.go` (`handleIncomingClientConn`, NATS callback)
- **What:**
  - When no client is registered, the ticket is published to NATS and the function returns without answering the visitor.
  - If the hub has no subscription, the message is dropped and the visitor is never answered.
  - If `getProxyNotificationConn` returns `nil` in the NATS callback, the visitor is also left waiting.
- **Fix:** Answer with 502 in each of these paths, and remove the pending entry.
- **Status:** Fixed, with one design change.
  - A visitor that arrives with no tunnel client connected now gets 502 immediately. The ticket is no longer published to NATS. A reverse connection must reach the instance that holds the visitor, so publishing to another instance could never be answered.
  - A NATS ticket that no client can take answers its visitor with 502.
  - Visitors that are still waiting when the last client disconnects get 502.
  - The NATS subscription still exists, but it now only delivers tickets published by other instances. Nothing in this codebase publishes them anymore. If you want to remove NATS from the visitor path entirely, that is a separate decision.
- **Verified:** End to end, a visitor with no client got 502 in 2.5 ms. `hub_test.go` covers the no-client, NATS and expiry paths.

### 11. NATS subscription manager returns `(nil, nil)` on failure
- **Where:** `server/subscriptions/subscriptions.go` (`GetSubscriptionManager`)
- **What:** Inside the `sync.Once` closure, `err` is shadowed (`conf, err := ...`), so the outer `err` is always nil. If NATS is down, the function returns `(nil, nil)`. Callers then dereference the nil manager and panic. `sync.Once` also prevents any retry.
- **Fix:** Remove the shadowing, return the connection error, and retry on later calls instead of using `Once`.
- **Status:** Fixed. `GetSubscriptionManager` returns the NATS error, and it does not cache a failed connection, so the next call tries again. The shared connection is protected by a mutex.
- **Verified:** `server/subscriptions/subscriptions_test.go` checks that an unreachable NATS gives an error with no manager, on two calls in a row.

### 12. Data races that can crash the process
- **Where:** `server/manager/hub.go`
- **What:**
  - `hub.ProxyNotificationConns` is read by `getProxyNotificationConn` in the NATS callback goroutine while the hub loop modifies it. This can cause `fatal error: concurrent map iteration and map write`, which cannot be recovered.
  - The hub loop and the NATS callback both write to the same `ProxyNotificationConn` (`bufio.Writer`) without a lock, so ticket lines can be interleaved.
- **Fix:** Route all map access and writes through the hub loop, or protect them with a mutex. Serialize writes per connection.
- **Status:** Fixed.
  - The hub loop is the only goroutine that reads or writes `ProxyNotificationConns`, `incomingClientConns`, and the writes to tunnel clients. The NATS callback only queues the message on `natsMessages`.
  - The unregister path no longer writes "close" from a separate goroutine. It closes from the loop.
  - Every write to a tunnel client has a 5 s deadline, so a client that stopped reading cannot block the loop.
- **Verified:** `go test -race ./server/...` passes. The tests do not exercise concurrent NATS traffic, so the race fix is checked by design, not by a reproduction.

### 13. Reverse connection over TLS does not work as written
- **Where:** `server/manager/hub.go` (`handleIncomingClientConn`), `helper/net.go` (`GetPublicIP`), `client/manager/http.go` (`ConnectByAddress`)
- **What:**
  - `GetPublicIP` returns the first non-loopback IPv4 address of the server's interfaces, which is usually a private address, not the public one.
  - The message is `IP:ticket` with no port.
  - With `https`/`wss`, the client calls `tls.Dial("tcp", addr)` with no port and fails with "missing port".
  - With `http`/`ws`, the client ignores `addr` and connects to the host in the URL.
- **Fix:** Have the server send an explicit public address and port, or have the client always use the configured URL. Remove the dependence on `GetPublicIP`.
- **Status:** Fixed for the client side. `ConnectByAddress` now dials the host from the server URL for both plain and TLS connections (`client/manager/http.go`). The `addr` argument was removed. 
- **Verified:** Visitor requests over `https://localhost:5050` return the backend response through the tunnel (6 of 6 requests with one live client). Before the fix, the same setup timed out.
- **Status (completed):** the server now sends only the ticket. The address it used to send (from `GetPublicIP`) is gone, and `helper.GetPublicIP` was deleted. The client parses one ticket per line and checks it is hex (`client/main.go`).
- **Verified:** end to end over TLS with the new ticket format, 8 of 8 visitor requests returned the backend response.

### 14. Client panics on bad input
- **Where:** `client/main.go` (`readMessage`, `establishConnection`)
- **What:**
  - `readMessage` calls `strings.Split(line, ":")` and reads `data[1]` without checking the length. An IPv6 address or a line without `:` causes an index-out-of-range panic.
  - `establishConnection` writes to `connection` when it is `nil`.
  - The `recover()` calls hide these panics, and the client silently drops the ticket loop.
- **Fix:** Parse with `strings.LastIndex` (or `net.SplitHostPort`) and check the result. Do not write to a nil connection.
- **Status (completed):**
  - `readMessage` reads one line and checks it with `isTicket` (1 to 64 lowercase hex characters). Any other line is an error, so a ticket cannot inject a URL or a separator.
  - `establishConnection` no longer writes to a nil connection. When the reverse connection fails there is nothing to answer, so it logs the error.
  - `recover` is kept, but it logs the panic instead of hiding it.
- **Verified:** `client/main_test.go` covers valid tickets, bad lines, EOF, and the length limit.

### 15. Hubs and goroutines are never released
- **Where:** `server/manager/manager.go` (`hubs`), `server/manager/hub.go` (`listen`, `handleShutdown`)
- **What:** Hubs are stored in a `sync.Map` and never removed. Deleting a domain through the admin API does not stop its hub. `handleShutdown` is never triggered.
- **Fix:** Remove the hub when its last client disconnects, and stop its goroutine.
- **Status (completed):**
  - A hub stops when its last tunnel client leaves. It tells the manager to forget it (`onIdle`), and its loop exits. A later client creates a new hub.
  - Deleting a domain, or updating its key or policy, calls `Manager.DropHub` through `Admin.SetDomainChangedHook`. The hub disconnects its clients, so they reconnect under the current configuration.
  - Shutdown calls `Manager.Close`, which stops every hub and flushes the traffic counters.
  - Senders outside the hub loop select on `done`, so they never block on a stopped hub.
- **Verified:** `hub_test.go` and `lifecycle_test.go` cover the idle stop, `DropHub`, and singleton creation. Live: changing the domain key dropped the hub, the client reconnected by itself, and the tunnel answered 8 of 8.

### 16. Dead clients are not detected
- **Where:** `server/manager/hub.go` (`checkConnection`), `client/main.go` (`checkConnection`)
- **What:** The server has no keepalive or read deadline. A client that disappears without closing its TCP connection stays registered until a write to it fails. Visitors are assigned to that dead socket in the meantime.
- **Fix:** Add read deadlines, and make the client send pings within that interval.
- **Observed:** Clients killed with `kill -9` are removed from the hub immediately (the server's read returns EOF). But during testing, one registration stayed in the hub after its client was gone. Visitors whose ticket was sent to that registration timed out, so requests intermittently failed (1 of 3 in one run). The cause is not confirmed yet; the hub does not log which registration is stale.
- **Status (completed):**
  - The server sets a read deadline of 90 seconds on each tunnel client (`clientIdleTimeout`). A client that stops responding is removed instead of staying registered.
  - Clients already send a `ping` every 30 seconds, so a healthy client never hits the deadline.
  - Writes to a client have a 5-second deadline (done in the previous step).
- **Verified:** `TestSilentClientIsDetected` checks that a silent client is removed. Not yet tested live with a frozen client process, which would take 90 seconds.

### 17. Traffic counters are lost on database errors
- **Where:** `server/traffic/manager.go` (`saveAllToDatabase`), `server/traffic/database.go` (`updateDatabase`)
- **What:**
  - Counters are reset to zero before the database write, so a failed write loses the data.
  - `updateDatabase` calls `log.Fatalf` when the config cannot be read, which stops the process.
  - `TrafficManager.Close` is never called.
  - `db.GetConnection` lazily initializes a global without synchronization.
- **Fix:** Reset counters only after a successful write. Log errors instead of exiting. Guard the connection initialization with `sync.Once`.
- **Status (completed):**
  - Counters are reduced only by the amount that was written. If the database is down, the bytes stay pending for the next save.
  - Saves are serialized, so the same bytes are never written twice.
  - `updateDatabase` returns an error instead of calling `log.Fatalf`.
  - `TrafficManager.Close` runs on shutdown through `Manager.Close`.
  - `db.GetConnection` is guarded by a mutex.
- **Not verified:** no test yet for the retry after a failed write. Only the build was checked.

### 18. Ticket values are printed to stdout
- **Where:** `server/manager/hub.go` (`handleServerRequest`)
- **What:** For an invalid ticket, every pending ticket is printed with `fmt.Println`.
- **Fix:** Remove the debug print.
- **Status (completed):** the `fmt.Println` of pending tickets was removed when the hub was rewritten. Checked: `grep` finds no print in `server/manager`.

---

## Medium: functional bugs

### 19. Server command-line flags have no effect
- **Where:** `server/config/config.go` (`loadConfig`)
- **What:** `-admin-addr`, `-manager-addr`, `-proxy-addr`, `-admin-secret`, `-tls-cert`, `-tls-key`, the Redis and database flags, and the NATS flag are parsed but never copied into `appConfig`.
- **Fix:** Apply each non-empty flag value over the file and default values.
- **Status (completed):** the flags now override the file. Each flag the user writes replaces its value, and the result is validated like the file. A flag left unwritten changes nothing, so `-redis-db 0` works. The RabbitMQ flags were removed, because the configuration has no RabbitMQ settings.
- **Verified:** `overrides_test.go` covers overrides applied after the file, bad overrides rejected, and flag paths not rebased. Live: `-admin-secret short -proxy-addr nope` made the server print both errors and exit with code 1.

### 20. Example configuration does not match the config structs
- **Where:** `server/config.yml.example`, `server/config/config.go`
- **What:** The example uses `certs:`, `database.dbname`, and `serverUrl`. The structs expect `tls.certificate_path` and `tls.key_path`, and `database.database`. Unknown keys are ignored silently, so the database name falls back to `app_db`.
- **Fix:** Make the example match the structs, and reject unknown keys.
- **Status (completed):** `server/config.yml.example` and `client/config.example.yml` were regenerated, and they use the keys the structs read.
- **Verified:** `shipped_example_test.go` loads the shipped example, and checks that it is refused while the placeholder key is still in it.

### 21. `allowMultipleConnections: false` may be stored as `true` **(verify)**
- **Where:** `server/db/models.go`, `server/auth/postgres.go` (`AddDomain`)
- **What:** The model has `default:true`. GORM omits zero values for fields with a default tag, so `false` is replaced by the database default.
- **Fix:** Use a pointer type, or set the field explicitly with `Select` or `FirstOrCreate` options.
- **Status (completed):** the `default:true` tag was removed from the model, so GORM always writes the value, including `false`. The admin `POST` sets `allowMultipleConnections` to `true` before reading the JSON, so an omitted field keeps the default.
- **Verified:** live, a domain created with `false` read back as `false` (before the fix it read back as `true`). `TestCreateDomainMultipleConnectionsDefaults` covers the omitted and the explicit case.
- **Note:** existing rows and the column default in the database are unchanged. `AutoMigrate` does not drop defaults.

### 22. Admin update handler panics and returns wrong status codes
- **Where:** `server/admin/routes.go` (`updateDomain`)
- **What:** `domain["apiKey"].(string)` and `domain["allowMultipleConnections"].(bool)` are unchecked type assertions, so a wrong JSON type panics. A missing domain returns 500 instead of 404.
- **Fix:** Use checked assertions, and return 400 and 404 where they apply.
- **Status (completed):** `auth.ErrDomainNotFound` is returned for a missing domain. The admin API answers 404 for it and 500 for other errors. `PATCH` checks the JSON types and answers 400 on a wrong type or an empty key, instead of panicking.
- **Verified:** live, `PATCH` on a missing domain returned 404, and a string for `allowMultipleConnections` returned 400. Unit tests cover the same cases.

### 23. Install script fails silently
- **Where:** `server/install.sh`
- **What:**
  - It copies `config.yml`, which is in `.gitignore` and not in the repo. Errors are hidden by `&>/dev/null`, so the service starts without `admin_secret_key` and exits through `log.Fatal`.
  - It builds to the fixed path `/tmp/lipstick-6415415`, which is vulnerable to symlink attacks when run as root.
  - It checks `$USER` instead of `id -u`.
- **Fix:** Fail loudly on copy errors, build to a `mktemp` path, and check `id -u`.
- **Status (completed):** `install.sh` runs with `set -eu`, checks `id -u`, builds into a `mktemp` directory, and refuses to run without `config.yml`. Nothing is hidden with `&>/dev/null`. It creates a `lipstick` system user, installs the configuration as `0640 root:lipstick`, and enables the service.
- **Verified:** `sh -n` passes. I did not run the script, because it installs a system service.

### 24. systemd unit has an invalid directive and runs as root
- **Where:** `server/lipstick-server.service`
- **What:** `ReadOnlyDirectories=` is not a valid directive (the correct one is `ReadOnlyPaths=`). The service runs as `User=root`. `PIDFile` points to a file that is never written.
- **Fix:** Create a dedicated service user, use `ReadOnlyPaths=`, and remove `PIDFile` or write the PID file.
- **Status (completed):** the unit runs as `User=lipstick`. `ReadOnlyDirectories=` is replaced by `ProtectSystem=strict` and `ReadOnlyPaths=`. `PIDFile` is removed, since nothing wrote it. The configuration is read-only, and only `/etc/lipstick/certs` is writable.
- **Verified:** `systemd-analyze verify` reports only that `/usr/bin/lipstick` does not exist yet, which is expected before installation.

### 25. Docker image runs as root
- **Where:** `Dockerfile`
- **What:** The image runs as root, does not declare `EXPOSE`, and copies binaries from `bin/`, which `build.sh` creates.
- **Fix:** Add a non-root user, `EXPOSE` the ports, and build inside a multi-stage Dockerfile.
- **Status (completed):** the Dockerfile is multi-stage. The build stage compiles only the server. The runtime is `gcr.io/distroless/static-debian12:nonroot`, with no shell, and it runs as `nonroot`. It exposes 5050 and 5051. A `.dockerignore` keeps `config.yml`, `client_key.txt`, the certificates and `.git` out of the image.
- **Verified:** the image builds (39.9 MB). Its user is `nonroot:nonroot`, and without a configuration it prints the error and stops. I did not run a tunnel through the container.

### 26. README and code disagree on defaults
- **Where:** `README.md`
- **What:**
  - Ports: proxy `:8080` vs `:5050`, manager `:8081` vs `:5051`.
  - Client target: `127.0.0.1:8082` vs `tcp://127.0.0.1:12000`.
  - The documented `-k` default is `super_secret_key`, which is a weak default that users may copy.
- **Fix:** Make the README match the code, and remove the example secret.
- **Status (completed):** `README.md` was rewritten to match the code. The flags, the ports and the defaults are the real ones. The environment-variable tables were removed, because the code does not read those variables (only `DEBUG`). The example secret `super_secret_key` was removed.

### 27. Client configuration loads at package init
- **Where:** `client/config/config.go`, `client/main.go`
- **What:** `flag.Parse()` runs during package initialization, and `main.go` assigns `configuration, _ = config.GetConfig()` at package level, ignoring the error. This makes the package hard to test.
- **Fix:** Load configuration inside `main` and handle the error.
- **Status (completed):** the client loads its configuration on demand, through `GetConfig` with a `flag.FlagSet`. Nothing is parsed at package initialization, and `main` handles the error.
- **Verified:** the client configuration tests call the loader directly, and `go vet` is clean.

### 28. Header reader waits for `\r\n` only
- **Where:** `helper/http.go` (`ReadUntilHeadersEnd`)
- **What:** The loop checks `line == "\r\n"`. A request that ends headers with a bare `\n` is read until EOF, which can leave the goroutine blocked.
- **Fix:** Accept `\n` as well as `\r\n`, and set a read deadline.
- **Status (completed):** `ReadUntilHeadersEnd` reads one byte at a time, so it never reads past the headers and the body stays on the connection. It accepts `\n` and `\r\n`, limits the headers to 64 KiB, and uses a 10-second deadline that is cleared before returning.
- **Verified:** `helper/http_test.go` covers both line endings, the body that remains on the connection, the limit, and truncated headers.

### 29. `go vet` warnings
- **Where:** `client/main.go:127`, `client/main.go:135`, `server/manager/hub.go:188`
- **What:** `fmt.Fprintf` is called with a non-constant format string (`helper.BadGatewayResponse`). If that string ever contains `%`, the output is corrupted.
- **Fix:** Use `Write([]byte(...))` or `Fprint`.
- **Status (completed):** `go vet ./...` reports nothing.

### 30. Dead code
- **Where:** `server/manager/hub.go` (`handleIncomingClientConn2`), `helper/net.go` (`MonitorIdle`, `RemoteConn.used`), `helper/listener.go` (`ListenerConn`, `NewListenerConn`), `server/manager/ticket.go` (overflow check)
- **What:** Unused functions. `MonitorIdle` also writes `used` without synchronization. The overflow check in `TickerManager.generate` can never be true.
- **Fix:** Delete the unused code.
- **Status (completed):** `handleIncomingClientConn2`, `MonitorIdle`, `ListenerConn`, `NewListenerConn`, and the overflow check in `TickerManager` were removed. A search for them finds nothing.

### 31. Client treats a rejected upgrade as connected
- **Where:** `client/main.go` (`startClient`), `client/manager/http.go` (`Connect`)
- **What:** `Connect` writes the upgrade request and returns without reading the response status. `startClient` then reads the headers, ignores the status, and logs `Connected to server`. With a wrong `APISecret`, the server answers 401 and the client still reports a successful connection, then waits for tickets that never arrive.
- **Observed:** Reproduced locally with a wrong key: the server logged `Unauthorized tunnel client for domain:localhost`, and the client printed `Connected to server at http://localhost:5051`.
- **Fix:** Parse the response status line and headers, and treat anything other than `101` or `200` as a failed connection. Retry with backoff and log the status code.
- **Status (completed):** `Connect` reads the response status line and accepts only `200`. Any other status, such as `401` for a wrong key, is an error that includes the line. `startClient` retries with a wait that doubles from 3 s up to 60 s, and resets after a successful connection.
- **Verified:** `client/manager/connect_test.go` covers a refused handshake and an accepted one, with tickets still readable after the headers. Live, a wrong key printed `server refused the connection: HTTP/1.1 401 Unauthorized` and waited 3, 6 and 12 seconds.

---

### 32. Opt-in plain TCP ports for intranet services (design decision, new attack surface)
- **Where:** `server/manager/intranet.go`, `server/manager/routes.go` (`upgrade`, `writeRefusal`), `server/config/config.go` (`intranet_ports`), `client/config/config.go` (`-intranet-port`), `client/main.go`
- **What:** Services that cannot speak TLS (for example RAW printing on 9100) cannot be told apart by domain on a plain-TCP port, because the connection carries no SNI or Host. The port itself identifies the service. A client asks for a port in the upgrade request (`X-Lipstick-Intranet-Port`). The server reserves it only if the policy allows: the domain must be listed, the port must be in `port_range`, the port must be free, and the bind address must be a specific intranet interface (not `0.0.0.0` or `::`). Each port serves one domain, and connections from networks outside `allowed_cidrs` are closed before the tunnel. Ports close when the domain has no clients left. The tunnel to the client stays TLS.
- **Risk:** this is the first plain-text path into the server. It is an exception to "TLS is always required", and it is the main exposure to review.
- **Mitigations in code:** the policy is owned by the server, not the client; the client only chooses a port inside it. Refusals are explicit (403 policy, 409 taken). The source is filtered on accept. The config refuses a wildcard bind, a wide-open CIDR, a domain without networks, a duplicate domain and a range that overlaps the server's own ports.
- **Mitigations expected from the operator:** open only the port range, and only to the intranet ranges, in the cloud firewall or security group. Prefer a private bind address.
- **Verified:** `server/config/intranet_test.go` covers the policy rules. `server/manager/intranet_test.go` covers refusal by domain and by range, the taken port, service to an allowed source, refusal of other sources, and release of the port. `client/config/intranet_test.go` covers the flag. End to end, a reserved port answered an allowed loopback source with the backend response, an out-of-range request got 403 on the wire and the client showed it, and the TLS tunnel kept working.
- **Not verified:**
  - A refusal from a non-allowed address on another machine (the tests run on loopback).
  - The race where a domain loses its last client while another reserves the same port at the same moment. The release runs when the hub goes idle, so a reservation that lands in that window can be closed. This needs a test.
  - A 409 end to end (two domains on one port). It is covered by unit tests only.

### 33. TCP sessions never ended and end-of-file was lost (relay bug)
- **Where:** `server/manager/hub.go` (`syncConnections`), `client/handlers/tcp.go` (`HandleTCP`), `helper/net.go` (`CloseWrite`), `client/manager/http.go` (`CustomConn.CloseWrite`)
- **What:** Two problems in the relay of raw TCP traffic. The relay only ended when the tunnel side failed, so a visitor that closed left its session open forever. Half-close was not passed on, so a client waiting for end-of-file never finished, and its reply was lost. Found while testing intranet ports: about 100 sessions stayed in `syncConnections`, and a visitor that waited for end-of-file timed out.
- **Fix:** each direction now passes its end-of-file on (`CloseWrite`). The server tells the client when the visitor is done, and the client half-closes the service. Each side has a bounded drain time (10 s), so an idle peer cannot keep a session open. Both sides can finish their reply after the other has finished.
- **Verified:** `server/manager/relay_test.go` checks end-of-file in both directions and that the session ends when the visitor closes. `client/handlers/relay_test.go` checks that the service finishes its reply after the tunnel side ends. Live: 200 of 200 raw round-trips completed, the TLS raw path went 8 of 8 at sizes from 100 to 4096 bytes, and HTTP through the TLS port went 8 of 8.
- **Why the earlier tests passed:** they read exactly as many bytes as they sent, and never waited for end-of-file. The end-of-file test found the bug.

### 34. Two clients on one domain with different targets share visitors at random (open)
- **Where:** `server/manager/hub.go` (`getProxyNotificationConn`), `client/main.go` (one control connection per `-p` target)
- **What:** With `allowMultipleConnections: true`, the hub picks a client at random for each visitor. If a stale client for the same domain is still connected with another target, half the visitors reach the wrong service. Seen live: an old client on `-p 127.0.0.1:8082` received raw visitors meant for an echo service on `8090`, and returned an HTML error page.
- **Why it matters:** an intranet port is one service, so a second service under the same domain is silently mixed in. It is the same rule as "one service per domain", and the server cannot detect it today.
- **Possible fix:** the client sends its target label in the upgrade request, and the hub refuses a client whose label differs from the others on the same domain (403). Or the policy names one target per domain in the server configuration.
- **Status:** open. Not changed.

### 35. HTTP through the tunnel: keep-alive, request bodies and the 502 response (relay bugs)
- **Where:** `client/handlers/http.go` (`HandleHTTP`, `serveRequest`, `sendErrorResponse`), `helper/errors.go` (`BadGatewayResponse`)
- **What found by testing a normal HTTP server:**
  - **Keep-alive:** the client served one request per reverse connection, then closed. A second request on the same visitor connection got no response. Clients that reuse connections (most API clients) failed.
  - **POST bodies:** the body was forwarded without a length, so Go sent it chunked. The server read `Content-Length` and got `0` bytes, so a POST arrived empty.
  - **502:** when the service was down, the visitor got only the HTML body, with no status line, so it saw no HTTP response at all. `BadGatewayResponse` also used LF line endings, which HTTP does not allow.
- **Fix:**
  - One buffered reader is shared by all requests on a visitor connection, so the read-ahead of one request is not lost.
  - Length and framing (`ContentLength`, `TransferEncoding`) are passed on as received. The body is drained after each response.
  - The connection stays open for the next request unless either message says `Connection: close`, or the response has no length.
  - WebSocket reads from the same reader.
  - `BadGatewayResponse` is a complete HTTP/1.1 502 with CRLF endings and `Connection: close`. All paths use it.
- **Verified** (`/tmp/.../scratchpad/http_suite.py`, normal Python HTTP server behind the tunnel, certificate verified as a visitor would):
  - 7 of 7 checks: GET small text, GET a 5 MiB binary body, POST a 1 MiB body with matching SHA-256, HEAD, 404 pass-through, keep-alive (10 requests on one connection), and 20 parallel clients doing 3 GETs and 1 POST each.
  - With the backend down, the visitor got `HTTP/1.1 502 Bad Gateway`. After the backend came back, the next request returned 200.
- **Not verified:** chunked responses from the backend, `Expect: 100-continue`, and long-lived streams (SSE or MJPEG) across the keep-alive loop.

### 36. Visitor request body lost when it arrived with the headers (server bug)
- **Where:** `helper/listener.go` (`handleHTTPConn`), `helper/net.go` (`ConnWithBuffer.Unread`)
- **What:** When a request's headers and body arrived in one read, the listener consumed the headers and then replaced the buffer with the headers alone (`SetBuffer`). The body was dropped, and the tunnel client received a corrupted stream: a POST with a small body got no reply. Found with `curl --data-binary`. Python sent the headers and body in separate writes, so it had hidden the bug.
- **Fix:** the headers are put back in front of the bytes not yet read (`Unread`), so the stream reaches the client exactly as it was sent.
- **Verified:** `helper/unread_test.go` covers the headers-and-body single read. Live: `curl --data-binary` of 3 bytes and of 1 MiB (SHA-256 matches) now return 200.

### 37. WebSocket handshake rejected: Host header not forwarded (client bug)
- **Where:** `helper/io.go` (`HTTPRequestToString`), used by `client/handlers/http.go` (`handleWebSocket`)
- **What:** Go keeps the `Host` header in `request.Host`, not in `request.Header`. The forwarding wrote only `request.Header`, so the WebSocket handshake reached the service without `Host`. Jupyter answers `400 Bad Request` to that handshake, so no WebSocket connection could be opened through the tunnel. The same request with `Host` gets `101 Switching Protocols`.
- **Fix:** `HTTPRequestToString` writes `Host` from `request.Host`.
- **Verified:** `helper/io_test.go` covers the header. Live: a Jupyter kernel executed `print(2+2)`, `sum(i*i for i in range(1000))` and a 2000-line output through `wss://localhost:5050`.

### 38. Forwarded Host was the target address, which broke services that check Origin (client behavior)
- **Where:** `client/handlers/http.go` (`serveRequest`)
- **What:** the request to the service carried `Host: 127.0.0.1` (the target), not the `Host` the visitor sent. Grafana Live compares `Origin` with `Host` and answered `403 origin not allowed` to a WebSocket that came through the tunnel.
- **Fix:** the service receives the visitor's `Host`, including its port, as a reverse proxy normally passes it on. This is a behavior change: services that depended on seeing the target address in `Host` will now see the public one.
- **Verified:** `client/handlers/host_test.go`. Live: Grafana Live accepts the WebSocket with `GF_SERVER_ROOT_URL` set to the tunnel address; Jupyter and S3 pass again after the change. Ollama and Prometheus were not re-run after this change.

### 39. Reverse tunnel connections were not authenticated (security)
- **Where:** `server/manager/manager.go` (`handleTunnel`), `client/main.go` (`establishConnection`), `helper/tunnelauth.go`
- **What:** the control connection checks the domain key, but the reverse connection `GET /<ticket>` checked only the ticket. The ticket travels only over TLS and is single-use, so the risk was small today. It becomes the main path for traffic once a connection pool exists, so it had to be closed first.
- **Fix:** each reverse connection carries `X-Lipstick-Proof`, an HMAC-SHA256 of the domain key over the ticket. The server recomputes it and compares in constant time. The key is not sent on that connection. A missing, wrong, or empty proof gets `401` and the connection is closed. A domain with an empty key never verifies.
- **Compatibility:** clients older than this change cannot serve traffic. Update the server and every client together.
- **Later change (see 40):** a rejected reverse connection is now closed without a reply, not answered with `401`. A `401` written onto the connection reached the client as visitor data.
- **Verified:** `helper/tunnelauth_test.go` covers the proof's binding to key and ticket, empty and garbage proofs, and that the raw key is not accepted as a proof. Live: a correct client serves traffic (200); a connection without a proof gets 401; a wrong proof gets 401; the raw key in the header gets 401; a correct proof for an unknown ticket passes the check and then gets 502.

### 40. Multiplexed sessions replace the pool of waiting connections (performance)
- **Where:** `server/manager/mux.go`, `helper/stream.go`, `helper/tunnelauth.go`, `server/manager/listener.go` (route `/mux`), `client/mux.go`, `client/main.go`
- **What:** the client opens 8 authenticated TLS connections and runs a yamux session on each. Every visitor becomes one stream: the server opens it, writes `LIPSTICK-PORT <port>` and relays the visitor. A session is authenticated once, when it is opened, with a proof over a one-time nonce (the nonce carries the client time, and is refused if older than 2 minutes or seen before). Visitors fall back to the ticket path when no session can take them. The earlier pool of waiting connections was replaced.
- **Design choice:** authentication happens per session, not per visitor. The server opens streams and the client accepts them, so only the client that opened a session receives traffic on it. Per-visitor authentication would add one HMAC round per visitor and would not change what the client can be asked to do. Tell me if you want a proof on every stream as well.
- **Half-close:** yamux's `Close` stops writes and keeps reads open, so the relay's half-close still works. Checked on the intranet port: 1 MiB sent, half-closed, and the echo returned whole.
- **Measured here** (one machine, 16 cores, everything local, Go load generator, backend answers 1 KiB):
  - new connection per request, ticket path (before any change here): about 1 900 req/s, p50 5.1 ms
  - new connection per request, previous pool: about 2 050 req/s, p50 4.5 ms
  - new connection per request, multiplexed (8 sessions): about 2 650 req/s, p50 3.7 ms
  - keep-alive, previous pool: about 13 000 req/s, p50 0.7 ms
  - keep-alive, multiplexed with 2 sessions: about 7 900 req/s, p50 1.2 ms (streams shared two connections)
  - keep-alive, multiplexed with 8 sessions: about 11 000 req/s, p50 0.8 ms
  - direct to the backend, new TCP connection per request: about 23 700 req/s, p50 0.38 ms
- **What the numbers show:** multiplexing removes the per-visitor connection from the client, and that helps when each visitor opens a new connection. With keep-alive it is slightly slower than the earlier pool, because streams share the session connections. The sessions count matters: with 2, keep-alive dropped by about 40%. The remaining cost for a new connection per visitor is the visitor's own TLS handshake, which the tunnel does not control.
- **Verified:** unit tests for proofs, nonces, stream assignment, closed sessions, a stopped hub, nonce replay, and an end-to-end stream served by the client. Live: 9 connections to the manager (1 control + 8 sessions); visitors served through the proxy and through the intranet port; nonce replay refused; old nonce refused; after a server restart the client reconnects its sessions and traffic resumes.

### 41. TLS session resumption for client reconnections (performance)
- **Where:** `client/tlsconf/tlsconf.go`
- **What:** the client keeps a shared TLS session cache, so reconnecting to the same server resumes the session and skips the full key exchange. Each connection still needs its own proof, so resumption does not weaken authentication.
- **Measured here:** about 2 050 to 2 130 req/s with new connections per request, p50 about 4.5 ms. The gain is small, because most of the handshake cost is the visitor's own connection, which the client cannot resume.

### 42. Visitors could not reach the server that held their tunnel (distributed routing)
- **Where:** `server/manager/cluster.go`, `server/manager/hub.go`, `server/manager/manager.go` (`HandleHTTPConn`, `HandleTCPConn`), `server/manager/listener.go` (route `/relay`), `server/subscriptions/subscriptions.go` (`SubscribeBroadcast`), `server/config/config.go` (`cluster`)
- **What:** a visitor could arrive at a server with no tunnel for its domain, while the client was connected to another server. The ticket was published on NATS, but the reverse connection landed on a server without the waiting visitor, so the visit never paired. Nothing in the repository published the tickets either. The NATS ticket path was removed.
- **Fix:** the server that receives a visitor without a local session relays it to a peer that has one. Presence is published on NATS every 5 seconds and expires after 15. The relay is a TLS connection to the peer's manager port, verified with the cluster certificate (or `cluster.ca_file`), and authenticated with a proof made with the domain key over a one-time nonce. The peer hands the visitor to one of its sessions and keeps the original port.
- **Bug found while testing:** the presence subscription used a queue group, so each message reached only one of the two servers. It now uses a broadcast subscription (`SubscribeBroadcast`).
- **Trust:** NATS only chooses where to relay. A peer with a different certificate fails TLS verification, so a fake presence message cannot receive visitors.
- **Verified:** unit tests for presence (freshness, own messages, stale peers, the freshest peer), config validation, and the no-cluster 502. Live, with two servers sharing the database and NATS: a visitor entering B reached a client on A (200); a visitor entering A reached a client on B (200); with no session anywhere, both entries returned 502 in about 3 ms; the request log on the serving server recorded the original port (5050 for a visitor that entered A).
- **Not verified:** servers on different machines; a server failing in the middle of a load; cluster certificates signed by a CA (the `ca_file` path is written but not exercised).
- **Update (see 44):** relays now use one persistent session per peer and domain. The per-visit TLS connection described above is gone.

### 43. NATS accepted anonymous and plaintext connections (security)
- **Where:** `server/config/config.go` (`NatsConfig`, `validateAuth`), `server/subscriptions/subscriptions.go` (`NewSubscriptionManager`)
- **What:** the server connected to NATS without credentials and over plain `nats://`. Presence messages decide where visitors are relayed, so anyone who could publish on NATS could steer relays, and anyone who could read it could learn which domains run where.
- **Fix:** `nats.username` and `nats.password`, or `nats.token`, are passed to the connection. Credentials are refused over a plain URL. A cluster refuses to start without credentials and without `tls://`. A single server may still run without credentials, as before. The password is never logged.
- **Verified:** config tests for each rule. Live, against a separate authenticated TLS NATS on port 4223 (the normal NATS on 4222 was not changed): a wrong password stops the server with `Authorization Violation`; an anonymous connection is refused; two cluster servers with correct credentials relay a visitor (200); the password does not appear in any log.
- **Not verified:** NATS ACLs per subject (the server can still publish on any subject); credentials rotation; `nats.ca_file` with a CA-signed NATS certificate.

### 44. Relays opened a new TLS connection per visitor (performance)
- **Where:** `server/manager/cluster.go` (`relayFor`, `dialRelay`, `serveRelayStreams`), `server/manager/relay_session_test.go`
- **What:** each relayed visitor dialed the peer again, which cost a full TLS handshake. Measured: about 1 700 req/s with new connections, against 2 700 direct.
- **Fix:** one persistent yamux session per peer and domain, authenticated once with the relay proof. Each visit is one stream. A session that fails is dropped and reopened on the next visit.
- **Measured here:** about 2 500 req/s with new connections (p50 4.0 ms), against 2 700 direct (p50 3.6 ms). With keep-alive, about 9 700 req/s.
- **Verified:** tests show a relayed visitor reaches a client session with its original port, and a relayed visitor with no session gets 502 back over the relay. Live: 30 relayed visits left the connection count between the servers unchanged.

### 45. UDP intranet ports (feature)
- **Where:** `server/manager/udp.go`, `server/manager/intranet.go` (registry keyed by protocol and port; `reserveIntranetUDPPort`), `server/manager/mux.go` (`openStream`), `server/manager/routes.go` (`X-Lipstick-Intranet-UDP-Port`), `helper/udpframe.go`, `client/mux.go` (`serveUDPFlow`), `client/config/config.go` (`intranet_udp_port`, `udp_target`)
- **What:** a client can reserve a plain UDP port inside the server's policy, the same as a TCP one. Each source address gets a flow: one stream in a client session, with datagrams framed by a 2-byte length. Flows close after 60 seconds without traffic. Datagrams from sources outside the allowed networks are dropped and logged. A full flow queue drops datagrams, as UDP does.
- **Design choice:** the port stays plain, without TLS, as the TCP intranet ports are. The server decides the policy, and the client only chooses a port inside it.
- **Verified:** unit tests for framing (boundaries, empty and maximum sizes), a full round trip through the server and the client's local service, and policy for UDP addresses. Live, on the intranet address: 200 of 200 echoed datagrams came back intact; 2 000 bulk datagrams came back intact in 0.14 seconds; a datagram from an outside address got no reply.
- **Not verified:** RTP or BACnet; flow expiry under sustained load; packet loss on the link between server and client, which TCP turns into delay.
- **DNS (see 46).**

### 46. DNS through intranet ports (verification)
- **Where:** no code change; the UDP intranet port of entry 45 and the TCP intranet port.
- **Setup:** dnsmasq as a real DNS server on loopback, with two records and one large TXT record. Lipstick reserved a TCP port and a UDP port on the intranet address, and forwarded both to dnsmasq.
- **Verified:** A and TXT queries over UDP return the right answers; 300 of 300 sequential queries were answered (p50 0.07 ms); an 800-byte answer over UDP with EDNS came back complete 100 times out of 100; a 1 544-byte answer was truncated over UDP (TC bit) and came back complete over the TCP port; a source outside the allowed network got no answer.
- **Note:** dnsmasq's default EDNS limit is 1 232 bytes, so larger answers are truncated over UDP by the server, as the DNS protocol expects. That is dnsmasq's policy, not a tunnel limit.
- **Not verified:** DNS over a link that drops packets; resolvers that query the reserved port from many source ports at once.

### 47. DTLS for UDP: proxy and verified local target (feature)
- **Where:** `server/manager/dtls_proxy.go`, `server/manager/dtls_sni.go`, `server/main.go` (`proxy.udp`), `client/mux.go` (`dialUDPTarget`), `client/config/config.go` (`udp_target_tls`), `client/tlsconf/tlsconf.go` (`Roots`), `server/manager/udp.go` (flows take a reply function). Library: `github.com/pion/dtls/v3`.
- **What:** the UDP counterpart of the TLS proxy. One UDP socket on `proxy.address` receives every visitor. The first datagram of each source is read for its SNI, which picks the domain. That source then becomes its own DTLS connection, terminated with the server's certificate, and its datagrams go to the domain's client as an intranet UDP flow. On the client, `udp_target_tls` connects to the local service over DTLS, verified against `ca_file` and the target's host name, and the handshake runs at connection time so a refusal is logged.
- **Design choice:** the server terminates DTLS, so it can route by SNI and it sees the plaintext, as the TLS proxy does. An option that passes DTLS through without terminating it is not built.
- **Verified:** the SNI reader is tested with valid ClientHellos and with every truncation of one. Live: a DTLS visitor (openssl) reached the local service and got its answer, for a plain local target and for a DTLS local target; an unknown SNI and a non-DTLS datagram got no answer; a local service with a certificate the CA does not sign was refused, and the client log said so.
- **Not verified:** more than one domain sharing the port (only `localhost` was configured); visitors from several sources at once; handshake cost under load; cluster routing for DTLS.

### 48. Passthrough stayed on after it was turned off (bug)
- **Where:** `server/auth/manager.go` (`MakeAuthManager`)
- **What:** the admin API and the proxy each created their own auth manager, and each keeps its own domain cache. Turning passthrough off through the API updated the admin's cache, but the proxy kept reading its old value until the cache expired, so visitors still got the service's certificate. The admin API showed `false` the whole time.
- **Fix:** one auth manager per process, shared by the admin API and the proxy. A domain update now takes effect for both at once.
- **Verified:** live, with the same `proxy_pass` in `https://` for both modes. Passthrough on: visitor sees the service certificate, 200. Passthrough off: visitor sees the server's certificate, 200, and the visit appears in the request log. The switch works in both directions.
- **Also seen:** a 502 right after a domain change is the hub being dropped while the client reconnects (`Hub not found`). It lasts about 3 seconds. Changing a domain disconnects its client by design; the 502 is within that window.

### 49. Passthrough TLS per domain (feature)
- **Where:** `server/manager/manager.go` (`RoutePassthrough`), `server/manager/mux.go` (`takePassthrough`, `LIPSTICK-PASS`), `helper/sni_listener.go`, `helper/clienthello.go`, `client/passthrough.go`, `client/mux.go` (`LIPSTICK-PASS` as plain TCP).
- **What:** a domain with `passthrough` on forwards its TLS without ending it. The proxy reads the SNI of the first record and sends the bytes to a client session that announced passthrough. That client's service presents a certificate for the domain; the client checks it at each connection with verified TLS before announcing.
- **Trade-offs (accepted):** no HTTP request log and no panel data for those domains; the server's certificate does not cover them.
- **Not verified:** more than one domain on passthrough; clients on different machines.

### 50. Passthrough measured against terminated TLS (performance)
- **Method:** one machine, load generator in Go (same tool as entries 40 to 44), 10 workers, 8 seconds per run. The service is a Go HTTPS server with its own certificate, the same in every run. The client has `proxy_pass` in `https://`, so the service gets TLS in both modes. Each mode was measured twice; the two runs agree within about 2%.
- **Results:**
  - Terminated at the server, new connection per request: 2 001 and 1 859 req/s (p50 4.9 and 5.3 ms).
  - Terminated at the server, keep-alive: 9 669 and 9 547 req/s (p50 1.0 ms).
  - Passthrough, new connection per request: 2 999 and 2 970 req/s (p50 3.3 ms).
  - Passthrough, keep-alive: 35 370 and 35 029 req/s (p50 0.27 ms).
  - Direct TLS to the service, no tunnel: 3 950 req/s new connection (p50 2.5 ms); 86 261 req/s keep-alive (p50 0.09 ms).
- **What it means:** passthrough removes one TLS layer from every byte and the server's handshake from every new connection. With keep-alive it is about 3.7 times faster than terminated and about 40% of direct TLS. The remaining cost is the tunnel's own encryption between server and client, and the service's TLS.
- **Limits of the measurement:** one machine, loopback, one domain, one service, no packet loss. Production numbers need a real network.

### 51. Visitor authentication: users, tokens, and a restricted mode that latches (feature)
- **Where:** `server/auth/visitor.go` (argon2id passwords, hashed tokens, latch), `server/manager/guard.go` (per-request relay, checks, brute-force limiter), `server/manager/routes.go` (latch at upgrade, refusal reasons), `server/admin/routes.go` (credential endpoints, latch rules), `server/db/models.go` (`VisitorUser`, `VisitorToken`, `AuthLatched`).
- **Behavior:** a client asks with `visitor_auth: true`. The domain latches to restricted for good, until an admin clears it. A client that asks on a passthrough domain, or on a domain with no credentials, is refused with the reason. An admin cannot turn passthrough on for a restricted domain.
- **Design choice:** a restricted domain is served through a relay that reads and checks every request, not only the first on a connection. Responses are kept in the order of their requests: the next request is read only after the response to the previous one is complete.
- **Bug found during testing:** a refused second request could be answered before the first one's response, which breaks HTTP ordering. Fixed by waiting for each response to end before reading the next request.
- **Also fixed:** the admin API and the proxy had separate auth managers with separate caches, so a passthrough change could take minutes to apply on one of them (see entry 48, which this supersedes for the cause).
- **Verified:** tests for per-request checks, pipelined requests, chunked bodies, Content-Length bodies, head size limits, token scopes, the brute-force limiter, and the password and token hashing. Live: no credentials gives 401 with the challenge; a user and password give 200; a wrong one gives 401, and after five attempts 429; a token gives 200 inside its scope and 403 outside it; a bad token gives 401; a good and a bad request pipelined on one connection give 200 then 401; a client that does not ask still gets enforcement; clearing the latch opens the domain; a client asking on a passthrough domain is refused with the reason; passthrough cannot be turned on for a restricted domain.
- **Not verified:** WebSocket traffic on a restricted domain; many concurrent visitors with credentials; cluster consistency of the latch (it lives in the database, and each server reads it through its cache).
- **Limits (stated):** no per-route permissions for users; no cache for verified Basic credentials, so each Basic request costs one argon2id check; a client refused for passthrough keeps retrying with backoff.

### 52. Regression suite: unit and in-process integration tests (quality)
- **Where:** `server/manager/harness_test.go`, `server/manager/integration_test.go`, and the unit tests of each package. The client binary is built by the test itself.
- **What:** a real server runs in-process with an in-memory domain store, the real proxy wiring (SNI, TCP, HTTP, passthrough, DTLS, intranet TCP and UDP), and the real client as a child process. No PostgreSQL or NATS is needed. Fifteen end-to-end scenarios cover TCP and UDP paths and their configurations (see the README, "Desarrollo y pruebas").
- **Bugs found by the suite and fixed:**
  - *Passthrough startup race (client):* the mux sessions were opened before the passthrough certificate check, so the first sessions did not carry the passthrough header, and a passthrough visitor got the server's certificate. The check now runs first.
  - *DTLS handshake never completing in the untrusted-service scenario:* this is correct behaviour (the server drops the visitor when the local service refuses), but the test assumed the handshake completes. The test now accepts either outcome, and the helpers bound their handshakes so a stuck one fails fast instead of hanging for minutes.
- **Verified:** `go vet ./...` is clean. `go test -race ./...` passes for every package. The integration scenarios pass three times in a row under `-race`, and the passthrough scenario five times.
- **Not covered by the suite:** a cluster across machines; sustained load; WebSocket on a restricted domain; UDP flow expiry under load; the installer with a real systemd; PostgreSQL and NATS paths (covered by the live checks in the entries above).

### 53. Tests on SQLite and an in-memory presence bus (quality)
- **Where:** `server/auth/postgres.go` (`NewAuthManagerWithDB`), `server/manager/cluster.go` (`PresenceBus`, `NatsBus`, `StartCluster(conf, tls, bus)`), `server/manager/harness_test.go` (SQLite per test, `memBus`), `server/manager/cluster_integration_test.go`, `server/auth/visitor_db_test.go`.
- **What:** the test suite no longer uses an in-memory domain store. Each test opens its own SQLite database with the production schema, through the production auth code (`gorm.io/driver/sqlite`). Presence between servers goes through a small `PresenceBus` interface: NATS in production, an in-memory bus in the tests. This made a two-server cluster testable in-process.
- **Bugs found while doing it:**
  - `gin.SetMode` was called per test server, which wrote a global while the previous server read it (a data race reported only under `-race`). It is now set once.
- **Verified:** `go test -race ./...` passes. The integration scenarios pass three times in a row under `-race`. The cluster scenario passes repeatedly: a visit that enters one server reaches the client on the other.
- **Not covered by the tests:** PostgreSQL-specific SQL (the SQL used is portable, but the production database is PostgreSQL); the real NATS server and its failure modes; a cluster across machines.

### 54. Per-domain CIDR access rules (feature)
- **Where:** `helper/cidr.go` (parsing, validation, the check), `server/db/models.go` (`AllowedCIDRs`, column `allowed_cidrs`), `server/manager/guard.go` (`sourceAllowedFor`, `refuseSource`), `server/manager/manager.go` (HTTP and TCP entry, passthrough), `server/manager/dtls_proxy.go`, `server/admin/routes.go` (`allowedCidrs` on create and update).
- **Behavior:** a domain lists the networks its visitors may come from. Empty means no restriction. Single addresses are accepted (`/32` or `/128`). An invalid rule is refused with 400 and not stored. A visitor outside the rules gets 403 before anything reaches the tunnel, on HTTP, TLS-terminated HTTP, TCP, passthrough and DTLS.
- **Design choice:** the check uses the TCP or UDP source address only. `X-Forwarded-For` is not trusted: trusting it would let any visitor claim any address. A deployment behind a load balancer sees the balancer's address, and that is documented.
- **Fail-closed:** a stored rule that cannot be read refuses every visitor of the domain, so a corrupt rule never opens it.
- **Verified:** unit tests for parsing (networks, single addresses, IPv6, invalid items, canonical form, nil addresses). Database round-trip on SQLite. Integration scenarios: outside the networks gets 403, inside gets 200, a single-address rule, passthrough obeys the rule, DTLS obeys the rule. Live, on the running server: a rule that excludes the visitor gives 403; an invalid rule gives 400; an IPv4 rule admits IPv4 and refuses IPv6; clearing the rules restores access.
- **Not covered:** intranet ports still use their own policy from the server configuration, not the domain's rules; the cluster does not share rules across servers beyond the shared database.

### 55. Admin API reference (Swagger / OpenAPI)
- **Where:** `server/admin/swagger.go` (general information and the key scheme), annotations above each handler in `server/admin/routes.go`, generated `server/admin/docs/` (`docs.go`, `swagger.json`, `swagger.yaml`), `server/admin/swagger_test.go`.
- **Served at:** `/swagger/index.html` (UI) and `/swagger/doc.json` (spec), on the admin port.
- **Authentication documented:** an `AdminKey` apiKey scheme in the `Authorization` header, raw value with no `Bearer` prefix, matching the code. Routes that change data and the visitor user and token lists declare it. Domain, stats, health, request-log and traffic reads do not.
- **Verified:** a test reads the spec and checks each protected route declares the key and each read route does not; a test checks the UI is served. Live: `/swagger/index.html` and `/swagger/doc.json` return 200.
- **Caveat:** the read routes are unauthenticated by design. If the admin port is ever bound to a non-loopback address, those reads become public; the description says so.
- **Tooling:** swag v1.16.4 (CLI and library), gin-swagger v1.6.0, swaggo/files v1.0.1.

### 56. Visitor users sign requests with HMAC instead of passwords (change)
- **Replaces:** the HTTP Basic users of entry 51 (argon2id password hashes). Entry 51's API tokens are unchanged.
- **Why:** a password sent on every request, even over TLS, is a secret the visitor sends each time; a per-user secret used to sign each request is never sent.
- **Where:** `helper/visitorsig.go` (signing, parsing, verifying, shared with tests), `server/manager/guard.go` (`checkSignedRequest`, `signedNonceCache`), `server/auth/visitor.go` (`AddVisitorUser` returns the secret, `RotateVisitorUser`, `VisitorUserSecret`), `server/db/models.go` (`VisitorUser.Secret`), `server/db/manager.go` (drops `password_hash`), `server/admin/routes.go` (create returns the secret once; rotate route).
- **Scheme:** `Authorization: LipstickHMAC user, ts, nonce, sig`. The signature is HMAC-SHA256 with the user's secret over the method, request target, Host, user, timestamp and nonce. Window ±2 minutes. A nonce is refused for the whole window (replay). The signature is checked before the nonce is recorded, so only holders of the secret can fill the cache. Bad signatures count toward the existing limiter.
- **Trade-off, stated:** the server must hold each user's secret to verify signatures, so the secret is stored (not hashed). Anyone with read access to the database can impersonate the users. Argon2 is gone, so verification costs one HMAC, not one argon2id run per request.
- **Secret at creation:** the create body takes `username` and an optional `secret` (at least 32 characters). If omitted, the server generates one and returns it once. A supplied secret is never echoed back.
- **Not signed:** the request body. TLS protects it; the signature does not.
- **Migration:** users created before this change have no secret and cannot sign requests. Create them again. The `password_hash` column is dropped at startup.
- **Verified:** helper tests (valid, each covered field tampered, malformed headers, username rules, a vector computed by the README's Python example); manager tests (replay, skew, wrong secret, moved path, unknown user, limiter, nonce expiry); an integration scenario with the real client binary (signed 200, replay 401, rotation invalidates the old secret, bearer still works); auth tests on SQLite.

### 57. Typed responses for every admin endpoint (documentation)
- **Where:** `server/admin/models.go` (response and request body types), `server/admin/routes.go` (every handler writes these types; annotations rewritten from one list), `server/admin/swagger_test.go`.
- **Change:** no handler writes an untyped map. Errors are `errorResponse`, confirmations `statusResponse`, the create and rotate responses `visitorUserResponse`, tokens `visitorTokenCreated` and `visitorTokenItem`, traffic `trafficResponse`. Each annotation lists the failure codes that handler can return.
- **Found while doing it:** the rotate route had no annotation of its own (its block sat on top of the delete route's), so the reference was missing it.
- **Verified:** a test fails if any response is a generic object or the rotate route is missing. Live, the field names of health, domains, domain, stats, traffic and tokens match the documented schemas exactly.
- **Known gap:** the create-token response has no `id`; the id comes from the list. Adding it means changing `AddVisitorToken` to return the id as well.

### 58. Admin accounts and session tokens replace the admin key (change)
- **Replaces:** the shared `admin_secret_key` that every write request sent in the Authorization header.
- **Why:** one key shared by everyone cannot say who changed what, and it is hard to rotate without breaking every client. Accounts give each person their own password, and sessions expire.
- **Accounts:** created, reset, disabled and enabled only from the console on the server machine: `lipstick-server admin create|reset-password|disable|enable|list`. The password is typed in a terminal without echo; the command refuses when standard input is not a terminal, so no password comes from a pipe or a script. No port is opened and no key is needed.
- **Login:** `POST /login` checks the password (argon2id) and returns a token. The token is 256 random bits; only its SHA-256 hash is stored. It lasts 12 hours and `POST /logout` ends it. Writes need `Authorization: Bearer <token>`.
- **Brute force:** five wrong passwords lock the account for 15 minutes (429 with Retry-After). The answer never says whether the user or the password was wrong.
- **Revocation:** disabling an account or resetting its password ends all its sessions at once.
- **Code:** `server/db/models.go` (`AdminUser`, `AdminSession`), `server/auth/admin_users.go` (store), `server/admin/sessions.go` (login, logout, bearer check, limiter), `server/admin/console.go` (commands), `server/main.go` (the `admin` subcommand, detected after the config flags).
- **Config:** `admin_secret_key` is no longer required or used. It is still accepted so existing files load, and the server warns about it at startup. The `-admin-secret` flag is removed.
- **Roles:** stored (`admin`, `operator`) but both have the same access to the write API for now. Finer permissions are pending.
- **Not done:** a reset of the whole configuration. It would delete data, so it waits for an explicit decision.
- **Verified:** store tests on SQLite (login, sessions, disable, password reset, expiry, hashes stored, input rules); API tests (token accepted, wrong password, lock after five failures, bearer challenge); console tests (two matching passwords, short password, missing username, unknown command, disable and enable, list without secrets); live: `admin list` reads the real database and `admin create` refuses without a terminal.

### 59. Web panel login with an HttpOnly session cookie (change)
- **Why:** the panel showed data to anyone who reached the admin port, and a token given to a web page is readable by any script on it.
- **Now:** every route except `/health`, `/login`, `/web/login` and the panel's own files needs a session. Without one, `/` shows a login form and nothing else. `POST /web/login` sets the session in the cookie `lipstick_session`: HttpOnly, Secure, SameSite=Strict, Path=/. The answer carries no token. `POST /logout` ends the session and clears the cookie.
- **CSRF:** a change made with the cookie must carry `X-Lipstick-CSRF: 1`. A page on another site cannot set a custom header without a preflight, and the server never answers one. Bearer requests are not affected: browsers do not send them on their own.
- **Strict page policy:** `default-src 'none'; script-src 'self'; style-src 'self'; frame-ancestors 'none'`. The panel's script and stylesheet moved to files in `server/admin/web/` (embedded in the binary), so no inline script or style is needed.
- **Lock:** five wrong passwords lock the account for 15 minutes, counted together for `/login` and `/web/login`. The lock applies at the fifth wrong password, as documented.
- **Verified:** tests for the cookie attributes, the token missing from the web login answer, the CSRF rule for cookie changes and for Bearer, the shared lock, logout clearing the cookie, public assets, and no inline script or style in the pages.
- **Left for later:** the cookie is not bound to the client's address or to a device; a session stolen from the machine itself is still a session.

### 60. Account and visitor-user management in the panel; superuser role (feature)
- **Roles:** `superuser` (manages admin accounts) and `operator` (manages domains and visitor users, changes its own password). The old `admin` name is migrated to `superuser` at startup. The first account is still created from the console.
- **Enforced:** `requireSuperuser` on the account-management routes; every other route accepts both roles. The role is carried by the session (`Authenticate` returns it).
- **Rules:** the last active superuser cannot be disabled or demoted; a superuser cannot disable itself; changing one's own password needs the current one and ends every session of the account.
- **API:** `GET /account`, `POST /account/password`, `GET|POST /admin-users`, `POST /admin-users/{username}/password|role|disable|enable`. Visitor-user routes are unchanged.
- **Panel:** three sections, *Mi cuenta*, *Cuentas de administrador* (hidden unless superuser), *Usuarios de visitante* (per domain). The scripts build the page with `textContent`, so no name from the server is ever read as HTML. A new secret is shown once.
- **Verified:** store tests (last superuser, own password with the current one, sessions ended); API tests on a real SQLite store (operator refused with 403 on every account route, duplicate 409, short password 400, self-disable 409, disable and reactivate, reset ends sessions, last superuser 409, CSRF on cookie changes); the panel's element ids match the scripts; the scripts pass `node --check`.
- **Not done:** no audit log of account changes yet; no per-domain permissions (an operator manages every domain).

### 61. Panel redesign: screens, Tailwind, and the gaps on the server (feature)
- **Structure:** a sidebar application with hash routes: Panel, Dominios (list and detail), Tráfico y peticiones, Usuarios visitantes, Mi cuenta, Cuentas de administrador (superusers only), Configuración. Each screen is one file in `server/admin/web/js/`.
- **Styling:** Tailwind CSS v4. The source is `server/admin/frontend/app.css`; `npm run build` writes `server/admin/web/css/app.css`, which the binary serves. Colors are CSS variables, so the dark theme switches on `data-theme` without duplicate classes. The compiled file is committed, so building the server needs no Node.
- **Why not HTMX:** the screens need the JSON API that already exists and client-side filters. HTMX would need HTML fragment endpoints that repeat the logic, plus a vendored dependency under the page policy. The panel uses plain JavaScript with no dependencies.
- **Security kept:** every node is built with `textContent`; a test fails if a script uses `innerHTML`, `outerHTML`, `insertAdjacentHTML`, `document.write` or `eval`. The page policy forbids inline scripts and styles. Writes send the CSRF header. Destructive actions ask for confirmation, and deleting a domain asks for its name. A one-time secret or key is shown once in a box.
- **Serving:** `GET /admin/js/*` and `/admin/css/*` serve only files that exist in the embedded tree; other names are 404. A test checks that every asset a page names exists.
- **Roles:** the panel hides the accounts screen for operators and says why; the server refuses their calls as well.
- **Gaps in the server, not invented in the panel:**
  1. Close all of my sessions at once (only the current one can be closed).
  2. Visitor users with their creation date and status (the list returns names only). The screen shows names and actions.
  3. Method and status filters on `/requests` (the panel filters up to 500 rows in the browser).
  4. A read-only server information endpoint: version, uptime, listen addresses.
  5. An audit log of account and domain changes.
- **Not verified in a browser here:** the screens were checked for syntax, element ids and the built stylesheet, not by clicking through them.

### 62. Cluster-wide state in the database; honest health (change)
- **Why:** `/stats` and the recent requests came from each server's memory, so two admin APIs in a cluster answered differently. `/health` always said `ok`, even when the database or NATS was down.
- **State:** `node_statuses` (node, domain, status, counters, ports, updated_at). Each server writes its rows every 5 s (`StartNodeStatus`) and removes them when it stops. `ReadClusterStatus` adds up the rows reported in the last 20 s, taking the best status when several servers serve the same domain.
- **Requests:** `RecentRequests` reads the request table for every query. The in-memory ring still feeds the persister but is no longer read by the API.
- **Health:** the database is pinged and, with a cluster, NATS connectivity is checked; each check has a 2 s deadline. 200 when all pass, 503 with `degraded` otherwise. The answer names the failed check and never its error text.
- **Cache:** domain cache lifetime reduced from 5 minutes to 5 seconds, so changes made on one server reach the others within that time.
- **Errors:** `/stats` and `/requests` answer 503 when the database does not respond, instead of showing an empty or stale state.
- **Verified:** tests on a shared SQLite file with two connections standing in for two servers and a third reader: identical totals from every connection; silent servers drop out; servers remove domains they stop serving; recent requests newest first from the database; health ok, degraded without leaking the error, and a hung check cut off after 2 s; 503 when the database is down.
- **Not verified:** two real servers on PostgreSQL and NATS. The tests use SQLite and do not prove the PostgreSQL upsert or latency under load.
- **Known limit:** a domain change disconnects the clients only of the server that received the change; clients on other servers reconnect on their own. A NATS broadcast of that event is the next step.
- **Known limit:** `bytesSinceStart` is the sum of each server's own counter, so it drops when one server restarts.

### 63. Light theme only, account menu, logo and language (change)
- **Theme:** the dark theme is removed. The panel has one light theme; the colors remain CSS variables, so a later theme needs only new values.
- **Logo:** `server/admin/web/img/logo.png`, resized to 256 px (38 KB), served publicly as an image. The sidebar logo links to the dashboard.
- **Account menu:** the name and role at the top right open a menu with the account, the language choice and sign out. It closes with Escape or a click outside, and it is keyboard operable.
- **Languages:** English (default) and Spanish. The code is written in English and passes every text through `t()`. The Spanish table is in `js/i18n.js`, keyed by the English text. A test fails when a string used by the panel has no Spanish text, and a deliberately added untranslated string was confirmed to fail it. The choice is kept in this browser.
- **Not translated:** error messages that the server sends are shown as the server wrote them; most are in English. Translating them would need a message code in each response.
- **Verified:** the admin package tests pass; the translation test; the logo served as `image/png` without a session.
- **Not verified in a browser here:** the menu and the language switch were not clicked through.

### 64. Daily traffic counted small requests (bug)
- **Symptom:** "Traffic today" showed 0 B while requests were being served; the request log had them.
- **Cause:** each hub counted bytes in memory and wrote them to the daily total only when they reached the 64 KB threshold. Small responses never got there, and anything still in memory was lost on restart.
- **Fix:** the hub also flushes its bytes every 5 seconds (`usageFlushInterval`) and on shutdown (`flushUsage`).
- **Verified:** unit test (bytes below the threshold leave memory on flush, the total since start is kept, a flush with nothing waiting writes nothing). Live: 20 requests of 261 bytes produced 5,220 bytes in `daily_consumptions` within 12 seconds, where before the fix the total stayed at 0.
- **Note:** the 5-second window means the daily total can lag by up to 5 seconds, and bytes waiting in memory are lost only if the process is killed hard.

## Recommended order

1. Items 1, 2, 3, and 4: authentication, unpredictable tickets, TLS verification, and admin API hardening.
2. Item 6: rotate and remove the committed private key.
3. Items 8, 9, and 10: listener resilience, visitor timeouts, and visitor responses.
4. Items 11 and 12: subscription error handling and data races.
5. Item 13: reverse-connection design.
6. The rest, in order of severity.

## Suggested tests before fixing

- Upgrade request with a wrong or missing `Authorization` header must be rejected.
- Two requests with guessed ticket values must not attach to a visitor connection.
- A malformed request to the manager port must not stop later clients from connecting.
- A visitor with no client must receive 502 within a bounded time.
- Concurrent client register, unregister, and visitor traffic must pass `go test -race`.
