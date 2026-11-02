// Package scripts áp script SQL theo "chủ sổ": một thư mục db/ = một package = một schema.
// Script chỉ thêm (add-only), chạy theo thứ tự tên file, ghi sổ platform.migrations kèm checksum.
package scripts

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Profile quyết định bộ luật lint và cách runner chuẩn bị schema
type Profile string

const (
	// ProfilePlatform: script nền tảng (schema platform), được viết hàm/trigger
	ProfilePlatform Profile = "platform"
	// ProfileMaster: DB control plane (schema public)
	ProfileMaster Profile = "master"
	// ProfileModule: script của gói module/feature (schema biz_* / ft_*), luật chặt nhất
	ProfileModule Profile = "module"
)

const (
	defaultTimeoutMinutes = 2
	maxTimeoutMinutes     = 30
)

// Package là tập script của một chủ sổ
type Package struct {
	Name        string // khoá trong sổ: platform | master | <module key> | <feature key>
	Version     string // version gói (ghi vào sổ, phục vụ đối chiếu release)
	Schema      string // platform | public | biz_<key> | ft_<module>__<name>
	Profile     Profile
	LocalTables []string // bảng chỉ dùng tại cửa hàng: không capture, được phép DML
	Scripts     []Script
}

// Script là một file SQL; Checksum tính sau khi chuẩn hoá CRLF -> LF
type Script struct {
	Name           string
	SQL            string
	Checksum       string
	RollbackUnsafe bool // marker "-- platform:allow DB010 <lý do>"
	Concurrently   bool // marker "-- platform:concurrently": chạy ngoài transaction
	TimeoutMinutes int  // marker "-- platform:timeout <n>m", mặc định 2, tối đa 30
	markerErr      error
}

var markerRe = regexp.MustCompile(`^--\s*platform:(\S+)\s*(.*)$`)

// NewScript chuẩn hoá nội dung và đọc marker ở các dòng chú thích đầu file
func NewScript(name, sql string) Script {
	sql = strings.ReplaceAll(sql, "\r\n", "\n")
	sum := sha256.Sum256([]byte(sql))
	s := Script{Name: name, SQL: sql, Checksum: hex.EncodeToString(sum[:]), TimeoutMinutes: defaultTimeoutMinutes}
	sc := bufio.NewScanner(strings.NewReader(sql))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		if !strings.HasPrefix(line, "--") {
			break // marker chỉ có hiệu lực ở phần đầu file
		}
		m := markerRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		switch m[1] {
		case "allow":
			if strings.HasPrefix(m[2], "DB010") {
				s.RollbackUnsafe = true
			}
		case "concurrently":
			s.Concurrently = true
		case "timeout":
			n, err := strconv.Atoi(strings.TrimSuffix(strings.TrimSpace(m[2]), "m"))
			if err != nil || n < 1 || n > maxTimeoutMinutes {
				s.markerErr = fmt.Errorf("timeout marker must be 1..%dm", maxTimeoutMinutes)
				continue
			}
			s.TimeoutMinutes = n
		}
	}
	return s
}

// LoadDir đọc mọi *.sql trong dir của fsys, sắp theo tên
func LoadDir(fsys fs.FS, dir string) ([]Script, error) {
	entries, err := fs.ReadDir(fsys, dir)
	if err != nil {
		return nil, err
	}
	var out []Script
	for _, e := range entries {
		if e.IsDir() || path.Ext(e.Name()) != ".sql" {
			continue
		}
		b, err := fs.ReadFile(fsys, path.Join(dir, e.Name()))
		if err != nil {
			return nil, err
		}
		out = append(out, NewScript(e.Name(), string(b)))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// ModuleSchema: schema của module (biz_<key>)
func ModuleSchema(key string) string { return "biz_" + key }

// FeatureSchema: schema của feature (ft_<module>__<name>)
func FeatureSchema(module, name string) string { return "ft_" + module + "__" + name }
