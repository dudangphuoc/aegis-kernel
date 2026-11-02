package gateway

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/netip"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/dudangphuoc/aegis-kernel/core/contract"
	"github.com/dudangphuoc/aegis-kernel/core/controlplane"
	"github.com/dudangphuoc/aegis-kernel/core/feature"
	"github.com/dudangphuoc/aegis-kernel/core/loader"
)

type APIGateway struct {
	manager   *loader.ModuleManager
	fm        *feature.FeatureManager
	cpStore   *controlplane.Store
	cpHandler *controlplane.APIHandler
	logger    *slog.Logger
	jwtSecret []byte
	// trustedProxies: proxy được phép báo IP client qua X-Forwarded-For (xem clientip.go)
	trustedProxies []netip.Prefix
}

func NewAPIGateway(m *loader.ModuleManager, fm *feature.FeatureManager, store *controlplane.Store, cpHandler *controlplane.APIHandler, jwtSecret []byte, logger *slog.Logger) *APIGateway {
	return &APIGateway{
		manager:   m,
		fm:        fm,
		cpStore:   store,
		cpHandler: cpHandler,
		logger:    logger,
		jwtSecret: jwtSecret,
	}
}

func (gw *APIGateway) Handler() http.Handler {
	r := chi.NewRouter()

	// Chuỗi Middleware chuẩn trên Chi
	r.Use(middleware.RequestID)
	r.Use(gw.clientIPMiddleware)
	r.Use(middleware.Recoverer)
	r.Use(corsMiddleware)

	// 1. Endpoints quản trị hệ thống của Kernel
	r.Get("/healthz", gw.handleHealth)
	r.Route("/kernel", func(r chi.Router) {
		r.Get("/modules", gw.handleListModules)
		r.Get("/openapi", gw.handleDynamicOpenAPI)
	})

	// 2. Đăng ký toàn bộ API Control Plane & Migrations (/api/cp/*)
	gw.cpHandler.RegisterRoutes(r)

	// 2b. API chẩn đoán hệ thống (tách khỏi response nghiệp vụ)
	r.Get("/api/v1/system/runtime-info", gw.handleRuntimeInfo)

	// 3. Gán các Route động từ các MicroModule qua Chi MethodFunc
	for _, loaded := range gw.manager.GetLoadedModules() {
		mod := loaded.Module
		moduleName := loaded.Descriptor.Name

		for _, routeDef := range loaded.Routes {
			route := routeDef
			pattern := fmt.Sprintf("%s %s", route.Method, route.Path)
			gw.logger.Info("mapping dynamic route on chi router",
				slog.String("module", moduleName),
				slog.String("pattern", pattern),
				slog.String("feature_flag", route.FeatureFlag),
				slog.Bool("optional_tenant", route.OptionalTenant),
			)

			r.MethodFunc(route.Method, route.Path, func(w http.ResponseWriter, req *http.Request) {
				// 0. Call-Chain Guard: chặn gọi vòng A -> B -> A giữa các module
				chain, err := nextCallChain(req, moduleName)
				if err != nil {
					writeJSON(w, http.StatusLoopDetected, map[string]string{"error": err.Error()})
					return
				}

				// A. Bóc tách TenantID từ Header X-Tenant-ID hoặc query ?tenant_id=
				tenantID := strings.TrimSpace(req.Header.Get("X-Tenant-ID"))
				if tenantID == "" {
					tenantID = strings.TrimSpace(req.URL.Query().Get("tenant_id"))
				}

				var tenantCtx contract.TenantRequestContext
				if tenantID != "" {
					resolved, err := gw.cpStore.ResolveTenantRequestContext(req.Context(), tenantID, moduleName)
					if err != nil {
						writeJSON(w, http.StatusNotFound, map[string]string{
							"error":     "tenant not found or not active in control plane",
							"tenant_id": tenantID,
						})
						return
					}
					tenantCtx = resolved
				} else if !route.OptionalTenant {
					writeJSON(w, http.StatusBadRequest, map[string]string{
						"error": "missing required header 'X-Tenant-ID'",
						"hint":  "provide header 'X-Tenant-ID: <your-tenant-id>'",
					})
					return
				}

				// B. Xác thực JWT: bắt buộc với route RequireAuth, tùy chọn với route còn lại
				claims, status, err := gw.verifyRequestJWT(req, tenantID)
				if err != nil {
					writeJSON(w, status, map[string]string{"error": err.Error()})
					return
				}
				if route.RequireAuth && claims == nil {
					w.Header().Set("WWW-Authenticate", `Bearer realm="aegis"`)
					writeJSON(w, http.StatusUnauthorized, map[string]string{
						"error": "authentication required",
						"hint":  "provide header 'Authorization: Bearer <access_token>' from POST /api/v1/auth/login",
					})
					return
				}

				// C. Kiểm tra Feature Flag (nếu route có gán cờ)
				if route.FeatureFlag != "" && tenantID != "" {
					if !gw.fm.IsEnabledForTenant(tenantID, moduleName, route.FeatureFlag) {
						writeJSON(w, http.StatusForbidden, map[string]string{
							"error":        fmt.Sprintf("feature '%s' is not enabled for tenant '%s'", route.FeatureFlag, tenantID),
							"tenant_id":    tenantID,
							"feature_flag": route.FeatureFlag,
						})
						return
					}
				}

				// D. Đọc body payload, query params và Chi URL params
				bodyBytes, _ := io.ReadAll(req.Body)
				queryParams := make(map[string]string)
				for k, v := range req.URL.Query() {
					if len(v) > 0 {
						queryParams[k] = v[0]
					}
				}
				if rctx := chi.RouteContext(req.Context()); rctx != nil {
					for i, k := range rctx.URLParams.Keys {
						if i < len(rctx.URLParams.Values) && k != "" {
							queryParams[k] = rctx.URLParams.Values[i]
						}
					}
				}

				headers := buildModuleHeaders(req, claims)
				headers[contract.HeaderCallChain] = chain

				invReq := contract.InvocationRequest{
					Path:        route.Path,
					Method:      req.Method,
					QueryParams: queryParams,
					Headers:     headers,
					Body:        bodyBytes,
					Tenant:      tenantCtx,
				}

				// E. Gọi RPC sang process của module
				resp, err := mod.Execute(req.Context(), invReq)
				if err != nil {
					gw.logger.Error("module execution error", slog.Any("error", err))
					writeJSON(w, http.StatusBadGateway, map[string]string{"error": "module RPC execution failed"})
					return
				}

				for k, v := range resp.Headers {
					w.Header().Set(k, v)
				}
				if resp.StatusCode == 0 {
					resp.StatusCode = http.StatusOK
				}
				w.WriteHeader(resp.StatusCode)
				_, _ = w.Write(resp.Body)
			})
		}
	}

	return r
}

