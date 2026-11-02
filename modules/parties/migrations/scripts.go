package scripts

import (
	_ "embed"

	"github.com/dudangphuoc/aegis-kernel/sdk/dbaudit"
	"github.com/dudangphuoc/aegis-kernel/sdk/migrator"
)

//go:embed 0001_init_parties.up.sql
var parties0001Up string

//go:embed 0001_init_parties.down.sql
var parties0001Down string

// GetPartiesMigrationScripts: môi trường dev nên gộp toàn bộ schema vào 1 version.
// Audit tại tầng DB: trigger trên parties -> aegis_audit_outbox (bỏ cột hạ tầng).
func GetPartiesMigrationScripts() []migrator.MigrationScript {
	audit := dbaudit.Migration(1, dbaudit.Table{Name: "parties", Entity: "Party", PK: "id", Ignore: []string{"processed_by"}})
	return []migrator.MigrationScript{{
		Version: 1,
		Name:    "init_parties",
		UpSQL:   parties0001Up + "\n" + audit.UpSQL,
		DownSQL: audit.DownSQL + parties0001Down,
	}}
}
