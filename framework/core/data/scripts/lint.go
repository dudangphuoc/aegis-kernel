package scripts

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
)

// Violation là một vi phạm lint, mã DBxxx ổn định để tra cứu (platform db explain)
type Violation struct {
	Code    string
	Script  string
	Line    int
	Message string
}

func (v Violation) String() string {
	return fmt.Sprintf("%s:%d: %s %s", v.Script, v.Line, v.Code, v.Message)
}

// Mô tả các luật; giải thích vì sao nằm trong cookbook 04 §3.5
var Rules = map[string]string{
	"DB001": "file name must be yyyyMMddHHmm_<slug>.sql",
	"DB002": "scripts are add-only: no DROP, RENAME, ALTER ... TYPE, TRUNCATE, DELETE",
	"DB003": "module scripts must not GRANT/REVOKE/SET, create roles, schemas, functions, triggers or DO blocks",
	"DB004": "module scripts must not name a schema: the runner sets search_path",
	"DB005": "synced table needs store_id uuid NOT NULL DEFAULT platform.store_id() and a primary key",
	"DB006": "UNIQUE constraint/index on a synced table must include store_id",
	"DB007": "INSERT/UPDATE only into localTables (synced rows come from the store)",
	"DB008": "concurrently scripts hold exactly one CREATE INDEX CONCURRENTLY IF NOT EXISTS; CONCURRENTLY needs the marker",
	"DB009": "ADD COLUMN must be nullable or have a DEFAULT",
	"DB011": "invalid platform marker",
}

var (
	fileNameRe   = regexp.MustCompile(`^\d{12}_[a-z0-9_]+\.sql$`)
	destructRe   = regexp.MustCompile(`^(drop\b|truncate\b|delete\b|alter .*\brename\b|alter .*\balter column\b.*\btype\b|alter .*\bset data type\b)`)
	platformDrop = regexp.MustCompile(`^drop (trigger|function) if exists\b`)
	privilegeRe  = regexp.MustCompile(`^(grant|revoke|set|reset|do\b|create (or replace )?(function|procedure|trigger|role|user|schema|extension)\b|alter (role|user|schema|default privileges)\b|create policy\b)`)
	createTblRe  = regexp.MustCompile(`^create (unlogged )?table (if not exists )?([a-z_][a-z0-9_.]*)`)
	qualifiedRe  = regexp.MustCompile(`^(create (unlogged )?table (if not exists )?|alter table (if exists )?(only )?|create (unique )?index (concurrently )?(if not exists )?[a-z0-9_]* ?on (only )?|insert into |update |references )([a-z_][a-z0-9_]*)\.`)
	referencesRe = regexp.MustCompile(`\breferences ([a-z_][a-z0-9_]*)\.`)
	uniqueIdxRe  = regexp.MustCompile(`^create unique index .* on (only )?([a-z0-9_]+)\s*(using \w+\s*)?\((.*)\)`)
	uniqueConRe  = regexp.MustCompile(`\bunique\s*\(([^)]*)\)`)
	dmlRe        = regexp.MustCompile(`^(insert into|update) ([a-z_][a-z0-9_]*)`)
	addColRe     = regexp.MustCompile(`\badd column (if not exists )?[a-z0-9_]+ [^,]*`)
	concIdxRe    = regexp.MustCompile(`^create (unique )?index concurrently if not exists `)
	storeIDColRe = regexp.MustCompile(`\bstore_id uuid not null default platform\.store_id\(\)`)
)

// Lint kiểm cả package; trả mọi vi phạm (không dừng ở vi phạm đầu)
func Lint(p Package) []Violation {
	var vs []Violation
	for _, s := range p.Scripts {
		vs = append(vs, lintScript(p, s)...)
	}
	return vs
}

func lintScript(p Package, s Script) []Violation {
	var vs []Violation
	add := func(code string, line int, msg string) {
		if msg == "" {
			msg = Rules[code]
		}
		vs = append(vs, Violation{Code: code, Script: s.Name, Line: line, Message: msg})
	}
	if !fileNameRe.MatchString(s.Name) {
		add("DB001", 1, "")
	}
	if s.markerErr != nil {
		add("DB011", 1, s.markerErr.Error())
	}

	stmts := splitStatements(s.SQL)
	if s.Concurrently {
		if len(stmts) != 1 || !concIdxRe.MatchString(stmts[0].text) {
			add("DB008", 1, "")
		}
	}
	local := map[string]bool{"outbox": true, "inbox": true}
	for _, t := range p.LocalTables {
		local[strings.ToLower(t)] = true
	}

	for _, st := range stmts {
		t := st.text
		if !s.Concurrently && strings.Contains(t, " concurrently ") {
			add("DB008", st.line, "")
		}
		if destructRe.MatchString(t) && !(p.Profile == ProfilePlatform && platformDrop.MatchString(t)) {
			add("DB002", st.line, "")
		}
		if m := addColRe.FindString(t); m != "" && strings.Contains(m, "not null") && !strings.Contains(m, "default") {
			add("DB009", st.line, "")
		}
		if p.Profile != ProfileModule {
			continue
		}
		if privilegeRe.MatchString(t) {
			add("DB003", st.line, "")
		}
		if qualifiedRe.MatchString(t) || referencesRe.MatchString(t) {
			add("DB004", st.line, "")
		}
		if m := createTblRe.FindStringSubmatch(t); m != nil && !local[m[3]] {
			if !storeIDColRe.MatchString(t) || !strings.Contains(t, "primary key") {
				add("DB005", st.line, "")
			}
			for _, cols := range uniqueConRe.FindAllStringSubmatch(t, -1) {
				if !hasColumn(cols[1], "store_id") {
					add("DB006", st.line, "")
				}
			}
			if strings.Contains(t, " unique,") || strings.Contains(t, " unique)") {
				add("DB006", st.line, "column-level UNIQUE cannot include store_id: use UNIQUE (store_id, ...)")
			}
		}
		if m := uniqueIdxRe.FindStringSubmatch(t); m != nil && !local[m[2]] && !hasColumn(m[4], "store_id") {
			add("DB006", st.line, "")
		}
		if m := dmlRe.FindStringSubmatch(t); m != nil && !local[m[2]] {
			add("DB007", st.line, "")
		}
	}
	return vs
}

func hasColumn(list, col string) bool {
	parts := strings.Split(list, ",")
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	return slices.Contains(parts, col)
}
