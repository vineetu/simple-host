package rules

import "math"

// Kind mirrors SQLite storage classes, plus binding/literal booleans and lists.
type Kind uint8

const (
	KindNull Kind = iota
	KindBool
	KindInt
	KindFloat
	KindText
	KindBlob
	KindList
)

// Value uses only the field selected by Kind. Stored text may contain any bytes;
// stored floats may be infinite. Literals and bindings have stricter constraints.
type Value struct {
	Kind  Kind
	Bool  bool
	Int   int64
	Float float64
	Text  string
	List  []string
}

func Null() Value       { return Value{Kind: KindNull} }
func Bool(b bool) Value { return Value{Kind: KindBool, Bool: b} }
func Int(i int64) Value { return Value{Kind: KindInt, Int: i} }
func Float(f float64) Value {
	if math.IsNaN(f) {
		panic("rules: NaN must be represented as Null()")
	}
	return Value{Kind: KindFloat, Float: f}
}
func Text(s string) Value        { return Value{Kind: KindText, Text: s} }
func List(items ...string) Value { return Value{Kind: KindList, List: append([]string(nil), items...)} }
