package gateway

import (
	"errors"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/dudangphuoc/aegis-kernel/core/contract"
	"github.com/dudangphuoc/aegis-kernel/sdk/security"
	"github.com/go-chi/chi/v5/middleware"
)

var errMissingBearer = errors.New("missing bearer token")

// trustedHeaders là các header chỉ Gateway được phép đặt; giá trị từ client bị loại bỏ
var trustedHeaders = map[string]bool{
	contract.HeaderActorID:       true,
	contract.HeaderActorUsername: true,
	contract.HeaderActorRole:     true,
	contract.HeaderActorSecLevel: true,
	contract.HeaderClientIP:      true,
	contract.HeaderUserAgent:     true,
	contract.HeaderRequestID:     true,
	"Authorization":              true, // Module không cần raw token, chỉ nhận Actor đã xác thực
}

// bearerToken trích chuỗi token từ header Authorization
func bearerToken(req *http.Request) (string, error) {
	h := strings.TrimSpace(req.Header.Get("Authorization"))
	if h == "" {
		return "", errMissingBearer
	}
	scheme, token, ok := strings.Cut(h, " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") || strings.TrimSpace(token) == "" {
		return "", errors.New("authorization header must be 'Bearer <token>'")
	}
	return strings.TrimSpace(token), nil
}

// verifyRequestJWT xác thực JWT (nếu có) và đối chiếu tenant.
// Trả về claims = nil khi request không mang token.
func (gw *APIGateway) verifyRequestJWT(req *http.Request, tenantID string) (*security.TokenClaims, int, error) {
	token, err := bearerToken(req)
	if errors.Is(err, errMissingBearer) {
		return nil, 0, nil
	}
	if err != nil {
		return nil, http.StatusUnauthorized, err
	}
	claims, err := security.VerifyToken(gw.jwtSecret, token, time.Now())
	if err != nil {
		return nil, http.StatusUnauthorized, err
	}
	if claims.TenantID != tenantID {
		return nil, http.StatusForbidden, errors.New("token tenant does not match X-Tenant-ID")
	}
	return claims, 0, nil
}

// buildModuleHeaders sao chép header client (trừ header tin cậy) rồi gắn Actor/Client context
func buildModuleHeaders(req *http.Request, claims *security.TokenClaims) map[string]string {
	headers := make(map[string]string)
	for k, v := range req.Header {
		if len(v) > 0 && !trustedHeaders[http.CanonicalHeaderKey(k)] {
			headers[k] = v[0]
		}
	}

	if ip, _, err := net.SplitHostPort(req.RemoteAddr); err == nil {
		headers[contract.HeaderClientIP] = ip
	} else if req.RemoteAddr != "" {
		headers[contract.HeaderClientIP] = req.RemoteAddr // middleware.RealIP có thể đã thay bằng IP thuần
	}
	if ua := req.UserAgent(); ua != "" {
		headers[contract.HeaderUserAgent] = ua
	}
	if id := middleware.GetReqID(req.Context()); id != "" {
		headers[contract.HeaderRequestID] = id
	}
	if claims != nil {
		headers[contract.HeaderActorID] = claims.Subject
		headers[contract.HeaderActorUsername] = claims.Username
		headers[contract.HeaderActorRole] = claims.Role
		headers[contract.HeaderActorSecLevel] = claims.SecurityLevel
	}
	return headers
}
