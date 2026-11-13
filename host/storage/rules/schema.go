package rules

type Type uint8

const (
	TypeBool Type = iota + 1
	TypeText
	TypeNumber
	TypeTextList
)

type Binding struct {
	Type         Type
	Nullable     bool // Only text and number may be nullable.
	AddCheckOnly bool
}
type BindingSchema map[string]Binding
type Values map[string]Value
type Column struct{ Name string }
type Table struct {
	Name    string
	Columns []Column
	RowKey  string // rowid, _rowid_, oid or the quoted SQL identifier "key".
}
type Row map[string]Value
