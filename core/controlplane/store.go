package controlplane

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"strings"
	"sync"
	"time"

	_ "github.com/lib/pq"
	"github.com/dudangphuoc/AegisKernel/core/contract"
	authmigrations "github.com/dudangphuoc/AegisKernel/modules/auth/migrations"
	partiesmigrations "github.com/dudangphuoc/AegisKernel/modules/parties/migrations"
	"github.com/dudangphuoc/AegisKernel/sdk/migrator"
	"golang.org/x/crypto/bcrypt"
)

type CPUser struct {
	ID        string    `json:"id"`
	Username  string    `json:"username"`
	FullName  string    `json:"full_name"`
	Email     string    `json:"email"`
	Role      string    `json:"role"` // SUPER_ADMIN, OPERATOR
	CreatedAt time.Time `json:"created_at"`
}

type TenantSetting struct {
	Name        string                                 `json:"name"`
	Plan        string                                 `json:"plan"`
	GlobalFlags map[string]bool                        `json:"global_flags"`
	Modules     map[string]contract.TenantModuleConfig `json:"modules"`
}

type Store struct {
	mu             sync.RWMutex
	db             *sql.DB
	baseHost       string
	basePort       string
	baseUser       string
	basePass       string
	baseSSLMode    string
	logger         *slog.Logger
	migratorRunner *migrator.Runner
	globalFeatures map[string]bool
}

func NewStore(dbURL string, logger *slog.Logger) (*Store, error) {
	db, err := sql.Open("postgres", dbURL)
	if err != nil {
		return nil, fmt.Errorf("failed to open cp_db: %w", err)
	}
	db.SetMaxOpenConns(20)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(time.Hour)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		return nil, fmt.Errorf("failed to ping cp_db: %w", err)
	}

	// Parse base DB connection details để tự động sinh dynamic connection string cho các tenant
	u, err := url.Parse(dbURL)
	host := "localhost"
	port := "5432"
	user := "postgres"
	pass := "postgres"
	sslmode := "disable"

	if err == nil {
		if u.Hostname() != "" {
			host = u.Hostname()
		}
		if u.Port() != "" {
			port = u.Port()
		}
		if u.User != nil {
			user = u.User.Username()
			if p, ok := u.User.Password(); ok {
				pass = p
			}
		}
		if sm := u.Query().Get("sslmode"); sm != "" {
			sslmode = sm
		}
	}

	store := &Store{
		db:             db,
		baseHost:       host,
		basePort:       port,
		baseUser:       user,
		basePass:       pass,
		baseSSLMode:    sslmode,
		logger:         logger,
		migratorRunner: migrator.NewRunner(),
		globalFeatures: map[string]bool{"enable_global_audit": true},
	}

	if err := store.initSchema(ctx); err != nil {
		return nil, fmt.Errorf("failed to init control plane schema: %w", err)
	}

	return store, nil
}

