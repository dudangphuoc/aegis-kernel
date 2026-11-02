package sdk

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"slices"
	"sync"
	"syscall"
	"time"

	"github.com/dudangphuoc/aegis-kernel/core/contract"
	"github.com/dudangphuoc/aegis-kernel/core/transport"
	"github.com/dudangphuoc/aegis-kernel/sdk/multitenant"
	"github.com/dudangphuoc/aegis-kernel/sdk/tenantdb"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/hashicorp/go-plugin"
)

type RouteHandlerFunc func(ctx context.Context, req contract.InvocationRequest) (contract.InvocationResponse, error)

type routeItem struct {
	def     contract.RouteDefinition
	handler RouteHandlerFunc
}

// ModuleApp là base application hỗ trợ module đăng ký route, inject tenant và xử lý invocation
type ModuleApp struct {
	mu            sync.RWMutex
	descriptor    contract.ModuleDescriptor
	routes        map[string]routeItem
	initHook      func(ctx context.Context, cfg contract.ModuleInitConfig) error
	shutdownHooks []func(ctx context.Context) error
	database      *tenantdb.Manager // nil nếu module không dùng DB của tenant
	subscriptions map[string]EventHandlerFunc
	outbox        *Outbox
	busEnabled    bool
	logger        *slog.Logger
}

func NewModuleApp(desc contract.ModuleDescriptor) *ModuleApp {
	app := &ModuleApp{
		descriptor:    desc,
		routes:        make(map[string]routeItem),
		subscriptions: make(map[string]EventHandlerFunc),
		outbox:        NewOutbox(0),
		logger:        slog.New(slog.NewTextHandler(os.Stderr, nil)).With(slog.String("module", desc.Name)),
	}
	return app
}

// UseTenantDatabase bật DB của tenant, gắn schema biz_<tên module>. Thay đổi dữ liệu được trigger capture
// ghi vào platform.changes; Kernel đọc lại để dựng audit (không có relay trong module).
// Repository lấy kết nối qua Database().Resolve(ctx). Gọi 1 lần trước sdk.Serve.
func (app *ModuleApp) UseTenantDatabase() *tenantdb.Manager {
	app.mu.Lock()
	defer app.mu.Unlock()
	if app.database == nil {
		app.database = tenantdb.NewManager("biz_" + app.descriptor.Name)
		app.shutdownHooks = append(app.shutdownHooks, app.database.Close)
	}
	return app.database
}

// Database trả Manager đã bật bằng UseTenantDatabase (nil nếu chưa bật)
func (app *ModuleApp) Database() *tenantdb.Manager {
	app.mu.RLock()
	defer app.mu.RUnlock()
	return app.database
}

func (app *ModuleApp) OnInitialize(fn func(ctx context.Context, cfg contract.ModuleInitConfig) error) {
	app.initHook = fn
}

// OnShutdown đăng ký hook dọn dẹp tài nguyên (đóng DB pools, flush queue) khi module tắt.
// Các hook chạy theo thứ tự ngược với thứ tự đăng ký.
func (app *ModuleApp) OnShutdown(fn func(ctx context.Context) error) {
	app.mu.Lock()
	defer app.mu.Unlock()
	app.shutdownHooks = append(app.shutdownHooks, fn)
}

func (app *ModuleApp) RegisterRoute(def contract.RouteDefinition, handler RouteHandlerFunc) {
	app.mu.Lock()
	defer app.mu.Unlock()
	key := fmt.Sprintf("%s %s", def.Method, def.Path)
	app.routes[key] = routeItem{def: def, handler: handler}
}

func (app *ModuleApp) GetDescriptor(_ context.Context) (contract.ModuleDescriptor, error) {
	app.mu.RLock()
	defer app.mu.RUnlock()
	desc := app.descriptor
	desc.Subscriptions = slices.Clone(app.descriptor.Subscriptions)
	desc.AuditTables = slices.Clone(app.descriptor.AuditTables)
	return desc, nil
}

func (app *ModuleApp) Initialize(ctx context.Context, config contract.ModuleInitConfig) error {
	if app.initHook != nil {
		return app.initHook(ctx, config)
	}
	return nil
}

func (app *ModuleApp) GetRoutes(_ context.Context) ([]contract.RouteDefinition, error) {
	app.mu.RLock()
	defer app.mu.RUnlock()
	res := make([]contract.RouteDefinition, 0, len(app.routes))
	for _, r := range app.routes {
		res = append(res, r.def)
	}
	return res, nil
}

