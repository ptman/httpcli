// Package vars holds the variables available to a request and the sources
// they can come from (.env, http-client.env.json, files and responses).
package vars

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"math/big"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Scope is a flat variable map. Values may be nested decoded JSON so that
// response bodies can be addressed with dotted paths.
type Scope struct {
	values map[string]any
}

func New() *Scope {
	return &Scope{values: map[string]any{}}
}

func (s *Scope) Set(name string, value any) {
	s.values[name] = value
}

func (s *Scope) SetStrings(values map[string]string) {
	for name, value := range values {
		s.values[name] = value
	}
}

func (s *Scope) Clone() *Scope {
	clone := New()
	for name, value := range s.values {
		clone.values[name] = value
	}
	return clone
}

// Lookup resolves a name or a dotted path such as "login.token" or
// "items.0.id" into nested maps and arrays.
func (s *Scope) Lookup(path string) (any, bool) {
	if v, ok := s.values[path]; ok {
		return v, true
	}
	// Accept the VS Code REST Client spelling of response chaining.
	if name, rest, ok := strings.Cut(path, ".response.body.$"); ok {
		path = name + "." + strings.TrimPrefix(rest, ".")
	} else if name, ok := strings.CutSuffix(path, ".response.body.*"); ok {
		path = name
	}

	return Path(s.values, path)
}

// Path walks decoded JSON by a dotted path such as "login.token" or
// "items.0.id", accepting array indexes.
func Path(value any, path string) (any, bool) {
	for _, part := range strings.Split(path, ".") {
		switch node := value.(type) {
		case map[string]any:
			next, ok := node[part]
			if !ok {
				return nil, false
			}
			value = next
		case []any:
			i, err := strconv.Atoi(part)
			if err != nil || i < 0 || i >= len(node) {
				return nil, false
			}
			value = node[i]
		default:
			return nil, false
		}
	}
	return value, true
}

// resolveVariable expands special variables like $uuid and $timestamp. They are
// computed on lookup rather than stored.
func resolveVariable(name string) (any, bool) {
	// Accept both $uuid and uuid forms.
	clean := strings.TrimPrefix(name, "$")
	switch clean {
	case "uuid":
		return uuid(), true
	case "timestamp":
		return time.Now().UTC().Format(time.RFC3339), true
	case "isoTimestamp":
		return time.Now().UTC().Format(time.RFC3339Nano), true
	case "randomInt":
		n, err := rand.Int(rand.Reader, big.NewInt(1<<31-1))
		if err != nil {
			return 0, false
		}
		return n.Int64(), true
	}
	return nil, false
}

// uuid generates a random v4 UUID string.
func uuid() string {
	r := make([]byte, 16)
	_, _ = rand.Read(r)
	r[6] = (r[6] & 0x0f) | 0x40 // version 4
	r[8] = (r[8] & 0x3f) | 0x80 // variant 10
	h := fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		r[0:4], r[4:6], r[6:8], r[8:10], r[10:16])
	return h
}

var placeholder = regexp.MustCompile(`\{\{(.*?)\}\}`)

// Interpolate replaces every {{name}} in text and fails on unknown names so a
// typo cannot silently send a request to the wrong place. Names starting with
// % are percent-encoded (encodeURIComponent-style, safe in a path segment or a
// query value) before substitution. Special variables like $uuid and $timestamp
// are expanded on lookup.
func (s *Scope) Interpolate(text string) (string, error) {
	var b strings.Builder
	last := 0
	for _, match := range placeholder.FindAllStringSubmatchIndex(text, -1) {
		name := strings.TrimSpace(text[match[2]:match[3]])
		encoded := false
		if name, encoded = strings.CutPrefix(name, "%"); encoded {
			name = strings.TrimSpace(name)
		}
		value, found := s.Lookup(name)
		if !found {
			if dyn, ok := resolveVariable(name); ok {
				value, found = dyn, true
			}
		}
		if !found {
			return "", fmt.Errorf("unknown variable %q", name)
		}
		b.WriteString(text[last:match[0]])
		if encoded {
			b.WriteString(encodeComponent(Format(value)))
		} else {
			b.WriteString(Format(value))
		}
		last = match[1]
	}
	b.WriteString(text[last:])
	return b.String(), nil
}

