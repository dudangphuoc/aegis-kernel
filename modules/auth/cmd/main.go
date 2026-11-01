package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/dudangphuoc/aegis-kernel/core/contract"
	"github.com/dudangphuoc/aegis-kernel/modules/auth/internal/adapter/storage"
	"github.com/dudangphuoc/aegis-kernel/modules/auth/internal/business/command"
	"github.com/dudangphuoc/aegis-kernel/modules/auth/internal/domain"
	mfasecurity "github.com/dudangphuoc/aegis-kernel/modules/auth/internal/features/mfa_security"
	securityaudit "github.com/dudangphuoc/aegis-kernel/modules/auth/internal/features/security_audit"
	"github.com/dudangphuoc/aegis-kernel/sdk"
	"github.com/dudangphuoc/aegis-kernel/sdk/cqrs"
)

func main() {
	app := sdk.NewModuleApp(contract.ModuleDescriptor{
		Name:        "auth",
		Version:     "1.0.0",
		Description: "Module quản lý đăng nhập, security, và cấp phát Tenants",
	})

	// 1. Dependency Injection - PostgreSQL Repository
	authRepo := storage.NewPostgresAuthRepo()

	// 2. CQRS Handlers
	stdLoginHandler := command.NewLoginHandler(authRepo)
	mfaLoginOverride := mfasecurity.NewMFALoginOverrideHandler(authRepo)

	// Gắn kết Base Business và Feature Override qua FeatureDispatcher
	loginDispatcher := cqrs.NewFeatureDispatcher[command.LoginCommand, *domain.AuthSession]("login_dispatcher", stdLoginHandler.Handle).
		RegisterOverride("mfa_security", mfaLoginOverride.Handle)

	auditLogsHandler := securityaudit.NewSecurityAuditQueryHandler(authRepo)

	// 3. Đăng ký các Routes của Module
	// Route 1: Đăng nhập (Áp dụng Feature Override tự động theo Tenant)
	app.RegisterRoute(contract.RouteDefinition{
		Method:         "POST",
		Path:           "/api/v1/auth/login",
		Summary:        "Đăng nhập tài khoản (Tự động chuyển đổi Standard hoặc MFA theo cấu hình Tenant)",
		RequireAuth:    false,
		OptionalTenant: false,
	}, func(ctx context.Context, req contract.InvocationRequest) (contract.InvocationResponse, error) {
		var cmd command.LoginCommand
		if err := json.Unmarshal(req.Body, &cmd); err != nil {
			return sdk.JSONResponse(http.StatusBadRequest, map[string]string{"error": "invalid json body"})
		}

		session, err := loginDispatcher.Dispatch(ctx, cmd)
		if err != nil {
			status := http.StatusUnauthorized
			if errors.Is(err, domain.ErrMFARequired) {
				status = http.StatusForbidden
			}
			return sdk.JSONResponse(status, map[string]string{"error": err.Error()})
		}
		return sdk.JSONResponse(http.StatusOK, session)
	})

	// Route 2: [Feature Extend] Xem Audit Logs của Tenant
	app.RegisterRoute(contract.RouteDefinition{
		Method:         "GET",
		Path:           "/api/v1/auth/audit-logs",
		Summary:        "Xem lịch sử nhật ký bảo mật (Chỉ mở khi Tenant bật feature security_audit)",
		RequireAuth:    true,
		OptionalTenant: false,
		FeatureFlag:    "security_audit",
	}, func(ctx context.Context, req contract.InvocationRequest) (contract.InvocationResponse, error) {
		logs, err := auditLogsHandler.Handle(ctx)
		if err != nil {
			return sdk.JSONResponse(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		}
		return sdk.JSONResponse(http.StatusOK, map[string]any{"audit_logs": logs})
	})

	sdk.Serve(app)
}
