# Kế Hoạch [03]: Kiến Trúc PgBouncer × Node, Tái Thiết Lập Lifecycle, Giao Tiếp Liên Module, JWT Auth & Phân Hệ Audit 3 Tầng (Bất Đồng Bộ)

---

## 1. Mục Tiêu Cốt Lõi (Goal Description)

Giải quyết toàn diện các bài toán kiến trúc Enterprise Multi-Tenant ở quy mô lớn: **Database Connection Pooling (`PgBouncer × Node`)**, **Database & Process Lifecycle**, **Inter-Module Communication**, **Chuẩn hóa JWT Authentication & Tách biệt System Telemetry API**, cùng **Module Audit Độc Lập 3 Tầng (Asynchronous Interceptor + Entity Snapshot + Tamper-Evident Hash)**:

1. **Chấm dứt kết nối trực tiếp tới PostgreSQL — Triển khai tầng `PgBouncer × Node`:**
   * Các Micro-Modules **tuyệt đối không kết nối trực tiếp** vào PostgreSQL (`:5432`).
   * Xây dựng tầng **PgBouncer × Node** (`:6432`, `:6433`) với **Wildcard Dynamic Databases (`* = host=postgres port=5432`)** và **`pool_mode = transaction`**.
   * Phân tách rõ kết nối:
     * **Data Plane (Modules `auth`, `parties`, `audit`):** Đi qua cụm `PgBouncer × Node` (`:6432/:6433`, kèm `binary_parameters=yes`).
     * **Control Plane DDL / Migrator:** Kết nối cổng quản trị (`:5432`) để thực thi `CREATE/DROP DATABASE` và `pg_advisory_lock` (Session-level).

2. **Tái thiết lập Database & Module Lifecycle (`sdk/multitenant`, `sdk/app.go`, `core/contract`):**
   * **Loại bỏ `MigrateUp` khỏi Request Path:** Xóa bỏ hoàn toàn việc chạy migration tự động bên trong `ConnectionFactory` của các Repository.
   * **Smart Multi-Tenant Connection Manager (Idle TTL Eviction + LRU + Safe Rebind):**
     * Tự động `db.Close()` và giải phóng khỏi RAM nếu Tenant không phát sinh request sau `IdleTTL` (`15 phút`).
     * Khi Tenant cập nhật `ConnectionString` mới, tự động đóng (`Close()`) pool cũ trước khi mở pool mới.
   * **Graceful Shutdown 2 Pha:** Mở rộng `contract.MicroModule` thêm hàm `Shutdown(ctx context.Context) error` để đóng sạch toàn bộ DB Pools và flush hàng đợi sự kiện trước khi tiến trình con kết thúc.

3. **Chuẩn hóa Đăng Nhập bằng JWT & Tách biệt Thông Tin Hệ Thống (System Telemetry API):**
   * **JWT Authentication (`HS256` / `RS256`):**
     * Thay thế hoàn toàn chuỗi giả lập (`std-token-...`, `mfa-high-token-...`) bằng chuẩn **JSON Web Token (JWT)** có chữ ký số.
     * JWT Claims chứa đầy đủ ngữ cảnh định danh: `sub` (User ID), `usr` (Username), `tid` (Tenant ID), `rol` (Role), `acr` / `amr` (Security Level: `STANDARD` hoặc `MFA_HIGH_ASSURANCE`), `jti` (Token ID), `iat`, `exp`.
     * API Gateway (`core/gateway`) tích hợp **JWT Verifier Middleware** tự động xác thực chữ ký, kiểm tra hạn dùng (`exp`), đối chiếu `tid` với header `X-Tenant-ID`, và truyền thông tin Actor (`X-Actor-ID`, `X-Actor-Username`, `X-Actor-Role`) xuống các Module.
   * **Loại bỏ thông tin hạ tầng khỏi Business Response & Tách thành API Hệ thống riêng biệt:**
     * Xóa bỏ các trường lộ thông tin hệ thống (`database_source` chứa connection string, `auth_flow_used`, `processed_by`) ra khỏi DTO phản hồi của các API nghiệp vụ (`POST /api/v1/auth/login`, `POST /api/v1/parties`, ...).
     * Tách thành endpoint chẩn đoán riêng biệt có phân quyền: `GET /api/v1/system/runtime-info` (hoặc `/api/cp/tenants/{tenant_id}/diagnostics`) chỉ trả về thông tin luồng thực thi, trạng thái pool PgBouncer, các feature flags đang kích hoạt và DB target đã che mật khẩu (masked DSN).

