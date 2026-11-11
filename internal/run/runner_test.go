package run

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"httpcli/internal/httpfile"
	"httpcli/internal/vars"
)

func scopeWith(values map[string]string) *vars.Scope {
	scope := vars.New()
	scope.SetStrings(values)
	return scope
}

func runAll(t *testing.T, f *httpfile.File, base *vars.Scope, dir string) []Result {
	t.Helper()
	var results []Result
	New(http.DefaultClient, base, dir).RunAll(context.Background(), f, f.Requests, func(res Result) {
		results = append(results, res)
	})
	return results
}

func TestRunSendsInterpolatedRequest(t *testing.T) {
	var (
		gotMethod string
		gotPath   string
		gotToken  string
		gotAgent  string
		gotBody   string
	)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		gotMethod, gotPath = r.Method, r.URL.Path
		gotToken, gotAgent, gotBody = r.Header.Get("X-Token"), r.Header.Get("User-Agent"), string(body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"ok":true}`)
	}))
	defer server.Close()

	f := parse(t, `@id = 7
@host = `+server.URL+`

POST {{host}}/items/{{id}}
Content-Type: application/json
X-Token: {{token}}

{"id": {{id}}}
`)
	results := runAll(t, f, scopeWith(map[string]string{"token": "abc"}), ".")
	if len(results) != 1 {
		t.Fatalf("results = %d", len(results))
	}
	res := results[0]
	if res.Err != nil {
		t.Fatalf("Err = %v", res.Err)
	}
	if res.Failures != nil {
		t.Errorf("Failures = %v", res.Failures)
	}
	if gotMethod != "POST" || gotPath != "/items/7" {
		t.Errorf("server saw %s %s", gotMethod, gotPath)
	}
	if gotToken != "abc" {
		t.Errorf("X-Token = %q, want abc", gotToken)
	}
	if gotAgent != "httpcli" {
		t.Errorf("User-Agent = %q, want httpcli", gotAgent)
	}
	if gotBody != `{"id": 7}` {
		t.Errorf("body = %q", gotBody)
	}
	if res.Response.Status != 200 || string(res.Response.Body) != `{"ok":true}` {
		t.Errorf("response = %d %q", res.Response.Status, res.Response.Body)
	}
	if res.Response.Duration <= 0 {
		t.Errorf("Duration = %v", res.Response.Duration)
	}
}

func TestRunChainsNamedResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/login" {
			_, _ = fmt.Fprint(w, `{"token":"secret","user":{"name":"ada"}}`)
			return
		}
		if got := r.Header.Get("Authorization"); got != "Bearer secret" {
			t.Errorf("Authorization = %q, want Bearer secret", got)
		}
		if got := r.Header.Get("X-User"); got != "ada" {
			t.Errorf("X-User = %q, want ada", got)
		}
		_, _ = fmt.Fprint(w, `{"ok":true}`)
	}))
	defer server.Close()

	f := parse(t, `### Login
# @name login
POST `+server.URL+`/login

### Profile
GET `+server.URL+`/profile
Authorization: Bearer {{login.token}}
X-User: {{login.user.name}}
`)
	results := runAll(t, f, vars.New(), ".")
	for _, res := range results {
		if res.Err != nil {
			t.Fatalf("Err = %v", res.Err)
		}
	}
}

func TestRunOAuth2ClientCredentials(t *testing.T) {
	var tokenCalls int32
	var form, authHeader string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/token" {
			atomic.AddInt32(&tokenCalls, 1)
			body, _ := io.ReadAll(r.Body)
			form, authHeader = string(body), r.Header.Get("Authorization")
			_, _ = fmt.Fprint(w, `{"access_token":"tok","token_type":"bearer"}`)
			return
		}
		if got := r.Header.Get("Authorization"); got != "Bearer tok" {
			t.Errorf("Authorization = %q, want Bearer tok", got)
		}
		_, _ = fmt.Fprint(w, `{"ok":true}`)
	}))
	defer server.Close()

	f := parse(t, `@oauth2_tokenEndpoint = `+server.URL+`/token
@oauth2_clientId = client
@oauth2_clientSecret = secret
@oauth2_scope = read

### First
GET `+server.URL+`/api
Authorization: oauth2 client_credentials

### Second
GET `+server.URL+`/api
Authorization: oauth2
`)
	results := runAll(t, f, vars.New(), ".")
	for _, res := range results {
		if res.Err != nil {
			t.Fatalf("Err = %v", res.Err)
		}
	}
	if calls := atomic.LoadInt32(&tokenCalls); calls != 1 {
		t.Errorf("token endpoint called %d times, want 1", calls)
	}
	if form != "grant_type=client_credentials&scope=read" {
		t.Errorf("token form = %q", form)
	}
	if authHeader != basicAuth("client", "secret") {
		t.Errorf("token Authorization = %q", authHeader)
	}
}

func basicAuth(user, pass string) string {
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req.SetBasicAuth(user, pass)
	return req.Header.Get("Authorization")
}

func TestRunOAuth2UnsupportedFlow(t *testing.T) {
	f := parse(t, `GET https://example.com/api
Authorization: oauth2 password
`)
	results := runAll(t, f, vars.New(), ".")
	if results[0].Err == nil || !strings.Contains(results[0].Err.Error(), "unsupported oauth2 flow") {
		t.Fatalf("Err = %v", results[0].Err)
	}
}

func TestRunOAuth2MissingEndpoint(t *testing.T) {
	f := parse(t, `GET https://example.com/api
Authorization: oauth2
`)
	results := runAll(t, f, vars.New(), ".")
	if results[0].Err == nil || !strings.Contains(results[0].Err.Error(), "oauth2_tokenEndpoint") {
		t.Fatalf("Err = %v", results[0].Err)
	}
}

func TestRunOAuth2ServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/token" {
			w.WriteHeader(500)
			_, _ = fmt.Fprintf(w, `{"error":"internal_error","error_description":"%s"}`, strings.Repeat("x", 210))
			return
		}
		w.WriteHeader(200)
	}))
	defer server.Close()

	f := parse(t, `@oauth2_tokenEndpoint = `+server.URL+`/token
@oauth2_clientId = client

###
GET `+server.URL+`/api
Authorization: oauth2
`)
	results := runAll(t, f, vars.New(), ".")
	if results[0].Err == nil {
		t.Fatal("Err = nil, want error")
	}
	if !strings.Contains(results[0].Err.Error(), "500") {
		t.Fatalf("error = %q, want 500", results[0].Err)
	}
	if !strings.Contains(results[0].Err.Error(), "...") {
		t.Fatalf("error = %q, want truncated snippet", results[0].Err)
	}
}

func TestRunBodyFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "body.json"), []byte(`{"id":{{id}}}`), 0o600); err != nil {
		t.Fatal(err)
	}

	var bodies []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		bodies = append(bodies, string(body))
	}))
	defer server.Close()

	f := parse(t, `### Substituted
POST `+server.URL+`/one
Content-Type: application/json

<@ body.json

### Raw
POST `+server.URL+`/two
Content-Type: application/json

< body.json
`)
	results := runAll(t, f, scopeWith(map[string]string{"id": "5"}), dir)
	for _, res := range results {
		if res.Err != nil {
			t.Fatalf("Err = %v", res.Err)
		}
	}
	if len(bodies) != 2 {
		t.Fatalf("bodies = %v", bodies)
	}
	if bodies[0] != `{"id":5}` {
		t.Errorf("substituted body = %q", bodies[0])
	}
	if bodies[1] != `{"id":{{id}}}` {
		t.Errorf("raw body = %q", bodies[1])
	}
}

func TestRunMissingBodyFile(t *testing.T) {
	f := parse(t, `POST https://example.com
Content-Type: application/json

< missing.json
`)
	results := runAll(t, f, vars.New(), t.TempDir())
	if results[0].Err == nil || !strings.Contains(results[0].Err.Error(), "body file") {
		t.Fatalf("Err = %v", results[0].Err)
	}
}

func TestRunBodyFileOutsideDir(t *testing.T) {
	f := parse(t, `POST https://example.com
Content-Type: application/json

< ../secret.json
`)
	results := runAll(t, f, vars.New(), t.TempDir())
	if results[0].Err == nil || !strings.Contains(results[0].Err.Error(), "outside the file's directory") {
		t.Fatalf("Err = %v, want outside-directory error", results[0].Err)
	}
}

func TestRunRejectsHeaderInjection(t *testing.T) {
	f := parse(t, `GET https://example.com
X-Test: {{evil}}
`)
	results := runAll(t, f, scopeWith(map[string]string{"evil": "a\r\nX-Injected: b"}), ".")
	if results[0].Err == nil {
		t.Fatal("Err = nil, want invalid header error")
	}
	if !strings.Contains(results[0].Err.Error(), "invalid header") {
		t.Fatalf("Err = %q, want invalid header", results[0].Err)
	}
}

func TestRunFormBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		v, err := url.ParseQuery(string(body))
		if err != nil {
			t.Fatalf("ParseQuery: %v", err)
		}
		if v.Get("username") != "admin" {
			t.Errorf("username = %q, want admin", v.Get("username"))
		}
		if v.Get("password") != "secret" {
			t.Errorf("password = %q, want secret", v.Get("password"))
		}
	}))
	defer server.Close()

	f := parse(t, `POST `+server.URL+`/login
Content-Type: application/x-www-form-urlencoded

username=admin
&password=secret
`)
	results := runAll(t, f, vars.New(), ".")
	if results[0].Err != nil {
		t.Fatalf("Err = %v", results[0].Err)
	}
}

func TestRunAssertionFailures(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_, _ = fmt.Fprint(w, `{"ok":true}`)
	}))
	defer server.Close()

	f := parse(t, `GET `+server.URL+`
?? status == 201
?? status == 200
?? body ok == true
`)
	results := runAll(t, f, vars.New(), ".")
	if len(results[0].Failures) != 1 {
		t.Fatalf("Failures = %v, want exactly one", results[0].Failures)
	}
	if !strings.Contains(results[0].Failures[0], "status == 200: got 201") {
		t.Errorf("failure = %q", results[0].Failures[0])
	}
	if results[0].Response == nil {
		t.Error("response missing on assertion failure")
	}
}

func TestRunCanceledContext(t *testing.T) {
	f := parse(t, `GET https://example.com
`)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	var results []Result
	New(http.DefaultClient, vars.New(), ".").RunAll(ctx, f, f.Requests, func(res Result) {
		results = append(results, res)
	})
	if len(results) != 1 || results[0].Err == nil {
		t.Fatalf("results = %#v, want a transport error", results)
	}
}

func TestRunFileVariableError(t *testing.T) {
	f := parse(t, `@url = {{missing}}

###
GET {{url}}
`)
	var results []Result
	New(http.DefaultClient, vars.New(), ".").RunAll(context.Background(), f, f.Requests, func(res Result) {
		results = append(results, res)
	})
	if len(results) != 1 {
		t.Fatalf("results = %d, want 1 run-level result", len(results))
	}
	if results[0].Request != nil {
		t.Error("run-level failure carries a Request")
	}
	if results[0].Err == nil || !strings.Contains(results[0].Err.Error(), "variable url") {
		t.Fatalf("Err = %v, want variable url", results[0].Err)
	}
	if !results[0].Failed() {
		t.Error("run-level failure is not Failed()")
	}
}

func TestRunRequestVariableError(t *testing.T) {
	f := parse(t, `GET https://example.com
@x = {{missing}}
`)
	results := runAll(t, f, vars.New(), ".")
	if results[0].Err == nil || !strings.Contains(results[0].Err.Error(), "variable x") {
		t.Fatalf("Err = %v", results[0].Err)
	}
}

func TestRunRequestVariablesDoNotLeak(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer server.Close()

	f := parse(t, `### One
@only = 1
GET `+server.URL+`/{{only}}

### Two
GET `+server.URL+`/{{only}}
`)
	results := runAll(t, f, vars.New(), ".")
	if results[0].Err != nil {
		t.Fatalf("first request failed: %v", results[0].Err)
	}
	if results[1].Err == nil || !strings.Contains(results[1].Err.Error(), `unknown variable "only"`) {
		t.Fatalf("second request Err = %v, want unknown variable", results[1].Err)
	}
}

func TestDecodeBody(t *testing.T) {
	if got := decodeBody([]byte(`{"a":1}`)); got == nil {
		t.Error("valid JSON decoded to nil")
	}
	for _, raw := range []string{`{"a":1} trailing`, `not json`, ``} {
		if got := decodeBody([]byte(raw)); got != raw {
			t.Errorf("decodeBody(%q) = %#v, want the raw string", raw, got)
		}
	}
}

func TestRunInvalidURL(t *testing.T) {
	tests := []string{"not-a-url", "ftp://example.com/file", "https://"}
	for _, raw := range tests {
		f := parse(t, "GET "+raw+"\n")
		results := runAll(t, f, vars.New(), ".")
		if results[0].Err == nil {
			t.Errorf("GET %q: Err = nil, want error", raw)
		}
	}
}

func TestResultFailed(t *testing.T) {
	if (&Result{}).Failed() {
		t.Error("empty Result.Failed() = true, want false")
	}
	r := Result{Err: fmt.Errorf("transport error")}
	if !r.Failed() {
		t.Error("Result with Err.Failed() = false, want true")
	}
	r2 := Result{Failures: []string{"status == 200"}}
	if !r2.Failed() {
		t.Error("Result with Failures.Failed() = false, want true")
	}
}

func TestRunRelativeURLWithHost(t *testing.T) {
	var gotPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.WriteHeader(200)
	}))
	defer server.Close()

	f := parse(t, `@host = `+server.URL+`
###
GET /relative`)
	results := runAll(t, f, vars.New(), ".")
	if results[0].Err != nil {
		t.Fatalf("Err = %v", results[0].Err)
	}
	if gotPath != "/relative" {
		t.Errorf("path = %q, want /relative", gotPath)
	}
}

func TestRunRelativeURLWithoutHostFails(t *testing.T) {
	f := parse(t, "GET /path\n")
	results := runAll(t, f, vars.New(), ".")
	if results[0].Err == nil || !strings.Contains(results[0].Err.Error(), "only http and https") {
		t.Fatalf("Err = %v, want only http error", results[0].Err)
	}
}

func TestRunBasicAuthShorthand(t *testing.T) {
	var gotAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(200)
	}))
	defer server.Close()

	f := parse(t, `GET `+server.URL+`
Authorization: Basic admin password`)
	results := runAll(t, f, vars.New(), ".")
	if results[0].Err != nil {
		t.Fatalf("Err = %v", results[0].Err)
	}
	// The shorthand "Basic admin password" should be base64-encoded.
	want := "Basic " + base64.StdEncoding.EncodeToString([]byte("admin:password"))
	if gotAuth != want {
		t.Errorf("Authorization = %q, want %q", gotAuth, want)
	}
}

func TestRunFormBodyPreservesBytes(t *testing.T) {
	var bodies []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		bodies = append(bodies, string(data))
	}))
	defer server.Close()

	f := parse(t, `### Multi-line
POST `+server.URL+`/multi
Content-Type: application/x-www-form-urlencoded

a=1
&a=2

### Single line
POST `+server.URL+`/single
Content-Type: application/x-www-form-urlencoded

a=1&b=2
`)
	for _, res := range runAll(t, f, vars.New(), ".") {
		if res.Err != nil {
			t.Fatalf("Err = %v", res.Err)
		}
	}
	want := []string{"a=1&a=2", "a=1&b=2"}
	if len(bodies) != len(want) {
		t.Fatalf("bodies = %v, want %v", bodies, want)
	}
	for i := range want {
		if bodies[i] != want[i] {
			t.Errorf("body %d = %q, want %q", i, bodies[i], want[i])
		}
	}
}

func TestRunOAuth2SeparateTokensPerScope(t *testing.T) {
	var calls int32
	var authorizations []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/token" {
			n := atomic.AddInt32(&calls, 1)
			_, _ = fmt.Fprintf(w, `{"access_token":"tok%d"}`, n)
			return
		}
		authorizations = append(authorizations, r.Header.Get("Authorization"))
		w.WriteHeader(200)
	}))
	defer server.Close()

	f := parse(t, `@oauth2_tokenEndpoint = `+server.URL+`/token
@oauth2_clientId = client

### Read
@oauth2_scope = read
GET `+server.URL+`/read
Authorization: oauth2

### Write
@oauth2_scope = write
GET `+server.URL+`/write
Authorization: oauth2
`)
	for _, res := range runAll(t, f, vars.New(), ".") {
		if res.Err != nil {
			t.Fatalf("Err = %v", res.Err)
		}
	}
	if tokenCalls := atomic.LoadInt32(&calls); tokenCalls != 2 {
		t.Errorf("token endpoint called %d times, want 2 (one per scope)", tokenCalls)
	}
	if len(authorizations) != 2 || authorizations[0] == authorizations[1] {
		t.Errorf("authorization headers = %v, want one token per scope", authorizations)
	}
}

func TestRunBasicAuthOnlyOnAuthorization(t *testing.T) {
	var thing, auth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		thing, auth = r.Header.Get("X-Thing"), r.Header.Get("Authorization")
		w.WriteHeader(200)
	}))
	defer server.Close()

	f := parse(t, `GET `+server.URL+`
X-Thing: Basic a b
Authorization: Basic user:pa ss`)
	results := runAll(t, f, vars.New(), ".")
	if results[0].Err != nil {
		t.Fatalf("Err = %v", results[0].Err)
	}
	if thing != "Basic a b" {
		t.Errorf("X-Thing = %q, want it unchanged", thing)
	}
	want := "Basic " + base64.StdEncoding.EncodeToString([]byte("user:pa ss"))
	if auth != want {
		t.Errorf("Authorization = %q, want %q", auth, want)
	}
}

func TestRunBasicAuthAlreadyEncoded(t *testing.T) {
	var gotAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(200)
	}))
	defer server.Close()

	// Value with no colon should pass through unchanged.
	f := parse(t, `GET `+server.URL+`
Authorization: Basic dXNlcjpwYXNz`)
	results := runAll(t, f, vars.New(), ".")
	if results[0].Err != nil {
		t.Fatalf("Err = %v", results[0].Err)
	}
	if gotAuth != "Basic dXNlcjpwYXNz" {
		t.Errorf("Authorization = %q, want unchanged", gotAuth)
	}
}
