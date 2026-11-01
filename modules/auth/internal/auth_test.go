package test

import (
	"context"
	"errors"
	"testing"

	"github.com/dudangphuoc/AegisKernel/core/contract"
	"github.com/dudangphuoc/AegisKernel/modules/auth/internal/adapter/storage"
	"github.com/dudangphuoc/AegisKernel/modules/auth/internal/business/command"
	"github.com/dudangphuoc/AegisKernel/modules/auth/internal/domain"
	mfasecurity "github.com/dudangphuoc/AegisKernel/modules/auth/internal/features/mfa_security"
	"github.com/dudangphuoc/AegisKernel/sdk/cqrs"
	"github.com/dudangphuoc/AegisKernel/sdk/multitenant"
)

func TestAuthModule_MultiTenantFeatureOverride(t *testing.T) {
	repo := storage.NewPostgresAuthRepo()
	stdLoginHandler := command.NewLoginHandler(repo)
	mfaLoginOverride := mfasecurity.NewMFALoginOverrideHandler(repo)

	dispatcher := cqrs.NewFeatureDispatcher[command.LoginCommand, *domain.AuthSession]("login", stdLoginHandler.Handle).
		RegisterOverride("mfa_security", mfaLoginOverride.Handle)

	// Context Tenant A: mfa_security = false
	ctxTenantA := multitenant.WithTenant(context.Background(), contract.TenantRequestContext{
		TenantID:         "tenant-a",
		ConnectionString: "postgres://postgres:postgres@localhost:5432/auth_tenant_a?sslmode=disable",
		Features:         map[string]bool{"mfa_security": false},
	})

	// Context Tenant B: mfa_security = true
	ctxTenantB := multitenant.WithTenant(context.Background(), contract.TenantRequestContext{
		TenantID:         "tenant-b",
		ConnectionString: "postgres://postgres:postgres@localhost:5432/auth_tenant_b?sslmode=disable",
		Features:         map[string]bool{"mfa_security": true},
	})

	// 1. Tenant A đăng nhập chỉ cần user/pass (Standard Business)
	sessionA, err := dispatcher.Dispatch(ctxTenantA, command.LoginCommand{
		Username: "admin",
		Password: "pass123",
	})
	if err != nil {
		t.Fatalf("Tenant A standard login failed: %v", err)
	}
	if sessionA.AuthFlowUsed != "business:standard_login" || sessionA.SecurityLevel != "STANDARD" {
		t.Fatalf("Tenant A expected standard login flow, got %s", sessionA.AuthFlowUsed)
	}

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
	if sessionB.AuthFlowUsed != "feature:mfa_security (override)" || sessionB.SecurityLevel != "MFA_HIGH_ASSURANCE" {
		t.Fatalf("Tenant B expected MFA high assurance flow, got %s", sessionB.AuthFlowUsed)
	}
}
