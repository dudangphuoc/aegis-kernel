// Package db chứa script schema của module audit (schema biz_audit). Control Plane áp lên DB tenant;
// sau này đi trong gói (platform pack) thay vì được import trực tiếp.
package db

import (
	"embed"

	"github.com/dudangphuoc/aegis-kernel/framework/core/data/scripts"
)

const (
	Key     = "audit"
	Version = "1.0.0"
)

//go:embed *.sql
var files embed.FS

// Package trả package script của module (lint profile module)
func Package() (scripts.Package, error) {
	s, err := scripts.LoadDir(files, ".")
	return scripts.Package{Name: Key, Version: Version, Schema: scripts.ModuleSchema(Key), Profile: scripts.ProfileModule, LocalTables: []string{"audits", "entity_changes", "entity_audit_logs"}, Scripts: s}, err
}
