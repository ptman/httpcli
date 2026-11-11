package httpfile

import (
	"strings"
	"testing"
)

func mustParse(t *testing.T, src string) *File {
	t.Helper()
	f, err := Parse(src)
	if err != nil {
		t.Fatalf("Parse() error: %v", err)
	}
	return f
}

func one(t *testing.T, src string) *Request {
	t.Helper()
	f := mustParse(t, src)
	if len(f.Requests) != 1 {
		t.Fatalf("got %d requests, want 1", len(f.Requests))
	}
	return f.Requests[0]
}

func TestParseSimpleRequest(t *testing.T) {
	req := one(t, `GET https://example.com/users?page=2
Accept: application/json

`)

	if req.Method != "GET" {
		t.Errorf("Method = %q, want GET", req.Method)
	}
	if req.URL != "https://example.com/users?page=2" {
		t.Errorf("URL = %q", req.URL)
	}
	if len(req.Headers) != 1 || req.Headers[0] != (Header{"Accept", "application/json"}) {
		t.Errorf("Headers = %#v", req.Headers)
	}
	if req.Body != "" {
		t.Errorf("Body = %q, want empty", req.Body)
	}
	if req.Line != 1 {
		t.Errorf("Line = %d, want 1", req.Line)
	}
}

func TestParseMethodDefaultsToGet(t *testing.T) {
	req := one(t, "https://example.com/ping\n")
	if req.Method != "GET" || req.URL != "https://example.com/ping" {
		t.Errorf("got %s %q, want GET https://example.com/ping", req.Method, req.URL)
	}
}

func TestParseWebDAVMethod(t *testing.T) {
	req := one(t, "PROPFIND https://example.com/dav\n")
	if req.Method != "PROPFIND" {
		t.Errorf("Method = %q, want PROPFIND", req.Method)
	}
}

func TestParseHTTPVersion(t *testing.T) {
	req := one(t, "POST https://example.com/items HTTP/1.1\n")
	if req.Method != "POST" || req.URL != "https://example.com/items" {
		t.Errorf("got %s %q", req.Method, req.URL)
	}
}

func TestParseSeparatorsAndTitles(t *testing.T) {
	f := mustParse(t, `### List users
GET https://example.com/users

### Create user
POST https://example.com/users
Content-Type: application/json

{"name":"Ada"}
`)

	if len(f.Requests) != 2 {
		t.Fatalf("got %d requests, want 2", len(f.Requests))
	}
	if f.Requests[0].Title != "List users" {
		t.Errorf("title = %q", f.Requests[0].Title)
	}
	if f.Requests[1].Title != "Create user" {
		t.Errorf("title = %q", f.Requests[1].Title)
	}
	if f.Requests[1].Line != 5 {
		t.Errorf("second request line = %d, want 5", f.Requests[1].Line)
	}
	if f.Requests[1].Body != `{"name":"Ada"}` {
		t.Errorf("body = %q", f.Requests[1].Body)
	}
}

func TestParseKeepsBlankLinesInsideBody(t *testing.T) {
	req := one(t, `POST https://example.com
Content-Type: text/plain

line one
line two

line four
`)
	want := "line one\nline two\n\nline four"
	if req.Body != want {
		t.Errorf("Body = %q, want %q", req.Body, want)
	}
}

func TestParseComments(t *testing.T) {
	req := one(t, `# leading comment
// another comment
GET https://example.com
/* block
   comment */
Accept: text/plain
// inside headers

body line
# comment inside body
text after comment
`)

	if req.Method != "GET" {
		t.Errorf("Method = %q", req.Method)
	}
	if len(req.Headers) != 1 {
		t.Errorf("Headers = %#v", req.Headers)
	}
	if want := "body line\n# comment inside body\ntext after comment"; req.Body != want {
		t.Errorf("Body = %q, want %q", req.Body, want)
	}
}

func TestParseURLContinuation(t *testing.T) {
	req := one(t, `GET https://example.com/api
    /users
    ?page=2
    &limit=10
Accept: application/json
`)
	want := "https://example.com/api/users?page=2&limit=10"
	if req.URL != want {
		t.Errorf("URL = %q, want %q", req.URL, want)
	}
	if len(req.Headers) != 1 || req.Headers[0].Name != "Accept" {
		t.Errorf("Headers = %#v", req.Headers)
	}
}

func TestParseHeaderContinuation(t *testing.T) {
	req := one(t, `GET https://example.com
Authorization: Bearer
    abcdef
Accept: text/plain
`)
	if len(req.Headers) != 2 {
		t.Fatalf("Headers = %#v", req.Headers)
	}
	if req.Headers[0].Value != "Bearer abcdef" {
		t.Errorf("header value = %q, want %q", req.Headers[0].Value, "Bearer abcdef")
	}
}

