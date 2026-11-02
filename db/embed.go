// Package db nhúng script nền tảng vào binary: hub, cloud và CLI dùng cùng một bản,
// nên runtime luôn biết chính xác script platform nào đi kèm version của nó.
package db

import (
	"embed"

	"github.com/dudangphuoc/aegis-kernel/framework/core/data/scripts"
)

//go:embed platform/*.sql master/*.sql
var files embed.FS

// Platform là package script "platform" (schema platform), áp lên mọi DB trước tiên
func Platform(version string) (scripts.Package, error) {
	s, err := scripts.LoadDir(files, "platform")
	return scripts.Package{Name: "platform", Version: version, Schema: "platform", Profile: scripts.ProfilePlatform, Scripts: s}, err
}

// Master là package script của DB control plane (schema public), áp sau platform
func Master(version string) (scripts.Package, error) {
	s, err := scripts.LoadDir(files, "master")
	return scripts.Package{Name: "master", Version: version, Schema: "public", Profile: scripts.ProfileMaster, Scripts: s}, err
}
