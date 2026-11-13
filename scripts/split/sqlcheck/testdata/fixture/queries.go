package fixture

import (
	"bytes"
	_ "embed"
	"example.test/fixture/shared"
	"fmt"
	"strings"
)

//go:embed assets/query.sql
var embedded string
var Plain = "SELECT * FROM event_teams"
var Combined = "SELECT " + columns + " FROM events"
var NotHack = "SELECT * FROM event_domains WHERE users.event_account"
var Ambiguous = "UPDATE site_notifications SET events=$1"
var Raw = `SELECT *
 FROM event_teams
 JOIN events ON true`
var hackColumns = map[string][]string{"event_teams": {"id"}, "oauth_grants": {"selected_team_id"}, "site_notifications": {"events"}}
var jsonObject = map[string]any{"events": 1}
var Prose = "events happen every day"
var NamedProse = "event_teams is a product table"

func Runtime(suffix, table string) string { return "SELECT * FROM event_teams " + suffix }
func Unknown(table string) string         { return "SELECT * FROM " + table }
func Formatted(id int) string             { return fmt.Sprintf("SELECT * FROM event_teams WHERE id=%d", id) }
func Append(s string) string              { s += " FROM event_teams"; return s }
func IgnoreError() error                  { return fmt.Errorf("SELECT * FROM event_teams") }

var OwnedColumn = "SELECT g.selected_team_id FROM oauth_grants g"
var InnerQueries = map[string][]string{"queries": {"SELECT * FROM event_teams"}}

func LocalConstants() string {
	const localColumns = columns + ", extra"
	return "SELECT " + localColumns + " FROM event_teams"
}
func preserve(s string) string            { return s }
func NestedConstant(suffix string) string { return suffix + preserve("SELECT * FROM event_teams") }

var UpperQuery = "SELECT * FROM EVENTS"
var SchemaQuery = `SELECT * FROM "public"."events"`
var UnresolvedColumn = "SELECT g.selected_team_id"

type Query string

const NamedQuery Query = "SELECT * FROM events"

var StructQueries = []struct{ name, query string }{{"x", `SELECT count(*) FROM event_teams`}}
var StructList = map[string]struct{ category string }{"x": {"events"}, "event_teams": {"other"}}
var Route = "DELETE /v1/events/{name}"
var HackRoute = "DELETE /v1/hack/events/..."
var CrossPackage = "SELECT " + shared.Columns + " FROM event_teams"

func Joined(table string) string { return strings.Join([]string{"SELECT * FROM", table}, " ") }
func JoinedHack(suffix string) string {
	return strings.Join([]string{"SELECT * FROM event_teams", suffix}, " ")
}
func Built(suffix string, id int) string {
	var b strings.Builder
	b.WriteString("SELECT * FROM widgets")
	b.WriteString(suffix)
	fmt.Fprintf(&b, " WHERE id=%d", id)
	return b.String()
}
func Buffered(suffix string) string {
	b := &bytes.Buffer{}
	b.WriteString("SELECT * FROM widgets")
	b.WriteString(suffix)
	return b.String()
}

var MixedQuery = "SELECT * FROM Events"
var JoinedConstant = strings.Join([]string{"SELECT * FROM", "events"}, " ")
var JoinedAmbiguous = strings.Join([]string{"UPDATE site_notifications SET", "events", "=$1"}, " ")
var JoinedProse = strings.Join([]string{"about", "event_teams"}, " ")

func TwoBuilders(first, second string) {
	var a, b strings.Builder
	a.WriteString("SELECT * FROM widgets")
	b.WriteString("SELECT * FROM gadgets")
	a.WriteString(first)
	b.WriteString(second)
}

type builderHolder struct{ b strings.Builder }

func FieldBuilders(first, second string) {
	var a, b builderHolder
	a.b.WriteString("SELECT * FROM widgets")
	b.b.WriteString("SELECT * FROM gadgets")
	a.b.WriteString(first)
	b.b.WriteString(second)
}