4. **Tách Module `[audit]` Độc Lập — SDK Audit Interceptor (Bất đồng bộ) & Thiết Kế Dữ Liệu 3 Layer + Snapshot + Hash Toàn Vẹn:**
   * **Xóa bỏ Audit cục bộ trong module `auth`:** Tách tính năng ghi log kiểm toán ra thành một Micro-Module chuyên trách độc lập: **`modules/audit`** (sở hữu database vật lý riêng theo từng Tenant: `audit_<tenant_id>`).
   * **SDK Audit Interceptor (`sdk/audit/interceptor.go`):**
     * Đặt Interceptor tự động tại các Module nghiệp vụ (`auth`, `parties`, ...) ở cả 2 điểm chạm: **Command/Route Interceptor** (thu thập thông tin Client IP, User-Agent, JWT Actor, Request Metadata) và **Repository Unit-of-Work / Snapshot Hook** (chụp `BeforeSnapshot` và `AfterSnapshot` của các Entity bị thay đổi).
     * Sau khi nghiệp vụ chính commit thành công, Interceptor **đẩy dữ liệu bất đồng bộ (Async Push)** qua Outbox / Kernel Event Bus về **`[audit module]`** mà không làm chậm (zero latency impact) luồng phản hồi của người dùng.
   * **Thiết kế lưu trữ Audit 3 Layer có Snapshot & Hash toàn vẹn (Tamper-Evident):**
     1. **Layer 1 — `audits` (Request & Actor Context):** Ghi nhận ai (`user_id`, `username`, `role`), từ đâu (`client_ip`, `user_agent`, `client_info`), gọi vào đâu (`module`, `http_method`, `path`, `command_name`, `request_id`) làm thay đổi dữ liệu.
     2. **Layer 2 — `entity_changes` (Affected Table & Entity Snapshot):** Ghi nhận cụm bảng (`table_name`) và bản ghi (`entity_id`, `entity_name`) nào bị tác động (`INSERT`, `UPDATE`, `DELETE`), lưu kèm **`before_snapshot` (JSONB)** và **`after_snapshot` (JSONB)**.
     3. **Layer 3 — `entity_audit_logs` (Field-level Diff & Cryptographic Hash Chain):** Ghi nhận chi tiết từng trường thay đổi (`field_name`, `old_value`, `new_value`) kèm **`prev_hash`** và **`integrity_hash` (SHA-256)** móc nối dạng chuỗi khối (Hash Chain) để chống chỉnh sửa trái phép trong DB.

5. **Giao tiếp Liên Module qua Kernel Bus & Chống Gọi Chồng Chéo (Cycle Guard):**
   * Mọi giao tiếp giữa các Module đều đi qua Kernel: **Bất đồng bộ qua Domain Events** (ưu tiên số 1, dùng cho Audit & đồng bộ trạng thái liên module) hoặc **Đồng bộ có kiểm soát chuỗi gọi (`X-Aegis-Call-Chain`)** để chặn đứng vòng lặp `A -> B -> A`.

---

## 2. SUMMARY: Kiến Trúc Tổng Thể & Checklist

### 2.1. Sơ đồ Kiến trúc Tổng thể: JWT Gateway, Audit Interceptor (Async), PgBouncer × Node & 3-Layer Audit DB

