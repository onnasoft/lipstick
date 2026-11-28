# Lipstick — Technical Review & Productization Roadmap

## 1. Executive Summary

Lipstick has a solid technical foundation and already implements several advanced networking capabilities. It is beyond the stage of a simple prototype, but it still requires improvements in security, reliability, installation experience, administration, and observability before it can be considered a mature product.

The primary objective should not be adding more networking features. The focus should be turning the existing implementation into a secure, reliable, easy-to-deploy, and maintainable product.

### Current assessment

| Area | Assessment | Priority |
|---|---|---|
| Core networking | Strong foundation | Maintain |
| HTTP/HTTPS proxying | Implemented | Validate edge cases |
| TCP and intranet ports | Implemented | Reliability testing |
| WebSockets | Supported | Stress testing |
| TLS | Implemented | Security review |
| Authentication | Implemented for tunnel clients | Expand access controls |
| Multiplexing | Implemented | Reliability testing |
| Client reconnection | Implemented | Improve recovery |
| Database persistence | Implemented | Operational improvements |
| Admin API | Implemented | Security review |
| Admin dashboard | Basic monitoring | Improve UX and management |
| Installation | Requires technical setup | Simplify |
| Observability | Basic | Expand |
| High availability | Limited | Future milestone |
| Product documentation | Available | Improve onboarding |

---

# 2. Current Capabilities

Lipstick currently provides the following capabilities:

- Reverse tunneling architecture.
- HTTP and HTTPS traffic forwarding.
- TCP and intranet port forwarding.
- WebSocket support.
- TLS-secured tunnel communication.
- Client authentication.
- Connection multiplexing.
- Client reconnection.
- Domain management.
- PostgreSQL persistence.
- Traffic tracking.
- Administrative API.
- Admin dashboard.
- YAML configuration.
- Docker support.
- Cluster-related infrastructure using NATS.
- Benchmarking and functional testing.

### Tested scenarios

The project includes testing or documented validation for scenarios such as:

- HTTP traffic.
- WebSockets.
- TCP connections.
- SSH.
- Redis.
- Jupyter.
- Grafana.
- Emulated devices.
- Performance benchmarking.

These capabilities provide a good foundation for productization.

---

# 3. Identified Gaps

## 3.1 Security

**Priority: P0 — Critical**

The current tunnel authentication protects the connection between the client and the server. However, the public endpoint exposed by the tunnel may still be accessible to anyone who knows its domain.

### Required improvements

- [x] Visitor authentication. (implemented: see README, Autenticación de visitantes)
- [x] Visitor authentication with per-user HMAC signatures (implemented: see README, Autenticación de visitantes)
- [x] Token-based access control. (implemented: see README, Autenticación de visitantes)
- [x] IP allowlisting. (implemented: README, Reglas de acceso por red)
- [x] CIDR-based access rules. (implemented: README, Reglas de acceso por red)
- [x] Rate limiting by IP. (implemented: README, Límites de uso; revision.md, item 66)
- [x] Rate limiting by domain. (implemented: README, Límites de uso; revision.md, item 66)
- [x] Connection limits. (implemented per IP and per domain; per-IP concurrency verified live: 20 of 40 simultaneous connections held, the rest refused with 429)
- [x] Protection against brute-force attempts. (implemented: see README, Autenticación de visitantes)
- [ ] Configurable bandwidth limits.
- [ ] Configurable traffic quotas.
- [ ] Client credential revocation.
- [ ] Client credential rotation.
- [ ] Domain-level access policies.
- [ ] Secure handling of secrets.
- [ ] Security audit of administrative endpoints.
- [ ] Protection against abuse of publicly exposed services.

### Acceptance criteria

- A domain can be protected independently of tunnel authentication.
- Unauthorized visitors cannot access protected services.
- Administrators can configure IP restrictions.
- Administrators can configure connection and request limits.
- Client credentials can be revoked without recreating the entire server.
- Security policies do not interfere with legitimate HTTP, WebSocket, or TCP traffic.

---

## 3.2 Reliability

**Priority: P1 — High**

The system must remain stable under network failures, client disconnections, backend failures, and prolonged operation.

### Required improvements