func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Tenant-ID, X-Aegis-Call-Chain")
		w.Header().Set("Access-Control-Expose-Headers", "WWW-Authenticate")

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (gw *APIGateway) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{
		"status":        "kernel healthy",
		"router":        "go-chi/chi v5.5.0",
		"control_plane": "postgres connected",
	})
}

func (gw *APIGateway) handleListModules(w http.ResponseWriter, _ *http.Request) {
	modules := gw.manager.GetLoadedModules()
	type modInfo struct {
		Name        string                     `json:"name"`
		Version     string                     `json:"version"`
		Description string                     `json:"description"`
		Routes      []contract.RouteDefinition `json:"routes"`
	}
	res := make([]modInfo, 0, len(modules))
	for _, m := range modules {
		res = append(res, modInfo{
			Name:        m.Descriptor.Name,
			Version:     m.Descriptor.Version,
			Description: m.Descriptor.Description,
			Routes:      m.Routes,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"modules": res})
}

func (gw *APIGateway) handleDynamicOpenAPI(w http.ResponseWriter, _ *http.Request) {
	paths := make(map[string]map[string]any)

	for _, loaded := range gw.manager.GetLoadedModules() {
		for _, r := range loaded.Routes {
			m := strings.ToLower(r.Method)
			if paths[r.Path] == nil {
				paths[r.Path] = make(map[string]any)
			}
			paths[r.Path][m] = map[string]any{
				"tags":        []string{loaded.Descriptor.Name},
				"summary":     r.Summary,
				"description": fmt.Sprintf("Module: %s (v%s) | FeatureFlag: %s | OptionalTenant: %v", loaded.Descriptor.Name, loaded.Descriptor.Version, r.FeatureFlag, r.OptionalTenant),
				"parameters": []map[string]any{
					{
						"name":        "Authorization",
						"in":          "header",
						"required":    r.RequireAuth,
						"description": "Bearer <JWT> từ POST /api/v1/auth/login",
						"schema":      map[string]string{"type": "string"},
					},
					{
						"name":        "X-Tenant-ID",
						"in":          "header",
						"required":    !r.OptionalTenant,
						"description": "Tenant Identifier",
						"schema":      map[string]string{"type": "string"},
					},
				},
				"responses": map[string]any{
					"200": map[string]string{"description": "Success"},
				},
			}
		}
	}

	spec := map[string]any{
		"openapi": "3.0.0",
		"info": map[string]string{
			"title":       "Control Plane Dynamic API Gateway (Chi v5.5.0)",
			"version":     "3.0.0",
			"description": "PostgreSQL Multi-Tenant Gateway với Dynamic OpenAPI & Control Plane",
		},
		"paths": paths,
	}

	writeJSON(w, http.StatusOK, spec)
}

func writeJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}