func (s *Store) initSchema(ctx context.Context) error {
	queries := []string{
		// Bảng quản trị viên Control Plane
		`CREATE TABLE IF NOT EXISTS cp_users (
			id VARCHAR(64) PRIMARY KEY,
			username VARCHAR(128) NOT NULL UNIQUE,
			password_hash VARCHAR(255) NOT NULL,
			full_name VARCHAR(255) NOT NULL,
			email VARCHAR(255) NOT NULL,
			role VARCHAR(64) NOT NULL DEFAULT 'SUPER_ADMIN',
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		);`,
		// Bảng phiên đăng nhập của CP Admin
		`CREATE TABLE IF NOT EXISTS cp_sessions (
			token VARCHAR(128) PRIMARY KEY,
			user_id VARCHAR(64) NOT NULL REFERENCES cp_users(id) ON DELETE CASCADE,
			expires_at TIMESTAMPTZ NOT NULL,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		);`,
		// Bảng tenants do Control Plane quản lý (Không hardcode dữ liệu demo)
		`CREATE TABLE IF NOT EXISTS cp_tenants (
			tenant_id VARCHAR(64) PRIMARY KEY,
			name VARCHAR(255) NOT NULL,
			plan VARCHAR(64) NOT NULL DEFAULT 'standard',
			global_flags JSONB NOT NULL DEFAULT '{}'::jsonb,
			status VARCHAR(32) NOT NULL DEFAULT 'ACTIVE',
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		);`,
		// Bảng cấu hình dynamic database & features cho từng module của tenant
		`CREATE TABLE IF NOT EXISTS cp_tenant_modules (
			tenant_id VARCHAR(64) NOT NULL REFERENCES cp_tenants(tenant_id) ON DELETE CASCADE,
			module_name VARCHAR(64) NOT NULL,
			connection_string TEXT NOT NULL,
			features JSONB NOT NULL DEFAULT '{}'::jsonb,
			settings JSONB NOT NULL DEFAULT '{}'::jsonb,
			updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			PRIMARY KEY (tenant_id, module_name)
		);`,
	}

	for _, q := range queries {
		if _, err := s.db.ExecContext(ctx, q); err != nil {
			return err
		}
	}

	// Đảm bảo có sẵn 1 user Super Admin khởi tạo cho Control Plane
	var adminCount int
	if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM cp_users`).Scan(&adminCount); err == nil && adminCount == 0 {
		s.logger.Info("seeding default super admin into cp_users")
		hash, _ := bcrypt.GenerateFromPassword([]byte("admin123"), bcrypt.DefaultCost)
		_, _ = s.db.ExecContext(ctx, `
			INSERT INTO cp_users (id, username, password_hash, full_name, email, role)
			VALUES ('USR-CP-ADMIN', 'admin', $1, 'System Administrator', 'admin@controlplane.local', 'SUPER_ADMIN')
			ON CONFLICT DO NOTHING`, string(hash))
	}

	return nil
}

// AuthenticateUser xác thực đăng nhập vào Control Plane Admin Dashboard
func (s *Store) AuthenticateUser(ctx context.Context, username, password string) (*CPUser, string, error) {
	var u CPUser
	var hash string
	query := `SELECT id, username, password_hash, full_name, email, role, created_at FROM cp_users WHERE username = $1`
	err := s.db.QueryRowContext(ctx, query, username).Scan(
		&u.ID, &u.Username, &hash, &u.FullName, &u.Email, &u.Role, &u.CreatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, "", errors.New("invalid username or password")
	}
	if err != nil {
		return nil, "", err
	}

	if err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)); err != nil {
		return nil, "", errors.New("invalid username or password")
	}

	// Tạo session token
	token := fmt.Sprintf("cp-session-%s-%d", u.Username, time.Now().UnixNano())
	expiresAt := time.Now().Add(24 * time.Hour)
	_, err = s.db.ExecContext(ctx, `INSERT INTO cp_sessions (token, user_id, expires_at) VALUES ($1, $2, $3)`, token, u.ID, expiresAt)
	if err != nil {
		return nil, "", err
	}

	return &u, token, nil
}

// ValidateSession xác thực token cho middleware
func (s *Store) ValidateSession(ctx context.Context, token string) (*CPUser, error) {
	var u CPUser
	query := `
		SELECT u.id, u.username, u.full_name, u.email, u.role, u.created_at
		FROM cp_sessions s
		JOIN cp_users u ON s.user_id = u.id
		WHERE s.token = $1 AND s.expires_at > NOW()`
	err := s.db.QueryRowContext(ctx, query, token).Scan(
		&u.ID, &u.Username, &u.FullName, &u.Email, &u.Role, &u.CreatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, errors.New("session expired or invalid")
	}
	return &u, err
}

// ListUsers danh sách users của Control Plane
func (s *Store) ListUsers(ctx context.Context) ([]CPUser, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, username, full_name, email, role, created_at FROM cp_users ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var users []CPUser
	for rows.Next() {
		var u CPUser
		if err := rows.Scan(&u.ID, &u.Username, &u.FullName, &u.Email, &u.Role, &u.CreatedAt); err != nil {
			return nil, err
		}
		users = append(users, u)
	}
	return users, rows.Err()
}

// CreateUser tạo user quản trị viên mới
func (s *Store) CreateUser(ctx context.Context, username, password, fullName, email, role string) (*CPUser, error) {
	username = strings.TrimSpace(username)
	if username == "" || password == "" {
		return nil, errors.New("username and password cannot be empty")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return nil, err
	}
	id := fmt.Sprintf("USR-CP-%d", time.Now().UnixNano())
	if role == "" {
		role = "OPERATOR"
	}
	query := `INSERT INTO cp_users (id, username, password_hash, full_name, email, role, created_at)
			  VALUES ($1, $2, $3, $4, $5, $6, NOW())`
	_, err = s.db.ExecContext(ctx, query, id, username, string(hash), fullName, email, role)
	if err != nil {
		return nil, err
	}
	return &CPUser{
		ID:        id,
		Username:  username,
		FullName:  fullName,
		Email:     email,
		Role:      role,
		CreatedAt: time.Now(),
	}, nil
}

// DeleteUser xóa user quản trị
func (s *Store) DeleteUser(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM cp_users WHERE id = $1`, id)
	return err
}

