package employeedirectory

import (
	"context"

	"github.com/dudangphuoc/AegisKernel/modules/parties/internal/domain"
)

type DepartmentSummary struct {
	Department string   `json:"department"`
	HeadCount  int      `json:"head_count"`
	Employees  []string `json:"employees"` // Danh sách Họ tên nhân viên
}

type EmployeeDirectoryResponse struct {
	TotalEmployees int                 `json:"total_employees"`
	Departments    []DepartmentSummary `json:"departments"`
}

type EmployeeDirectoryQueryHandler struct {
	repo domain.PartyRepository
}

func NewEmployeeDirectoryQueryHandler(repo domain.PartyRepository) *EmployeeDirectoryQueryHandler {
	return &EmployeeDirectoryQueryHandler{repo: repo}
}

// Handle tổng hợp sơ đồ tổ chức nhân sự theo phòng ban cho Tenant
func (h *EmployeeDirectoryQueryHandler) Handle(ctx context.Context) (*EmployeeDirectoryResponse, error) {
	employees, err := h.repo.List(ctx, string(domain.PartyTypeEmployee))
	if err != nil {
		return nil, err
	}

	depMap := make(map[string][]string)
	for _, emp := range employees {
		dept := "Unassigned"
		if emp.Employee != nil && emp.Employee.Department != "" {
			dept = emp.Employee.Department
		}
		depMap[dept] = append(depMap[dept], emp.FullName+" ("+emp.Identity.Number+")")
	}

	deptSummaries := make([]DepartmentSummary, 0, len(depMap))
	for dept, members := range depMap {
		deptSummaries = append(deptSummaries, DepartmentSummary{
			Department: dept,
			HeadCount:  len(members),
			Employees:  members,
		})
	}

	return &EmployeeDirectoryResponse{
		TotalEmployees: len(employees),
		Departments:    deptSummaries,
	}, nil
}

