package ledger

import (
	"testing"
	"time"

	"github.com/dudangphuoc/aegis-kernel/modules/audit/internal/domain"
)

func s(v string) *string { return &v }

func TestCanonicalStableAcrossKeyOrderAndBigNumbers(t *testing.T) {
	a, _ := Canonical([]byte(`{"b": 1793592193495213000, "a": {"y": 1, "x": "<v>"}}`))
	b, _ := Canonical([]byte(`{"a":{"x":"<v>","y":1},"b":1793592193495213000}`))
	if string(a) != string(b) {
		t.Fatalf("not canonical: %s vs %s", a, b)
	}
	if want := `{"a":{"x":"` + "\\u003cv\\u003e" + `","y":1},"b":1793592193495213000}`; string(a) != want {
		t.Fatalf("got %s", a)
	}
	if n, _ := Canonical([]byte("null")); n != nil {
		t.Fatal("null must canonicalize to nil")
	}
}

func TestDiff(t *testing.T) {
	before, _ := Canonical([]byte(`{"id":"P1","name":"A","email":"a@x","gone":true}`))
	after, _ := Canonical([]byte(`{"id":"P1","name":"B","email":"a@x","added":1}`))
	diffs, err := Diff(before, after)
	if err != nil {
		t.Fatal(err)
	}
	if len(diffs) != 3 {
		t.Fatalf("expected 3 diffs, got %+v", diffs)
	}
	// Thứ tự theo tên trường: added, gone, name
	if diffs[0].Field != "added" || diffs[0].Old != nil || *diffs[0].New != "1" {
		t.Fatalf("added: %+v", diffs[0])
	}
	if diffs[1].Field != "gone" || *diffs[1].Old != "true" || diffs[1].New != nil {
		t.Fatalf("gone: %+v", diffs[1])
	}
	if diffs[2].Field != "name" || *diffs[2].Old != `"A"` || *diffs[2].New != `"B"` {
		t.Fatalf("name: %+v", diffs[2])
	}

	ins, _ := Diff(nil, after)
	if len(ins) != 4 {
		t.Fatalf("insert must diff every field, got %d", len(ins))
	}
}

func TestChainHashSensitivity(t *testing.T) {
	base := ChainHash(GenesisHash, "acme", "AUD-1", "d1", s("CHG-1"), "c1", s("name"), s(`"A"`), s(`"B"`))
	variants := []string{
		ChainHash(GenesisHash, "acme", "AUD-1", "d1", s("CHG-1"), "c1", s("name"), s(`"A"`), s(`"C"`)),
		ChainHash(GenesisHash, "acme", "AUD-1", "d2", s("CHG-1"), "c1", s("name"), s(`"A"`), s(`"B"`)),
		ChainHash(GenesisHash, "acme", "AUD-1", "d1", nil, "c1", s("name"), s(`"A"`), s(`"B"`)),
		ChainHash(GenesisHash, "acme", "AUD-1", "d1", s("CHG-1"), "c1", s("name"), nil, s(`"B"`)),
		ChainHash(GenesisHash, "acme", "AUD-1", "d1", s("CHG-1"), "c1", s("name"), s(""), s(`"B"`)),
		// Ghép nhập nhằng: dịch chuỗi giữa 2 phần không được cho cùng hash
		ChainHash(GenesisHash, "acmeA", "UD-1", "d1", s("CHG-1"), "c1", s("name"), s(`"A"`), s(`"B"`)),
	}
	for i, v := range variants {
		if v == base {
			t.Fatalf("variant %d collides with base", i)
		}
	}
}

func TestDigestIgnoresTimezoneAndNanoseconds(t *testing.T) {
	ts := time.Date(2026, 11, 2, 10, 0, 0, 123456789, time.FixedZone("ICT", 7*3600))
	a := AuditDigest(domain.AuditRecord{ID: "A", OccurredAt: ts})
	b := AuditDigest(domain.AuditRecord{ID: "A", OccurredAt: ts.UTC().Truncate(time.Microsecond)})
	if a != b {
		t.Fatal("digest must be stable after DB round-trip of timestamp")
	}
}

func TestEmptyClientInfoKeepsLegacyDigest(t *testing.T) {
	ci, err := CanonicalClientInfo([]byte(` {} `))
	if err != nil || ci != nil {
		t.Fatalf("empty client_info must normalize to nil: %s %v", ci, err)
	}
	legacy := AuditDigest(domain.AuditRecord{ID: "A"})
	if AuditDigest(domain.AuditRecord{ID: "A", ClientInfo: ci}) != legacy {
		t.Fatal("audits written before client_info existed must keep their digest")
	}
	withInfo, _ := CanonicalClientInfo([]byte(`{"app":"x"}`))
	if AuditDigest(domain.AuditRecord{ID: "A", ClientInfo: withInfo}) == legacy {
		t.Fatal("client_info must be covered by the digest")
	}
}
