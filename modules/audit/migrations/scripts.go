package scripts

import (
	_ "embed"

	"github.com/dudangphuoc/aegis-kernel/sdk/migrator"
)

//go:embed 0001_init_audit_layers.up.sql
var audit0001Up string

//go:embed 0001_init_audit_layers.down.sql
var audit0001Down string

func GetAuditMigrationScripts() []migrator.MigrationScript {
	return []migrator.MigrationScript{
		{
			Version: 1,
			Name:    "init_audit_layers",
			UpSQL:   audit0001Up,
			DownSQL: audit0001Down,
		},
	}
}
