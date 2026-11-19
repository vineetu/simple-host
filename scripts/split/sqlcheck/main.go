// sqlcheck inventories platform references to product-owned database objects.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

func findRoot(dir string) (string, error) {
	for {
		if _, e := os.Stat(filepath.Join(dir, "go.mod")); e == nil {
			return dir, nil
		}
		p := filepath.Dir(dir)
		if p == dir {
			return "", fmt.Errorf("no go.mod above working directory")
		}
		dir = p
	}
}
func main() {
	cwd, e := os.Getwd()
	if e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(2)
	}
	root, e := findRoot(cwd)
	if e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(2)
	}
	os.Exit(run(root, os.Args[1:], os.Stdout, os.Stderr))
}
func run(root string, args []string, out, errs io.Writer) int {
	fs := flag.NewFlagSet("sqlcheck", flag.ContinueOnError)
	fs.SetOutput(errs)
	o := options{}
	fs.BoolVar(&o.Warn, "warn", false, "write report and exit zero despite findings")
	fs.BoolVar(&o.Tests, "tests", false, "include tests")
	fs.BoolVar(&o.Prose, "prose", false, "list ordinary-word prose")
	fs.StringVar(&o.Objects, "objects", "scripts/split/hack-objects.txt", "authority file")
	fs.StringVar(&o.Schema, "schema", "host/db/schema.sql", "schema file")
	fs.StringVar(&o.Report, "report", "-", "report path, or - for standard output")
	prop := fs.Bool("propose-objects", false, "propose schema ownership")
	mig := fs.Bool("migrations", false, "check migration ownership")
	pm := fs.Bool("propose-migrations", false, "propose migration ownership")
	fix := fs.Bool("fix", false, "rewrite mixed migration comments")
	if e := fs.Parse(args); e != nil {
		return 2
	}
	fail := func(e error) int { fmt.Fprintln(errs, e); return 2 }
	path := func(s string) string {
		if filepath.IsAbs(s) {
			return s
		}
		return filepath.Join(root, s)
	}
	b, e := os.ReadFile(path(o.Schema))
	if e != nil {
		return fail(e)
	}
	// Ownership analysis needs both halves; the product schema itself is the
	// authoritative object list for the enforced platform query scan.
	var productSchema schemaInfo
	if o.Schema == "host/db/schema.sql" {
		product, err := os.ReadFile(path("products/hack/db/schema.sql"))
		if err != nil {
			return fail(err)
		}
		productSchema = parseSchema(string(product))
		b = append(append(b, '\n'), product...)
	}
	z := parseSchema(string(b))
	objects, e := readObjects(path(o.Objects))
	if e != nil && !(*prop && os.IsNotExist(e)) {
		return fail(e)
	}
	if *prop {
		proposed, info := proposeObjects(z)
		for _, ob := range proposed {
			fmt.Fprintf(out, "%s | %s | %s | %s\n", ob.Kind, ob.Name, ob.Slice, ob.Note)
		}
		fmt.Fprintln(out, "\n# Proposal information")
		for _, i := range info {
			fmt.Fprintln(out, "# "+i)
		}
		fmt.Fprintln(out, "# Never proposed: site_events, site_event_deliveries (MCP Events); event_domains (platform feature); paper_run (platform metrics); image_scan_* (platform scanning)")
		cols, e := migrationColumns(root, z)
		if e != nil {
			return fail(e)
		}
		fmt.Fprintln(out, "# columns added to platform tables by migrations whose name contains hack")
		for _, c := range cols {
			fmt.Fprintln(out, "# "+c)
		}
		a, p := map[string]bool{}, map[string]bool{}
		for _, ob := range objects {
			a[ob.Kind+" "+ob.Name] = true
		}
		for _, ob := range proposed {
			p[ob.Kind+" "+ob.Name] = true
		}
		for _, n := range sortedKeys(a) {
			if !p[n] {
				fmt.Fprintln(out, "# in file, not proposed: "+n)
			}
		}
		for _, n := range sortedKeys(p) {
			if !a[n] {
				fmt.Fprintln(out, "# proposed, not in file: "+n)
			}
		}
		return 0
	}
	if *mig || *pm {
		order, baseline, e := migrationOrder(root)
		if e != nil {
			return fail(e)
		}
		all, e := proposeMigrations(root, order, objects, z)
		if e != nil {
			return fail(e)
		}
		if *pm {
			for _, m := range all {
				fmt.Fprintf(out, "%s | %s | %s\n", m.Name, m.Owner, m.Reason)
			}
			return 0
		}
		text, code, e := checkMigrations(path("scripts/split/migration-owners.txt"), all, baseline, *fix)
		if e != nil {
			return fail(e)
		}
		fmt.Fprint(out, text)
		return code
	}
	if *fix {
		return fail(fmt.Errorf("-fix requires -migrations"))
	}
	if o.Schema == "host/db/schema.sql" && o.Objects == "scripts/split/hack-objects.txt" {
		objects = nil
		for _, name := range sortedKeys(productSchema.Tables) {
			objects = append(objects, object{Kind: "table", Name: name})
		}
		for _, name := range sortedKeys(productSchema.Functions) {
			objects = append(objects, object{Kind: "function", Name: name})
		}
		for _, name := range sortedKeys(productSchema.Triggers) {
			objects = append(objects, object{Kind: "trigger", Name: name})
		}
		for _, column := range sortedKeys(productSchema.Columns) {
			for _, qualified := range productSchema.Columns[column] {
				if !productSchema.Tables[strings.SplitN(qualified, ".", 2)[0]] {
					objects = append(objects, object{Kind: "column", Name: qualified})
				}
			}
		}
		o.Objects = "products/hack/db/schema.sql"
	}
	r, e := scan(root, o, objects, z)
	if e != nil {
		return fail(e)
	}
	o.Objects = rel(root, path(o.Objects))
	o.Schema = rel(root, path(o.Schema))
	if o.Report != "-" {
		o.Report = rel(root, path(o.Report))
	}
	text := render(r, o)
	if o.Report == "-" {
		fmt.Fprint(out, text)
	} else {
		p := path(o.Report)
		if e := os.MkdirAll(filepath.Dir(p), 0755); e != nil {
			return fail(e)
		}
		if e := os.WriteFile(p, []byte(text), 0644); e != nil {
			return fail(e)
		}
	}
	return scanCode(r, o.Warn)
}
