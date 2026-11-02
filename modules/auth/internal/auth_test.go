package test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/dudangphuoc/aegis-kernel/core/contract"
	authdb "github.com/dudangphuoc/aegis-kernel/modules/auth/db"
	"github.com/dudangphuoc/aegis-kernel/modules/auth/internal/adapter/storage"
	"github.com/dudangphuoc/aegis-kernel/modules/auth/internal/business/command"
	"github.com/dudangphuoc/aegis-kernel/modules/auth/internal/domain"
	mfasecurity "github.com/dudangphuoc/aegis-kernel/modules/auth/internal/features/mfa_security"
	"github.com/dudangphuoc/aegis-kernel/sdk/cqrs"
	"github.com/dudangphuoc/aegis-kernel/sdk/multitenant"
	"github.com/dudangphuoc/aegis-kernel/sdk/security"
	"github.com/dudangphuoc/aegis-kernel/sdk/tenantdb"
	"github.com/dudangphuoc/aegis-kernel/sdk/tenantdb/tenanttest"
)

func TestAuthModule_MultiTenantFeatureOverride(t *testing.T) {
	pkg, err := authdb.Package()
	if err != nil {
		t.Fatal(err)
	}
	_, dsnA := tenanttest.FreshTenant(t, "tenant_auth_test_a", pkg)
	_, dsnB := tenanttest.FreshTenant(t, "tenant_auth_test_b", pkg)
	secret := []byte("test-secret-test-secret-test-secret")
	dbm := tenantdb.NewManager("biz_auth")
	t.Cleanup(func() { _ = dbm.Close(context.Background()) })
	repo := storage.NewPostgresAuthRepo(dbm)
	stdLoginHandler := command.NewLoginHandler(repo, secret)
	mfaLoginOverride := mfasecurity.NewMFALoginOverrideHandler(repo, secret)

	dispatcher := cqrs.NewFeatureDispatcher[command.LoginCommand, *domain.AuthSession]("login", stdLoginHandler.Handle).
		RegisterOverride("mfa_security", mfaLoginOverride.Handle)

	// Context Tenant A: mfa_security = false
	ctxTenantA := multitenant.WithTenant(context.Background(), contract.TenantRequestContext{
		TenantID:         "tenant-a",
		ConnectionString: dsnA,
		Features:         map[string]bool{"mfa_security": false},
	})

	// Context Tenant B: mfa_security = true
	ctxTenantB := multitenant.WithTenant(context.Background(), contract.TenantRequestContext{
		TenantID:         "tenant-b",
		ConnectionString: dsnB,
		Features:         map[string]bool{"mfa_security": true},
	})

	// Tài khoản admin mỗi tenant (Control Plane tạo khi provision; script không được seed dữ liệu)
	for _, ctx := range []context.Context{ctxTenantA, ctxTenantB} {
		if err := repo.CreateUser(ctx, &domain.UserAccount{ID: "USR-ADMIN-01", Username: "admin", Password: "pass123", Role: "TENANT_ADMIN", MFACode: "654321"}); err != nil {
			t.Fatal(err)
		}
	}

	// 1. Tenant A đăng nhập chỉ cần user/pass (Standard Business)
	sessionA, err := dispatcher.Dispatch(ctxTenantA, command.LoginCommand{
		Username: "admin",
		Password: "pass123",
	})
	if err != nil {
		t.Fatalf("Tenant A standard login failed: %v", err)
	}
	assertSession(t, secret, sessionA, "tenant-a", security.SecurityLevelStandard)

	// 2. Tenant B đăng nhập không có MFA code -> Phải bị chặn với ErrMFARequired (MFA Feature Override)
	_, err = dispatcher.Dispatch(ctxTenantB, command.LoginCommand{
		Username: "admin",
		Password: "pass123",
	})
	if !errors.Is(err, domain.ErrMFARequired) {
		t.Fatalf("Tenant B expected ErrMFARequired, got: %v", err)
	}

	// 3. Tenant B bổ sung mã OTP -> Thành công với High Assurance
	sessionB, err := dispatcher.Dispatch(ctxTenantB, command.LoginCommand{
		Username: "admin",
		Password: "pass123",
		MFACode:  "654321",
	})
	if err != nil {
		t.Fatalf("Tenant B MFA login failed: %v", err)
	}
	assertSession(t, secret, sessionB, "tenant-b", security.SecurityLevelMFA)
}

// assertSession kiểm tra JWT hợp lệ và claims khớp với user/tenant trong session
func assertSession(t *testing.T, secret []byte, s *domain.AuthSession, tenantID, level string) {
	t.Helper()
	if s.TokenType != "Bearer" || s.User.SecurityLevel != level || s.User.TenantID != tenantID {
		t.Fatalf("unexpected session: %+v", s)
	}
	claims, err := security.VerifyToken(secret, s.AccessToken, time.Now())
	if err != nil {
		t.Fatalf("issued token does not verify: %v", err)
	}
	if claims.TenantID != tenantID || claims.SecurityLevel != level || claims.Subject != s.User.ID || claims.Username != "admin" {
		t.Fatalf("unexpected claims: %+v", claims)
	}
}
