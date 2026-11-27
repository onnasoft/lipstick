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

### 7. TLS fails open to plain text
- **Where:** `server/config/config.go` (`GetTLSConfig`), `helper/listener.go` (`NewListenerManagerTCP`), `server/main.go`
- **What:**
  - `GetTLSConfig` returns `nil` when the certificate cannot be loaded. It only logs the error, and the server starts without TLS.
  - `NewListenerManagerTCP` returns `nil` on error, and `main.go` then calls methods on that `nil` value.
- **Fix:** Fail startup when TLS is configured but cannot be loaded. Return and check errors from the listener constructor.

---

## High: stability and denial of service

### 8. One malformed request stops the manager from accepting clients
- **Where:** `server/manager/listener.go` (`CustomListener.handle`, `Accept`)
- **What:** Errors from parsing are sent through `cl.conn` and returned from `Accept`. `http.Server.Serve` returns on a non-temporary `Accept` error, so the server stops accepting connections until restarted.
- **Impact:** Sending garbage to the manager port takes the service down.
- **Observed:** Reproduced locally. Opening and closing empty TCP connections to port 5051 logged `Error running listener: invalid request`, and the port stopped answering until the server was restarted.
- **Fix:** Close the bad connection inside `handle` and never pass parse errors to `Accept`.

### 9. Visitor connections have no timeout or limit
- **Where:** `server/manager/hub.go` (`incomingClientConns`, `handleIncomingClientConn`)
- **What:** Pending visitor connections are stored until a client responds. There is no deadline, no cap, and no cleanup.
- **Impact:** Opening many connections to the proxy port exhausts memory and file descriptors.
- **Fix:** Add a read or response deadline, a per-hub maximum of pending connections, and cleanup on timeout.

### 10. Visitors can hang without a response
- **Where:** `server/manager/hub.go` (`handleIncomingClientConn`, NATS callback)
- **What:**
  - When no client is registered, the ticket is published to NATS and the function returns without answering the visitor.
  - If the hub has no subscription, the message is dropped and the visitor is never answered.
  - If `getProxyNotificationConn` returns `nil` in the NATS callback, the visitor is also left waiting.
- **Fix:** Answer with 502 in each of these paths, and remove the pending entry.

### 11. NATS subscription manager returns `(nil, nil)` on failure
- **Where:** `server/subscriptions/subscriptions.go` (`GetSubscriptionManager`)
- **What:** Inside the `sync.Once` closure, `err` is shadowed (`conf, err := ...`), so the outer `err` is always nil. If NATS is down, the function returns `(nil, nil)`. Callers then dereference the nil manager and panic. `sync.Once` also prevents any retry.
- **Fix:** Remove the shadowing, return the connection error, and retry on later calls instead of using `Once`.

### 12. Data races that can crash the process
- **Where:** `server/manager/hub.go`
- **What:**
  - `hub.ProxyNotificationConns` is read by `getProxyNotificationConn` in the NATS callback goroutine while the hub loop modifies it. This can cause `fatal error: concurrent map iteration and map write`, which cannot be recovered.
  - The hub loop and the NATS callback both write to the same `ProxyNotificationConn` (`bufio.Writer`) without a lock, so ticket lines can be interleaved.
- **Fix:** Route all map access and writes through the hub loop, or protect them with a mutex. Serialize writes per connection.

### 13. Reverse connection over TLS does not work as written
- **Where:** `server/manager/hub.go` (`handleIncomingClientConn`), `helper/net.go` (`GetPublicIP`), `client/manager/http.go` (`ConnectByAddress`)
- **What:**
  - `GetPublicIP` returns the first non-loopback IPv4 address of the server's interfaces, which is usually a private address, not the public one.
  - The message is `IP:ticket` with no port.
  - With `https`/`wss`, the client calls `tls.Dial("tcp", addr)` with no port and fails with "missing port".
  - With `http`/`ws`, the client ignores `addr` and connects to the host in the URL.
