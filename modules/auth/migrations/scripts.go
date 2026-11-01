package scripts

import (
	_ "embed"
	"github.com/dudangphuoc/AegisKernel/sdk/migrator"
)

//go:embed 0001_init_auth.up.sql
var auth0001Up string

//go:embed 0001_init_auth.down.sql
var auth0001Down string

//go:embed 0002_add_security_metadata.up.sql
var auth0002Up string

//go:embed 0002_add_security_metadata.down.sql
var auth0002Down string

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
	}
}

