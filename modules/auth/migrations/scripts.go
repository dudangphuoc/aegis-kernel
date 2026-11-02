package scripts

import (
	_ "embed"

	"github.com/dudangphuoc/aegis-kernel/sdk/dbaudit"
	"github.com/dudangphuoc/aegis-kernel/sdk/migrator"
)

//go:embed 0001_init_auth.up.sql
var auth0001Up string

//go:embed 0001_init_auth.down.sql
var auth0001Down string

// GetAuthMigrationScripts: môi trường dev nên gộp toàn bộ schema vào 1 version.
// Audit tại tầng DB: trigger trên users -> aegis_audit_outbox (mật khẩu, mã MFA chỉ ghi "có đổi").
func GetAuthMigrationScripts() []migrator.MigrationScript {
	audit := dbaudit.Migration(1, dbaudit.Table{Name: "users", Entity: "UserAccount", PK: "id", Secrets: []string{"password", "mfa_code"}})
	return []migrator.MigrationScript{{
		Version: 1,
		Name:    "init_auth",
		UpSQL:   auth0001Up + "\n" + audit.UpSQL,
		DownSQL: audit.DownSQL + auth0001Down,
	}}
}
