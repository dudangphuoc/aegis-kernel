package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"

	"github.com/dudangphuoc/aegis-kernel/core/contract"
	"github.com/dudangphuoc/aegis-kernel/modules/audit/internal/adapter/storage"
	"github.com/dudangphuoc/aegis-kernel/modules/audit/internal/domain"
	"github.com/dudangphuoc/aegis-kernel/sdk"
	"github.com/dudangphuoc/aegis-kernel/sdk/audit"
)

const (
	defaultListLimit = 50
	maxListLimit     = 200
)

func main() {
	app := sdk.NewModuleApp(contract.ModuleDescriptor{
		Name:        "audit",
		Version:     "1.0.0",
		Description: "Module kiểm toán 3 tầng (Request, Entity Snapshot, Field Diff + SHA-256 Hash Chain)",
	})

	// 1. Dependency Injection - PostgreSQL Repository (audit_<tenant_id>)
	auditRepo := storage.NewPostgresAuditRepo()
	app.OnShutdown(func(context.Context) error { return auditRepo.Close() })

	// Nhận audit.Envelope từ mọi module qua Kernel Event Bus (không mở qua HTTP).
	// Ingest idempotent theo envelope ID nên an toàn với giao lại (at-least-once).
	app.Subscribe(sdk.TopicAuditRecorded, func(ctx context.Context, ev contract.Event) error {
		var env audit.Envelope
		if err := json.Unmarshal(ev.Payload, &env); err != nil {
			return fmt.Errorf("decode audit envelope %s: %w", ev.ID, err)
		}
		return auditRepo.Ingest(ctx, env)
	})

	// 2. Đăng ký các Routes của Module
	// Route 1: Xem nhật ký kiểm toán 3 tầng của Tenant
	app.RegisterRoute(contract.RouteDefinition{
		Method:         "GET",
		Path:           "/api/v1/audits",
		Summary:        "Xem nhật ký kiểm toán 3 tầng (?limit=, mặc định 50, tối đa 200) — chỉ TENANT_ADMIN",
		RequireAuth:    true,
		OptionalTenant: false,
	}, func(ctx context.Context, req contract.InvocationRequest) (contract.InvocationResponse, error) {
		if resp, denied := requireTenantAdmin(req); denied {
			return resp, nil
		}
		limit := defaultListLimit
		if v, err := strconv.Atoi(req.QueryParams["limit"]); err == nil && v > 0 {
			limit = min(v, maxListLimit)
		}
		audits, err := auditRepo.List(ctx, limit)
		if err != nil {
			return sdk.JSONResponse(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		}
		return sdk.JSONResponse(http.StatusOK, map[string]any{"total": len(audits), "data": audits})
	})

	// Route 2: Kiểm tra toàn vẹn hash chain
	app.RegisterRoute(contract.RouteDefinition{
		Method:         "GET",
		Path:           "/api/v1/audits/verify-integrity",
		Summary:        "Kiểm tra toàn vẹn chuỗi băm SHA-256 của nhật ký kiểm toán — chỉ TENANT_ADMIN",
		RequireAuth:    true,
		OptionalTenant: false,
	}, func(ctx context.Context, req contract.InvocationRequest) (contract.InvocationResponse, error) {
		if resp, denied := requireTenantAdmin(req); denied {
			return resp, nil
		}
		report, err := auditRepo.VerifyIntegrity(ctx)
		if err != nil {
			return sdk.JSONResponse(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		}
		return sdk.JSONResponse(http.StatusOK, report)
	})

	sdk.Serve(app)
}

// requireTenantAdmin dựa trên header Actor do Gateway đặt sau khi xác thực JWT
func requireTenantAdmin(req contract.InvocationRequest) (contract.InvocationResponse, bool) {
	if req.Headers[contract.HeaderActorRole] == "TENANT_ADMIN" {
		return contract.InvocationResponse{}, false
	}
	resp, _ := sdk.JSONResponse(http.StatusForbidden, map[string]string{"error": domain.ErrForbidden.Error()})
	return resp, true
}