// BuildConnectionString tự động xây dựng connection string chuẩn cho dynamic database của tenant
func (s *Store) BuildConnectionString(dbName string) string {
	return fmt.Sprintf("postgres://%s:%s@%s:%s/%s?sslmode=%s",
		s.baseUser, s.basePass, s.baseHost, s.basePort, dbName, s.baseSSLMode)
}

// GetTenants trả về danh sách tất cả Tenants hiện có trong PostgreSQL
func (s *Store) GetTenants(ctx context.Context) (map[string]TenantSetting, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT tenant_id, name, plan, global_flags FROM cp_tenants ORDER BY created_at ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	res := make(map[string]TenantSetting)
	for rows.Next() {
		var id, name, plan string
		var flagsJSON []byte
		if err := rows.Scan(&id, &name, &plan, &flagsJSON); err != nil {
			return nil, err
		}
		var flags map[string]bool
		_ = json.Unmarshal(flagsJSON, &flags)

		res[id] = TenantSetting{
			Name:        name,
			Plan:        plan,
			GlobalFlags: flags,
			Modules:     make(map[string]contract.TenantModuleConfig),
		}
	}

	// Lấy modules theo từng tenant
	modRows, err := s.db.QueryContext(ctx, `SELECT tenant_id, module_name, connection_string, features, settings FROM cp_tenant_modules`)
	if err == nil {
		defer modRows.Close()
		for modRows.Next() {
			var tID, mName, connStr string
			var featJSON, settJSON []byte
			if err := modRows.Scan(&tID, &mName, &connStr, &featJSON, &settJSON); err == nil {
				if t, ok := res[tID]; ok {
					var features map[string]bool
					var settings map[string]string
					_ = json.Unmarshal(featJSON, &features)
					_ = json.Unmarshal(settJSON, &settings)

					t.Modules[mName] = contract.TenantModuleConfig{
						TenantID:         tID,
						ConnectionString: connStr,
						Features:         features,
						Settings:         settings,
					}
					res[tID] = t
				}
			}
		}
	}

	return res, nil
}

// ResolveTenantRequestContext resolve context cho 1 Tenant x Module (Không hardcode)
func (s *Store) ResolveTenantRequestContext(ctx context.Context, tenantID, moduleName string) (contract.TenantRequestContext, error) {
	var name, plan string
	var flagsJSON []byte
	err := s.db.QueryRowContext(ctx, `SELECT name, plan, global_flags FROM cp_tenants WHERE tenant_id = $1`, tenantID).Scan(&name, &plan, &flagsJSON)
	if errors.Is(err, sql.ErrNoRows) {
		return contract.TenantRequestContext{}, fmt.Errorf("tenant '%s' not registered in control plane", tenantID)
	}
	if err != nil {
		return contract.TenantRequestContext{}, err
	}

	mergedFeatures := make(map[string]bool)
	for k, v := range s.globalFeatures {
		mergedFeatures[k] = v
	}
	var flags map[string]bool
	_ = json.Unmarshal(flagsJSON, &flags)
	for k, v := range flags {
		mergedFeatures[k] = v
	}

	var modConnStr string
	var featJSON, settJSON []byte
	err = s.db.QueryRowContext(ctx, `SELECT connection_string, features, settings FROM cp_tenant_modules WHERE tenant_id = $1 AND module_name = $2`, tenantID, moduleName).Scan(&modConnStr, &featJSON, &settJSON)
	if err != nil {
		return contract.TenantRequestContext{}, fmt.Errorf("module '%s' not configured for tenant '%s'", moduleName, tenantID)
	}

	var modFeat map[string]bool
	var modSett map[string]string
	_ = json.Unmarshal(featJSON, &modFeat)
	_ = json.Unmarshal(settJSON, &modSett)
	for k, v := range modFeat {
		mergedFeatures[k] = v
	}

	return contract.TenantRequestContext{
		TenantID:         tenantID,
		ConnectionString: modConnStr,
		Features:         mergedFeatures,
		Settings:         modSett,
	}, nil
}