- **Fix:** Have the server send an explicit public address and port, or have the client always use the configured URL. Remove the dependence on `GetPublicIP`.
- **Status:** Fixed for the client side. `ConnectByAddress` now dials the host from the server URL for both plain and TLS connections (`client/manager/http.go`). The `addr` argument was removed. `GetPublicIP` is still used by the server and should be removed as part of a proper fix.
- **Verified:** Visitor requests over `https://localhost:5050` return the backend response through the tunnel (6 of 6 requests with one live client). Before the fix, the same setup timed out.

### 14. Client panics on bad input
- **Where:** `client/main.go` (`readMessage`, `establishConnection`)
- **What:**
  - `readMessage` calls `strings.Split(line, ":")` and reads `data[1]` without checking the length. An IPv6 address or a line without `:` causes an index-out-of-range panic.
  - `establishConnection` writes to `connection` when it is `nil`.
  - The `recover()` calls hide these panics, and the client silently drops the ticket loop.
- **Fix:** Parse with `strings.LastIndex` (or `net.SplitHostPort`) and check the result. Do not write to a nil connection.

### 15. Hubs and goroutines are never released
- **Where:** `server/manager/manager.go` (`hubs`), `server/manager/hub.go` (`listen`, `handleShutdown`)
- **What:** Hubs are stored in a `sync.Map` and never removed. Deleting a domain through the admin API does not stop its hub. `handleShutdown` is never triggered.
- **Fix:** Remove the hub when its last client disconnects, and stop its goroutine.