```mermaid
flowchart TD
    subgraph Client["1. CLIENT & ADMIN UI"]
        UI["React Dashboard / Client App<br/>(Authorization: Bearer <JWT> + X-Tenant-ID)"]
    end

    subgraph Kernel["2. AEGIS KERNEL & CONTROL PLANE (:8080)"]
        GW["API Gateway<br/>(JWT Verifier + Tenant Resolver + Call-Chain Guard)"]
        SysAPI["System Runtime API<br/>(GET /api/v1/system/runtime-info)"]
        Bus["Kernel Async Event Bus & Mediator<br/>(Non-blocking Dispatch to [audit] & subscribers)"]
        CP["Control Plane & Migrator (:5432 Admin)"]
    end

    UI ==> GW
    GW --> SysAPI
    GW --> Bus

    subgraph Modules["3. PLUG-AND-PLAY MICRO-MODULES (IPC RPC)"]
        subgraph ModAuth["Module: auth"]
            AuthHandler["JWT Login (Standard / MFA)"]
            AuthInterceptor["Audit Interceptor + Snapshot Hook"]
        end
        subgraph ModParties["Module: parties"]
            PartyHandler["Party Commands + Snapshottable Entity"]
            PartyInterceptor["Audit Interceptor + Snapshot Hook"]
        end
        subgraph ModAudit["Module: audit (NEW)"]
            AuditConsumer["Async Audit Event Consumer"]
            AuditHasher["Diff Calculator & SHA-256 Hash Chain Engine"]
            AuditConsumer --> AuditHasher
        end
    end

    GW <== "Execute / Shutdown RPC" ==> ModAuth & ModParties & ModAudit
    AuthInterceptor & PartyInterceptor -. "Async Push AuditPayload<br/>(Client Info + Entity Snapshots)" .-> Bus
    Bus -. "Deliver Async Batch/Event" .-> AuditConsumer

    subgraph Pooler["4. PGBOUNCER × NODE LAYER (Transaction Pooling :6432 / :6433)"]
        PGB1["PgBouncer Node 1 (:6432)"]
        PGB2["PgBouncer Node 2 (:6433)"]
    end

    ModAuth & ModParties & ModAudit == "Smart ConnectionManager<br/>(TTL Eviction + binary_parameters=yes)" ==> PGB1 & PGB2

    subgraph PG["5. POSTGRESQL CLUSTER (:5432 - Physical Isolation)"]
        DB_CP[("cp_db")]
        DB_AuthT[("auth_{tenant_id}")]
        DB_PartyT[("parties_{tenant_id}")]
        DB_AuditT[("audit_{tenant_id}<br/>1. audits<br/>2. entity_changes<br/>3. entity_audit_logs")]
    end

    PGB1 & PGB2 ==> DB_AuthT & DB_PartyT & DB_AuditT
    CP == "Session/DDL :5432" ==> DB_CP & DB_AuthT & DB_PartyT & DB_AuditT
```

---

### 2.2. Checklist Triển Khai

- [x] **Bước 1: Hạ tầng `PgBouncer × Node` (`docker-compose.yml` & `scripts/pgbouncer/`)**
  - [x] Cấu hình cụm `pgbouncer-node-1` (`:6432`) và `pgbouncer-node-2` (`:6433`) với `pool_mode = transaction` và wildcard `* = host=postgres port=5432`.
  - [x] Cập nhật `core/controlplane/store.go`: Tách biệt `BuildAdminConnectionString` (`:5432` cho DDL & Migration Advisory Lock) và `BuildRuntimeConnectionString` (`:6432/:6433` kèm `binary_parameters=yes` cho các Module `auth`, `parties`, `audit`).
- [x] **Bước 2: Tái thiết lập Database & Module Lifecycle (`sdk/multitenant`, `sdk/app.go`, `core/contract`)**
  - [x] Nâng cấp `ConnectionManager` hỗ trợ **Idle TTL Eviction** (`15m`), **LRU Eviction**, **Safe Rebind** (đóng pool cũ khi đổi `ConnectionString`), và **`CloseAll()`**.
  - [x] Loại bỏ lệnh gọi `runner.MigrateUp(...)` khỏi `NewPostgresAuthRepo` và `NewPostgresPartyRepo`.
  - [x] Bổ sung `Shutdown(ctx context.Context) error` vào `contract.MicroModule`, `core/transport/rpc.go` và `core/loader/manager.go`.
- [x] **Bước 3: Nâng cấp Login sang chuẩn JWT & Tách API Thông Tin Hệ Thống (`sdk/jwt`, `modules/auth`, `core/gateway`)**
  - [x] Tạo package `sdk/security/jwt.go`: Ký (`SignToken`) và xác thực (`VerifyToken`) chuẩn JWT (`HS256`) chứa các claims chuẩn (`sub`, `usr`, `tid`, `rol`, `sec_level`, `iat`, `exp`, `jti`).
  - [x] Cập nhật `LoginHandler` và `MFALoginOverrideHandler` trong `modules/auth`: Phát hành chuỗi JWT chuẩn; loại bỏ `DatabaseSource` và `AuthFlowUsed` ra khỏi struct `AuthSession` trả về cho client.
  - [x] Loại bỏ `DatabaseSource` và `ProcessedBy` khỏi JSON response của `domain.Party` trong `modules/parties`.
  - [x] Tạo API hệ thống riêng biệt `GET /api/v1/system/runtime-info` trên Gateway để trả về thông tin chẩn đoán (Tenant ID, Active Features, Module Route Matrix, Masked Pool Target, Execution Mode) phục vụ Admin/Developer.
  - [x] Cập nhật Gateway Middleware: Tự động giải mã `Authorization: Bearer <jwt>`, kiểm tra khớp `claims.TenantID == X-Tenant-ID`, và đính kèm thông tin Actor (`X-Actor-ID`, `X-Actor-Username`, `X-Actor-Role`, `X-Client-IP`, `X-User-Agent`) vào `InvocationRequest`.
