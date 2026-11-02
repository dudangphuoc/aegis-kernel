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

//go:embed 0002_add_security_metadata.up.sql
var auth0002Up string

//go:embed 0002_add_security_metadata.down.sql
var auth0002Down string

//go:embed 0003_drop_security_audit_logs.up.sql
var auth0003Up string

//go:embed 0003_drop_security_audit_logs.down.sql
var auth0003Down string

func GetAuthMigrationScripts() []migrator.MigrationScript {
	return []migrator.MigrationScript{
		{
			Version: 1,
			Name:    "init_auth_tables",
			UpSQL:   auth0001Up,
			DownSQL: auth0001Down,
		},
		{
			Version: 2,
			Name:    "add_security_metadata",
			UpSQL:   auth0002Up,
			DownSQL: auth0002Down,
		},
		{
			Version: 3,
			Name:    "drop_security_audit_logs",
			UpSQL:   auth0003Up,
			DownSQL: auth0003Down,
		},
		// Audit tại tầng DB: trigger trên users -> aegis_audit_outbox (mật khẩu, mã MFA chỉ ghi "có đổi")
		dbaudit.Migration(4, dbaudit.Table{Name: "users", Entity: "UserAccount", PK: "id", Secrets: []string{"password", "mfa_code"}}),
	}
}

