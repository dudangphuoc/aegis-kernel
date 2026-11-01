package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/dudangphuoc/AegisKernel/core/contract"
	"github.com/dudangphuoc/AegisKernel/modules/parties/internal/adapter/storage"
	"github.com/dudangphuoc/AegisKernel/modules/parties/internal/business/command"
	"github.com/dudangphuoc/AegisKernel/modules/parties/internal/business/query"
	"github.com/dudangphuoc/AegisKernel/modules/parties/internal/domain"
	ekycverification "github.com/dudangphuoc/AegisKernel/modules/parties/internal/features/ekyc_verification"
	employeedirectory "github.com/dudangphuoc/AegisKernel/modules/parties/internal/features/employee_directory"
	"github.com/dudangphuoc/AegisKernel/sdk"
	"github.com/dudangphuoc/AegisKernel/sdk/cqrs"
)

func main() {
	app := sdk.NewModuleApp(contract.ModuleDescriptor{
		Name:        "parties",
		Version:     "1.0.0",
		Description: "Module quản lý đối tác (Employee, Customer, Identity CMND/CCCD)",
	})

	// 1. Dependency Injection - PostgreSQL Repository
	partyRepo := storage.NewPostgresPartyRepo()

	// 2. CQRS Handlers
	stdRegisterHandler := command.NewRegisterPartyHandler(partyRepo)
	ekycOverrideHandler := ekycverification.NewEKYCRegisterOverrideHandler(partyRepo)

	// Gắn kết Base Business và Feature Override qua FeatureDispatcher
	registerDispatcher := cqrs.NewFeatureDispatcher[command.RegisterPartyCommand, *domain.Party]("register_party_dispatcher", stdRegisterHandler.Handle).
		RegisterOverride("ekyc_verification", ekycOverrideHandler.Handle)

	listPartiesHandler := query.NewListPartiesHandler(partyRepo)
	findByIdentityHandler := query.NewFindByIdentityHandler(partyRepo)
	directoryHandler := employeedirectory.NewEmployeeDirectoryQueryHandler(partyRepo)

	// 3. Đăng ký các Routes của Module
	// Route 1: Đăng ký đối tác (Tự động chuyển đổi Standard CMND hoặc eKYC CCCD 12 số theo Tenant)
	app.RegisterRoute(contract.RouteDefinition{
		Method:         "POST",
		Path:           "/api/v1/parties",
		Summary:        "Đăng ký đối tác mới (Nhân viên hoặc Khách hàng kèm CMND/CCCD)",
		RequireAuth:    true,
		OptionalTenant: false,
	}, func(ctx context.Context, req contract.InvocationRequest) (contract.InvocationResponse, error) {
		var cmd command.RegisterPartyCommand
		if err := json.Unmarshal(req.Body, &cmd); err != nil {
			return sdk.JSONResponse(http.StatusBadRequest, map[string]string{"error": "invalid json body"})
		}

		party, err := registerDispatcher.Dispatch(ctx, cmd)
		if err != nil {
			status := http.StatusBadRequest
			if errors.Is(err, domain.ErrDuplicateIdentity) {
				status = http.StatusConflict
			}
			return sdk.JSONResponse(status, map[string]string{"error": err.Error()})
		}
		return sdk.JSONResponse(http.StatusCreated, party)
	})

	// Route 2: Danh sách đối tác của Tenant (hỗ trợ query ?type=EMPLOYEE hoặc CUSTOMER)
	app.RegisterRoute(contract.RouteDefinition{
		Method:         "GET",
		Path:           "/api/v1/parties",
		Summary:        "Xem danh sách đối tác của Tenant (lọc qua query ?type=EMPLOYEE|CUSTOMER)",
		RequireAuth:    false,
		OptionalTenant: false,
	}, func(ctx context.Context, req contract.InvocationRequest) (contract.InvocationResponse, error) {
		filterType := req.QueryParams["type"]
		list, err := listPartiesHandler.Handle(ctx, filterType)
		if err != nil {
			return sdk.JSONResponse(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		}
		return sdk.JSONResponse(http.StatusOK, map[string]any{
			"total": len(list),
			"data":  list,
		})
	})

	// Route 3: Tra cứu đối tác theo số CMND / CCCD
	app.RegisterRoute(contract.RouteDefinition{
		Method:         "GET",
		Path:           "/api/v1/parties/identity",
		Summary:        "Tra cứu hồ sơ đối tác qua số CMND hoặc CCCD (?number=...)",
		RequireAuth:    false,
		OptionalTenant: false,
	}, func(ctx context.Context, req contract.InvocationRequest) (contract.InvocationResponse, error) {
		number := req.QueryParams["number"]
		if number == "" {
			return sdk.JSONResponse(http.StatusBadRequest, map[string]string{"error": "query parameter 'number' is required"})
		}

		party, err := findByIdentityHandler.Handle(ctx, number)
		if err != nil {
			if errors.Is(err, domain.ErrPartyNotFound) {
				return sdk.JSONResponse(http.StatusNotFound, map[string]string{"error": "party not found with provided identity number"})
			}
			return sdk.JSONResponse(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		}
		return sdk.JSONResponse(http.StatusOK, party)
	})

	// Route 4: [Feature Extend] Sơ đồ tổ chức nhân sự theo phòng ban
	app.RegisterRoute(contract.RouteDefinition{
		Method:         "GET",
		Path:           "/api/v1/parties/employees/directory",
		Summary:        "Sơ đồ tổ chức nhân sự theo phòng ban (Chỉ mở khi Tenant bật feature employee_directory)",
		RequireAuth:    true,
		OptionalTenant: false,
		FeatureFlag:    "employee_directory",
	}, func(ctx context.Context, req contract.InvocationRequest) (contract.InvocationResponse, error) {
		summary, err := directoryHandler.Handle(ctx)
		if err != nil {
			return sdk.JSONResponse(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		}
		return sdk.JSONResponse(http.StatusOK, summary)
	})

	sdk.Serve(app)
}