- [x] **Bước 4: Xây dựng SDK Entity Snapshot & Audit Interceptor Bất Đồng Bộ (`sdk/audit/`)**
  - [x] Định nghĩa interface `SnapshottableEntity` (`EntityName() string`, `TableName() string`, `EntityID() string`, `Snapshot() map[string]any`).
  - [x] Xây dựng `AuditTracker` gắn vào `context.Context` của mỗi Request để các Repository ghi nhận các thay đổi thực thể (`RecordInsert`, `RecordUpdate(before, after)`, `RecordDelete(before)`).
  - [x] Xây dựng **Audit Interceptor Middleware** trong `sdk/app.go`: Tự động bọc các route thay đổi dữ liệu (`POST`, `PUT`, `PATCH`, `DELETE`), thu thập toàn bộ ngữ cảnh Client (`IP`, `UserAgent`, `Actor`, `RequestID`) + danh sách `EntityChanges`, và đẩy **bất đồng bộ (Async Non-blocking)** về Kernel Bus để chuyển tới module `audit`.
  > **Cập nhật thiết kế (thay cho Interceptor ở tầng API):** audit được bắt **tại tầng DB** (`sdk/dbaudit`).
  > Trigger Postgres trên từng bảng ghi before/after vào `aegis_audit_outbox` trong cùng transaction với dữ liệu nghiệp vụ
  > (cột bí mật chỉ ghi `[REDACTED:CHANGED]`, cột hạ tầng bị loại). DB wrapper gắn ngữ cảnh request bằng `set_config(..., true)`.
  > Relay gom outbox theo transaction thành Envelope, đẩy qua Kernel Bus và chỉ xóa dòng khi được ack; quét định kỳ 30s
  > để bắt cả lần ghi không qua SDK (psql, script, seed — actor `SYSTEM`). Không còn `RecordInsert/RecordUpdate` trong repository.
- [x] **Bước 5: Xây dựng Module `[audit]` Độc Lập với Thiết Kế 3 Layer & Hash Toàn Vẹn (`modules/audit/`)**
  - [x] Khởi tạo module mới `modules/audit` (tự động cấp phát DB `audit_<tenant_id>` khi tạo Tenant trong Control Plane).
  - [x] Thiết kế SQL Migration `0001_init_audit_layers.up.sql` gồm 3 bảng chuẩn hóa:
    1. `audits`: Lưu ngữ cảnh Request, Client Info, IP, Actor.
    2. `entity_changes`: Lưu bảng bị tác động (`table_name`, `entity_id`, `operation`) kèm `before_snapshot` (JSONB) và `after_snapshot` (JSONB).
    3. `entity_audit_logs`: Lưu chi tiết từng trường thay đổi (`field_name`, `old_value`, `new_value`) kèm `prev_hash` và `integrity_hash` (SHA-256).
  - [x] Xây dựng **Diff & Hash Chain Engine**: Tự động so sánh `before_snapshot` vs `after_snapshot` để tách ra danh sách các cột thay đổi, tính toán mã băm `SHA-256` nối tiếp với bản ghi trước đó, và cung cấp API kiểm tra tính toàn vẹn (`GET /api/v1/audits/verify-integrity`).
  - [x] Dọn dẹp bảng `security_audit_logs` cũ ra khỏi `modules/auth` để tập trung 100% về `modules/audit`.
- [x] **Bước 6: Xây dựng Kernel Event Bus & Call-Chain Cycle Guard (`core/bus/`)**
  - [x] Triển khai bộ điều phối sự kiện bất đồng bộ trong Kernel (`PublishEvent` / `OnEvent`) kết hợp hàng đợi Worker Pool cho phép đẩy dữ liệu Audit và Domain Events giữa các Module.
  - [x] Triển khai `X-Aegis-Call-Chain` Guard chặn các lệnh gọi vòng lặp đồng bộ (`A -> B -> A`).

