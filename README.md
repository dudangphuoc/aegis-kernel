# 🛡️ AegisKernel

> Dynamic multi-tenant micro-kernel with a control plane and plug-and-play modules, written in Go.

**AegisKernel** combines **Clean Architecture + CQRS** with two layers of isolation: **process isolation** (modules run as separate processes via `hashicorp/go-plugin`) and **physical database isolation** (each tenant gets its own PostgreSQL databases, provisioned automatically by the Control Plane).

## ✨ Key Features

- **Dynamic tenant provisioning:** no static config. Registering a tenant creates one database `tenant_<tenant_id>` with a schema per module (`platform`, `biz_auth`, `biz_parties`, `biz_audit`), applies the add-only scripts (`db/`, `modules/<m>/db/`), binds it to the tenant's root store and seeds its first admin. Every write is captured by triggers into `platform.changes`; the kernel change feed turns it into audit records.
- **CQRS + feature overrides:** base logic lives in `internal/business`, per-tenant variants in `internal/features` and are toggled at runtime (e.g. standard login vs. MFA).
- **HTTP gateway (`go-chi/chi`):** standard `net/http`, with middleware for request ID, real IP, recovery, CORS, tenant detection and feature guard.
- **Migration engine:** per-tenant Up/Down (canary) or batch migrate, safe via `pg_advisory_lock` and transactions.
- **React admin dashboard (`web/`):** users, tenants, migrations, feature flags (hot reload) and an API playground.

## 🏛️ Architecture

```mermaid
flowchart TD
    UI["React Admin Dashboard :5173"]

    subgraph Kernel["Aegis Kernel :8080"]
        CPAPI["Control Plane API /api/cp/*"]
        Migrator["Migration Runner"]
        CPStore["Control Plane Store (cp_db)"]
        GW["API Gateway /api/v1/*<br/>Tenant Resolver + Feature Guard"]
        CPAPI --> CPStore
        CPAPI --> Migrator
        GW --> CPStore
    end

    UI -- "Bearer Token" --> CPAPI
    UI -- "X-Tenant-ID" --> GW

    GW -- "go-plugin RPC" --> ModAuth["Module: auth"]
    GW -- "go-plugin RPC" --> ModParties["Module: parties"]
    GW -- "go-plugin RPC" --> ModAudit["Module: audit"]

    CPStore --> DB_CP[("cp_db")]
    ModAuth --> DB_Auth[("auth_{tenant_id}")]
    ModParties --> DB_Party[("parties_{tenant_id}")]
    ModAudit --> DB_Audit[("audit_{tenant_id}")]
```

## 📂 Structure

```text
core/        contract, controlplane, feature, gateway, loader, transport
launcher/    kernel entry point (main.go)
sdk/         cqrs, tenantdb (DB of the tenant, schema per module), multitenant, app.go (standalone debug)
modules/     auth, parties, audit
web/         React 19 + TypeScript + Vite dashboard
cookbooks/   design docs
scripts/     PostgreSQL init
```

## ⚡ Quickstart

```bash
make docker-up          # PostgreSQL :5432 (cp_db) + PgBouncer nodes :6432, :6433
PGBOUNCER_NODES=localhost:6432,localhost:6433 AEGIS_JWT_SECRET=<32+ chars> make run   # kernel on :8080
cd web && npm install && make run-ui   # dashboard on :5173
```

Default login: `admin` / `admin123`.

Optional kernel env: `TRUSTED_PROXIES` (CIDR/IP list, e.g. `10.0.0.0/8`) — only these peers may set `X-Forwarded-For`/`X-Real-IP`; by default the audited client IP is the TCP peer.

## 🔄 Adding a Migration

1. Add `NNNN_name.up.sql` and `NNNN_name.down.sql` to `modules/<module>/migrations/`.
2. Embed and register them in that folder's `scripts.go`.
3. Rebuild: `make build-modules && make build-kernel`.
4. New tenants migrate automatically. For existing tenants, use the **Migrations** tab (Up per tenant, or batch).

## 🐞 Standalone Debugging

`sdk/app.go` supports standalone HTTP mode. Use the `.vscode/launch.json` configs: **Module Auth** (`:8081`), **Module Parties** (`:8082`) and **Module Audit** (`:8083`).

## 📊 Benchmark

~2,002 RPS over 239,910 requests · avg 43 ms (P99 125 ms) · 0% errors · peak CPU 68.9%.

## 🧪 Testing

```bash
make test
```