// ProvisionTenant đăng ký tự động Tenant mới:
// 1. Lưu metadata vào cp_tenants
// 2. Tự động sinh DB connection strings & thực hiện CREATE DATABASE trên PostgreSQL
// 3. Tự động chạy toàn bộ SQL migrations khởi tạo
// 4. Khởi tạo tài khoản admin đầu tiên cho chính Tenant đó (init user admin)
func (s *Store) ProvisionTenant(
	ctx context.Context,
	tenantID, name, plan string,
	adminUsername, adminPassword string,
	authFeatures, partiesFeatures map[string]bool,
) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	tenantID = strings.ToLower(strings.TrimSpace(tenantID))
	name = strings.TrimSpace(name)
	if tenantID == "" || name == "" {
		return errors.New("tenant_id and name are required")
	}

	// Kiểm tra trùng
	var count int
	_ = s.db.QueryRowContext(ctx, `SELECT count(*) FROM cp_tenants WHERE tenant_id = $1`, tenantID).Scan(&count)
	if count > 0 {
		return fmt.Errorf("tenant '%s' already registered", tenantID)
	}

	// 1. Lưu vào cp_tenants
	flagsJSON, _ := json.Marshal(map[string]bool{})
	_, err := s.db.ExecContext(ctx, `INSERT INTO cp_tenants (tenant_id, name, plan, global_flags) VALUES ($1, $2, $3, $4)`,
		tenantID, name, plan, flagsJSON)
	if err != nil {
		return fmt.Errorf("failed to register tenant in cp_db: %w", err)
	}

	// 2. Tự động tạo 2 databases vật lý: auth_<tenant_id> và parties_<tenant_id>
	authDBName := fmt.Sprintf("auth_%s", tenantID)
	partiesDBName := fmt.Sprintf("parties_%s", tenantID)

	if err := s.createDatabaseIfNotExists(ctx, authDBName); err != nil {
		return fmt.Errorf("failed to create database %s: %w", authDBName, err)
	}
	if err := s.createDatabaseIfNotExists(ctx, partiesDBName); err != nil {
		return fmt.Errorf("failed to create database %s: %w", partiesDBName, err)
	}

	// Xây dựng connection string động (không hardcode)
	authConn := s.BuildConnectionString(authDBName)
	partiesConn := s.BuildConnectionString(partiesDBName)

	if authFeatures == nil {
		authFeatures = map[string]bool{"mfa_security": plan == "enterprise", "security_audit": true}
	}
	if partiesFeatures == nil {
		partiesFeatures = map[string]bool{"ekyc_verification": plan == "enterprise", "employee_directory": true}
	}

	authFeatJSON, _ := json.Marshal(authFeatures)
	partiesFeatJSON, _ := json.Marshal(partiesFeatures)

	_, _ = s.db.ExecContext(ctx, `INSERT INTO cp_tenant_modules (tenant_id, module_name, connection_string, features, settings) VALUES ($1, 'auth', $2, $3, '{}'::jsonb)`, tenantID, authConn, authFeatJSON)
	_, _ = s.db.ExecContext(ctx, `INSERT INTO cp_tenant_modules (tenant_id, module_name, connection_string, features, settings) VALUES ($1, 'parties', $2, $3, '{}'::jsonb)`, tenantID, partiesConn, partiesFeatJSON)

	// 3. Chạy SQL migrations và khởi tạo User Admin đầu tiên trong auth database của Tenant
	if adminUsername == "" {
		adminUsername = "admin"
	}
	if adminPassword == "" {
		adminPassword = "admin" + tenantID
	}

	// Thực hiện migrate và seed admin cho auth database của tenant
	authDB, err := sql.Open("postgres", authConn)
	if err != nil {
		return fmt.Errorf("failed to connect to new auth db: %w", err)
	}
	defer authDB.Close()

	if err := s.migratorRunner.MigrateUp(ctx, authDB, authmigrations.GetAuthMigrationScripts()); err != nil {
		return fmt.Errorf("failed to migrate auth db for %s: %w", tenantID, err)
	}

	// Khởi tạo user admin cụ thể cho tenant này
	_, err = authDB.ExecContext(ctx, `
		INSERT INTO users (id, tenant_id, username, password, role, mfa_code, created_at)
		VALUES ($1, $2, $3, $4, 'TENANT_ADMIN', '654321', NOW())
		ON CONFLICT (username) DO UPDATE SET password = EXCLUDED.password`,
		fmt.Sprintf("USR-%s-ADMIN", strings.ToUpper(tenantID)),
		tenantID,
		adminUsername,
		adminPassword,
	)
	if err != nil {
		return fmt.Errorf("failed to init tenant admin user: %w", err)
	}

	// Thực hiện migrate cho parties database của tenant
	partiesDB, err := sql.Open("postgres", partiesConn)
	if err != nil {
		return fmt.Errorf("failed to connect to new parties db: %w", err)
	}
	defer partiesDB.Close()

	if err := s.migratorRunner.MigrateUp(ctx, partiesDB, partiesmigrations.GetPartiesMigrationScripts()); err != nil {
		return fmt.Errorf("failed to migrate parties db for %s: %w", tenantID, err)
	}

	s.logger.Info("✅ Successfully auto-provisioned tenant with dedicated databases & admin",
		slog.String("tenant_id", tenantID),
		slog.String("admin_username", adminUsername),
	)

	return nil
}