- [ ] Review open concurrency issues.
- [ ] Validate domain/client association consistency.
- [ ] Improve handling of HTTP edge cases.
- [ ] Validate chunked transfer encoding.
- [ ] Validate `Expect: 100-continue`.
- [ ] Validate HTTP keep-alive.
- [ ] Validate long-running HTTP streams.
- [ ] Validate Server-Sent Events.
- [ ] Validate large uploads and downloads.
- [ ] Validate slow clients.
- [ ] Validate backend timeouts.
- [ ] Validate abrupt client disconnections.
- [ ] Validate server restarts.
- [ ] Validate database restarts.
- [ ] Validate TLS errors.
- [ ] Validate reconnect behavior.
- [ ] Validate concurrent access and race conditions.

### Acceptance criteria

- Connections are handled correctly when clients disconnect unexpectedly.
- The server does not leak goroutines or resources.
- Long-running streams behave correctly.
- Backend failures produce predictable responses.
- Reconnection does not create inconsistent state.
- The system can operate continuously without manual intervention.

---

## 3.3 Installation and Deployment Experience

**Priority: P2 — High**

Installation should require minimal technical knowledge and should not force users to manually configure every dependency.

### Required improvements

- [ ] Official installation script.
- [ ] Checksums for downloaded binaries.
- [ ] Release version verification.
- [ ] Linux AMD64 binaries.
- [ ] Linux ARM64 binaries.
- [ ] Windows support.
- [ ] macOS support.
- [ ] Docker images.
- [ ] Official Docker Compose deployment.
- [ ] Guided configuration.
- [ ] Systemd service generation.
- [ ] Automatic startup configuration.
- [ ] Client installation simplification.
- [ ] Server installation simplification.
- [ ] Upgrade process.
- [ ] Configuration validation command.
- [ ] Database migration management.
- [ ] Default configuration templates.

### Proposed CLI experience

```bash
lipstick install
lipstick setup
lipstick start
lipstick status
lipstick logs
lipstick upgrade
```

### Acceptance criteria

A new user should be able to install Lipstick, configure the server, connect a client, and publish a local service without manually assembling all dependencies.

---

## 3.4 Administration Dashboard

**Priority: P3 — Medium/High**

The current dashboard provides monitoring functionality but should evolve into a complete administration interface.

### Required improvements

- [ ] Domain creation.
- [ ] Domain deletion.
- [ ] Domain configuration.
- [ ] Domain activation and deactivation.
- [ ] Client credential generation.
- [ ] Client credential rotation.
- [ ] Client disconnection.
- [ ] Client status.
- [ ] Last connection timestamp.
- [ ] Client IP information.
- [ ] Connection duration.
- [ ] Traffic consumption.
- [ ] Access policies.
- [ ] IP allowlists.
- [ ] Connection limits.
- [ ] Bandwidth limits.
- [ ] Request history.
- [ ] Error history.
- [ ] Server health.
- [ ] Database health.
- [ ] Component status.

### UX improvements

- Modern visual design.
- Responsive layout.
- Dark theme.
- Clear status indicators.
- Improved metrics cards.
- Better request table.
- Domain filtering.
- Port filtering.
- Loading states.
- Error states.
- Empty states.
- Smooth updates.
- No unnecessary UI flickering.
- Copyable configuration.
- Copyable credentials, when appropriate.
- Confirmation dialogs for destructive actions.

### Acceptance criteria

The dashboard should become the primary operational interface for Lipstick instead of being limited to monitoring.

---

## 3.5 Observability

**Priority: P4 — Medium**

The existing logs and traffic statistics should be expanded to provide sufficient visibility for production deployments.

### Required improvements

- [ ] Prometheus metrics endpoint.
- [ ] Active connections metric.
- [ ] Connected clients metric.
- [ ] Active sessions metric.
- [ ] Pending visitors metric.
- [ ] Traffic throughput.
- [ ] Traffic consumption by domain.
- [ ] Connection latency.
- [ ] Tunnel latency.
- [ ] HTTP response status metrics.
- [ ] Connection errors.
- [ ] Reconnection metrics.
- [ ] Timeout metrics.
- [ ] CPU and memory information.
- [ ] Structured logging.
- [ ] Health checks.
- [ ] Readiness checks.
- [ ] Alerting integration.

### Optional integrations

- Grafana.
- Prometheus.
- OpenTelemetry.
- External alerting systems.

### Acceptance criteria

Operators should be able to understand the health of Lipstick, identify failures, and diagnose problems without manually inspecting the source code.

---

## 3.6 High Availability and Scalability

**Priority: P5 — Future**

