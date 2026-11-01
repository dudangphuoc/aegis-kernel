package controlplane

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
)

type APIHandler struct {
	store            *Store
	migrationService *MigrationService
}

func NewAPIHandler(store *Store, migService *MigrationService) *APIHandler {
	return &APIHandler{
		store:            store,
		migrationService: migService,
	}
}

// RegisterRoutes đăng ký toàn bộ cụm route quản trị Control Plane lên Chi Router
func (h *APIHandler) RegisterRoutes(r chi.Router) {
	r.Route("/api/cp", func(r chi.Router) {
		// Public endpoint: Đăng nhập Control Plane Admin
		r.Post("/auth/login", h.handleLogin)

		// Protected endpoints: Yêu cầu Bearer Session Token hợp lệ
		r.Group(func(r chi.Router) {
			r.Use(h.authMiddleware)

			r.Get("/auth/me", h.handleGetMe)
			r.Get("/overview", h.handleOverview)

			// Quản trị User của Control Plane
			r.Route("/users", func(r chi.Router) {
				r.Get("/", h.handleListUsers)
				r.Post("/", h.handleCreateUser)
				r.Delete("/{id}", h.handleDeleteUser)
			})

			// Quản trị Tenants & Module Features
			r.Route("/tenants", func(r chi.Router) {
				r.Get("/", h.handleGetTenants)
				r.Post("/", h.handleProvisionTenant)
				r.Delete("/{tenant_id}", h.handleDeleteTenant)
				r.Put("/{tenant_id}/modules/{module_name}", h.handleUpdateTenantModule)
			})

			// Quản trị Multi-Tenant Database Migrations
			r.Route("/migrations", func(r chi.Router) {
				r.Get("/status", h.handleGetMigrationStatuses)
				r.Post("/run", h.handleRunMigration)
				r.Post("/run-all", h.handleBatchMigrateAll)
			})
		})
	})
}

type ctxUserKey struct{}

// authMiddleware là Chi-compatible standard middleware (func(http.Handler) http.Handler)
func (h *APIHandler) authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authHeader := r.Header.Get("Authorization")
		token := strings.TrimPrefix(authHeader, "Bearer ")
		token = strings.TrimSpace(token)
		if token == "" {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "missing or invalid authorization header"})
			return
		}

		user, err := h.store.ValidateSession(r.Context(), token)
		if err != nil {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "session expired or invalid"})
			return
		}

		ctx := context.WithValue(r.Context(), ctxUserKey{}, user)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

func (h *APIHandler) handleLogin(w http.ResponseWriter, r *http.Request) {
	var req loginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json body"})
		return
	}

	user, token, err := h.store.AuthenticateUser(r.Context(), req.Username, req.Password)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"token": token,
		"user":  user,
	})
}

func (h *APIHandler) handleGetMe(w http.ResponseWriter, r *http.Request) {
	user := r.Context().Value(ctxUserKey{}).(*CPUser)
	writeJSON(w, http.StatusOK, map[string]any{"user": user})
}

func (h *APIHandler) handleListUsers(w http.ResponseWriter, r *http.Request) {
	users, err := h.store.ListUsers(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"users": users})
}

type createUserRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
	FullName string `json:"full_name"`
	Email    string `json:"email"`
	Role     string `json:"role"`
}

func (h *APIHandler) handleCreateUser(w http.ResponseWriter, r *http.Request) {
	var req createUserRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json body"})
		return
	}

	user, err := h.store.CreateUser(r.Context(), req.Username, req.Password, req.FullName, req.Email, req.Role)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"user": user})
}

func (h *APIHandler) handleDeleteUser(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if err := h.store.DeleteUser(r.Context(), id); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"message": "user deleted successfully"})
}

func (h *APIHandler) handleOverview(w http.ResponseWriter, r *http.Request) {
	tenants, _ := h.store.GetTenants(r.Context())
	statuses, _ := h.migrationService.GetAllTenantMigrationStatuses(r.Context())

	pendingCount := 0
	for _, s := range statuses {
		if s.Status == "PENDING_MIGRATIONS" {
			pendingCount++
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"total_tenants":          len(tenants),
		"total_databases":        len(statuses),
		"pending_migrations_dbs": pendingCount,
		"system_health":          "HEALTHY",
	})
}

func (h *APIHandler) handleGetTenants(w http.ResponseWriter, r *http.Request) {
	tenants, err := h.store.GetTenants(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"tenants": tenants})
}

type provisionTenantRequest struct {
	TenantID        string          `json:"tenant_id"`
	Name            string          `json:"name"`
	Plan            string          `json:"plan"`
	AdminUsername   string          `json:"admin_username"`
	AdminPassword   string          `json:"admin_password"`
	AuthFeatures    map[string]bool `json:"auth_features"`
	PartiesFeatures map[string]bool `json:"parties_features"`
}

func (h *APIHandler) handleProvisionTenant(w http.ResponseWriter, r *http.Request) {
	var req provisionTenantRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json body"})
		return
	}

	req.TenantID = strings.TrimSpace(req.TenantID)
	if req.TenantID == "" || req.Name == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "tenant_id and name are required"})
		return
	}

	err := h.store.ProvisionTenant(
		r.Context(),
		req.TenantID,
		req.Name,
		req.Plan,
		req.AdminUsername,
		req.AdminPassword,
		req.AuthFeatures,
		req.PartiesFeatures,
	)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusCreated, map[string]any{
		"message":   "Tenant provisioned successfully: dedicated databases created, migrations executed, and admin user initialized.",
		"tenant_id": req.TenantID,
	})
}

func (h *APIHandler) handleDeleteTenant(w http.ResponseWriter, r *http.Request) {
	tenantID := chi.URLParam(r, "tenant_id")
	if err := h.store.DeleteTenant(r.Context(), tenantID); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"message": "tenant and dedicated databases dropped successfully"})
}

type updateModuleRequest struct {
	ConnectionString string          `json:"connection_string"`
	Features         map[string]bool `json:"features"`
}

func (h *APIHandler) handleUpdateTenantModule(w http.ResponseWriter, r *http.Request) {
	tenantID := chi.URLParam(r, "tenant_id")
	moduleName := chi.URLParam(r, "module_name")

	var req updateModuleRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json body"})
		return
	}

	if err := h.store.UpdateTenantModuleConfig(r.Context(), tenantID, moduleName, req.ConnectionString, req.Features); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"message":     "Tenant module configuration updated in control plane",
		"tenant_id":   tenantID,
		"module_name": moduleName,
		"features":    req.Features,
	})
}

func (h *APIHandler) handleGetMigrationStatuses(w http.ResponseWriter, r *http.Request) {
	statuses, err := h.migrationService.GetAllTenantMigrationStatuses(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"statuses": statuses})
}

type runMigrationRequest struct {
	TenantID   string `json:"tenant_id"`
	ModuleName string `json:"module_name"`
	Action     string `json:"action"` // "up" or "down"
}

func (h *APIHandler) handleRunMigration(w http.ResponseWriter, r *http.Request) {
	var req runMigrationRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json body"})
		return
	}

	if err := h.migrationService.RunMigrationForTenant(r.Context(), req.TenantID, req.ModuleName, req.Action); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"message":     "Migration executed successfully",
		"tenant_id":   req.TenantID,
		"module_name": req.ModuleName,
		"action":      req.Action,
	})
}

func (h *APIHandler) handleBatchMigrateAll(w http.ResponseWriter, r *http.Request) {
	report, err := h.migrationService.BatchMigrateAllTenants(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"message": "Batch migration completed",
		"results": report,
	})
}

func writeJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}