// DeleteTenant xóa tenant và thu hồi databases
func (s *Store) DeleteTenant(ctx context.Context, tenantID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Xóa khỏi metadata
	_, err := s.db.ExecContext(ctx, `DELETE FROM cp_tenants WHERE tenant_id = $1`, tenantID)
	if err != nil {
		return err
	}

	// Drop databases
	authDBName := fmt.Sprintf("auth_%s", tenantID)
	partiesDBName := fmt.Sprintf("parties_%s", tenantID)
	_ = s.dropDatabase(ctx, authDBName)
	_ = s.dropDatabase(ctx, partiesDBName)

	return nil
}

func (s *Store) createDatabaseIfNotExists(ctx context.Context, dbName string) error {
	var exists bool
	_ = s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM pg_database WHERE datname = $1)`, dbName).Scan(&exists)
	if !exists {
		_, err := s.db.ExecContext(ctx, fmt.Sprintf(`CREATE DATABASE "%s"`, dbName))
		return err
	}
	return nil
}

func (s *Store) dropDatabase(ctx context.Context, dbName string) error {
	// Terminate active connections first
	_, _ = s.db.ExecContext(ctx, fmt.Sprintf(`
		SELECT pg_terminate_backend(pg_stat_activity.pid)
		FROM pg_stat_activity
		WHERE pg_stat_activity.datname = '%s' AND pid <> pg_backend_pid();`, dbName))
	_, err := s.db.ExecContext(ctx, fmt.Sprintf(`DROP DATABASE IF EXISTS "%s"`, dbName))
	return err
}

// UpdateTenantModuleConfig cập nhật nóng Feature Flags & Connection String từ Dashboard
func (s *Store) UpdateTenantModuleConfig(ctx context.Context, tenantID, moduleName, connStr string, features map[string]bool) error {
	featJSON, _ := json.Marshal(features)
	query := `
	INSERT INTO cp_tenant_modules (tenant_id, module_name, connection_string, features, updated_at)
	VALUES ($1, $2, $3, $4, NOW())
	ON CONFLICT (tenant_id, module_name) DO UPDATE SET
		connection_string = CASE WHEN EXCLUDED.connection_string <> '' THEN EXCLUDED.connection_string ELSE cp_tenant_modules.connection_string END,
		features = EXCLUDED.features,
		updated_at = NOW();`
	_, err := s.db.ExecContext(ctx, query, tenantID, moduleName, connStr, featJSON)
	return err
}