---

## 3. DETAIL: Thiết Kế Chi Tiết Từng Hạng Mục

### 3.1. Chuẩn Hóa JWT Authentication & Tách Biệt System Telemetry API

#### 1. Cấu trúc JWT Claims (`sdk/security/jwt.go`)
```go
type TokenClaims struct {
    TokenID       string `json:"jti"`
    Subject       string `json:"sub"`       // User ID (VD: USR-ADMIN-01)
    Username      string `json:"usr"`       // Username
    TenantID      string `json:"tid"`       // Tenant ID (Bắt buộc khớp với X-Tenant-ID)
    Role          string `json:"rol"`       // TENANT_ADMIN, USER, ...
    SecurityLevel string `json:"sec_level"` // STANDARD | MFA_HIGH_ASSURANCE
    IssuedAt      int64  `json:"iat"`
    ExpiresAt     int64  `json:"exp"`
}
```
* **Response của `POST /api/v1/auth/login` sau khi tinh gọn:**
  ```json
  {
    "access_token": "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9...",
    "token_type": "Bearer",
    "expires_in": 86400,
    "user": {
      "id": "USR-ADMIN-01",
      "username": "admin",
      "tenant_id": "acme-corp",
      "role": "TENANT_ADMIN",
      "security_level": "MFA_HIGH_ASSURANCE"
    }
  }
  ```
  *(Tuyệt đối không còn `database_source` hay `auth_flow_used` bên trong response đăng nhập).*

#### 2. Tách API Thông Tin Hệ Thống (`GET /api/v1/system/runtime-info`)
* Dành riêng cho Dashboard Simulator hoặc Admin kiểm tra trạng thái định tuyến của một Tenant:
  ```json
  {
    "tenant_id": "acme-corp",
    "gateway_node": "aegis-gw-01",
    "pooler_topology": "pgbouncer-cluster (transaction_mode)",
    "modules": {
      "auth": {
        "active_Login_strategy": "feature:mfa_security",
        "enabled_features": ["mfa_security"],
        "masked_datasource": "postgres://postgres:***@pgbouncer-node-1:6432/auth_acme_corp"
      },
      "parties": {
        "active_register_strategy": "feature:ekyc_verification",
        "enabled_features": ["ekyc_verification", "employee_directory"],
        "masked_datasource": "postgres://postgres:***@pgbouncer-node-1:6432/parties_acme_corp"
      },
      "audit": {
        "status": "ACTIVE_ASYNC_INTERCEPTOR",
        "masked_datasource": "postgres://postgres:***@pgbouncer-node-1:6432/audit_acme_corp"
      }
    }
  }
  ```

---

### 3.2. Thiết Kế Module `[audit]` Độc Lập & Mô Hình Dữ Liệu 3 Layer + Snapshot

#### 1. Sơ đồ Quan hệ Dữ liệu (ERD) của 3 Layer trong `audit_<tenant_id>`