func TestParseFileAndRequestVariables(t *testing.T) {
	f := mustParse(t, `@host = https://example.com
@base := {{host}}/v1

### Get item
@id = 42
GET {{base}}/items/{{id}}
`)
	if len(f.Vars) != 2 {
		t.Fatalf("file vars = %#v", f.Vars)
	}
	if f.Vars[0].Name != "host" || f.Vars[0].Value != "https://example.com" {
		t.Errorf("first var = %#v", f.Vars[0])
	}
	if f.Vars[1].Name != "base" || f.Vars[1].Value != "{{host}}/v1" {
		t.Errorf("second var = %#v", f.Vars[1])
	}
	if len(f.Requests) != 1 {
		t.Fatalf("requests = %d", len(f.Requests))
	}
	req := f.Requests[0]
	if len(req.Vars) != 1 || req.Vars[0].Name != "id" || req.Vars[0].Value != "42" {
		t.Errorf("request vars = %#v", req.Vars)
	}
}

func TestParseGlobalRegionAfterRequest(t *testing.T) {
	f := mustParse(t, `GET https://example.com

### Shared
@token = abc
`)
	if len(f.Requests) != 1 {
		t.Fatalf("requests = %d", len(f.Requests))
	}
	if len(f.Vars) != 1 || f.Vars[0].Name != "token" {
		t.Errorf("file vars = %#v", f.Vars)
	}
}

func TestParseNameAndRefs(t *testing.T) {
	f := mustParse(t, `### Login
# @name login
POST https://example.com/login

### Protected
# @name protected
# @ref login
# @ref other
GET https://example.com/protected
`)
	if f.Requests[0].Name != "login" {
		t.Errorf("name = %q", f.Requests[0].Name)
	}
	refs := f.Requests[1].Refs
	if len(refs) != 2 || refs[0] != "login" || refs[1] != "other" {
		t.Errorf("refs = %#v", refs)
	}
}

func TestParseMetadataAfterRequestLine(t *testing.T) {
	req := one(t, `GET https://example.com
# @name after
Accept: text/plain
`)
	if req.Name != "after" {
		t.Errorf("Name = %q, want after", req.Name)
	}
}

func TestParseAssertions(t *testing.T) {
	req := one(t, `POST https://example.com
Content-Type: application/json

{"ok":true}
?? status == 201
?? header content-type includes json
?? body ok == true
?? duration < 5000
`)

	want := []Assertion{
		{Kind: AssertStatus, Op: "==", Value: "201", Line: 5},
		{Kind: AssertHeader, Name: "content-type", Op: "includes", Value: "json", Line: 6},
		{Kind: AssertBody, Name: "ok", Op: "==", Value: "true", Line: 7},
		{Kind: AssertDuration, Op: "<", Value: "5000", Line: 8},
	}
	if len(req.Assertions) != len(want) {
		t.Fatalf("assertions = %#v", req.Assertions)
	}
	for i, a := range req.Assertions {
		if a != want[i] {
			t.Errorf("assertion %d = %#v, want %#v", i, a, want[i])
		}
	}
	if req.Body != `{"ok":true}` {
		t.Errorf("assertions leaked into body: %q", req.Body)
	}
}

func TestParseBodyInclude(t *testing.T) {
	raw := one(t, `POST https://example.com
Content-Type: application/json

< body.json
`)
	if raw.BodyFile != "body.json" || raw.BodySubst {
		t.Errorf("include = %q subst=%v", raw.BodyFile, raw.BodySubst)
	}

	subst := one(t, `POST https://example.com
Content-Type: application/json

<@ {{template}}.json
`)
	if subst.BodyFile != "{{template}}.json" || !subst.BodySubst {
		t.Errorf("include = %q subst=%v", subst.BodyFile, subst.BodySubst)
	}
}

func TestParseXMLBodyIsNotAnInclude(t *testing.T) {
	req := one(t, `POST https://example.com
Content-Type: application/xml

<?xml version="1.0"?>
<root><item>1</item></root>
`)
	if req.BodyFile != "" {
		t.Errorf("XML body parsed as include %q", req.BodyFile)
	}
	if !strings.HasPrefix(req.Body, "<?xml") {
		t.Errorf("Body = %q", req.Body)
	}
}

func TestParseRejectsScripts(t *testing.T) {
	tests := []string{
		"GET https://example.com\n\n> {%\nclient.test('x', () => {});\n%}\n",
		"GET https://example.com\n\n{{\nconsole.log('x');\n}}\n",
		"< {%\nrequest.variables.set('x', 1);\n%}\nGET https://example.com\n",
	}
	for _, src := range tests {
		_, err := Parse(src)
		if err == nil || !strings.Contains(err.Error(), "not supported") {
			t.Errorf("Parse(%q) error = %v, want unsupported", src, err)
		}
	}
}

func TestParseCRLFAndBOM(t *testing.T) {
	req := one(t, "\ufeffGET https://example.com\r\nAccept: text/plain\r\n\r\nhello\r\n")
	if req.URL != "https://example.com" {
		t.Errorf("URL = %q", req.URL)
	}
	if req.Body != "hello" {
		t.Errorf("Body = %q", req.Body)
	}
}

