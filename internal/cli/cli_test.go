package cli

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func invoke(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var stdout, stderr strings.Builder
	code := Main(args, &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

func writeFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestRunFirstRequestOnly(t *testing.T) {
	var hits int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		_, _ = fmt.Fprint(w, `{"path":"`+r.URL.Path+`"}`)
	}))
	defer server.Close()

	dir := t.TempDir()
	path := writeFile(t, dir, "api.http", `### One
GET `+server.URL+`/one

### Two
GET `+server.URL+`/two
`)

	code, stdout, stderr := invoke(t, "run", path)
	if code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, stderr)
	}
	if got := atomic.LoadInt32(&hits); got != 1 {
		t.Errorf("server hits = %d, want 1", got)
	}
	for _, want := range []string{`"path": "/one"`, "### One"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout %q missing %q", stdout, want)
		}
	}
}

func TestRunFlagsBeforeFileName(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer server.Close()

	dir := t.TempDir()
	file := writeFile(t, dir, "api.http", "GET "+server.URL+"\n")

	code, stdout, stderr := invoke(t, "run", "-output", "none", file)
	if code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, stderr)
	}
	if stdout != "" {
		t.Errorf("stdout = %q, want empty", stdout)
	}
}

func TestRunAll(t *testing.T) {
	var hits int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
	}))
	defer server.Close()

	dir := t.TempDir()
	path := writeFile(t, dir, "api.http", `GET `+server.URL+`/one

###
GET `+server.URL+`/two
`)

	if code, _, stderr := invoke(t, "run", "-all", path); code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, stderr)
	}
	if got := atomic.LoadInt32(&hits); got != 2 {
		t.Errorf("server hits = %d, want 2", got)
	}
}

func TestRunByName(t *testing.T) {
	var path string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
	}))
	defer server.Close()

	dir := t.TempDir()
	file := writeFile(t, dir, "api.http", `###
# @name login
POST `+server.URL+`/login

###
# @name profile
# @ref login
GET `+server.URL+`/profile
`)

	code, _, stderr := invoke(t, "run", "-name", "profile", file)
	if code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, stderr)
	}
	if path != "/profile" {
		t.Errorf("last path = %q, want /profile (login must run first)", path)
	}
}

func TestRunUnknownName(t *testing.T) {
	dir := t.TempDir()
	file := writeFile(t, dir, "api.http", "GET https://example.com\n")
	code, _, stderr := invoke(t, "run", "-name", "missing", file)
	if code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	if !strings.Contains(stderr, `no request named "missing"`) {
		t.Errorf("stderr = %q", stderr)
	}
}

func TestRunByLine(t *testing.T) {
	var path string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
	}))
	defer server.Close()

	dir := t.TempDir()
	file := writeFile(t, dir, "api.http", `GET `+server.URL+`/one

###
GET `+server.URL+`/two
`)

	if code, _, stderr := invoke(t, "run", "-line", "4", file); code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, stderr)
	}
	if path != "/two" {
		t.Errorf("path = %q, want /two", path)
	}
}

func TestRunAssertionFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	dir := t.TempDir()
	file := writeFile(t, dir, "api.http", `GET `+server.URL+`
?? status == 200
`)

	code, stdout, stderr := invoke(t, "run", file)
	if code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
	if !strings.Contains(stdout, "500") {
		t.Errorf("stdout = %q", stdout)
	}
	if !strings.Contains(stderr, "assertion failed: line 2: status == 200: got 500") {
		t.Errorf("stderr = %q", stderr)
	}
}

func TestRunTransportError(t *testing.T) {
	dir := t.TempDir()
	file := writeFile(t, dir, "api.http", "GET http://127.0.0.1:1/nope\n")
	code, _, stderr := invoke(t, "run", file)
	if code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
	if !strings.Contains(stderr, "error:") {
		t.Errorf("stderr = %q", stderr)
	}
}

