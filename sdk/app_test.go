package sdk

import (
	"context"
	"testing"

	"github.com/dudangphuoc/aegis-kernel/core/contract"
	"github.com/dudangphuoc/aegis-kernel/sdk/tenantdb"
)

// Execute chỉ gắn ngữ cảnh request; việc ghi nhận thay đổi thuộc về trigger ở tầng DB
func TestExecuteAttachesAuditRequestContext(t *testing.T) {
	app := NewModuleApp(contract.ModuleDescriptor{Name: "demo"})
	var got *tenantdb.RequestContext
	app.RegisterRoute(contract.RouteDefinition{Method: "POST", Path: "/users"},
		func(ctx context.Context, _ contract.InvocationRequest) (contract.InvocationResponse, error) {
			tenantdb.SetAction(ctx, "CREATE_USER")
			got = tenantdb.FromContext(ctx)
			return JSONResponse(201, nil)
		})

	_, err := app.Execute(context.Background(), contract.InvocationRequest{
		Method: "POST", Path: "/users", Tenant: contract.TenantRequestContext{TenantID: "acme"},
		Headers: map[string]string{
			contract.HeaderActorID: "USR-1", contract.HeaderActorRole: "TENANT_ADMIN",
			contract.HeaderClientIP: "10.0.0.7", contract.HeaderRequestID: "req-1",
			"X-Client-App": "mobile", "X-Unlisted": "ignored",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || got.TenantID != "acme" || got.Actor.ID != "USR-1" || got.Client.IP != "10.0.0.7" ||
		got.RequestID != "req-1" || got.SourceModule != "demo" || got.Action != "CREATE_USER" {
		t.Fatalf("unexpected request context: %+v", got)
	}
	if len(got.Client.Details) != 1 || got.Client.Details["app"] != "mobile" {
		t.Fatalf("unexpected client details: %v", got.Client.Details)
	}

	// Request không có JWT -> ANONYMOUS
	app.RegisterRoute(contract.RouteDefinition{Method: "GET", Path: "/anon"},
		func(ctx context.Context, _ contract.InvocationRequest) (contract.InvocationResponse, error) {
			got = tenantdb.FromContext(ctx)
			return JSONResponse(200, nil)
		})
	_, _ = app.Execute(context.Background(), contract.InvocationRequest{Method: "GET", Path: "/anon"})
	if got.Actor.ID != "ANONYMOUS" {
		t.Fatalf("expected ANONYMOUS actor, got %q", got.Actor.ID)
	}
}
