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

// rootPath resolves s against root unless it is already absolute.
func rootPath(root, s string) string {
	if filepath.IsAbs(s) {
		return s
	}
	return filepath.Join(root, s)
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
	owners := fs.Bool("owners", false, "check that each table is queried only by its owner package")
	proposeOwners := fs.Bool("propose-owners", false, "list the tables only one package queries")
	if e := fs.Parse(args); e != nil {
		return 2
	}
	fail := func(e error) int { fmt.Fprintln(errs, e); return 2 }
	z, productSchema, e := loadSchema(root, o.Schema)
	if e != nil {
		return fail(e)
	}
	if *owners || *proposeOwners {
		code, e := checkOwners(root, z, *proposeOwners, out)
		if e != nil {
			return fail(e)
		}
		return code
	}
	objects, e := readObjects(rootPath(root, o.Objects))
	if e != nil && !(*prop && os.IsNotExist(e)) {
		return fail(e)
	}
	if *prop {
		if e := writeObjectProposal(root, objects, z, out); e != nil {
			return fail(e)
		}
		return 0
	}
	if *mig || *pm {
		code, e := runMigrations(root, objects, z, *pm, *fix, out)
		if e != nil {
			return fail(e)
		}
		return code
	}
	if *fix {
		return fail(fmt.Errorf("-fix requires -migrations"))
	}
	if o.Schema == "host/db/schema.sql" && o.Objects == "scripts/split/hack-objects.txt" {
		objects = productObjects(productSchema)
		o.Objects = "products/hack/db/schema.sql"
	}
	r, e := scan(root, o, objects, z)
	if e != nil {
		return fail(e)
	}
	if e := writeReport(root, r, o, out); e != nil {
		return fail(e)
	}
	return scanCode(r, o.Warn)
}

// loadSchema parses the schema file. For the default platform schema it
// appends the product schema, since ownership analysis needs both halves,
// and also returns the product schema alone.
func loadSchema(root, schema string) (z, productSchema schemaInfo, err error) {
	b, e := os.ReadFile(rootPath(root, schema))
	if e != nil {
		return z, productSchema, e
	}
	// Ownership analysis needs both halves; the product schema itself is the
	// authoritative object list for the enforced platform query scan.
	if schema == "host/db/schema.sql" {
		product, err := os.ReadFile(rootPath(root, "products/hack/db/schema.sql"))
		if err != nil {
			return z, productSchema, err
		}
		productSchema = parseSchema(string(product))
		b = append(append(b, '\n'), product...)
	}
	return parseSchema(string(b)), productSchema, nil
}

// writeObjectProposal prints the proposed object ownership, how it was
// derived, the migration-added platform columns, and where the proposal and
// the authority file disagree.
func writeObjectProposal(root string, objects []object, z schemaInfo, out io.Writer) error {
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
		return e
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
	return nil
}

// runMigrations prints the migration ownership proposal when propose is set,
// and otherwise checks it against migration-owners.txt, rewriting its mixed
// comments when fix is set.
func runMigrations(root string, objects []object, z schemaInfo, propose, fix bool, out io.Writer) (int, error) {
	order, baseline, e := migrationOrder(root)
	if e != nil {
		return 2, e
	}
	all, e := proposeMigrations(root, order, objects, z)
	if e != nil {
		return 2, e
	}
	if propose {
		for _, m := range all {
			fmt.Fprintf(out, "%s | %s | %s\n", m.Name, m.Owner, m.Reason)
		}
		return 0, nil
	}
	text, code, e := checkMigrations(rootPath(root, "scripts/split/migration-owners.txt"), all, baseline, fix)
	if e != nil {
		return 2, e
	}
	fmt.Fprint(out, text)
	return code, nil
}

// productObjects lists the product schema's tables, functions, triggers and
// the columns it adds to platform tables, as the scan's object list.
func productObjects(productSchema schemaInfo) []object {
	var objects []object
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
	return objects
}

// writeReport renders the scan with root-relative paths, to standard output
// or to the report file.
func writeReport(root string, r scanResult, o options, out io.Writer) error {
	o.Objects = rel(root, rootPath(root, o.Objects))
	o.Schema = rel(root, rootPath(root, o.Schema))
	if o.Report != "-" {
		o.Report = rel(root, rootPath(root, o.Report))
	}
	text := render(r, o)
	if o.Report == "-" {
		fmt.Fprint(out, text)
		return nil
	}
	p := rootPath(root, o.Report)
	if e := os.MkdirAll(filepath.Dir(p), 0755); e != nil {
		return e
	}
	return os.WriteFile(p, []byte(text), 0644)
}
