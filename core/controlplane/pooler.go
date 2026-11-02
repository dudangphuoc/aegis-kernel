package controlplane

import (
	"hash/fnv"
	"net"
	"net/url"
	"strings"
)

// ConfigureRuntimePooler bật định tuyến Data Plane qua PgBouncer × Node (VD: "localhost:6432,localhost:6433").
// Không gọi (hoặc danh sách rỗng) thì module kết nối thẳng PostgreSQL như trước.
func (s *Store) ConfigureRuntimePooler(nodes []string) {
	var clean []string
	for _, n := range nodes {
		if n = strings.TrimSpace(n); n != "" {
			clean = append(clean, n)
		}
	}
	s.mu.Lock()
	s.poolerNodes = clean
	s.mu.Unlock()
}

// PoolerNodes trả danh sách node PgBouncer đang dùng
func (s *Store) PoolerNodes() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]string(nil), s.poolerNodes...)
}

// BuildAdminConnectionString là DSN trực tiếp tới PostgreSQL (:5432) — dùng cho DDL, Migrator
// (pg_advisory_lock theo session) và là giá trị lưu trong cp_tenant_modules.
func (s *Store) BuildAdminConnectionString(dbName string) string {
	u := url.URL{
		Scheme:   "postgres",
		User:     url.UserPassword(s.baseUser, s.basePass),
		Host:     net.JoinHostPort(s.baseHost, s.basePort),
		Path:     "/" + dbName,
		RawQuery: url.Values{"sslmode": {s.baseSSLMode}}.Encode(),
	}
	return u.String()
}

// BuildRuntimeConnectionString chuyển DSN admin của tenant sang node PgBouncer (transaction pooling)
// kèm binary_parameters=yes. Mỗi tenant được gắn cố định vào 1 node theo hash TenantID để phân tải.
// DSN tùy chỉnh trỏ tới server khác (không phải PostgreSQL của Control Plane) được giữ nguyên.
func (s *Store) BuildRuntimeConnectionString(tenantID, adminDSN string) string {
	nodes := s.PoolerNodes()
	if len(nodes) == 0 {
		return adminDSN
	}
	u, err := url.Parse(adminDSN)
	if err != nil || u.Scheme == "" || !s.isAdminHost(u.Host) {
		return adminDSN
	}
	h := fnv.New32a()
	_, _ = h.Write([]byte(tenantID))
	u.Host = nodes[h.Sum32()%uint32(len(nodes))]
	q := u.Query()
	q.Set("binary_parameters", "yes")
	u.RawQuery = q.Encode()
	return u.String()
}

func (s *Store) isAdminHost(hostport string) bool {
	host, port, err := net.SplitHostPort(hostport)
	if err != nil {
		host, port = hostport, "5432"
	}
	return strings.EqualFold(host, s.baseHost) && port == s.basePort
}
