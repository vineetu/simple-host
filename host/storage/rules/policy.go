package rules

import (
	"bytes"
	"encoding/json"
	"io"
)

type Command string

const (
	CmdRead   Command = "read"
	CmdAdd    Command = "add"
	CmdEdit   Command = "edit"
	CmdDelete Command = "delete"
	CmdAll    Command = "all"
)

type Policy struct {
	Name        string  `json:"name"`
	Command     Command `json:"command"`
	Using       string  `json:"using,omitempty"`
	Check       string  `json:"check,omitempty"`
	Restrictive bool    `json:"restrictive,omitempty"`
}
type PolicySet struct {
	Version  int      `json:"version,omitempty"`
	Policies []Policy `json:"policies"`
}

// DecodeSet preserves clause bytes and canonicalizes an omitted version to 1.
func DecodeSet(raw []byte) (PolicySet, []RuleError) {
	fail := func(clause, msg string) (PolicySet, []RuleError) {
		return PolicySet{}, []RuleError{*refusal(clause, -1, msg)}
	}
	var wire struct {
		Version  json.RawMessage `json:"version"`
		Policies json.RawMessage `json:"policies"`
	}
	if err := strictJSON(raw, &wire); err != nil {
		return fail("json", err.Error())
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return fail("json", err.Error())
	}
	for name := range fields {
		if name != "version" && name != "policies" {
			return fail("json", "unknown field "+name)
		}
	}
	if len(wire.Policies) == 0 || bytes.Equal(bytes.TrimSpace(wire.Policies), []byte("null")) {
		return fail("json", "policies must be an array")
	}
	set := PolicySet{Version: 1}
	if len(wire.Version) != 0 && string(bytes.TrimSpace(wire.Version)) != "1" {
		return fail("version", "version must be 1")
	}
	if err := strictJSON(wire.Policies, &set.Policies); err != nil {
		return fail("json", err.Error())
	}
	// encoding/json accepts null for scalar fields and for struct elements.
	// Those are not policy objects or values in the wire contract.
	var objects []map[string]json.RawMessage
	if err := json.Unmarshal(wire.Policies, &objects); err != nil {
		return fail("json", err.Error())
	}
	for i, obj := range objects {
		if obj == nil {
			return fail("json", "policy must be an object")
		}
		for name, v := range obj {
			switch name {
			case "name", "command", "using", "check", "restrictive":
			default:
				return fail("json", "unknown field "+name)
			}
			if bytes.Equal(bytes.TrimSpace(v), []byte("null")) {
				return fail("json", "policy fields cannot be null")
			}
		}
		// Policy's string fields cannot retain presence. Refuse a supplied
		// forbidden clause here, even when its value is the empty string.
		p := set.Policies[i]
		for _, clause := range []string{"using", "check"} {
			_, present := obj[clause]
			forbidden := clause == "using" && p.Command == CmdAdd || clause == "check" && (p.Command == CmdRead || p.Command == CmdDelete)
			if present && forbidden {
				return PolicySet{}, []RuleError{{Policy: p.Name, Clause: clause, Position: -1, Message: "clause is not allowed for this command"}}
			}
		}
	}
	if errs := duplicateNames(set); len(errs) != 0 {
		return PolicySet{}, errs
	}
	return set, nil
}

func strictJSON(raw []byte, out any) error {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(out); err != nil {
		return err
	}
	var extra any
	if err := d.Decode(&extra); err != io.EOF {
		if err != nil {
			return err
		}
		return refusal("json", -1, "expected one JSON value")
	}
	return nil
}

func EncodeSet(set PolicySet) []byte {
	if set.Version == 0 {
		set.Version = 1
	}
	b, _ := json.Marshal(set)
	return b
}

func duplicateNames(set PolicySet) []RuleError {
	seen := map[string]bool{}
	var errs []RuleError
	for _, p := range set.Policies {
		if p.Name == "" {
			continue
		}
		if seen[p.Name] {
			errs = append(errs, RuleError{Policy: p.Name, Clause: "name", Position: -1, Message: "duplicate policy name"})
		}
		seen[p.Name] = true
	}
	return errs
}

type Op uint8

const (
	OpRead Op = iota + 1
	OpAdd
	OpEdit
	OpDelete
)

type Predicate struct {
	SQL  string
	Args []Value
}
type Tri uint8

const (
	False Tri = iota
	True
	Unknown
)

// Plan and Compile are placeholders for R1-S2a. No runtime caller uses them yet.
type Plan struct{}

func Compile(PolicySet, Table, BindingSchema, Limits) (*Plan, []RuleError) {
	return nil, []RuleError{*refusal("shape", -1, "not built yet")}
}

type Matrix struct{ Read, Add, Edit, Delete string }
type ConvertInput struct {
	Default      Matrix
	Tables       map[string]Matrix
	ApproveFirst bool
	Kind         string
}
type ConvertOutput struct {
	Default PolicySet
	Tables  map[string]PolicySet
	Manual  []string
}

// Convert is implemented by R2-S6. An unfinished converter converts nothing.
func Convert(ConvertInput) ConvertOutput { return ConvertOutput{Manual: []string{"not built yet"}} }
