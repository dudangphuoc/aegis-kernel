package main

import (
	"context"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"text/tabwriter"
	"time"

	_ "github.com/lib/pq"

	"github.com/dudangphuoc/aegis-kernel/db"
	"github.com/dudangphuoc/aegis-kernel/framework/core/data/scripts"
)

var slugRe = regexp.MustCompile(`^[a-z0-9_]+$`)

// dbNew tạo file script mới đặt tên theo giờ UTC, để thứ tự áp khớp thứ tự viết
func dbNew(args []string) error {
	if len(args) != 2 {
		return errors.New("usage: platform db new <dir> <slug>")
	}
	dir, slug := args[0], strings.ReplaceAll(strings.ToLower(args[1]), "-", "_")
	if !slugRe.MatchString(slug) {
		return fmt.Errorf("slug must match %s", slugRe)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	name := filepath.Join(dir, time.Now().UTC().Format("200601021504")+"_"+slug+".sql")
	f, err := os.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := f.WriteString("-- add-only: tables, nullable/defaulted columns, indexes. See `platform db explain DBxxx`.\n"); err != nil {
		return err
	}
	fmt.Println(name)
	return nil
}

func dbLint(args []string) error {
	fs := flag.NewFlagSet("lint", flag.ContinueOnError)
	profile := fs.String("profile", string(scripts.ProfileModule), "module|platform|master")
	local := fs.String("local", "", "comma-separated localTables")
	if err := fs.Parse(args); err != nil {
		return err
	}
	var pkgs []scripts.Package
	if fs.NArg() == 0 {
		p, err := db.Platform("")
		if err != nil {
			return err
		}
		m, err := db.Master("")
		if err != nil {
			return err
		}
		pkgs = append(pkgs, p, m)
	} else {
		s, err := scripts.LoadDir(os.DirFS(fs.Arg(0)), ".")
		if err != nil {
			return err
		}
		pkgs = append(pkgs, scripts.Package{Name: filepath.Base(fs.Arg(0)), Profile: scripts.Profile(*profile), LocalTables: splitList(*local), Scripts: s})
	}
	count := 0
	for _, p := range pkgs {
		for _, v := range scripts.Lint(p) {
			fmt.Println(v)
			count++
		}
	}
	if count > 0 {
		return fmt.Errorf("%d violation(s)", count)
	}
	fmt.Println("ok")
	return nil
}

func dbExplain(args []string) error {
	if len(args) != 1 {
		return errors.New("usage: platform db explain <DBxxx>")
	}
	msg, ok := scripts.Rules[strings.ToUpper(args[0])]
	if !ok {
		return fmt.Errorf("unknown rule %s", args[0])
	}
	fmt.Printf("%s: %s\n", strings.ToUpper(args[0]), msg)
	return nil
}

func dbPlanOrApply(args []string, apply bool) error {
	fs := flag.NewFlagSet("plan", flag.ContinueOnError)
	dsn := fs.String("dsn", os.Getenv("DATABASE_URL"), "direct postgres DSN (not through pgbouncer)")
	target := fs.String("target", "platform", "platform|master|module")
	dir := fs.String("dir", "", "module script directory (target=module)")
	key := fs.String("package", "", "package key (target=module)")
	schema := fs.String("schema", "", "schema (default biz_<package>)")
	version := fs.String("version", "", "package version recorded in the ledger")
	local := fs.String("local", "", "comma-separated localTables")
	track := fs.Bool("track", false, "attach change capture (hub databases)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *dsn == "" {
		return errors.New("--dsn or DATABASE_URL is required")
	}

	pkgs, err := targetPackages(*target, *dir, *key, *schema, *version, splitList(*local))
	if err != nil {
		return err
	}
	conn, err := sql.Open("postgres", *dsn)
	if err != nil {
		return err
	}
	defer conn.Close()
	r := &scripts.Runner{DB: conn, TrackChanges: *track}
	ctx := context.Background()

	var plans []scripts.PackagePlan
	if apply {
		plans, err = r.Apply(ctx, pkgs...)
	} else {
		plans, err = r.Plan(ctx, pkgs...)
	}
	printPlans(plans)
	return err
}

// targetPackages: platform luôn đi đầu vì master và module dùng hàm/bảng của nó
func targetPackages(target, dir, key, schema, version string, local []string) ([]scripts.Package, error) {
	platform, err := db.Platform(version)
	if err != nil {
		return nil, err
	}
	switch target {
	case "platform":
		return []scripts.Package{platform}, nil
	case "master":
		m, err := db.Master(version)
		return []scripts.Package{platform, m}, err
	case "module":
		if dir == "" || key == "" {
			return nil, errors.New("target=module needs --dir and --package")
		}
		s, err := scripts.LoadDir(os.DirFS(dir), ".")
		if err != nil {
			return nil, err
		}
		if schema == "" {
			schema = scripts.ModuleSchema(key)
		}
		return []scripts.Package{platform, {Name: key, Version: version, Schema: schema, Profile: scripts.ProfileModule, LocalTables: local, Scripts: s}}, nil
	}
	return nil, fmt.Errorf("unknown target %q", target)
}

func printPlans(plans []scripts.PackagePlan) {
	w := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
	fmt.Fprintln(w, "PACKAGE\tSCHEMA\tSCRIPT\tSTATUS")
	for _, p := range plans {
		for _, s := range p.Scripts {
			status := string(s.Status)
			if s.RollbackUnsafe {
				status += " (rollback-unsafe)"
			}
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", p.Package, p.Schema, s.Script, status)
		}
	}
	w.Flush()
}

func splitList(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
