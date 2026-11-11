package run

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"httpcli/internal/httpfile"
	"httpcli/internal/vars"
)

// check evaluates one assertion against a response.
func check(a httpfile.Assertion, resp *Response) error {
	got, found, err := field(a, resp)
	if err != nil {
		return fmt.Errorf("%s: %w", a, err)
	}
	if !found {
		return fmt.Errorf("%s: not found", a)
	}
	ok, err := compare(got, a.Op, a.Value)
	if err != nil {
		return fmt.Errorf("%s: %w", a, err)
	}
	if !ok {
		return fmt.Errorf("%s: got %s", a, vars.Format(got))
	}
	return nil
}

func field(a httpfile.Assertion, resp *Response) (any, bool, error) {
	switch a.Kind {
	case httpfile.AssertStatus:
		return float64(resp.Status), true, nil
	case httpfile.AssertDuration:
		// Fractional milliseconds so sub-millisecond responses still compare.
		return float64(resp.Duration) / float64(time.Millisecond), true, nil
	case httpfile.AssertHeader:
		values := resp.Header.Values(a.Name)
		if len(values) == 0 {
			return nil, false, nil
		}
		return strings.Join(values, ", "), true, nil
	case httpfile.AssertBody:
		if a.Name == "" {
			return string(resp.Body), true, nil
		}
		return jsonPath(resp.Body, a.Name)
	}
	return nil, false, fmt.Errorf("unknown assertion %q", a.Kind)
}

// jsonPath decodes a JSON body and walks it by dotted path, accepting array
// indexes.
func jsonPath(body []byte, path string) (any, bool, error) {
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	var doc any
	if err := decoder.Decode(&doc); err != nil {
		return nil, false, fmt.Errorf("body is not JSON: %w", err)
	}
	value, ok := vars.Path(doc, path)
	return value, ok, nil
}

func compare(got any, op, want string) (bool, error) {
	switch op {
	case "==", "equals":
		return equal(got, want), nil
	case "!=":
		return !equal(got, want), nil
	case ">", ">=", "<", "<=":
		left, ok := number(got)
		if !ok {
			return false, fmt.Errorf("cannot compare %q numerically", vars.Format(got))
		}
		right, err := strconv.ParseFloat(strings.TrimSpace(want), 64)
		if err != nil {
			return false, fmt.Errorf("invalid number %q", want)
		}
		switch op {
		case ">":
			return left > right, nil
		case ">=":
			return left >= right, nil
		case "<":
			return left < right, nil
		default:
			return left <= right, nil
		}
	case "startsWith":
		return strings.HasPrefix(vars.Format(got), want), nil
	case "endsWith":
		return strings.HasSuffix(vars.Format(got), want), nil
	case "includes", "contains":
		return strings.Contains(vars.Format(got), want), nil
	case "matches":
		pattern, err := regexp.Compile(want)
		if err != nil {
			return false, fmt.Errorf("invalid pattern %q: %w", want, err)
		}
		return pattern.MatchString(vars.Format(got)), nil
	case "exists", "isTrue":
		return truthy(got), nil
	case "isFalse":
		return !truthy(got), nil
	case "isNumber":
		_, ok := number(got)
		return ok, nil
	case "isString":
		_, ok := got.(string)
		return ok, nil
	case "isBoolean":
		_, ok := got.(bool)
		return ok, nil
	case "isArray":
		_, ok := got.([]any)
		return ok, nil
	}
	return false, fmt.Errorf("unknown condition %q", op)
}

func equal(got any, want string) bool {
	if got == nil {
		return want == "null"
	}
	if left, ok := number(got); ok {
		if right, err := strconv.ParseFloat(strings.TrimSpace(want), 64); err == nil {
			return left == right
		}
	}
	return vars.Format(got) == want
}

func number(value any) (float64, bool) {
	switch v := value.(type) {
	case float64:
		return v, true
	case json.Number:
		f, err := v.Float64()
		return f, err == nil
	}
	return 0, false
}

func truthy(value any) bool {
	switch v := value.(type) {
	case nil:
		return false
	case string:
		return v != ""
	case bool:
		return v
	}
	if n, ok := number(value); ok {
		return n != 0
	}
	return true
}
