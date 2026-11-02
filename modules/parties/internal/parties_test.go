package test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/dudangphuoc/aegis-kernel/core/contract"
	partiesdb "github.com/dudangphuoc/aegis-kernel/modules/parties/db"
	"github.com/dudangphuoc/aegis-kernel/modules/parties/internal/adapter/storage"
	"github.com/dudangphuoc/aegis-kernel/modules/parties/internal/business/command"
	"github.com/dudangphuoc/aegis-kernel/modules/parties/internal/business/query"
	"github.com/dudangphuoc/aegis-kernel/modules/parties/internal/domain"
	ekycverification "github.com/dudangphuoc/aegis-kernel/modules/parties/internal/features/ekyc_verification"
	employeedirectory "github.com/dudangphuoc/aegis-kernel/modules/parties/internal/features/employee_directory"
	"github.com/dudangphuoc/aegis-kernel/sdk/cqrs"
	"github.com/dudangphuoc/aegis-kernel/sdk/multitenant"
	"github.com/dudangphuoc/aegis-kernel/sdk/tenantdb"
	"github.com/dudangphuoc/aegis-kernel/sdk/tenantdb/tenanttest"
)

// tenantDB dựng DB tenant name với schema biz_parties, trả DSN runtime
func tenantDB(t *testing.T, name string) string {
	t.Helper()
	pkg, err := partiesdb.Package()
	if err != nil {
		t.Fatal(err)
	}
	_, dsn := tenanttest.Tenant(t, name, pkg)
	return dsn
}

func newManager(t *testing.T) *tenantdb.Manager {
	dbm := tenantdb.NewManager("biz_parties")
	t.Cleanup(func() { _ = dbm.Close(context.Background()) })
	return dbm
}