func TestParseEmptyFile(t *testing.T) {
	f := mustParse(t, "")
	if len(f.Requests) != 0 || len(f.Vars) != 0 {
		t.Errorf("got %d requests, %d vars", len(f.Requests), len(f.Vars))
	}
}

func TestParseErrors(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want string
	}{
		{"bad header", "GET https://example.com\nnot a header\n", "expected a header line"},
		{"unknown method", "FETCH https://example.com\n", `unknown method "FETCH"`},
		{"extra tokens", "GET https://example.com extra\n", "unexpected tokens"},
		{"url with trailing token", "https://example.com extra\n", "unexpected tokens"},
		{"empty name", "# @name\nGET https://example.com\n", "missing value for # @name"},
		{"unknown assertion", "GET https://example.com\n\n?? cookie == 1\n", "unknown assertion"},
		{"unknown condition", "GET https://example.com\n\n?? status = 200\n", `unknown condition "="`},
		{"missing condition", "GET https://example.com\n\n?? status\n", "missing condition"},
		{"missing value", "GET https://example.com\n\n?? status ==\n", `condition "==" needs a value`},
		{"value on no-value condition", "GET https://example.com\n\n?? status exists now\n", "takes no value"},
		{"header assertion without name", "GET https://example.com\n\n?? header == json\n", "needs a header name"},
		{"include without path", "POST https://example.com\n\n<\n", "missing file path"},
		{"invalid variable", "GET https://example.com\n@bad name = 1\n", "invalid variable definition"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := Parse(test.src)
			if err == nil {
				t.Fatal("Parse() error = nil, want error")
			}
			if !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %q, want substring %q", err, test.want)
			}
			if !strings.Contains(err.Error(), "line ") {
				t.Errorf("error has no line number: %q", err)
			}
		})
	}
}

func TestParseFormBodyKeepsLines(t *testing.T) {
	req := one(t, `POST https://example.com/login
Content-Type: application/x-www-form-urlencoded

username=admin
&password=secret
`)
	if want := "username=admin\n&password=secret"; req.Body != want {
		t.Errorf("Body = %q, want %q", req.Body, want)
	}
}

func TestParseMetaUnknownKeyword(t *testing.T) {
	f := mustParse(t, `GET https://example.com
# @unknown foo
`)
	req := f.Requests[0]
	if req.Name != "" {
		t.Errorf("Name = %q, want empty", req.Name)
	}
	if len(req.Refs) != 0 {
		t.Errorf("Refs = %v, want empty", req.Refs)
	}
}

func TestParseURLContinuationBeforeHeaders(t *testing.T) {
	req := one(t, `GET https://example.com
    /continued
Accept: text/plain
`)
	if req.URL != "https://example.com/continued" {
		t.Errorf("URL = %q, want https://example.com/continued", req.URL)
	}
}

func TestParseNameEqualsSyntax(t *testing.T) {
	tests := []struct {
		src  string
		name string
		line int
	}{
		{"# @name=login\nGET https://example.com", "login", 1},
		{"# @name = login\nGET https://example.com", "login", 1},
		{"# @name = foo bar\nGET https://example.com", "foo bar", 1},
	}
	for _, tc := range tests {
		f := mustParse(t, tc.src)
		if len(f.Requests) != 1 {
			t.Fatalf("%q: got %d requests, want 1", tc.src, len(f.Requests))
		}
		if f.Requests[0].Name != tc.name {
			t.Errorf("%q: Name = %q, want %q", tc.src, f.Requests[0].Name, tc.name)
		}
	}
}

func TestParseRefEqualsSyntax(t *testing.T) {
	f := mustParse(t, `# @ref=login
GET https://example.com`)
	if len(f.Requests[0].Refs) != 1 || f.Requests[0].Refs[0] != "login" {
		t.Errorf("Refs = %v, want [login]", f.Requests[0].Refs)
	}
}

func TestParseBodyKeepsCommentLines(t *testing.T) {
	req := one(t, `POST https://example.com
Content-Type: text/markdown

# heading
// not a comment
text
`)
	if want := "# heading\n// not a comment\ntext"; req.Body != want {
		t.Errorf("Body = %q, want %q", req.Body, want)
	}
}

func TestParseInlineBlockComment(t *testing.T) {
	req := one(t, "/* note */ GET https://example.com\n")
	if req.Method != "GET" || req.URL != "https://example.com" {
		t.Errorf("got %s %q, want GET https://example.com", req.Method, req.URL)
	}
}

func TestParseRequestWithoutLine(t *testing.T) {
	_, err := Parse("### Login\n# @name login\n# GET https://example.com\n")
	if err == nil || !strings.Contains(err.Error(), "no request line") {
		t.Fatalf("error = %v, want a no request line error", err)
	}
}

func TestParseMissingSeparator(t *testing.T) {
	_, err := Parse("GET https://example.com/a\n\nGET https://example.com/b\n")
	if err == nil || !strings.Contains(err.Error(), "###") {
		t.Fatalf("error = %v, want a missing ### hint", err)
	}
}