The current architecture has limitations regarding server availability and distribution of active clients.

### Future improvements

- [ ] Multi-instance routing.
- [ ] Session coordination.
- [ ] Active client discovery.
- [ ] Distributed domain routing.
- [ ] Failover.
- [ ] Load balancing.
- [ ] Distributed traffic accounting.
- [ ] Shared state coordination.
- [ ] Server health-based routing.
- [ ] Graceful shutdown.
- [ ] Recovery after node failure.

### Acceptance criteria

Multiple Lipstick server instances should be able to operate together while maintaining consistent routing and predictable failure behavior.

This milestone should not be a prerequisite for the initial stable release.

---

## 3.7 Commercial Readiness

The administration API and panel are functional and tested, but they are not yet fit for a paid production product. Gaps in priority order:

1. [x] **Audit log.** (Implemented: `server/audit`, `GET /audit`, panel screen; see revision.md, item 65.) Every change (domains, keys, accounts, visitor users and secrets, access rules) records who did it, when, and from where. Queryable by superusers.
2. [ ] **Encryption at rest.** Client keys (`api_key`) and visitor secrets are stored in clear text today. Encrypt them with a key held outside the database.
3. [ ] **Two-factor authentication for superusers.** A compromised superuser controls the whole tunnel; a password alone is not enough for a commercial product.
4. [ ] **Versioned migrations.** Replace startup `AutoMigrate` (which can drop columns) with explicit, reviewed migrations and a backup step before them.
5. [ ] **Tests on real PostgreSQL and NATS.** The cluster state, the request history and the health checks are proven on SQLite and an in-memory bus. Add PostgreSQL and NATS to the test suite, including failure cases.
6. [ ] **Cluster-wide change propagation.** A domain change reaches the clients of the server that received it only. Publish the change on NATS so every server disconnects its own clients.
7. [ ] **External security review and dependency scan.** Penetration test by a third party, and `govulncheck` in the release pipeline.
8. [ ] **Operator documentation.** TLS with a real certificate, backups and restore, what to do when the database or NATS fails, key rotation, and upgrade steps.

Less urgent: idle session timeout and a "sign out everywhere" action; permissions per domain instead of two general roles; metrics for a monitoring system; the configuration reset, still awaiting a decision on what it deletes.

### Acceptance criteria
- Every item above is checked off, with a test or a documented procedure that proves it.
- A security review by someone outside the project has no open high-severity findings.
- A restore from backup has been performed on a copy of the production database.

# 4. Productization Roadmap

## Milestone 1 — Security Hardening

**Goal:** Make Lipstick safe for controlled public exposure.

### Tasks

- [ ] Audit tunnel authentication.
- [ ] Audit administrative API access.
- [ ] Implement visitor authentication.
- [ ] Implement configurable access policies.
- [ ] Implement IP allowlists.
- [ ] Implement CIDR restrictions.
- [x] Implement rate limiting.
- [x] Implement connection limits.
- [ ] Implement bandwidth limits.
- [ ] Implement credential revocation.
- [ ] Implement credential rotation.
- [ ] Review TLS configuration.
- [ ] Review secret storage.
- [ ] Review public exposure risks.
- [ ] Document security limitations.

**Deliverable:** Lipstick 0.1.0 Beta — Security Baseline.

---

## Milestone 2 — Reliability

**Goal:** Ensure predictable behavior during failures and prolonged operation.

### Tasks

- [ ] Resolve open concurrency issues.
- [ ] Review client/domain association.
- [ ] Validate HTTP protocol edge cases.
- [ ] Validate WebSocket stability.
- [ ] Validate TCP forwarding.
- [ ] Validate SSE.
- [ ] Validate long-running streams.
- [ ] Validate large transfers.
- [ ] Test abrupt disconnects.
- [ ] Test backend failures.
- [ ] Test server restarts.
- [ ] Test database restarts.
- [ ] Test client reconnection.
- [ ] Run stress tests.
- [ ] Run race detector.
- [ ] Monitor resource leaks.
- [ ] Validate prolonged operation.

**Deliverable:** Reliability baseline and automated regression suite.

---

## Milestone 3 — Installation and Onboarding

**Goal:** Make installation and first use straightforward.

### Tasks

