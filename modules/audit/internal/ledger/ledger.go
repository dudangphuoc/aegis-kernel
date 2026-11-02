// Package ledger chứa logic thuần của sổ audit: chuẩn hóa JSON, diff từng trường và hash chain SHA-256.
// Không phụ thuộc DB để có thể kiểm thử độc lập và tái tính toán khi verify.
package ledger

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/dudangphuoc/aegis-kernel/modules/audit/internal/domain"
)

// GenesisHash là prev_hash của bản ghi đầu tiên trong chuỗi
var GenesisHash = strings.Repeat("0", 64)

// Canonical chuẩn hóa JSON: key được sắp xếp, số giữ nguyên dạng chuỗi (không qua float64).
// Áp dụng cả lúc ghi lẫn lúc đọc lại từ JSONB nên hash ổn định dù Postgres đổi thứ tự key.
func Canonical(raw []byte) (json.RawMessage, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil, nil
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	if v == nil {
		return nil, nil
	}
	return json.Marshal(v)
}

// CanonicalValue marshal giá trị Go rồi chuẩn hóa
func CanonicalValue(v any) (json.RawMessage, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return Canonical(b)
}

// CanonicalClientInfo chuẩn hóa client_info; object rỗng coi như không có (nil)
func CanonicalClientInfo(raw []byte) (json.RawMessage, error) {
	c, err := Canonical(raw)
	if err != nil || string(c) == "{}" {
		return nil, err
	}
	return c, nil
}

// NormalizeTime đưa thời gian về UTC, độ chính xác micro giây (khớp timestamptz của Postgres)
func NormalizeTime(t time.Time) time.Time { return t.UTC().Truncate(time.Microsecond) }

// FieldDiff là 1 trường thay đổi; Old/New nil nghĩa là trường không tồn tại ở phía đó
type FieldDiff struct {
	Field string
	Old   *string
	New   *string
}

// Diff so sánh 2 snapshot (đã chuẩn hóa) và trả các trường thay đổi theo thứ tự tên trường
func Diff(before, after json.RawMessage) ([]FieldDiff, error) {
	b, err := decodeObject(before)
	if err != nil {
		return nil, fmt.Errorf("before snapshot: %w", err)
	}
	a, err := decodeObject(after)
	if err != nil {
		return nil, fmt.Errorf("after snapshot: %w", err)
	}

	keys := make(map[string]struct{}, len(a)+len(b))
	for k := range b {
		keys[k] = struct{}{}
	}
	for k := range a {
		keys[k] = struct{}{}
	}
	sorted := make([]string, 0, len(keys))
	for k := range keys {
		sorted = append(sorted, k)
	}
	sort.Strings(sorted)

	var diffs []FieldDiff
	for _, k := range sorted {
		oldV, newV := valueString(b, k), valueString(a, k)
		if equalPtr(oldV, newV) {
			continue
		}
		diffs = append(diffs, FieldDiff{Field: k, Old: oldV, New: newV})
	}
	return diffs, nil
}

// AuditDigest băm toàn bộ nội dung Layer 1 để phát hiện sửa audits
func AuditDigest(a domain.AuditRecord) string {
	a.OccurredAt = NormalizeTime(a.OccurredAt)
	return digestJSON(a)
}

// ChangeDigest băm toàn bộ nội dung Layer 2 để phát hiện sửa entity_changes
func ChangeDigest(c domain.ChangeRecord) string {
	c.ChangedAt = NormalizeTime(c.ChangedAt)
	return digestJSON(c)
}

// ChainHash = SHA256(prev_hash ‖ tenant ‖ audit_id ‖ audit_digest ‖ change_id ‖ change_digest ‖ field ‖ old ‖ new).
// Mỗi phần được mã hóa dạng độ-dài-có-tiền-tố để không thể ghép nhập nhằng; nil khác chuỗi rỗng.
func ChainHash(prevHash, tenantID, auditID, auditDigest string, changeID *string, changeDigest string, field, oldV, newV *string) string {
	h := sha256.New()
	for _, p := range []*string{&prevHash, &tenantID, &auditID, &auditDigest, changeID, &changeDigest, field, oldV, newV} {
		if p == nil {
			h.Write([]byte("-1:"))
			continue
		}
		fmt.Fprintf(h, "%d:%s", len(*p), *p)
	}
	return hex.EncodeToString(h.Sum(nil))
}

func digestJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		panic(fmt.Sprintf("ledger: digest marshal: %v", err)) // chỉ chứa kiểu marshal được
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func decodeObject(raw json.RawMessage) (map[string]json.RawMessage, error) {
	if len(raw) == 0 {
		return map[string]json.RawMessage{}, nil
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, err
	}
	return m, nil
}

func valueString(m map[string]json.RawMessage, k string) *string {
	v, ok := m[k]
	if !ok {
		return nil
	}
	s := string(v)
	return &s
}

func equalPtr(a, b *string) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}
