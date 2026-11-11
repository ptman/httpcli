package run

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"httpcli/internal/httpfile"
	"httpcli/internal/vars"
)

// Response is the outcome of a single request.
type Response struct {
	Status   int
	Header   http.Header
	Body     []byte
	Duration time.Duration
}

// Result pairs a request with its response, transport error and failed
// assertions. Request is nil for a run-level failure such as a bad file-level
// variable; Err is always set in that case.
type Result struct {
	Request  *httpfile.Request
	URL      string // resolved URL, or the raw request URL when it could not be built
	Response *Response
	Err      error
	Failures []string
}

func (r Result) Failed() bool { return r.Err != nil || len(r.Failures) > 0 }

// Runner sends requests. Named responses are shared between requests of one
// run so later requests can chain on them.
type Runner struct {
	client *http.Client
	base   *vars.Scope
	dir    string
	tokens map[string]string
}

func New(client *http.Client, base *vars.Scope, dir string) *Runner {
	return &Runner{client: client, base: base, dir: dir, tokens: map[string]string{}}
}

// RunAll sends the planned requests in order and reports every outcome through
// emit, including a file-level variable error, whose Result has no Request.
// The callback is required; passing nil is a programming error and panics.
func (r *Runner) RunAll(ctx context.Context, f *httpfile.File, reqs []*httpfile.Request, emit func(Result)) {
	if emit == nil {
		panic("run.RunAll: emit callback is required")
	}
	shared := r.base.Clone()
	for _, def := range f.Vars {
		if err := define(shared, def); err != nil {
			emit(Result{Err: err})
			return
		}
	}
	for _, req := range reqs {
		emit(r.runOne(ctx, req, shared))
	}
}

func (r *Runner) runOne(ctx context.Context, req *httpfile.Request, shared *vars.Scope) Result {
	result := Result{Request: req, URL: req.URL}
	scope := shared.Clone()
	for _, def := range req.Vars {
		if err := define(scope, def); err != nil {
			result.Err = err
			return result
		}
	}

	resp, resolvedURL, err := r.do(ctx, req, scope)
	if resolvedURL != "" {
		result.URL = resolvedURL
	}
	if err != nil {
		result.Err = err
		return result
	}
	result.Response = resp
	if req.Name != "" {
		shared.Set(req.Name, decodeBody(resp.Body))
	}
	for _, assertion := range req.Assertions {
		if err := check(assertion, resp); err != nil {
			failure := err.Error()
			if assertion.Line > 0 {
				failure = fmt.Sprintf("line %d: %s", assertion.Line, failure)
			}
			result.Failures = append(result.Failures, failure)
		}
	}
	return result
}

func define(scope *vars.Scope, def httpfile.Var) error {
	value, err := scope.Interpolate(def.Value)
	if err != nil {
		return fmt.Errorf("line %d: variable %s: %w", def.Line, def.Name, err)
	}
	scope.Set(def.Name, value)
	return nil
}

