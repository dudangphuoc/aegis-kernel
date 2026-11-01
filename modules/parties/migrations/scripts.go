package scripts

import (
	_ "embed"
	"github.com/dudangphuoc/AegisKernel/sdk/migrator"
)

//go:embed 0001_init_parties.up.sql
var parties0001Up string

//go:embed 0001_init_parties.down.sql
var parties0001Down string

//go:embed 0002_add_party_tax_and_notes.up.sql
var parties0002Up string

//go:embed 0002_add_party_tax_and_notes.down.sql
var parties0002Down string

func GetPartiesMigrationScripts() []migrator.MigrationScript {
	return []migrator.MigrationScript{
		{
			Version: 1,
			Name:    "init_parties_tables",
			UpSQL:   parties0001Up,
			DownSQL: parties0001Down,
		},
		{
			Version: 2,
			Name:    "add_party_tax_and_notes",
			UpSQL:   parties0002Up,
			DownSQL: parties0002Down,
		},
	}
}

