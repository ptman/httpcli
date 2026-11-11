package vars

import (
	"encoding/json"
	"strconv"
	"testing"
	"time"
)

func TestInterpolate(t *testing.T) {
	scope := New()
	scope.SetStrings(map[string]string{"host": "example.com", "path": "/users"})
	scope.Set("port", 8080)
	scope.Set("enabled", true)

	got, err := scope.Interpolate("https://{{host}}:{{port}}{{path}}?on={{enabled}}")
	if err != nil {
		t.Fatalf("Interpolate() error: %v", err)
	}
	if want := "https://example.com:8080/users?on=true"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestInterpolateUnknownVariable(t *testing.T) {
	_, err := New().Interpolate("{{missing}}")
	if err == nil {
		t.Fatal("Interpolate() error = nil, want error")
	}
	if want := `unknown variable "missing"`; err.Error() != want {
		t.Errorf("error = %q, want %q", err, want)
	}
}

func TestInterpolateLeavesTextWithoutPlaceholders(t *testing.T) {
	got, err := New().Interpolate(`{"a": 1}`)
	if err != nil || got != `{"a": 1}` {
		t.Errorf("got %q, err %v", got, err)
	}
}

func TestLookupNestedJSON(t *testing.T) {
	var body any
	if err := json.Unmarshal([]byte(`{"token":"abc","user":{"roles":["admin","dev"]},"items":[{"id":7}]}`), &body); err != nil {
		t.Fatal(err)
	}
	scope := New()
	scope.Set("login", body)

	tests := map[string]string{
		"login.token":        "abc",
		"login.user.roles":   `["admin","dev"]`,
		"login.user.roles.1": "dev",
		"login.items.0.id":   "7",
		"login":              `{"items":[{"id":7}],"token":"abc","user":{"roles":["admin","dev"]}}`,
	}
	for path, want := range tests {
		value, ok := scope.Lookup(path)
		if !ok {
			t.Errorf("Lookup(%q) not found", path)
			continue
		}
		if got := Format(value); got != want {
			t.Errorf("Lookup(%q) = %q, want %q", path, got, want)
		}
	}

	if _, ok := scope.Lookup("login.missing"); ok {
		t.Error("Lookup(login.missing) found, want missing")
	}
	if _, ok := scope.Lookup("login.items.9"); ok {
		t.Error("Lookup(login.items.9) found, want missing")
	}
}

func TestLookupRestClientResponsePath(t *testing.T) {
	scope := New()
	scope.Set("login", map[string]any{"token": "abc"})
	scope.Set("plain", "text")

	value, ok := scope.Lookup("login.response.body.$.token")
	if !ok || Format(value) != "abc" {
		t.Errorf("jsonpath lookup = %v, %v", Format(value), ok)
	}
	value, ok = scope.Lookup("plain.response.body.*")
	if !ok || Format(value) != "text" {
		t.Errorf("body lookup = %v, %v", Format(value), ok)
	}
}

func TestCloneIsolation(t *testing.T) {
	base := New()
	base.Set("a", "1")
	clone := base.Clone()
	clone.Set("b", "2")

	if _, ok := base.Lookup("b"); ok {
		t.Error("clone leaked a variable into the parent")
	}
	if value, _ := clone.Lookup("a"); Format(value) != "1" {
		t.Errorf("clone lost parent value: %v", value)
	}
}

func TestInterpolatePercentEncoding(t *testing.T) {
	scope := New()
	scope.Set("user", "hello world")
	scope.Set("path", "items/42")

	got, err := scope.Interpolate("{{%user}}")
	if err != nil {
		t.Fatalf("Interpolate() error: %v", err)
	}
	if want := "hello%20world"; got != want {
		t.Errorf("%%user = %q, want %q", got, want)
	}

	got, err = scope.Interpolate("{{user}}")
	if err != nil {
		t.Fatalf("Interpolate() error: %v", err)
	}
	if want := "hello world"; got != want {
		t.Errorf("user = %q, want %q", got, want)
	}

	got, err = scope.Interpolate("https://example.com/{{%path}}")
	if err != nil {
		t.Fatalf("Interpolate() error: %v", err)
	}
	if want := "https://example.com/items%2F42"; got != want {
		t.Errorf("URL with percent-path = %q, want %q", got, want)
	}

	scope.Set("query", "a&b=c d")
	got, err = scope.Interpolate("https://example.com/?q={{%query}}")
	if err != nil {
		t.Fatalf("Interpolate() error: %v", err)
	}
	if want := "https://example.com/?q=a%26b%3Dc%20d"; got != want {
		t.Errorf("URL with percent-query = %q, want %q", got, want)
	}
}

func TestPath(t *testing.T) {
	value := map[string]any{
		"user":  map[string]any{"name": "ada"},
		"items": []any{map[string]any{"id": 7}},
	}
	got, ok := Path(value, "user.name")
	if !ok || got != "ada" {
		t.Errorf("Path(user.name) = %v, %v, want ada", got, ok)
	}
	got, ok = Path(value, "items.0.id")
	if !ok || Format(got) != "7" {
		t.Errorf("Path(items.0.id) = %v, %v, want 7", got, ok)
	}
	if _, ok := Path(value, "items.2"); ok {
		t.Error("Path(items.2) found, want missing")
	}
}

func TestInterpolateDynamicVariables(t *testing.T) {
	scope := New()

	tests := []struct {
		name string
		want func(string) bool
	}{
		{"{{uuid}}", func(got string) bool {
			return len(got) == 36 && got[8] == '-' && got[14] == '4'
		}},
		{"{{timestamp}}", func(got string) bool {
			_, err := time.Parse(time.RFC3339, got)
			return err == nil
		}},
		{"{{isoTimestamp}}", func(got string) bool {
			_, err := time.Parse(time.RFC3339Nano, got)
			return err == nil
		}},
		{"{{randomInt}}", func(got string) bool {
			n, err := strconv.Atoi(got)
			return err == nil && n >= 0
		}},
	}
	for _, tc := range tests {
		got, err := scope.Interpolate("{{" + tc.name[2:len(tc.name)-2] + "}}")
		if err != nil {
			t.Fatalf("%s: error: %v", tc.name, err)
		}
		if !tc.want(got) {
			t.Errorf("%s = %q, wanted valid %s", tc.name, got, tc.name)
		}
	}
}
