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
	set, err := decodeWire(raw)
	if err != nil {
		return PolicySet{}, []RuleError{*err}
	}
	if errs := duplicateNames(set); len(errs) != 0 {
		return PolicySet{}, errs
	}
	return set, nil
}

// decodeWire decodes the set strictly: exact field names, version 1, policies an array.
func decodeWire(raw []byte) (PolicySet, *RuleError) {
	jsonErr := func(err error) (PolicySet, *RuleError) { return PolicySet{}, refusal("json", -1, err.Error()) }
	var wire struct {
		Version  json.RawMessage `json:"version"`
		Policies json.RawMessage `json:"policies"`
	}
	if err := strictJSON(raw, &wire); err != nil {
		return jsonErr(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return jsonErr(err)
	}
	for name := range fields {
		if name != "version" && name != "policies" {
			return PolicySet{}, refusal("json", -1, "unknown field "+name)
		}
	}
	if len(wire.Policies) == 0 || jsonNull(wire.Policies) {
		return PolicySet{}, refusal("json", -1, "policies must be an array")
	}
	if len(wire.Version) != 0 && string(bytes.TrimSpace(wire.Version)) != "1" {
		return PolicySet{}, refusal("version", -1, "version must be 1")
	}
	set := PolicySet{Version: 1}
	if err := strictJSON(wire.Policies, &set.Policies); err != nil {
		return jsonErr(err)
	}
	var objects []map[string]json.RawMessage
	if err := json.Unmarshal(wire.Policies, &objects); err != nil {
		return jsonErr(err)
	}
	for i, obj := range objects {
		if err := checkPolicyObject(obj, set.Policies[i]); err != nil {
			return PolicySet{}, err
		}
	}
	return set, nil
}

var policyFields = map[string]bool{"name": true, "command": true, "using": true, "check": true, "restrictive": true}

// checkPolicyObject refuses what encoding/json lets through for one policy:
// a null object or field, a case-folded field name, and a forbidden clause
// that is present, even as "" (Policy's string fields cannot keep presence).
func checkPolicyObject(obj map[string]json.RawMessage, p Policy) *RuleError {
	if obj == nil {
		return refusal("json", -1, "policy must be an object")
	}
	for name, v := range obj {
		if !policyFields[name] {
			return refusal("json", -1, "unknown field "+name)
		}
		if jsonNull(v) {
			return refusal("json", -1, "policy fields cannot be null")
		}
	}
	for _, clause := range []string{"using", "check"} {
		_, present := obj[clause]
		if _, forbidden := clauseRule(p.Command, clause); present && forbidden {
			return &RuleError{Policy: p.Name, Clause: clause, Position: -1, Message: "clause is not allowed for this command"}
		}
	}
	return nil
}

func jsonNull(v json.RawMessage) bool { return bytes.Equal(bytes.TrimSpace(v), []byte("null")) }

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
