# Kế Hoạch Tái Thiết Kiến Trúc: Micro-Kernel + Clean Architecture & CQRS + Multi-Tenant Feature Engine

---

## 1. Review Kiến Trúc Hiện Tại (Architecture Review)

Qua rà soát toàn bộ mã nguồn hiện tại, dự án đang gặp **4 vấn đề cốt lõi**:

1. **Mất đi Clean Architecture & CQRS trong Business Module:**
   * File [examples/order-service/main.go](file:///Users/raiden/Workspace/2026/go-learn/examples/order-service/main.go) đang gom cả khai báo RPC, routing, lưu trữ dữ liệu và xử lý nghiệp vụ vào chung một struct `ExampleMicroserviceModule`.
   * Không còn sự phân tầng `domain` (Entities/Repository Interfaces), `usecase/command` (Ghi), `usecase/query` (Đọc) và `adapter` (Storage/HTTP). Các thành viên trong team không có không gian cấu trúc rõ ràng để code nghiệp vụ.
2. **Các thành phần bị dính chặt (Tight Coupling):**
   * [core/contract/module.go](file:///Users/raiden/Workspace/2026/go-learn/core/contract/module.go) đang trộn lẫn định nghĩa hợp đồng giao tiếp (DTO/Interface) với tầng vận chuyển `net/rpc` của `hashicorp/go-plugin`.
   * Business logic bên trong module đang phải tự đọc `InvocationRequest.Path` và `Method` bằng `if/else` thủ công thay vì thông qua bộ định tuyến nội bộ (Internal Router / CQRS Dispatcher).
3. **Chưa định hình mối quan hệ giữa `Business` (Cốt lõi) và `Feature` (Mở rộng / Ghi đè):**
   * Trong thực tế phát triển sản phẩm (tương tự mô hình Module/Feature của .NET ABP Framework), **`business/`** là nơi chứa luồng nghiệp vụ chuẩn (Default Command/Query Handlers), còn **`features/`** là nơi:
     * **Ghi đè (Override):** Thay thế một Command/Query Handler mặc định của `business` bằng một Handler chuyên biệt khi Feature đó được bật (ví dụ: `vip_discount` ghi đè cách tính giá của `CreateOrderCommand`).
     * **Mở rộng (Extend):** Đăng ký thêm các Command/Query/Route mới mà `business` gốc không có (ví dụ: `order_summary` mở thêm API thống kê doanh thu).
4. **Chưa giải quyết bài toán Multi-Tenancy (`[Tenant A]` vs `[Tenant B]`):**
   * File [config.json](file:///Users/raiden/Workspace/2026/go-learn/config.json) và [core/config/config.go](file:///Users/raiden/Workspace/2026/go-learn/core/config/config.go) đang cấu hình `connection_string` và `features` cố định cho cả module, chưa phân tách theo từng Tenant.
   * **Thiếu Tenant Detection:** [core/gateway/gateway.go](file:///Users/raiden/Workspace/2026/go-learn/core/gateway/gateway.go) chưa bóc tách định danh Tenant từ Request (`X-Tenant-ID` header / JWT / Subdomain) để truyền xuống Module.
   * **Thiếu Connection Resolver:** Tầng Adapter Database chưa có cơ chế dựa vào `TenantID` trong `context.Context` để chọn đúng Database (`Tenant A` đọc/ghi vào `db_tenant_a`, `Tenant B` đọc/ghi vào `db_tenant_b`).

---

## 2. SUMMARY: Tổng Quan Kiến Trúc Mới & Checklist (Người Dùng Xem)

### 2.1. Sơ đồ luồng vận hành mục tiêu (End-to-End Flow)

```mermaid
flowchart TD
    ReqA["Request từ Tenant A<br/>(Header: X-Tenant-ID: tenant-a)"] --> GW
    ReqB["Request từ Tenant B<br/>(Header: X-Tenant-ID: tenant-b)"] --> GW

    subgraph Kernel["1. CORE ENGINE (Host Process)"]
        GW["API Gateway + Tenant Detector Middleware"]
        TM["Tenant Registry & Feature Manager<br/>(Quản lý Config, Feature Flags & DB URL theo từng Tenant)"]
        GW <--> TM
    end

    GW == "IPC / RPC (Truyền kèm TenantContext)" ==> ModRouter

    subgraph OrderModule["2. BUSINESS MODULE: modules/order (Clean Architecture + CQRS)"]
        ModRouter["SDK Internal Router & Tenant Context Extractor"]
        Dispatcher["CQRS & Feature Dispatcher<br/>(Kiểm tra Feature Flag của Tenant hiện tại)"]

        ModRouter --> Dispatcher

        subgraph FeatureLayer["Tầng Features (Extend / Override)"]
            FeatVIP["Feature: vip_discount<br/>(Override CreateOrderCommand cho Tenant B)"]
            FeatExport["Feature: order_summary<br/>(Extend Query mới cho Tenant A)"]
        end

        subgraph BusinessLayer["Tầng Business Chuẩn (Core CQRS UseCases)"]
            CmdStd["Command: Standard CreateOrderHandler"]
            QryStd["Query: Standard ListOrdersHandler"]
        end

        Dispatcher -- "Nếu Tenant bật Feature Override" --> FeatVIP
        Dispatcher -- "Mặc định (Base Business)" --> CmdStd
        Dispatcher --> QryStd
        Dispatcher -- "Nếu Tenant bật Feature Extend" --> FeatExport

        subgraph DomainLayer["Tầng Domain"]
            Entities["Entities: Order, Product + Domain Repository Interfaces"]
        end

        FeatVIP --> Entities
        CmdStd --> Entities
        QryStd --> Entities

        subgraph AdapterLayer["Tầng Adapter (Multi-Tenant Infrastructure)"]
            ConnResolver["Tenant Connection Resolver<br/>(Resolve DB theo TenantID trong Context)"]
            DB_A[("DB Tenant A<br/>(postgres/memory_tenant_a)")]
            DB_B[("DB Tenant B<br/>(postgres/memory_tenant_b)")]
            ConnResolver --> DB_A
            ConnResolver --> DB_B
        end

        Entities --> ConnResolver
    end
```

### 2.2. Cấu trúc thư mục mục tiêu

```text
go-learn/
├── cookbooks/                              # Tài liệu thiết kế, kế hoạch & sổ tay kiến trúc
│   └── 01_architecture_refactoring_plan.md
├── launcher/                               # Entrypoint của Core Engine (Host)
│   └── main.go
├── core/                                   # Lõi Micro-Kernel (Không chứa code nghiệp vụ)
│   ├── contract/                           # Định nghĩa Interface MicroModule & DTO (Tách rời khỏi RPC)
│   ├── transport/                          # Triển khai RPC Client/Server cho hashicorp/go-plugin
│   ├── config/                             # Cấu hình Multi-Tenant (Tenants, Modules, Features, DBs)
│   ├── feature/                            # Feature Evaluator theo từng (Tenant + Module)
│   ├── loader/                             # Quét và khởi chạy các binary module trong ./bin/modules
│   └── gateway/                            # Dynamic API Gateway + Tenant Middleware + OpenAPI
├── sdk/                                    # Bộ công cụ giúp Developer viết Business Module cực nhanh
│   ├── app.go                              # Module Builder, Internal Router đăng ký Command/Query
│   ├── cqrs/                               # CQRS CommandBus & QueryBus hỗ trợ Feature Override
│   ├── multitenant/                        # TenantContext trong Go context & ConnectionResolver generic
│   └── serve.go                            # Hàm khởi chạy plugin process
├── modules/                                # NƠI CÁC MEMBER CODE BUSINESS & FEATURE
│   └── order/                              # Microservice Order (Đầy đủ Clean Architecture + CQRS)
│       ├── cmd/main.go                     # Điểm khởi tạo & lắp ráp DI của module Order
│       └── internal/
│           ├── domain/                     # 1. Domain: Entities (Order), Errors, Repository Interfaces
│           ├── business/                   # 2. Business (Core CQRS): Nơi code nghiệp vụ chuẩn
│           │   ├── command/                #    - CreateOrderHandler (Standard)
│           │   └── query/                  #    - ListOrdersHandler (Standard)
│           ├── features/                   # 3. Features: Nơi mở rộng hoặc ghi đè Business theo Tenant
│           │   ├── vip_discount/           #    - [Override] Ghi đè CreateOrder giảm giá 20% cho Tenant VIP
│           │   └── order_summary/          #    - [Extend] Thêm API thống kê doanh thu chỉ cho Tenant bật cờ
│           └── adapter/                    # 4. Adapter: Multi-Tenant Repository + Tenant Connection Resolver
│               └── storage/
├── config.json                             # Cấu hình mẫu cho [Tenant A] và [Tenant B] (khác DB & khác Feature)
└── Makefile
```

### 2.3. Checklist Tổng Quát (Người Dùng Kiểm Tra)

- [ ] **Bước 1: Chuẩn hóa Core Kernel & Multi-Tenant Contract**
  - [ ] Tách bạch `core/contract` (Interface/DTO) và `core/transport` (RPC implementation).
  - [ ] Nâng cấp `config.json` & `core/config` hỗ trợ cấu hình đa khách hàng (`tenants`: `tenant-a`, `tenant-b`), mỗi Tenant có `connection_string` và `features` riêng cho từng module.
  - [ ] Xây dựng `TenantMiddleware` tại `core/gateway` tự động nhận diện `X-Tenant-ID` từ HTTP Request và truyền xuống Module.
- [ ] **Bước 2: Xây dựng `sdk/` hỗ trợ CQRS, Feature Override & Multi-Tenant Context**
  - [ ] Tạo `sdk/multitenant`: Quản lý `TenantContext` (`TenantID`, `ConnectionString`, `ActiveFeatures`) bên trong `context.Context` và cung cấp `ConnectionManager[DB]`.
  - [ ] Tạo `sdk/cqrs`: Xây dựng `FeatureDispatcher[Req, Res]` cho phép đăng ký **Base Business Handler** và đăng ký **Feature Override Handler** (tự động chọn Handler dựa vào Feature Flag của Tenant đang gọi).
  - [ ] Tạo `sdk/app`: Cung cấp Internal Router để Module định nghĩa HTTP Route gắn trực tiếp vào Command/Query mà không cần viết `if/else` xử lý chuỗi URL.
- [ ] **Bước 3: Xây dựng Module `modules/order` chuẩn Clean Architecture + CQRS + Features**
  - [ ] Xóa thư mục `examples/order-service` cũ bị viết gộp.
  - [ ] Xây dựng `modules/order/internal/domain`: Entity `Order`, lỗi nghiệp vụ, interface `OrderRepository`.
  - [ ] Xây dựng `modules/order/internal/business`: Luồng CQRS chuẩn (`command/create_order.go`, `query/list_orders.go`).
  - [ ] Xây dựng `modules/order/internal/features`:
    - Feature `vip_discount` (**Override** `CreateOrderCommand`: tự động giảm giá 20% và gắn nhãn VIP khi Tenant bật cờ `vip_discount`).
    - Feature `order_summary` (**Extend** thêm Query & Route `GET /api/v1/orders/summary` chỉ mở cho Tenant bật cờ `order_summary`).
  - [ ] Xây dựng `modules/order/internal/adapter/storage`: Sử dụng `ConnectionManager` để cô lập hoàn toàn dữ liệu giữa DB của `tenant-a` và DB của `tenant-b`.
- [ ] **Bước 4: Kiểm thử End-to-End (Unit Test + Multi-Tenant Verification)**
  - [ ] Viết Unit Test cho CQRS Dispatcher, Feature Override và Multi-Tenant Connection Isolation.
  - [ ] Chạy kịch bản kiểm thử chứng minh: `Tenant A` và `Tenant B` gọi cùng 1 API trên cùng 1 source code nhưng thực thi logic Feature khác nhau và lưu vào 2 vùng Database hoàn toàn tách biệt.

---

## 3. DETAIL: Các Công Việc Chi Tiết (Dành Cho AI Thực Hiện)

> [!IMPORTANT]
> Khi thực thi kế hoạch này, AI cần tuân thủ chính xác tên file, chữ ký hàm (method signatures) và thứ tự phụ thuộc bên dưới để đảm bảo biên dịch thành công 100% (`go build ./...` và `go test -race ./...`).

### Task 1: Thiết kế lại `core/contract`, `core/transport` và `core/config` (Hỗ trợ Multi-Tenant)

1. **Cập nhật [core/contract/module.go](file:///Users/raiden/Workspace/2026/go-learn/core/contract/module.go)**:
   * Chỉ giữ lại các kiểu dữ liệu thuần (Pure Contracts), không import `net/rpc`:
     * `ModuleDescriptor`: `Name`, `Version`, `Description`.
     * `TenantModuleConfig`:
       ```go
       type TenantModuleConfig struct {
           TenantID         string            `json:"tenant_id"`
           ConnectionString string            `json:"connection_string"`
           Features         map[string]bool   `json:"features"`
           Settings         map[string]string `json:"settings"`
       }
       ```
     * `ModuleInitConfig`:
       ```go
       type ModuleInitConfig struct {
           Name              string                        `json:"name"`
           Version           string                        `json:"version"`
           Environment       string                        `json:"environment"`
           DefaultConnection string                        `json:"default_connection"`
           DefaultFeatures   map[string]bool               `json:"default_features"`
           Tenants           map[string]TenantModuleConfig `json:"tenants"` // Key: TenantID
       }
       ```
     * `TenantRequestContext`:
       ```go
       type TenantRequestContext struct {
           TenantID         string            `json:"tenant_id"`
           ConnectionString string            `json:"connection_string"`
           Features         map[string]bool   `json:"features"`
           Settings         map[string]string `json:"settings"`
       }
       ```
     * `InvocationRequest`: Bổ sung trường `Tenant TenantRequestContext` bên cạnh `Method`, `Path`, `Headers`, `Body`.
     * `RouteDefinition`: `Method`, `Path`, `Summary`, `RequireAuth`, `FeatureFlag`.
     * Interface `MicroModule`: `GetDescriptor`, `Initialize`, `GetRoutes`, `Execute`, `HealthCheck`.

2. **Tạo mới `core/transport/rpc.go`**:
   * Chuyển `Handshake`, `MicroModulePlugin`, `RPCClient`, `RPCServer` từ `core/contract` sang package `transport` để tách biệt giao thức vận chuyển IPC khỏi hợp đồng Domain.

3. **Cập nhật [core/config/config.go](file:///Users/raiden/Workspace/2026/go-learn/core/config/config.go) và [config.json](file:///Users/raiden/Workspace/2026/go-learn/config.json)**:
   * Định nghĩa cấu hình cho `tenants` (`tenant-a`, `tenant-b`):
     * `tenant-a` (Gói Standard + Bật tính năng mở rộng `order_summary`, tắt `vip_discount`, dùng DB `postgres://user:pass@localhost:5432/order_tenant_a`).
     * `tenant-b` (Gói Enterprise VIP + Bật tính năng ghi đè `vip_discount`, tắt `order_summary`, dùng DB `postgres://user:pass@localhost:5432/order_tenant_b`).
   * Viết helper `ResolveTenantModuleConfig(tenantID, moduleName string) (contract.TenantRequestContext, error)` để hợp nhất (merge) cờ tính năng theo thứ tự ưu tiên: `Tenant Module Features` > `Default Module Features` > `Global Kernel Features`.

---

### Task 2: Nâng cấp `core/feature`, `core/loader` và `core/gateway` (Detect Tenant & Enforce Feature)

1. **Cập nhật [core/feature/manager.go](file:///Users/raiden/Workspace/2026/go-learn/core/feature/manager.go)**:
   * Hỗ trợ kiểm tra `IsEnabledForTenant(tenantID, moduleName, featureFlag string) bool`.
2. **Cập nhật [core/loader/manager.go](file:///Users/raiden/Workspace/2026/go-learn/core/loader/manager.go)**:
   * Sử dụng `transport.MicroModulePlugin` và truyền đầy đủ `Tenants` map vào `ModuleInitConfig` khi gọi `mod.Initialize(ctx, initConfig)`.
3. **Cập nhật [core/gateway/gateway.go](file:///Users/raiden/Workspace/2026/go-learn/core/gateway/gateway.go)**:
   * Bổ sung logic **Detect Request Tenant**:
     * Đọc header `X-Tenant-ID` (hoặc query `?tenant_id=` nếu test trên trình duyệt).
     * Nếu request vào route của Module mà thiếu `X-Tenant-ID` hoặc Tenant không tồn tại trong hệ thống, trả về `400 Bad Request` / `403 Forbidden` tường minh.
   * Kiểm tra `route.FeatureFlag` theo đúng **Tenant đang gọi** (`IsEnabledForTenant`). Nếu Tenant A được bật `order_summary` thì cho phép đi tiếp; nếu Tenant B không bật `order_summary` thì chặn ngay tại Gateway với `403 Feature Disabled for Tenant`.
   * Đính kèm `TenantRequestContext` đã resolve vào `InvocationRequest.Tenant` gửi qua RPC xuống Module.
   * Thêm endpoint `GET /kernel/tenants` để dễ dàng kiểm tra cấu hình Multi-Tenant hiện hành.

---

### Task 3: Xây dựng bộ `sdk/` (Multi-Tenant Context, Connection Resolver & CQRS Feature Bus)

1. **Tạo mới `sdk/multitenant/context.go`**:
   * Định nghĩa `WithTenant(ctx context.Context, t contract.TenantRequestContext) context.Context` và `FromContext(ctx context.Context) (contract.TenantRequestContext, bool)`.
   * Helper `IsFeatureEnabled(ctx context.Context, flag string) bool` để tầng UseCase/Dispatcher kiểm tra nhanh cờ tính năng của Tenant hiện tại.

2. **Tạo mới `sdk/multitenant/resolver.go`**:
   * Xây dựng Generic Connection/Store Resolver `ConnectionManager[DB any]`:
     * Cho phép khởi tạo sẵn kết nối theo danh sách Tenant lúc `Initialize` hoặc khởi tạo lười (Lazy initialization + `sync.RWMutex`) dựa trên `(tenantID, connectionString)` lấy từ `multitenant.FromContext(ctx)`.
     * Đảm bảo mỗi `TenantID` nhận về một instance DB/Connection độc lập tương ứng với `ConnectionString` của Tenant đó.

3. **Tạo mới `sdk/cqrs/bus.go`**:
   * Thiết kế cơ chế gắn kết giữa **Business (Base)** và **Feature (Override)**:
     ```go
     type HandlerFunc[Req any, Res any] func(ctx context.Context, req Req) (Res, error)

     type FeatureDispatcher[Req any, Res any] struct {
         baseHandler HandlerFunc[Req, Res]
         overrides   []featureOverride[Req, Res]
     }
     ```
   * Cho phép:
     * `NewFeatureDispatcher[Req, Res](baseHandler HandlerFunc[Req, Res])`: Đăng ký luồng xử lý chuẩn từ `internal/business`.
     * `RegisterOverride(featureFlag string, handler HandlerFunc[Req, Res])`: Đăng ký luồng ghi đè từ `internal/features`.
     * `Dispatch(ctx context.Context, req Req) (Res, error)`: Tự động kiểm tra `multitenant.IsFeatureEnabled(ctx, flag)`. Nếu Tenant hiện tại bật `featureFlag`, thực thi `FeatureOverrideHandler`; ngược lại thực thi `BaseHandler`.

4. **Tạo mới `sdk/app.go` và cập nhật [sdk/serve.go](file:///Users/raiden/Workspace/2026/go-learn/sdk/serve.go)**:
   * Cung cấp `sdk.ModuleApp` tự động quản lý router nội bộ (`RegisterRoute(def contract.RouteDefinition, handler RouteHandlerFunc)`) và tự động nạp `req.Tenant` vào `ctx` bằng `multitenant.WithTenant` trước khi gọi vào Handler của Module.

---

### Task 4: Xây dựng Module `modules/order` theo chuẩn Clean Architecture + CQRS + Features

1. **Xóa thư mục cũ**: Xóa `examples/order-service`.
2. **Tầng Domain (`modules/order/internal/domain/`)**:
   * `order.go`: Struct `Order` (`ID`, `TenantID`, `ProductID`, `Quantity`, `OriginalPrice`, `DiscountAmount`, `FinalPrice`, `AppliedFeature`, `DatabaseSource`, `CreatedAt`).
   * `errors.go`: `ErrInvalidQuantity`, `ErrInvalidProduct`, `ErrTenantRequired`.
   * `repository.go`: Interface `OrderRepository` (`Create(ctx, *Order) error`, `ListAll(ctx) ([]*Order, error)`).
3. **Tầng Adapter Multi-Tenant (`modules/order/internal/adapter/storage/tenant_repo.go`)**:
   * Triển khai `MultiTenantOrderRepo` kết hợp `multitenant.ConnectionManager[*TenantDatabase]`.
   * Khi `Create` hoặc `ListAll` được gọi, Repository tự động gọi `resolver.Resolve(ctx)` để lấy đúng database instance được gắn với `(TenantID, ConnectionString)` của Tenant đó.
   * Lưu kèm thông tin `DatabaseSource: connString` vào bản ghi để khi test qua API nhìn thấy rõ đơn hàng được lấy từ DB của Tenant nào.
4. **Tầng Business Chuẩn (`modules/order/internal/business/`)**:
   * `command/create_order.go`:
     * Định nghĩa `CreateOrderCommand` (`ProductID`, `Quantity`, `UnitPrice`) và `CreateOrderHandler` (Standard Business): Tính `FinalPrice = Quantity * UnitPrice`, `DiscountAmount = 0`, `AppliedFeature = "standard-business"`, lưu vào `OrderRepository`.
   * `query/list_orders.go`:
     * Định nghĩa `ListOrdersQuery` và `ListOrdersHandler`: Trả về danh sách đơn hàng của Tenant hiện tại từ `OrderRepository`.
5. **Tầng Features (`modules/order/internal/features/`)**:
   * `vip_discount/override_create_order.go` (**Minh họa Feature ghi đè Business có sẵn**):
     * Khi cờ `vip_discount` được bật cho Tenant (ví dụ `tenant-b`), Handler này ghi đè `CreateOrderCommand`: Tự động giảm giá 20% (`DiscountAmount = 20%`), gắn `AppliedFeature = "feature:vip_discount (20% OFF)"` và lưu xuống `OrderRepository`.
   * `order_summary/summary_query.go` (**Minh họa Feature triển khai thêm Business mới**):
     * Khi cờ `order_summary` được bật cho Tenant (ví dụ `tenant-a`), cung cấp thêm Query Handler `GetOrderSummaryHandler` và đăng ký thêm Route `GET /api/v1/orders/summary` tính tổng số đơn, tổng doanh thu và tổng tiền tiết kiệm của Tenant đó.
6. **Tầng Wiring / Entrypoint của Module (`modules/order/cmd/main.go`)**:
   * Khởi tạo `MultiTenantOrderRepo`.
   * Khởi tạo `CreateOrderDispatcher` (`sdk/cqrs`): gắn `business/command.CreateOrderHandler` làm Base, và gắn `features/vip_discount` làm Override cho cờ `"vip_discount"`.
   * Đăng ký các HTTP Routes (`GET /api/v1/orders`, `POST /api/v1/orders`, và route mở rộng `GET /api/v1/orders/summary` với `FeatureFlag: "order_summary"`).

---

### Task 5: Cập nhật `Makefile`, `README.md` và Viết Kiểm Thử Tự Động

1. **Viết Unit & Integration Test (`modules/order/internal/order_multitenant_test.go`)**:
   * Kiểm tra cùng một `CreateOrderDispatcher`:
     * Khi gọi với context của `tenant-a` (`vip_discount = false`, DB `tenant_a_db`): Chạy luồng `standard-business`, không giảm giá, lưu vào DB A.
     * Khi gọi với context của `tenant-b` (`vip_discount = true`, DB `tenant_b_db`): Tự động chuyển sang luồng `feature:vip_discount`, giảm giá 20%, lưu vào DB B.
     * Kiểm tra tính cô lập dữ liệu (Data Isolation): Truy vấn danh sách đơn của `tenant-a` tuyệt đối không nhìn thấy đơn của `tenant-b`.
2. **Cập nhật `Makefile` và `README.md`**:
   * Cập nhật `make build-modules` trỏ sang `./modules/order/cmd`.
   * Thêm lệnh `make test` chạy `go test -v -race ./...`.
   * Cập nhật `README.md` giải thích rõ mô hình **Micro-Kernel + Clean Architecture + CQRS + Multi-Tenant Feature Engine** và các câu lệnh `curl` có kèm `X-Tenant-ID: tenant-a` và `X-Tenant-ID: tenant-b`.