- [ ] Build release binaries.
- [ ] Support AMD64.
- [ ] Support ARM64.
- [ ] Create installation script.
- [ ] Create Docker images.
- [ ] Create official Docker Compose deployment.
- [ ] Create guided setup.
- [ ] Create configuration validation.
- [ ] Create systemd integration.
- [ ] Simplify client setup.
- [ ] Simplify server setup.
- [ ] Implement upgrade workflow.
- [ ] Document troubleshooting.
- [ ] Create quick-start guide.

**Deliverable:** Lipstick can be installed and configured without manually assembling its deployment environment.

---

## Milestone 4 — Administration Dashboard

**Goal:** Provide a complete operational interface.

### Tasks

- [ ] Dashboard redesign.
- [ ] Domain management.
- [ ] Client management.
- [ ] Credential management.
- [ ] Access policy management.
- [ ] Traffic management.
- [ ] Connection management.
- [ ] Server health.
- [ ] Database health.
- [ ] Request history.
- [ ] Error history.
- [ ] Responsive design.
- [ ] Improved UX states.

**Deliverable:** Fully operational administrative dashboard.

---

## Milestone 5 — Observability

**Goal:** Provide sufficient visibility for production operation.

### Tasks

- [ ] Prometheus endpoint.
- [ ] Structured logging.
- [ ] Health endpoints.
- [ ] Readiness endpoints.
- [ ] Connection metrics.
- [ ] Traffic metrics.
- [ ] Error metrics.
- [ ] Latency metrics.
- [ ] Resource metrics.
- [ ] Alerting documentation.
- [ ] Grafana dashboard examples.

**Deliverable:** Operational observability baseline.

---

## Milestone 6 — Stable Release

**Goal:** Release Lipstick 1.0.0.

### Tasks

- [ ] Complete security baseline.
- [ ] Complete reliability baseline.
- [ ] Complete installation workflow.
- [ ] Complete administration dashboard.
- [ ] Complete observability baseline.
- [ ] Review documentation.
- [ ] Publish changelog.
- [ ] Define versioning policy.
- [ ] Define support policy.
- [ ] Define upgrade policy.
- [ ] Define compatibility matrix.
- [ ] Publish release binaries.
- [ ] Publish Docker images.
- [ ] Publish deployment examples.
- [ ] Perform final regression testing.

**Deliverable:** Lipstick 1.0.0 stable release.

---

# 5. What Should Not Be Prioritized Yet

The following features should not be prioritized before the productization milestones:

- Kubernetes integration.
- A complete distributed cluster architecture.
- Mobile applications.
- Multi-tenant SaaS infrastructure.
- Global deployment infrastructure.
- Rewriting the project in another language.
- Adding protocols without a concrete use case.
- Excessive configuration options.
- Complex deployment automation for every platform.
- Building a large ecosystem of integrations.

The focus should remain on the existing core capabilities.

---

# 6. Definition of Product Readiness

Lipstick should be considered ready for its initial stable release when a user can:

1. Install Lipstick using documented instructions.
2. Configure a server.
3. Create a domain.
4. Configure and install a client.
5. Publish a local service.
6. Protect the exposed service.
7. Monitor connections and traffic.
8. Diagnose common errors.
9. Upgrade the installation.
10. Recover from normal network failures without manual intervention.

Additional requirements:

- Security limitations are documented.
- Critical security issues are resolved.
- Automated regression tests exist.
- Prolonged operation has been validated.
- Releases are reproducible.
- Installation is documented.
- Configuration changes are backward compatible where possible.
- The project has a clear versioning policy.

---

# 7. Recommended Priorities

| Priority | Workstream | Objective |
|---|---|---|
| P0 | Security | Protect public services and prevent abuse |
| P1 | Reliability | Make networking behavior predictable |
| P2 | Installation | Reduce deployment friction |
| P3 | Dashboard | Improve administration and UX |
| P4 | Observability | Improve diagnosis and operations |
| P5 | Scalability | Add distributed architecture when justified |

---

# 8. Final Recommendation

Lipstick already has a substantial technical foundation. The next phase should focus on productization rather than expanding its feature set.

The recommended sequence is:

**Security → Reliability → Installation → Administration → Observability → Stable Release**

The objective is to transform Lipstick from a technically capable networking project into a product that can be installed, operated, maintained, and trusted by users without requiring direct intervention from its developer.

The first target should be a controlled beta release with a clear security baseline, reliable operation, and a simple installation experience. Once those foundations are validated, Lipstick can progressively move toward a stable 1.0.0 release.