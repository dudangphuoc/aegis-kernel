package gateway

import (
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"strings"
)

// ParseTrustedProxies đọc danh sách CIDR/IP proxy tin cậy (VD: "10.0.0.0/8,127.0.0.1")
func ParseTrustedProxies(list string) ([]netip.Prefix, error) {
	var res []netip.Prefix
	for _, item := range strings.Split(list, ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		if p, err := netip.ParsePrefix(item); err == nil {
			res = append(res, p.Masked())
			continue
		}
		addr, err := netip.ParseAddr(item)
		if err != nil {
			return nil, fmt.Errorf("invalid trusted proxy %q", item)
		}
		res = append(res, netip.PrefixFrom(addr.Unmap(), addr.Unmap().BitLen()))
	}
	return res, nil
}

// SetTrustedProxies cấu hình các proxy được phép báo IP client qua X-Forwarded-For / X-Real-IP.
// Gọi trước Handler(). Mặc định không tin proxy nào: IP client = địa chỉ kết nối TCP.
func (gw *APIGateway) SetTrustedProxies(prefixes []netip.Prefix) {
	gw.trustedProxies = prefixes
}

// clientIPMiddleware thay middleware.RealIP (tin header của bất kỳ ai, khiến client_ip trong audit giả mạo được).
// Chỉ khi kết nối đến từ proxy tin cậy mới đọc X-Forwarded-For, lấy IP đầu tiên tính từ phải không thuộc proxy tin cậy.
func (gw *APIGateway) clientIPMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if ip, ok := gw.resolveClientIP(r); ok {
			r.RemoteAddr = ip
		}
		next.ServeHTTP(w, r)
	})
}

func (gw *APIGateway) resolveClientIP(r *http.Request) (string, bool) {
	peer, ok := parseIP(r.RemoteAddr)
	if !ok || !gw.isTrustedProxy(peer) {
		return "", false
	}

	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		hops := strings.Split(xff, ",")
		for i := len(hops) - 1; i >= 0; i-- {
			ip, ok := parseIP(strings.TrimSpace(hops[i]))
			if !ok {
				return "", false // header hỏng: giữ địa chỉ kết nối
			}
			if !gw.isTrustedProxy(ip) || i == 0 {
				return ip.String(), true
			}
		}
	}
	if ip, ok := parseIP(strings.TrimSpace(r.Header.Get("X-Real-IP"))); ok {
		return ip.String(), true
	}
	return "", false
}

func (gw *APIGateway) isTrustedProxy(ip netip.Addr) bool {
	for _, p := range gw.trustedProxies {
		if p.Contains(ip) {
			return true
		}
	}
	return false
}

// parseIP nhận "ip", "ip:port" hoặc "[ipv6]:port"
func parseIP(s string) (netip.Addr, bool) {
	if s == "" {
		return netip.Addr{}, false
	}
	if host, _, err := net.SplitHostPort(s); err == nil {
		s = host
	}
	addr, err := netip.ParseAddr(s)
	if err != nil {
		return netip.Addr{}, false
	}
	return addr.Unmap(), true
}