```mermaid
erDiagram
    AUDITS ||--o{ ENTITY_CHANGES : "1 request tạo N tác động bảng"
    ENTITY_CHANGES ||--o{ ENTITY_AUDIT_LOGS : "1 bản ghi có K trường thay đổi"

    AUDITS {
        varchar(64) id PK "AUD-..."
        varchar(64) tenant_id "Tenant định danh"
        varchar(64) request_id "Correlation / Request ID"
        varchar(64) actor_id "User ID từ JWT (hoặc ANONYMOUS)"
        varchar(128) actor_username "Username thực hiện"
        varchar(64) actor_role "Role của User"
        varchar(64) client_ip "IP của Client"
        text user_agent "Trình duyệt / Thiết bị"
        jsonb client_info "Metadata bổ sung (Device, Geo, App)"
        varchar(64) source_module "Module phát sinh (auth, parties)"
        varchar(16) action_method "POST, PUT, DELETE..."
        varchar(255) action_path "/api/v1/parties"
        varchar(128) action_name "REGISTER_PARTY, LOGIN_MFA..."
        int status_code "HTTP Status (200, 201, 401...)"
        timestamptz occurred_at "Thời điểm diễn ra"
    }

    ENTITY_CHANGES {
        varchar(64) id PK "CHG-..."
        varchar(64) audit_id FK "Tham chiếu tới AUDITS.id"
        varchar(64) tenant_id "Tenant định danh"
        varchar(128) table_name "Tên bảng vật lý (parties, users)"
        varchar(128) entity_name "Tên Domain Entity (Party, UserAccount)"
        varchar(64) entity_id "Khóa chính của bản ghi bị tác động"
        varchar(16) operation "INSERT | UPDATE | DELETE"
        jsonb before_snapshot "Toàn bộ trạng thái Entity TRƯỚC khi đổi (NULL nếu INSERT)"
        jsonb after_snapshot "Toàn bộ trạng thái Entity SAU khi đổi (NULL nếu DELETE)"
        timestamptz changed_at "Thời điểm ghi nhận snapshot"
    }

    ENTITY_AUDIT_LOGS {
        varchar(64) id PK "LOG-..."
        varchar(64) entity_change_id FK "Tham chiếu tới ENTITY_CHANGES.id"
        varchar(64) audit_id FK "Tham chiếu nhanh tới AUDITS.id"
        varchar(64) tenant_id "Tenant định danh"
        varchar(128) field_name "Tên trường/cột bị thay đổi (VD: email, verification_status)"
        text old_value "Giá trị cũ (JSON serialized)"
        text new_value "Giá trị mới (JSON serialized)"
        varchar(64) prev_hash "integrity_hash của bản ghi liền trước (Genesis = 000...0)"
        varchar(64) integrity_hash "SHA-256(prev_hash | audit_id | entity_change_id | field | old | new | snapshot_hash)"
        timestamptz created_at "Thời điểm băm và lưu trữ"
    }
```

#### 2. Cơ chế hoạt động của SDK Audit Interceptor (`sdk/audit/`)

1. **Tầng Entity — Hỗ trợ chụp Snapshot (`Snapshottable`):**
   Mọi Entity trong `modules/auth` và `modules/parties` cài đặt phương thức `Snapshot()`:
   ```go
   // Ví dụ trên domain.Party:
   func (p *Party) TableName() string  { return "parties" }
   func (p *Party) EntityName() string { return "Party" }
   func (p *Party) EntityID() string   { return p.ID }
   func (p *Party) Snapshot() map[string]any {
       return map[string]any{
           "id":                  p.ID,
           "type":                p.Type,
           "full_name":           p.FullName,
           "email":               p.Email,
           "phone":               p.Phone,
           "identity_type":       p.Identity.Type,
           "identity_number":     p.Identity.Number,
           "verification_status": p.Identity.VerificationStatus,
       }
   }
   ```

2. **Tầng Repository / Handler — Ghi nhận thay đổi vào Context (`sdk/audit/tracker.go`):**
   Khi Repository thực hiện `INSERT`, `UPDATE`, hoặc `DELETE`, nó gọi helper của SDK:
   ```go
   // Khi tạo mới:
   audit.RecordInsert(ctx, party)

   // Khi cập nhật:
   audit.RecordUpdate(ctx, oldParty, updatedParty)

   // Khi xóa:
   audit.RecordDelete(ctx, existingParty)
   ```

3. **Tầng Module Interceptor (`sdk/app.go`) — Thu thập & Đẩy Bất Đồng Bộ:**
   * Ngay khi Request đi vào `ModuleApp.Execute(ctx, req)`:
     * Interceptor khởi tạo `ctx = audit.WithTracker(ctx)` chứa thông tin `ClientIP`, `UserAgent`, `ActorID`, `ActorUsername`, `ActorRole` (được Gateway giải mã từ JWT và truyền qua headers nội bộ).
   * Khi Handler thực thi xong:
     * Nếu có phát sinh `EntityChanges` trong `AuditTracker` (hoặc là sự kiện bảo mật quan trọng như `LOGIN_SUCCESS`, `LOGIN_FAILED`):
     * Interceptor đóng gói **`AuditEnvelope`** và đẩy vào **Async Channel / Kernel Event Bus** (`bus.PublishAsync("audit.recorded", envelope)`).
     * Request chính lập tức trả kết quả HTTP về cho người dùng mà **không phải chờ** tiến trình ghi DB của module `audit`.