func TestPartiesModule_MultiTenantIsolationAndEKYCOverride(t *testing.T) {
	dsnA, dsnB := tenantDB(t, "tenant_test_a"), tenantDB(t, "tenant_test_b")
	repo := storage.NewPostgresPartyRepo(newManager(t))
	stdHandler := command.NewRegisterPartyHandler(repo)
	ekycOverride := ekycverification.NewEKYCRegisterOverrideHandler(repo)

	dispatcher := cqrs.NewFeatureDispatcher[command.RegisterPartyCommand, *domain.Party]("register_party", stdHandler.Handle).
		RegisterOverride("ekyc_verification", ekycOverride.Handle)

	queryHandler := query.NewFindByIdentityHandler(repo)

	// Context Tenant A: ekyc_verification = false (Chấp nhận CMND 9 số)
	ctxTenantA := multitenant.WithTenant(context.Background(), contract.TenantRequestContext{
		TenantID:         "tenant-a",
		ConnectionString: dsnA,
		Features:         map[string]bool{"ekyc_verification": false, "employee_directory": true},
	})

	// Context Tenant B: ekyc_verification = true (Bắt buộc CCCD 12 số)
	ctxTenantB := multitenant.WithTenant(context.Background(), contract.TenantRequestContext{
		TenantID:         "tenant-b",
		ConnectionString: dsnB,
		Features:         map[string]bool{"ekyc_verification": true, "employee_directory": false},
	})

	// 1. Tenant A đăng ký khách hàng dùng CMND 9 số cũ -> Thành công
	cmndNumber := fmt.Sprintf("123%06d", time.Now().UnixNano()%1000000)
	partyA, err := dispatcher.Dispatch(ctxTenantA, command.RegisterPartyCommand{
		Type:     domain.PartyTypeCustomer,
		FullName: "Trần Thị Khách Hàng (Tenant A)",
		Email:    "customerA@alpha.com",
		Phone:    "0911222333",
		Identity: domain.IdentityDocument{
			Type:        domain.IdentityTypeCMND,
			Number:      cmndNumber, // 9 chữ số
			IssuedDate:  "2018-05-10",
			IssuedPlace: "CA Hà Nội",
		},
	})
	if err != nil {
		t.Fatalf("Tenant A register with CMND failed: %v", err)
	}
	if partyA.ProcessedBy != "business:standard_register_party" || partyA.Identity.VerificationStatus != "BASIC_DECLARED" {
		t.Fatalf("Tenant A expected standard registration, got %s", partyA.ProcessedBy)
	}

	// 2. Tenant B cố tình đăng ký bằng CMND 9 số cũ -> Phải bị từ chối bởi eKYC Policy
	_, err = dispatcher.Dispatch(ctxTenantB, command.RegisterPartyCommand{
		Type:     domain.PartyTypeCustomer,
		FullName: "Lê Văn Khách Hàng (Tenant B)",
		Email:    "customerB@beta.com",
		Phone:    "0933444555",
		Identity: domain.IdentityDocument{
			Type:   domain.IdentityTypeCMND,
			Number: "987654321", // 9 chữ số
		},
	})
	if !errors.Is(err, domain.ErrEKYCRequiresCCCD12Digits) {
		t.Fatalf("Tenant B expected ErrEKYCRequiresCCCD12Digits, got: %v", err)
	}

	// 3. Tenant B đăng ký với CCCD 12 số -> Thành công với tem EKYC_CHIP_VERIFIED
	cccdNumber := fmt.Sprintf("079%09d", time.Now().UnixNano()%1000000000)
	partyB, err := dispatcher.Dispatch(ctxTenantB, command.RegisterPartyCommand{
		Type:     domain.PartyTypeEmployee,
		FullName: "Phạm Văn Kỹ Sư (Tenant B)",
		Email:    "engineer@beta.com",
		Phone:    "0977888999",
		Identity: domain.IdentityDocument{
			Type:        domain.IdentityTypeCCCD,
			Number:      cccdNumber, // 12 chữ số
			IssuedDate:  "2023-01-15",
			IssuedPlace: "Cục CSQLHC về TTXH",
		},
		Employee: &domain.EmployeeProfile{
			EmployeeCode: "EMP-B-99",
			Department:   "Treasury",
			Position:     "Analyst",
		},
	})
	if err != nil {
		t.Fatalf("Tenant B register with CCCD failed: %v", err)
	}
	if partyB.ProcessedBy != "feature:ekyc_verification (override)" || partyB.Identity.VerificationStatus != "EKYC_CHIP_VERIFIED" {
		t.Fatalf("Tenant B expected eKYC override flow, got %s", partyB.ProcessedBy)
	}

	// 4. Kiểm tra cô lập dữ liệu (Database Isolation):
	// Tìm số CCCD của Tenant B bên trong vùng DB của Tenant A -> Phải báo Not Found
	_, err = queryHandler.Handle(ctxTenantA, cccdNumber)
	if !errors.Is(err, domain.ErrPartyNotFound) {
		t.Fatalf("Data leak: Tenant A was able to read identity of Tenant B!")
	}

	// Tìm số CCCD bên trong Tenant B -> Thấy chính xác
	foundB, err := queryHandler.Handle(ctxTenantB, cccdNumber)
	if err != nil || foundB.ID != partyB.ID {
		t.Fatalf("Tenant B failed to query its own party: %v", err)
	}
}

func TestPartiesModule_EmployeeDirectoryFeatureExtend(t *testing.T) {
	dsnA := tenantDB(t, "tenant_test_a")
	repo := storage.NewPostgresPartyRepo(newManager(t))
	directoryHandler := employeedirectory.NewEmployeeDirectoryQueryHandler(repo)

	ctxTenantA := multitenant.WithTenant(context.Background(), contract.TenantRequestContext{
		TenantID:         "tenant-a",
		ConnectionString: dsnA,
		Features:         map[string]bool{"employee_directory": true},
	})

	// Dữ liệu mẫu không còn nằm trong script (script cấm DML vào bảng đồng bộ): tạo nhân viên qua nghiệp vụ
	if _, err := command.NewRegisterPartyHandler(repo).Handle(ctxTenantA, command.RegisterPartyCommand{
		Type: domain.PartyTypeEmployee, FullName: "Nhân viên mẫu", Email: "staff@alpha.com", Phone: "0900000000",
		Identity: domain.IdentityDocument{Type: domain.IdentityTypeCMND, Number: fmt.Sprintf("9%08d", time.Now().UnixNano()%100000000), IssuedDate: "2020-01-01", IssuedPlace: "HN"},
		Employee: &domain.EmployeeProfile{EmployeeCode: "EMP-T", Department: "IT", Position: "Engineer"},
	}); err != nil {
		t.Fatalf("seed employee: %v", err)
	}

	dir, err := directoryHandler.Handle(ctxTenantA)
	if err != nil {
		t.Fatalf("Failed to query employee directory: %v", err)
	}
	if dir.TotalEmployees < 1 {
		t.Fatalf("Expected at least 1 employee, got %d", dir.TotalEmployees)
	}
}