func TestRunOutputFormats(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"ok":true}`)
	}))
	defer server.Close()

	dir := t.TempDir()
	file := writeFile(t, dir, "api.http", "GET "+server.URL+"\n")

	t.Run("short", func(t *testing.T) {
		code, stdout, _ := invoke(t, "run", file)
		if code != 0 {
			t.Fatalf("exit = %d", code)
		}
		for _, want := range []string{"GET " + server.URL, "200 OK", "\"ok\": true"} {
			if !strings.Contains(stdout, want) {
				t.Errorf("stdout %q missing %q", stdout, want)
			}
		}
	})

	t.Run("body", func(t *testing.T) {
		_, stdout, _ := invoke(t, "run", "-output", "body", file)
		if strings.Contains(stdout, "200 OK") {
			t.Errorf("body output contains the summary: %q", stdout)
		}
		if !strings.Contains(stdout, "\"ok\": true") {
			t.Errorf("stdout = %q", stdout)
		}
	})

	t.Run("headers", func(t *testing.T) {
		_, stdout, _ := invoke(t, "run", "-output", "headers", file)
		if !strings.Contains(stdout, "Content-Type: application/json") {
			t.Errorf("stdout = %q", stdout)
		}
		if strings.Contains(stdout, "\"ok\"") {
			t.Errorf("headers output contains the body: %q", stdout)
		}
	})

	t.Run("none", func(t *testing.T) {
		_, stdout, _ := invoke(t, "run", "-output", "none", file)
		if stdout != "" {
			t.Errorf("stdout = %q, want empty", stdout)
		}
	})
}

func TestRunEnvironmentAndOverride(t *testing.T) {
	var paths []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
	}))
	defer server.Close()

	dir := t.TempDir()
	writeFile(t, dir, ".env", "host="+server.URL+"\npath=env\n")
	writeFile(t, dir, "http-client.env.json", `{"$shared":{"host":"`+server.URL+`"},"dev":{"path":"dev"}}`)
	file := writeFile(t, dir, "api.http", "GET {{host}}/{{path}}\n")

	for _, test := range []struct {
		args []string
		want string
	}{
		{[]string{"run", file}, "/env"},
		{[]string{"run", "-env", "dev", file}, "/dev"},
		{[]string{"run", "-env", "dev", "-var", "path=cli", file}, "/cli"},
	} {
		if code, _, stderr := invoke(t, test.args...); code != 0 {
			t.Fatalf("%v: exit = %d, stderr = %s", test.args, code, stderr)
		}
		if got := paths[len(paths)-1]; got != test.want {
			t.Errorf("%v: path = %q, want %q", test.args, got, test.want)
		}
	}
}

func TestRunProcessEnvironmentOverridesEnvFiles(t *testing.T) {
	var paths []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
	}))
	defer server.Close()

	t.Setenv("host", server.URL)
	t.Setenv("path", "process")

	dir := t.TempDir()
	writeFile(t, dir, ".env", "host=http://127.0.0.1:1\npath=dotenv\n")
	file := writeFile(t, dir, "api.http", "GET {{host}}/{{path}}\n")

	tests := []struct {
		name string
		args []string
		want string
	}{
		{"process env wins over .env", []string{"run", file}, "/process"},
		{"-var wins over process env", []string{"run", "-var", "path=cli", file}, "/cli"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if code, _, stderr := invoke(t, test.args...); code != 0 {
				t.Fatalf("exit = %d, stderr = %s", code, stderr)
			}
			if got := paths[len(paths)-1]; got != test.want {
				t.Errorf("path = %q, want %q", got, test.want)
			}
		})
	}
}

func TestRunUnknownEnvironment(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "http-client.env.json", `{"dev":{}}`)
	file := writeFile(t, dir, "api.http", "GET https://example.com\n")

	code, _, stderr := invoke(t, "run", "-env", "stage", file)
	if code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	if !strings.Contains(stderr, `"stage"`) || !strings.Contains(stderr, "dev") {
		t.Errorf("stderr = %q", stderr)
	}
}

func TestRunBodyIncludeRelativeToFile(t *testing.T) {
	var body string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		body = string(data)
	}))
	defer server.Close()

	dir := t.TempDir()
	writeFile(t, dir, "payload.json", `{"ok":true}`)
	file := writeFile(t, dir, "api.http", "POST "+server.URL+"\nContent-Type: application/json\n\n< payload.json\n")

	if code, _, stderr := invoke(t, "run", file); code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, stderr)
	}
	if body != `{"ok":true}` {
		t.Errorf("body = %q", body)
	}
}

func TestRunInvalidUsage(t *testing.T) {
	dir := t.TempDir()
	file := writeFile(t, dir, "api.http", "GET https://example.com\n")

	tests := []struct {
		name string
		args []string
		want string
	}{
		{"no file", []string{"run"}, "Usage:"},
		{"two files", []string{"run", file, file}, "Usage:"},
		{"mutually exclusive", []string{"run", "-all", "-name", "x", file}, "mutually exclusive"},
		{"bad output", []string{"run", "-output", "yaml", file}, "unknown output format"},
		{"bad var", []string{"run", "-var", "novalue", file}, "invalid -var"},
		{"missing file", []string{"run", filepath.Join(dir, "nope.http")}, "no such file"},
		{"unknown command", []string{"send", file}, "unknown command"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			code, _, stderr := invoke(t, test.args...)
			if code != 2 {
				t.Fatalf("exit = %d, want 2", code)
			}
			if !strings.Contains(stderr, test.want) {
				t.Errorf("stderr = %q, want substring %q", stderr, test.want)
			}
		})
	}
}

func TestRunParseError(t *testing.T) {
	dir := t.TempDir()
	file := writeFile(t, dir, "api.http", "GET https://example.com\nnot a header\n")
	code, _, stderr := invoke(t, "run", file)
	if code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	if !strings.Contains(stderr, "line 2:") {
		t.Errorf("stderr = %q", stderr)
	}
}

func TestRunEmptyFile(t *testing.T) {
	dir := t.TempDir()
	file := writeFile(t, dir, "api.http", "# nothing here\n")
	code, _, stderr := invoke(t, "run", file)
	if code != 2 || !strings.Contains(stderr, "no requests") {
		t.Fatalf("exit = %d, stderr = %q", code, stderr)
	}
}

func TestRunFileVariableError(t *testing.T) {
	dir := t.TempDir()
	file := writeFile(t, dir, "api.http", "@url = {{missing}}\n\n###\nGET {{url}}\n")

	code, _, stderr := invoke(t, "run", file)
	if code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	if !strings.Contains(stderr, "variable url") {
		t.Errorf("stderr = %q", stderr)
	}
}

func TestRunHelpFlag(t *testing.T) {
	code, _, stderr := invoke(t, "run", "-h")
	if code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	if !strings.Contains(stderr, "Usage:") {
		t.Errorf("stderr = %q", stderr)
	}
}

func TestMainHelp(t *testing.T) {
	code, stdout, _ := invoke(t, "help")
	if code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	if !strings.Contains(stdout, "Usage:") || !strings.Contains(stdout, "httpcli run") {
		t.Errorf("stdout = %q", stdout)
	}
}

func TestOutputSizes(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(bytes.Repeat([]byte("x"), 999)) // < 1000 B
	}))
	defer server.Close()

	dir := t.TempDir()
	file := writeFile(t, dir, "api.http", "GET "+server.URL+"\n")

	code, stdout, _ := invoke(t, "run", file)
	if code != 0 {
		t.Fatalf("exit = %d", code)
	}
	if !strings.Contains(stdout, "999 B") {
		t.Errorf("stdout = %q, want 999 B", stdout)
	}

	server2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(bytes.Repeat([]byte("x"), 1500)) // > 1000 B
	}))
	defer server2.Close()

	file2 := writeFile(t, dir, "api2.http", "GET "+server2.URL+"\n")
	code2, stdout2, _ := invoke(t, "run", file2)
	if code2 != 0 {
		t.Fatalf("exit = %d", code2)
	}
	if !strings.Contains(stdout2, "1.5 kB") {
		t.Errorf("stdout = %q, want 1.5 kB", stdout2)
	}
}