4. **Tầng xử lý tại `[audit module]` — Diffing & Hash Chain:**
   * Module `audit` nhận `AuditEnvelope` bất đồng bộ:
     * **Bước 1:** Ghi bản ghi vào bảng **Layer 1 (`audits`)**.
     * **Bước 2:** Duyệt qua từng phần tử trong `EntityChanges`, ghi vào bảng **Layer 2 (`entity_changes`)** kèm `before_snapshot` và `after_snapshot`.
     * **Bước 3 (Diff & Hash):** Tự động chạy thuật toán so khớp từng key giữa `before_snapshot` và `after_snapshot`:
       * Với mỗi trường có giá trị thay đổi (`old_val != new_val`), lấy `integrity_hash` mới nhất của Tenant làm `prev_hash`.
       * Tính toán:
         $$\text{integrity\_hash} = \text{SHA256}(\text{prev\_hash} \parallel \text{tenant\_id} \parallel \text{audit\_id} \parallel \text{entity\_change\_id} \parallel \text{field\_name} \parallel \text{old\_value} \parallel \text{new\_value})$$
       * Lưu vào bảng **Layer 3 (`entity_audit_logs`)** trong cùng một SQL Transaction.

---

### 3.3. Cấu hình `PgBouncer × Node` & Tái Thiết Lập DB Lifecycle

1. **Cụm `PgBouncer × Node` (`:6432`, `:6433`):**
   * Đứng chắn trước PostgreSQL `:5432` cho toàn bộ 3 module: `auth_<tenant>`, `parties_<tenant>`, và `audit_<tenant>`.
   * Với chế độ `pool_mode = transaction`, dù 100 Tenants cùng lúc đẩy log bất đồng bộ vào `audit_<tenant>`, PgBouncer vẫn điều tiết mượt mà chỉ trên một số lượng kết nối vật lý cố định (ví dụ `default_pool_size = 15`), bảo vệ tuyệt đối PostgreSQL.
2. **Lifecycle của `ConnectionManager` (`sdk/multitenant/resolver.go`):**
   * Quản lý theo `tenantID` với `IdleTTL = 15m` và `MaxTenants` (LRU).
   * Tự động đóng pool cũ khi đổi `ConnectionString`.
   * Khi Kernel tắt (`SIGINT`/`SIGTERM`), Kernel chờ các worker bất đồng bộ của Bus đẩy nốt các gói tin Audit đang nằm trong hàng đợi (Drain Queue), sau đó gọi `Shutdown(ctx)` để các module đóng toàn bộ DB Pools.

---

## 4. Kế Hoạch Kiểm Chứng (Verification Plan)

1. **Kiểm chứng JWT Login & System Runtime API:**
   * Gọi `POST /api/v1/auth/login` -> Xác nhận trả về JWT chuẩn (`eyJ...`), payload gọn sạch không chứa `database_source`.
   * Dùng JWT vừa nhận gọi `POST /api/v1/parties` -> Gateway tự động trích xuất thông tin `Actor` từ JWT.
   * Gọi `GET /api/v1/system/runtime-info` với header `X-Tenant-ID` -> Nhận đầy đủ thông tin chẩn đoán hệ thống, trạng thái Feature Flags và Masked PgBouncer DSN.
2. **Kiểm chứng Luồng Audit 3 Layer Bất Đồng Bộ & Hash Toàn Vẹn:**
   * Tạo mới hoặc cập nhật một `Party` tại module `parties` -> Interceptor tự động chụp snapshot và bắn bất đồng bộ sang module `audit`.
   * Truy vấn `GET /api/v1/audits` (từ module `audit`) -> Kiểm tra đầy đủ 3 tầng dữ liệu:
     * **Layer 1 (`audits`):** Đúng IP, User-Agent, User ID từ JWT, Action `POST /api/v1/parties`.
     * **Layer 2 (`entity_changes`):** Bảng `parties`, `operation = INSERT/UPDATE`, đầy đủ `before_snapshot` & `after_snapshot`.
     * **Layer 3 (`entity_audit_logs`):** Chi tiết từng trường thay đổi (`full_name`, `email`, `verification_status`, ...) kèm chuỗi `prev_hash` -> `integrity_hash`.
   * Gọi `GET /api/v1/audits/verify-integrity` -> Xác nhận chuỗi băm SHA-256 hợp lệ 100% (`VALID / UNCOMPROMISED`).
3. **Kiểm chứng `PgBouncer × Node` & Lifecycle:**
   * Xác minh mọi kết nối từ `auth`, `parties`, `audit` đều đi qua cổng PgBouncer (`:6432/:6433`) và tự động thu hồi khi nhàn rỗi (`IdleTTL`) hoặc khi Graceful Shutdown.
