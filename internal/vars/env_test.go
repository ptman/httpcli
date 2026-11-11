package vars

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func write(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadDotEnv(t *testing.T) {
	path := write(t, ".env", `
# comment
host=https://example.com
export token = abc
quoted="hello world"
single='it works'
empty=
`)
	values, err := LoadDotEnv(path)
	if err != nil {
		t.Fatalf("LoadDotEnv() error: %v", err)
	}
	want := map[string]string{
		"host":   "https://example.com",
		"token":  "abc",
		"quoted": "hello world",
		"single": "it works",
		"empty":  "",
	}
	for name, value := range want {
		if values[name] != value {
			t.Errorf("%s = %q, want %q", name, values[name], value)
		}
	}
	if len(values) != len(want) {
		t.Errorf("got %d values, want %d: %#v", len(values), len(want), values)
	}
}

func TestLoadDotEnvMissingFile(t *testing.T) {
	values, err := LoadDotEnv(filepath.Join(t.TempDir(), ".env"))
	if err != nil {
		t.Fatalf("LoadDotEnv() error: %v", err)
	}
	if len(values) != 0 {
		t.Errorf("values = %#v, want none", values)
	}
}

func TestLoadDotEnvMalformed(t *testing.T) {
	path := write(t, ".env", "not an assignment\n")
	_, err := LoadDotEnv(path)
	if err == nil || !strings.Contains(err.Error(), "NAME=value") {
		t.Fatalf("error = %v, want NAME=value hint", err)
	}
	if !strings.Contains(err.Error(), ":1:") {
		t.Errorf("error has no line number: %v", err)
	}
}

func TestLoadEnvFile(t *testing.T) {
	path := write(t, "http-client.env.json", `{
  "$shared": {"host": "https://shared.example.com", "timeout": 10},
  "dev": {"host": "https://dev.example.com", "token": "dev-token"},
  "prod": {"host": "https://prod.example.com"}
}`)

	shared, err := LoadEnvFile(path, "")
	if err != nil {
		t.Fatalf("LoadEnvFile() error: %v", err)
	}
	if shared["host"] != "https://shared.example.com" || shared["timeout"] != "10" {
		t.Errorf("shared = %#v", shared)
	}

	dev, err := LoadEnvFile(path, "dev")
	if err != nil {
		t.Fatalf("LoadEnvFile(dev) error: %v", err)
	}
	if dev["host"] != "https://dev.example.com" || dev["token"] != "dev-token" {
		t.Errorf("dev overrides shared: %#v", dev)
	}
	if dev["timeout"] != "10" {
		t.Errorf("shared value lost: %#v", dev)
	}
}

func TestLoadEnvFileUnknownEnvironment(t *testing.T) {
	path := write(t, "http-client.env.json", `{"dev": {}, "prod": {}}`)
	_, err := LoadEnvFile(path, "stage")
	if err == nil {
		t.Fatal("error = nil, want error")
	}
	for _, want := range []string{`"stage"`, "dev", "prod"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q missing %q", err, want)
		}
	}
}

func TestLoadEnvFileMissing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "http-client.env.json")
	values, err := LoadEnvFile(path, "")
	if err != nil || len(values) != 0 {
		t.Fatalf("values = %#v, err = %v", values, err)
	}
	if _, err := LoadEnvFile(path, "dev"); err == nil {
		t.Fatal("error = nil, want error for an explicitly requested environment")
	}
}

func TestLoadDotEnvCommentsAndExport(t *testing.T) {
	path := write(t, ".env", `plain=value # trailing comment
export	TAB=tabbed
quoted="a # b" # after
expected=abc#def
`)
	values, err := LoadDotEnv(path)
	if err != nil {
		t.Fatalf("LoadDotEnv() error: %v", err)
	}
	want := map[string]string{
		"plain":    "value",
		"TAB":      "tabbed",
		"quoted":   "a # b",
		"expected": "abc#def",
	}
	for name, value := range want {
		if values[name] != value {
			t.Errorf("%s = %q, want %q", name, values[name], value)
		}
	}
}

func TestLoadEnvFileInvalidJSON(t *testing.T) {
	path := write(t, "http-client.env.json", `{`)
	if _, err := LoadEnvFile(path, ""); err == nil {
		t.Fatal("error = nil, want error")
	}
}