// encodeComponent percent-encodes a value for use inside a URL, keeping the
// unreserved characters that encodeURIComponent keeps. Reserved characters such
// as &, = and / are escaped, so the result is safe in a path segment and in a
// query value.
func encodeComponent(value string) string {
	var b strings.Builder
	for i := 0; i < len(value); i++ {
		c := value[i]
		switch {
		case 'a' <= c && c <= 'z', 'A' <= c && c <= 'Z', '0' <= c && c <= '9',
			c == '-', c == '_', c == '.', c == '~', c == '!', c == '*', c == '\'', c == '(', c == ')':
			b.WriteByte(c)
		default:
			_, _ = fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}

// Format renders a variable for substitution into a request.
func Format(value any) string {
	switch v := value.(type) {
	case nil:
		return ""
	case string:
		return v
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	case json.Number:
		return v.String()
	case bool:
		return strconv.FormatBool(v)
	default:
		data, err := json.Marshal(v)
		if err != nil {
			return fmt.Sprint(v)
		}
		return string(data)
	}
}

// LoadDotEnv reads a .env file. A missing file yields no values.
func LoadDotEnv(path string) (map[string]string, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	values := map[string]string{}
	for i, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = trimExport(line)
		name, value, ok := strings.Cut(line, "=")
		name = strings.TrimSpace(name)
		if !ok || name == "" {
			return nil, fmt.Errorf("%s:%d: expected NAME=value", path, i+1)
		}
		values[name] = unquote(stripComment(strings.TrimSpace(value)))
	}
	return values, nil
}

// trimExport drops a leading "export " or "export\t".
func trimExport(line string) string {
	rest, ok := strings.CutPrefix(line, "export")
	if !ok || (!strings.HasPrefix(rest, " ") && !strings.HasPrefix(rest, "\t")) {
		return line
	}
	return strings.TrimSpace(rest)
}

// stripComment removes a trailing "# comment" that is outside quotes.
func stripComment(value string) string {
	var quote byte
	for i := 0; i < len(value); i++ {
		switch c := value[i]; {
		case quote != 0:
			if c == quote {
				quote = 0
			}
		case c == '"' || c == '\'':
			quote = c
		case c == '#' && (i == 0 || value[i-1] == ' ' || value[i-1] == '\t'):
			return strings.TrimSpace(value[:i])
		}
	}
	return value
}

// LoadEnvFile reads the JetBrains http-client.env.json layout. The "$shared"
// block always applies; env selects one of the named environments.
func LoadEnvFile(path, env string) (map[string]string, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		if env != "" {
			return nil, fmt.Errorf("%s not found, cannot use environment %q", path, env)
		}
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	var file map[string]map[string]any
	if err := json.Unmarshal(data, &file); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	values := map[string]string{}
	for name, value := range file["$shared"] {
		values[name] = Format(value)
	}
	if env == "" {
		return values, nil
	}
	selected, ok := file[env]
	if !ok {
		return nil, fmt.Errorf("%s: environment %q not found (available: %s)", path, env, envNames(file))
	}
	for name, value := range selected {
		values[name] = Format(value)
	}
	return values, nil
}

func envNames(file map[string]map[string]any) string {
	var names []string
	for name := range file {
		if name != "$shared" {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	if len(names) == 0 {
		return "none"
	}
	return strings.Join(names, ", ")
}

func unquote(value string) string {
	if len(value) < 2 {
		return value
	}
	quote := value[0]
	if (quote != '"' && quote != '\'') || value[len(value)-1] != quote {
		return value
	}
	inner := value[1 : len(value)-1]
	if quote == '"' {
		inner = strings.ReplaceAll(inner, `\"`, `"`)
		inner = strings.ReplaceAll(inner, `\n`, "\n")
		inner = strings.ReplaceAll(inner, `\t`, "\t")
	}
	return inner
}
