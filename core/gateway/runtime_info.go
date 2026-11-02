package gateway

import (
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
)

type moduleRuntimeInfo struct {
	Version          string          `json:"version"`
	EnabledFeatures  []string        `json:"enabled_features"`
	FeatureRoutes    map[string]bool `json:"feature_routes"` // "METHOD path" -> route có đang mở cho tenant
	MaskedDatasource string          `json:"masked_datasource"`
}

// handleRuntimeInfo trả thông tin chẩn đoán của 1 tenant (feature, route, DSN đã che mật khẩu).
// Chỉ dành cho Control Plane admin (cp session) hoặc TENANT_ADMIN của chính tenant đó (JWT).
func (gw *APIGateway) handleRuntimeInfo(w http.ResponseWriter, req *http.Request) {
	tenantID := strings.TrimSpace(req.Header.Get("X-Tenant-ID"))
	if tenantID == "" {
		tenantID = strings.TrimSpace(req.URL.Query().Get("tenant_id"))
	}
	if tenantID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing required header 'X-Tenant-ID'"})
		return
	}

	if status, msg := gw.authorizeDiagnostics(req, tenantID); status != 0 {
		writeJSON(w, status, map[string]string{"error": msg})
		return
	}

	modules := make(map[string]moduleRuntimeInfo)
	for _, loaded := range gw.manager.GetLoadedModules() {
		name := loaded.Descriptor.Name
		resolved, err := gw.cpStore.ResolveTenantRequestContext(req.Context(), tenantID, name)
		if err != nil {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "tenant not found or not active in control plane", "tenant_id": tenantID})
			return
		}
		enabled := make([]string, 0, len(resolved.Features))
		for f, on := range resolved.Features {
			if on {
				enabled = append(enabled, f)
			}
		}
		sort.Strings(enabled)
		featureRoutes := make(map[string]bool)
		for _, r := range loaded.Routes {
			if r.FeatureFlag != "" {
				featureRoutes[r.Method+" "+r.Path] = resolved.Features[r.FeatureFlag]
			}
		}
		modules[name] = moduleRuntimeInfo{
			Version:          loaded.Descriptor.Version,
			EnabledFeatures:  enabled,
			FeatureRoutes:    featureRoutes,
			MaskedDatasource: maskDSN(resolved.ConnectionString),
		}
	}

	host, _ := os.Hostname()
	topology := "direct (no pooler)"
	nodes := gw.cpStore.PoolerNodes()
	if len(nodes) > 0 {
		topology = "pgbouncer-cluster (transaction_mode)"
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"tenant_id":       tenantID,
		"gateway_node":    host,
		"execution_mode":  "go-plugin-rpc",
		"pooler_topology": topology,
		"pooler_nodes":    nodes,
		"modules":         modules,
	})
}

// authorizeDiagnostics trả status != 0 nếu caller không có quyền xem chẩn đoán của tenant
func (gw *APIGateway) authorizeDiagnostics(req *http.Request, tenantID string) (int, string) {
	token, err := bearerToken(req)
	if err != nil {
		return http.StatusUnauthorized, "authentication required (control plane token or tenant admin JWT)"
	}
	// 1. Control Plane session token (dashboard admin)
	if _, err := gw.cpStore.ValidateSession(req.Context(), token); err == nil {
		return 0, ""
	}
	// 2. JWT của TENANT_ADMIN thuộc đúng tenant
	claims, status, err := gw.verifyRequestJWT(req, tenantID)
	if err != nil {
		return status, err.Error()
	}
	if claims.Role != "TENANT_ADMIN" {
		return http.StatusForbidden, "tenant admin role required"
	}
	return 0, ""
}

// maskDSN ẩn mật khẩu trong connection string dạng URL
func maskDSN(dsn string) string {
	u, err := url.Parse(dsn)
	if err != nil || u.Host == "" {
		return "***"
	}
	if u.User != nil {
		if _, hasPwd := u.User.Password(); hasPwd {
			u.User = url.UserPassword(u.User.Username(), "***")
		}
	}
	s := u.String()
	return strings.Replace(s, "%2A%2A%2A", "***", 1)
}
