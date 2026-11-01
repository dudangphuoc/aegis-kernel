package sdk

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/hashicorp/go-plugin"
	"github.com/dudangphuoc/AegisKernel/core/contract"
	"github.com/dudangphuoc/AegisKernel/core/transport"
	"github.com/dudangphuoc/AegisKernel/sdk/multitenant"
)

type RouteHandlerFunc func(ctx context.Context, req contract.InvocationRequest) (contract.InvocationResponse, error)

type routeItem struct {
	def     contract.RouteDefinition
	handler RouteHandlerFunc
}

// ModuleApp là base application hỗ trợ module đăng ký route, inject tenant và xử lý invocation
type ModuleApp struct {
	mu         sync.RWMutex
	descriptor contract.ModuleDescriptor
	routes     map[string]routeItem
	initHook   func(ctx context.Context, cfg contract.ModuleInitConfig) error
}

func NewModuleApp(desc contract.ModuleDescriptor) *ModuleApp {
	return &ModuleApp{
		descriptor: desc,
		routes:     make(map[string]routeItem),
	}
}

func (app *ModuleApp) OnInitialize(fn func(ctx context.Context, cfg contract.ModuleInitConfig) error) {
	app.initHook = fn
}

func (app *ModuleApp) RegisterRoute(def contract.RouteDefinition, handler RouteHandlerFunc) {
	app.mu.Lock()
	defer app.mu.Unlock()
	key := fmt.Sprintf("%s %s", def.Method, def.Path)
	app.routes[key] = routeItem{def: def, handler: handler}
}

func (app *ModuleApp) GetDescriptor(_ context.Context) (contract.ModuleDescriptor, error) {
	return app.descriptor, nil
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

	// Đưa TenantContext vào Go Context
	tenantCtx := multitenant.WithTenant(ctx, req.Tenant)
	return item.handler(tenantCtx, req)
}

func (app *ModuleApp) HealthCheck(_ context.Context) (bool, error) {
	return true, nil
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
					Features:         map[string]bool{"mfa_security": true, "ekyc_verification": true, "security_audit": true, "employee_directory": true},
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
	if err := server.ListenAndServe(); err != nil {
		logger.Error("standalone debug server error", slog.Any("error", err))
	}
}