func (r *Runner) do(ctx context.Context, req *httpfile.Request, scope *vars.Scope) (*Response, string, error) {
	httpReq, err := r.build(ctx, req, scope)
	if err != nil {
		return nil, "", err
	}
	resolvedURL := httpReq.URL.String()

	start := time.Now()
	resp, err := r.client.Do(httpReq)
	if err != nil {
		return nil, resolvedURL, err
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	elapsed := time.Since(start)
	if err != nil {
		return nil, resolvedURL, fmt.Errorf("read response body: %w", err)
	}
	return &Response{Status: resp.StatusCode, Header: resp.Header, Body: body, Duration: elapsed}, resolvedURL, nil
}

func (r *Runner) build(ctx context.Context, req *httpfile.Request, scope *vars.Scope) (*http.Request, error) {
	rawURL, err := scope.Interpolate(req.URL)
	if err != nil {
		return nil, fmt.Errorf("url: %w", err)
	}

	// Prepend @host for relative URLs (origin-form).
	if !strings.Contains(rawURL, "://") {
		if hostVal, ok := scope.Lookup("host"); ok {
			host := vars.Format(hostVal)
			if !strings.HasSuffix(host, "/") {
				host += "/"
			}
			// Remove leading / from rawURL to avoid double-slash.
			for len(rawURL) > 0 && rawURL[0] == '/' {
				rawURL = rawURL[1:]
			}
			rawURL = host + rawURL
		}
	}

	headers, err := r.headers(ctx, req, scope)
	if err != nil {
		return nil, err
	}

	body, err := r.body(req, scope)
	if err != nil {
		return nil, err
	}
	if isFormBody(headers) {
		body = joinFormLines(body)
	}

	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	httpReq, err := http.NewRequestWithContext(ctx, req.Method, rawURL, reader)
	if err != nil {
		return nil, err
	}
	if httpReq.URL.Scheme != "http" && httpReq.URL.Scheme != "https" {
		return nil, fmt.Errorf("url %q: only http and https are supported", rawURL)
	}
	if httpReq.URL.Host == "" {
		return nil, fmt.Errorf("url %q: missing host", rawURL)
	}
	for _, header := range headers {
		httpReq.Header.Add(header.Name, header.Value)
	}
	if httpReq.Header.Get("User-Agent") == "" {
		httpReq.Header.Set("User-Agent", "httpcli")
	}
	return httpReq, nil
}

func (r *Runner) headers(ctx context.Context, req *httpfile.Request, scope *vars.Scope) ([]httpfile.Header, error) {
	headers := make([]httpfile.Header, 0, len(req.Headers))
	for _, header := range req.Headers {
		value, err := scope.Interpolate(header.Value)
		if err != nil {
			return nil, fmt.Errorf("header %s: %w", header.Name, err)
		}
		flow, isOAuth := oauth2Flow(value)
		if isOAuth {
			if flow != "client_credentials" {
				return nil, fmt.Errorf("authorization: unsupported oauth2 flow %q (only client_credentials)", flow)
			}
			token, err := r.token(ctx, scope)
			if err != nil {
				return nil, err
			}
			headers = append(headers, httpfile.Header{Name: header.Name, Value: "Bearer " + token})
			continue
		}
		// Handle Basic auth shorthand: "Basic user pass" → base64-encoded.
		if isAuthHeader(header.Name) {
			value = basicShorthand(value)
		}
		headers = append(headers, httpfile.Header{Name: header.Name, Value: value})
	}
	// Header values are passed through unsanitized on purpose: net/http rejects
	// CRLF values, so an interpolated value cannot inject a header.
	// TestRunRejectsHeaderInjection pins that safety net.
	return headers, nil
}

func (r *Runner) body(req *httpfile.Request, scope *vars.Scope) (string, error) {
	if req.BodyFile == "" {
		body, err := scope.Interpolate(req.Body)
		if err != nil {
			return "", fmt.Errorf("body: %w", err)
		}
		return body, nil
	}

	path := filepath.Join(r.dir, filepath.FromSlash(req.BodyFile))
	// Includes are confined to the .http file's directory: a "../" path would
	// otherwise let a file upload anything the process can read.
	rel, err := filepath.Rel(r.dir, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("body file %s: outside the file's directory", req.BodyFile)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("body file: %w", err)
	}
	if !req.BodySubst {
		return string(data), nil
	}
	body, err := scope.Interpolate(string(data))
	if err != nil {
		return "", fmt.Errorf("body file %s: %w", req.BodyFile, err)
	}
	return body, nil
}

func isFormBody(headers []httpfile.Header) bool {
	for _, header := range headers {
		if strings.EqualFold(header.Name, "Content-Type") {
			return strings.HasPrefix(strings.ToLower(strings.TrimSpace(header.Value)), "application/x-www-form-urlencoded")
		}
	}
	return false
}

// joinFormLines turns the multi-line "&" form syntax into one query string.
// The body is otherwise sent unchanged, so duplicate keys and already-encoded
// values are preserved.
func joinFormLines(body string) string {
	lines := strings.Split(body, "\n")
	parts := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimPrefix(strings.TrimSpace(line), "&")
		if line != "" {
			parts = append(parts, line)
		}
	}
	return strings.Join(parts, "&")
}

func oauth2Flow(value string) (string, bool) {
	fields := strings.Fields(value)
	if len(fields) == 0 || !strings.EqualFold(fields[0], "oauth2") {
		return "", false
	}
	if len(fields) < 2 {
		return "client_credentials", true
	}
	return strings.ToLower(fields[1]), true
}

func decodeBody(body []byte) any {
	if !json.Valid(body) {
		return string(body)
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return string(body)
	}
	return value
}

// isAuthHeader reports whether a header carries credentials.
func isAuthHeader(name string) bool {
	return strings.EqualFold(name, "Authorization") || strings.EqualFold(name, "Proxy-Authorization")
}

// basicShorthand converts "Basic user pass" or "Basic user:pass" to a
// base64-encoded "Basic <credentials>" value. A value without a colon or a
// space is assumed to be base64 already and is sent unchanged.
func basicShorthand(value string) string {
	const prefix = "Basic "
	if !strings.HasPrefix(value, prefix) {
		return value
	}
	cred := strings.TrimSpace(value[len(prefix):])
	var raw string
	switch {
	case strings.Contains(cred, ":"):
		// The password may contain spaces; keep everything after the first colon.
		raw = cred
	case strings.Contains(cred, " "):
		user, password, _ := strings.Cut(cred, " ")
		raw = user + ":" + strings.TrimSpace(password)
	default:
		return value
	}
	return prefix + base64.StdEncoding.EncodeToString([]byte(raw))
}
