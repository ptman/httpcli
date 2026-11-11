package httpfile

import (
	"fmt"
	"strings"
)

var assertOps = map[string]bool{
	"==": true, "equals": true, "!=": true,
	">": true, ">=": true, "<": true, "<=": true,
	"startsWith": true, "endsWith": true, "includes": true, "contains": true,
	"exists": true, "isTrue": true, "isFalse": true, "isNumber": true,
	"isBoolean": true, "isString": true, "isArray": true, "matches": true,
}

var noValueOps = map[string]bool{
	"exists": true, "isTrue": true, "isFalse": true, "isNumber": true,
	"isBoolean": true, "isString": true, "isArray": true,
}

// parseAssertion reads a "?? field [name] condition [value]" line.
func parseAssertion(s string, no int) (Assertion, error) {
	tokens := strings.Fields(strings.TrimSpace(strings.TrimPrefix(s, "??")))
	if len(tokens) == 0 {
		return Assertion{}, fmt.Errorf("line %d: empty assertion", no)
	}

	a := Assertion{Kind: AssertKind(strings.ToLower(tokens[0])), Line: no}
	tokens = tokens[1:]
	switch a.Kind {
	case AssertStatus, AssertDuration:
	case AssertHeader:
		if len(tokens) == 0 || assertOps[tokens[0]] {
			return Assertion{}, fmt.Errorf("line %d: header assertion needs a header name", no)
		}
		a.Name = tokens[0]
		tokens = tokens[1:]
	case AssertBody:
		if len(tokens) > 0 && !assertOps[tokens[0]] {
			a.Name = tokens[0]
			tokens = tokens[1:]
		}
	default:
		return Assertion{}, fmt.Errorf("line %d: unknown assertion %q (want status, header, body or duration)", no, a.Kind)
	}

	if len(tokens) == 0 {
		return Assertion{}, fmt.Errorf("line %d: missing condition in %q", no, s)
	}
	a.Op = tokens[0]
	if !assertOps[a.Op] {
		return Assertion{}, fmt.Errorf("line %d: unknown condition %q", no, a.Op)
	}
	a.Value = strings.Join(tokens[1:], " ")
	if noValueOps[a.Op] {
		if a.Value != "" {
			return Assertion{}, fmt.Errorf("line %d: condition %q takes no value", no, a.Op)
		}
	} else if a.Value == "" {
		return Assertion{}, fmt.Errorf("line %d: condition %q needs a value", no, a.Op)
	}
	return a, nil
}
