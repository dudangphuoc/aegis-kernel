package main

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"

	"github.com/dudangphuoc/aegis-kernel/core/contract"
	"github.com/dudangphuoc/aegis-kernel/modules/auth/internal/adapter/storage"
	"github.com/dudangphuoc/aegis-kernel/modules/auth/internal/business/command"
	"github.com/dudangphuoc/aegis-kernel/modules/auth/internal/domain"
	mfasecurity "github.com/dudangphuoc/aegis-kernel/modules/auth/internal/features/mfa_security"
	"github.com/dudangphuoc/aegis-kernel/sdk"
	"github.com/dudangphuoc/aegis-kernel/sdk/cqrs"
	"github.com/dudangphuoc/aegis-kernel/sdk/security"
	"github.com/dudangphuoc/aegis-kernel/sdk/tenantdb"
)

func main() {
	app := sdk.NewModuleApp(contract.ModuleDescriptor{
		Name:        "auth",
		Version:     "1.0.0",
		Description: "Module quản lý đăng nhập, security, và cấp phát Tenants",
		AuditTables: []contract.AuditTable{
			{Table: "users", Entity: "UserAccount", PK: "id", Ignore: []string{"store_id"}, Secrets: []string{"password", "mfa_code"}},
		},
	})

	jwtSecret, err := security.SecretFromEnv()
	if err != nil {
		log.Fatalf("auth module: %v", err)
	}

	// 1. Dependency Injection - PostgreSQL Repository
	authRepo := storage.NewPostgresAuthRepo(app.UseTenantDatabase())

	// 2. CQRS Handlers
	stdLoginHandler := command.NewLoginHandler(authRepo, jwtSecret)
	mfaLoginOverride := mfasecurity.NewMFALoginOverrideHandler(authRepo, jwtSecret)

	// Gắn kết Base Business và Feature Override qua FeatureDispatcher
	loginDispatcher := cqrs.NewFeatureDispatcher[command.LoginCommand, *domain.AuthSession]("login_dispatcher", stdLoginHandler.Handle).
		RegisterOverride("mfa_security", mfaLoginOverride.Handle)

	// 3. Đăng ký các Routes của Module
	// Route 1: Đăng nhập (Áp dụng Feature Override tự động theo Tenant)
	app.RegisterRoute(contract.RouteDefinition{
		Method:         "POST",
		Path:           "/api/v1/auth/login",
		Summary:        "Đăng nhập tài khoản (Tự động chuyển đổi Standard hoặc MFA theo cấu hình Tenant)",
		RequireAuth:    false,
		OptionalTenant: false,
	}, func(ctx context.Context, req contract.InvocationRequest) (contract.InvocationResponse, error) {
		tenantdb.SetAction(ctx, "LOGIN")
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

	sdk.Serve(app)
}