func (app *ModuleApp) Execute(ctx context.Context, req contract.InvocationRequest) (contract.InvocationResponse, error) {
	key := fmt.Sprintf("%s %s", req.Method, req.Path)

	app.mu.RLock()
	item, ok := app.routes[key]
	app.mu.RUnlock()

	if !ok {
		return JSONResponse(http.StatusNotFound, map[string]string{
			"error": fmt.Sprintf("endpoint '%s' not found in module '%s'", key, app.descriptor.Name),
		})
	}

	// Đưa TenantContext, chuỗi gọi (Cycle Guard) và ngữ cảnh audit vào Go Context.
	// Ngữ cảnh audit chỉ là metadata gắn vào transaction; việc ghi nhận thay đổi do trigger ở tầng DB đảm nhận.
	tenantCtx := multitenant.WithTenant(ctx, req.Tenant)
	tenantCtx = withCallChain(tenantCtx, ParseCallChain(req.Headers[contract.HeaderCallChain]))
	tenantCtx = tenantdb.WithRequestContext(tenantCtx, tenantdb.RequestContextFromInvocation(app.descriptor.Name, req))
	return item.handler(tenantCtx, req)
}

func (app *ModuleApp) HealthCheck(_ context.Context) (bool, error) {
	return true, nil
}

func (app *ModuleApp) Shutdown(ctx context.Context) error {
	app.mu.Lock()
	hooks := app.shutdownHooks
	app.shutdownHooks = nil
	app.mu.Unlock()

	var errs []error
	for i := len(hooks) - 1; i >= 0; i-- {
		if err := hooks[i](ctx); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// JSONResponse là helper định dạng nhanh InvocationResponse
func JSONResponse(statusCode int, payload any) (contract.InvocationResponse, error) {
	b, err := json.Marshal(payload)
	if err != nil {
		return contract.InvocationResponse{
			StatusCode: http.StatusInternalServerError,
			Headers:    map[string]string{"Content-Type": "application/json"},
			Body:       []byte(`{"error":"json marshal error"}`),
		}, nil
	}
	return contract.InvocationResponse{
		StatusCode: statusCode,
		Headers:    map[string]string{"Content-Type": "application/json"},
		Body:       b,
	}, nil
}

// Serve khởi chạy MicroModule:
// - Nếu có STANDALONE_PORT: chạy trực tiếp HTTP Server (Chi v5.5.0) để developer F5 Debug trong IDE!
// - Mặc định: chạy qua HashiCorp Plugin RPC phục vụ Kernel Launcher
func Serve(impl contract.MicroModule) {
	standalonePort := os.Getenv("STANDALONE_PORT")
	if standalonePort != "" {
		runStandaloneDebugServer(impl, standalonePort)
		return
	}

	if app, ok := impl.(*ModuleApp); ok {
		app.UseKernelBus()
	}
	plugin.Serve(&plugin.ServeConfig{
		HandshakeConfig: transport.Handshake,
		Plugins: map[string]plugin.Plugin{
			"micro_module": &transport.MicroModulePlugin{Impl: impl},
		},
	})
}

func runStandaloneDebugServer(impl contract.MicroModule, port string) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug}))
	desc, _ := impl.GetDescriptor(context.Background())
	logger.Info("🐞 RUNNING IN STANDALONE DEBUG MODE (Chi v5.5.0 | F5 Supported!)",
		slog.String("module", desc.Name),
		slog.String("port", port),
	)

	_ = impl.Initialize(context.Background(), contract.ModuleInitConfig{
		Name:        desc.Name,
		Environment: "debug",
	})

	routes, _ := impl.GetRoutes(context.Background())
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)

	for _, routeDef := range routes {
		route := routeDef
		r.MethodFunc(route.Method, route.Path, func(w http.ResponseWriter, req *http.Request) {
			tenantID := req.Header.Get("X-Tenant-ID")
			if tenantID == "" {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"error":"missing required header X-Tenant-ID"}`))
				return
			}

			bodyBytes, _ := io.ReadAll(req.Body)
			queryParams := make(map[string]string)
			for k, v := range req.URL.Query() {
				if len(v) > 0 {
					queryParams[k] = v[0]
				}
			}

			invReq := contract.InvocationRequest{
				Path:        route.Path,
				Method:      req.Method,
				QueryParams: queryParams,
				Body:        bodyBytes,
				Tenant: contract.TenantRequestContext{
					TenantID:         tenantID,
					ConnectionString: fmt.Sprintf("postgres://postgres:postgres@localhost:5432/%s_%s?sslmode=disable", desc.Name, tenantID),
					Features:         map[string]bool{"mfa_security": true, "ekyc_verification": true, "employee_directory": true},
				},
			}
			resp, err := impl.Execute(req.Context(), invReq)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			for k, v := range resp.Headers {
				w.Header().Set(k, v)
			}
			w.WriteHeader(resp.StatusCode)
			_, _ = w.Write(resp.Body)
		})
	}

	server := &http.Server{
		Addr:              ":" + port,
		Handler:           r,
		ReadHeaderTimeout: 5 * time.Second,
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("standalone debug server error", slog.Any("error", err))
			stop()
		}
	}()
	<-ctx.Done()

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = server.Shutdown(shutdownCtx)
	if err := impl.Shutdown(shutdownCtx); err != nil {
		logger.Error("module shutdown error", slog.Any("error", err))
	}
}