### 16. Dead clients are not detected
- **Where:** `server/manager/hub.go` (`checkConnection`), `client/main.go` (`checkConnection`)
- **What:** The server has no keepalive or read deadline. A client that disappears without closing its TCP connection stays registered until a write to it fails. Visitors are assigned to that dead socket in the meantime.
- **Fix:** Add read deadlines, and make the client send pings within that interval.
- **Observed:** Clients killed with `kill -9` are removed from the hub immediately (the server's read returns EOF). But during testing, one registration stayed in the hub after its client was gone. Visitors whose ticket was sent to that registration timed out, so requests intermittently failed (1 of 3 in one run). The cause is not confirmed yet; the hub does not log which registration is stale.

### 17. Traffic counters are lost on database errors
- **Where:** `server/traffic/manager.go` (`saveAllToDatabase`), `server/traffic/database.go` (`updateDatabase`)
- **What:**
  - Counters are reset to zero before the database write, so a failed write loses the data.
  - `updateDatabase` calls `log.Fatalf` when the config cannot be read, which stops the process.
  - `TrafficManager.Close` is never called.
  - `db.GetConnection` lazily initializes a global without synchronization.
- **Fix:** Reset counters only after a successful write. Log errors instead of exiting. Guard the connection initialization with `sync.Once`.

### 18. Ticket values are printed to stdout
- **Where:** `server/manager/hub.go` (`handleServerRequest`)
- **What:** For an invalid ticket, every pending ticket is printed with `fmt.Println`.
- **Fix:** Remove the debug print.

---

## Medium: functional bugs

### 19. Server command-line flags have no effect
- **Where:** `server/config/config.go` (`loadConfig`)
- **What:** `-admin-addr`, `-manager-addr`, `-proxy-addr`, `-admin-secret`, `-tls-cert`, `-tls-key`, the Redis and database flags, and the NATS flag are parsed but never copied into `appConfig`.
- **Fix:** Apply each non-empty flag value over the file and default values.

### 20. Example configuration does not match the config structs
- **Where:** `server/config.yml.example`, `server/config/config.go`
- **What:** The example uses `certs:`, `database.dbname`, and `serverUrl`. The structs expect `tls.certificate_path` and `tls.key_path`, and `database.database`. Unknown keys are ignored silently, so the database name falls back to `app_db`.
- **Fix:** Make the example match the structs, and reject unknown keys.

### 21. `allowMultipleConnections: false` may be stored as `true` **(verify)**
- **Where:** `server/db/models.go`, `server/auth/postgres.go` (`AddDomain`)
- **What:** The model has `default:true`. GORM omits zero values for fields with a default tag, so `false` is replaced by the database default.
- **Fix:** Use a pointer type, or set the field explicitly with `Select` or `FirstOrCreate` options.

### 22. Admin update handler panics and returns wrong status codes
- **Where:** `server/admin/routes.go` (`updateDomain`)
- **What:** `domain["apiKey"].(string)` and `domain["allowMultipleConnections"].(bool)` are unchecked type assertions, so a wrong JSON type panics. A missing domain returns 500 instead of 404.
- **Fix:** Use checked assertions, and return 400 and 404 where they apply.

### 23. Install script fails silently
- **Where:** `server/install.sh`
- **What:**
  - It copies `config.yml`, which is in `.gitignore` and not in the repo. Errors are hidden by `&>/dev/null`, so the service starts without `admin_secret_key` and exits through `log.Fatal`.
  - It builds to the fixed path `/tmp/lipstick-6415415`, which is vulnerable to symlink attacks when run as root.
  - It checks `$USER` instead of `id -u`.
- **Fix:** Fail loudly on copy errors, build to a `mktemp` path, and check `id -u`.

### 24. systemd unit has an invalid directive and runs as root
- **Where:** `server/lipstick.service`
- **What:** `ReadOnlyDirectories=` is not a valid directive (the correct one is `ReadOnlyPaths=`). The service runs as `User=root`. `PIDFile` points to a file that is never written.
- **Fix:** Create a dedicated service user, use `ReadOnlyPaths=`, and remove `PIDFile` or write the PID file.

### 25. Docker image runs as root
- **Where:** `Dockerfile`
- **What:** The image runs as root, does not declare `EXPOSE`, and copies binaries from `bin/`, which `build.sh` creates.
- **Fix:** Add a non-root user, `EXPOSE` the ports, and build inside a multi-stage Dockerfile.

### 26. README and code disagree on defaults
- **Where:** `README.md`
- **What:**
  - Ports: proxy `:8080` vs `:5050`, manager `:8081` vs `:5051`.
  - Client target: `127.0.0.1:8082` vs `tcp://127.0.0.1:12000`.
  - The documented `-k` default is `super_secret_key`, which is a weak default that users may copy.
- **Fix:** Make the README match the code, and remove the example secret.

### 27. Client configuration loads at package init
- **Where:** `client/config/config.go`, `client/main.go`
- **What:** `flag.Parse()` runs during package initialization, and `main.go` assigns `configuration, _ = config.GetConfig()` at package level, ignoring the error. This makes the package hard to test.
- **Fix:** Load configuration inside `main` and handle the error.

### 28. Header reader waits for `\r\n` only
- **Where:** `helper/http.go` (`ReadUntilHeadersEnd`)
- **What:** The loop checks `line == "\r\n"`. A request that ends headers with a bare `\n` is read until EOF, which can leave the goroutine blocked.
- **Fix:** Accept `\n` as well as `\r\n`, and set a read deadline.

### 29. `go vet` warnings
- **Where:** `client/main.go:127`, `client/main.go:135`, `server/manager/hub.go:188`
- **What:** `fmt.Fprintf` is called with a non-constant format string (`helper.BadGatewayResponse`). If that string ever contains `%`, the output is corrupted.
- **Fix:** Use `Write([]byte(...))` or `Fprint`.

### 30. Dead code
- **Where:** `server/manager/hub.go` (`handleIncomingClientConn2`), `helper/net.go` (`MonitorIdle`, `RemoteConn.used`), `helper/listener.go` (`ListenerConn`, `NewListenerConn`), `server/manager/ticket.go` (overflow check)
- **What:** Unused functions. `MonitorIdle` also writes `used` without synchronization. The overflow check in `TickerManager.generate` can never be true.
- **Fix:** Delete the unused code.

### 31. Client treats a rejected upgrade as connected
- **Where:** `client/main.go` (`startClient`), `client/manager/http.go` (`Connect`)
- **What:** `Connect` writes the upgrade request and returns without reading the response status. `startClient` then reads the headers, ignores the status, and logs `Connected to server`. With a wrong `APISecret`, the server answers 401 and the client still reports a successful connection, then waits for tickets that never arrive.
- **Observed:** Reproduced locally with a wrong key: the server logged `Unauthorized tunnel client for domain:localhost`, and the client printed `Connected to server at http://localhost:5051`.
- **Fix:** Parse the response status line and headers, and treat anything other than `101` or `200` as a failed connection. Retry with backoff and log the status code.

---

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
