package httpfile

import (
	"fmt"
	"net/http"
	"strings"
)

type srcLine struct {
	no   int
	text string
}

type region struct {
	title string
	lines []srcLine
}

type parseState int

const (
	stateStart parseState = iota
	stateHeaders
	stateBody
)

// Parse reads the contents of a .http file.
func Parse(src string) (*File, error) {
	f := &File{}
	for _, reg := range splitRegions(src) {
		req, defs, err := parseRegion(reg)
		if err != nil {
			return nil, err
		}
		if req == nil {
			f.Vars = append(f.Vars, defs...)
			continue
		}
		req.Vars = append(req.Vars, defs...)
		f.Requests = append(f.Requests, req)
	}
	return f, nil
}

// splitRegions cuts the source at "###" lines and drops comments that occupy
// whole lines, keeping the original line numbers for error messages.
func splitRegions(src string) []region {
	src = strings.TrimPrefix(src, "\ufeff")
	src = strings.ReplaceAll(src, "\r\n", "\n")
	src = strings.ReplaceAll(src, "\r", "\n")

	regions := []region{{}}
	inBlock := false
	for i, text := range strings.Split(src, "\n") {
		var comment bool
		text, inBlock, comment = stripBlockComment(text, inBlock)
		if comment && strings.TrimSpace(text) == "" {
			continue
		}
		trimmed := strings.TrimSpace(text)
		if strings.HasPrefix(trimmed, "###") {
			title := strings.TrimSpace(strings.TrimLeft(trimmed, "#"))
			regions = append(regions, region{title: title})
			continue
		}
		last := &regions[len(regions)-1]
		last.lines = append(last.lines, srcLine{no: i + 1, text: text})
	}
	return regions
}

// stripBlockComment removes "/* ... */" spans from a line and reports whether
// the line ended inside an unterminated comment. Text after the closing "*/"
// is kept, so a request may follow a comment on the same line.
func stripBlockComment(line string, inBlock bool) (string, bool, bool) {
	commented := inBlock
	for {
		if inBlock {
			end := strings.Index(line, "*/")
			if end < 0 {
				return "", true, true
			}
			line, inBlock = line[end+2:], false
			continue
		}
		trimmed := strings.TrimLeft(line, " \t")
		if !strings.HasPrefix(trimmed, "/*") {
			return line, false, commented
		}
		commented = true
		rest := trimmed[2:]
		end := strings.Index(rest, "*/")
		if end < 0 {
			return "", true, true
		}
		line = rest[end+2:]
	}
}

// requestParser holds the state of one "###" region while its lines are read.
type requestParser struct {
	title    string
	req      *Request
	defs     []Var
	body     []string
	metaLine int
	state    parseState
}

func parseRegion(reg region) (*Request, []Var, error) {
	p := &requestParser{title: reg.title, state: stateStart}
	for _, line := range reg.lines {
		if isUnsupportedBlock(strings.TrimSpace(line.text)) {
			return nil, nil, fmt.Errorf("line %d: JavaScript blocks and response handlers are not supported", line.no)
		}
		if err := p.line(line); err != nil {
			return nil, nil, err
		}
	}
	return p.finish()
}

func (p *requestParser) line(line srcLine) error {
	text, trimmed := line.text, strings.TrimSpace(line.text)
	switch p.state {
	case stateStart:
		return p.startLine(trimmed, line.no)
	case stateHeaders:
		return p.headerLine(text, trimmed, line.no)
	default:
		return p.bodyLine(text, trimmed, line.no)
	}
}

func (p *requestParser) ensure() *Request {
	if p.req == nil {
		p.req = &Request{Title: p.title}
	}
	return p.req
}

// meta applies a "# @name"/"# @ref" line and reports whether it was one.
func (p *requestParser) meta(trimmed string, no int) (bool, error) {
	kind, values, ok := parseMeta(trimmed)
	if !ok {
		return false, nil
	}
	if err := setMeta(p.ensure(), kind, values); err != nil {
		return true, fmt.Errorf("line %d: %w", no, err)
	}
	p.metaLine = no
	return true, nil
}

func (p *requestParser) assertion(trimmed string, no int) error {
	a, err := parseAssertion(trimmed, no)
	if err != nil {
		return err
	}
	req := p.ensure()
	req.Assertions = append(req.Assertions, a)
	return nil
}

func (p *requestParser) startLine(trimmed string, no int) error {
	if trimmed == "" {
		return nil
	}
	if ok, err := p.meta(trimmed, no); ok || err != nil {
		return err
	}
	if def, ok, err := parseVarDef(trimmed, no); err != nil {
		return err
	} else if ok {
		p.defs = append(p.defs, def)
		return nil
	}
	if isComment(trimmed) {
		return nil
	}
	method, rawURL, err := parseRequestLine(trimmed)
	if err != nil {
		return fmt.Errorf("line %d: %w", no, err)
	}
	req := p.ensure()
	req.Method, req.URL, req.Line = method, rawURL, no
	p.state = stateHeaders
	return nil
}

func (p *requestParser) headerLine(text, trimmed string, no int) error {
	switch {
	case trimmed == "":
		p.state = stateBody
	case isComment(trimmed):
		_, err := p.meta(trimmed, no)
		return err
	case strings.HasPrefix(trimmed, "??"):
		return p.assertion(trimmed, no)
	case isIndented(text) && isURLContinuation(trimmed, len(p.ensure().Headers) == 0):
		p.ensure().URL += trimmed
	case isIndented(text) && len(p.ensure().Headers) > 0:
		last := &p.req.Headers[len(p.req.Headers)-1]
		last.Value = strings.TrimSpace(last.Value + " " + trimmed)
	default:
		if def, ok, err := parseVarDef(trimmed, no); err != nil {
			return err
		} else if ok {
			p.defs = append(p.defs, def)
			return nil
		}
		name, value, found := strings.Cut(trimmed, ":")
		name = strings.TrimSpace(name)
		if !found || name == "" || strings.ContainsAny(name, " \t") {
			return fmt.Errorf("line %d: expected a header line, got %q", no, trimmed)
		}
		req := p.ensure()
		req.Headers = append(req.Headers, Header{Name: name, Value: strings.TrimSpace(value)})
	}
	return nil
}

func (p *requestParser) bodyLine(text, trimmed string, no int) error {
	if strings.HasPrefix(trimmed, "??") {
		return p.assertion(trimmed, no)
	}
	if len(p.body) == 0 && isInclude(trimmed) {
		include := strings.TrimPrefix(trimmed, "<")
		req := p.ensure()
		if strings.HasPrefix(include, "@") {
			req.BodySubst = true
			include = include[1:]
		}
		path := strings.TrimSpace(include)
		if path == "" {
			return fmt.Errorf("line %d: missing file path in body include", no)
		}
		req.BodyFile = path
		return nil
	}
	if len(p.body) == 0 && looksLikeRequestLine(trimmed) {
		return fmt.Errorf("line %d: missing ### before a new request", no)
	}
	p.body = append(p.body, text)
	return nil
}

func (p *requestParser) finish() (*Request, []Var, error) {
	if p.req == nil {
		return nil, p.defs, nil
	}
	if p.req.Line == 0 {
		return nil, nil, fmt.Errorf("line %d: request has no request line", p.metaLine)
	}
	p.req.Body = strings.TrimRight(strings.Join(p.body, "\n"), " \t\n")
	return p.req, p.defs, nil
}

// methods mirrors the union of the method lists documented by the .http
// implementations (JetBrains, VS Code REST Client, httpyac).
var methods = map[string]bool{
	http.MethodGet: true, http.MethodHead: true, http.MethodPost: true,
	http.MethodPut: true, http.MethodDelete: true, http.MethodConnect: true,
	http.MethodPatch: true, http.MethodOptions: true, http.MethodTrace: true,
	"LOCK": true, "UNLOCK": true, "PROPFIND": true, "PROPPATCH": true,
	"COPY": true, "MOVE": true, "MKCOL": true, "MKCALENDAR": true,
	"ACL": true, "SEARCH": true, "CHECKOUT": true, "CHECKIN": true,
	"REPORT": true, "MERGE": true, "MKACTIVITY": true, "MKWORKSPACE": true,
	"VERSION-CONTROL": true, "BASELINE-CONTROL": true,
}

func parseRequestLine(s string) (string, string, error) {
	fields := strings.Fields(s)
	if n := len(fields); n > 1 && isHTTPVersion(fields[n-1]) {
		fields = fields[:n-1]
	}
	switch len(fields) {
	case 0:
		return "", "", fmt.Errorf("empty request line")
	case 1:
		return http.MethodGet, fields[0], nil
	}
	method := strings.ToUpper(fields[0])
	if !methods[method] {
		if strings.Contains(fields[0], "://") || strings.HasPrefix(fields[0], "/") {
			return "", "", fmt.Errorf("unexpected tokens after the URL in %q", strings.Join(fields, " "))
		}
		return "", "", fmt.Errorf("unknown method %q", fields[0])
	}
	if len(fields) != 2 {
		return "", "", fmt.Errorf("unexpected tokens after the URL in %q", strings.Join(fields, " "))
	}
	return method, fields[1], nil
}

func isHTTPVersion(s string) bool {
	rest, ok := strings.CutPrefix(s, "HTTP/")
	return ok && rest != "" && !strings.ContainsAny(rest, " \t")
}

// parseMeta reads # @name value, # @name value1 value2, # @ref value,
// and the JetBrains/httpyac shorthand # @name=value or # @name = value.
func parseMeta(trimmed string) (string, []string, bool) {
	rest, ok := strings.CutPrefix(trimmed, "# @")
	if !ok {
		return "", nil, false
	}
	// Handle JetBrains/httpyac shorthand: # @name=value or # @name = value.
	if name, value, ok := strings.Cut(rest, "="); ok {
		name = strings.TrimSpace(name)
		if name == "name" || name == "ref" {
			if name == "name" {
				// Single name value.
				return name, []string{strings.TrimSpace(value)}, true
			}
			// # @ref = value1 value2
			return name, strings.Fields(strings.TrimSpace(value)), true
		}
	}
	fields := strings.Fields(rest)
	if len(fields) == 0 {
		return "", nil, false
	}
	switch fields[0] {
	case "name", "ref":
		return fields[0], fields[1:], true
	}
	return "", nil, false
}

func parseVarDef(s string, no int) (Var, bool, error) {
	if !strings.HasPrefix(s, "@") {
		return Var{}, false, nil
	}
	rest := s[1:]
	var name, value string
	if n, v, ok := strings.Cut(rest, ":="); ok {
		name, value = n, v
	} else if n, v, ok := strings.Cut(rest, "="); ok {
		name, value = n, v
	} else {
		return Var{}, false, nil
	}
	name = strings.TrimSpace(name)
	if name == "" || strings.ContainsAny(name, " \t") {
		return Var{}, false, fmt.Errorf("line %d: invalid variable definition %q", no, s)
	}
	return Var{Name: name, Value: strings.TrimSpace(value), Line: no}, true, nil
}

// setMeta applies a parsed "# @name"/"# @ref" line to req.
func setMeta(req *Request, kind string, values []string) error {
	if len(values) == 0 {
		return fmt.Errorf("missing value for # @%s", kind)
	}
	if kind == "name" {
		req.Name = values[0]
		return nil
	}
	req.Refs = append(req.Refs, values...)
	return nil
}

// looksLikeRequestLine reports whether s starts with an HTTP method and a URL,
// which means a new request is missing its ### separator.
func looksLikeRequestLine(s string) bool {
	fields := strings.Fields(s)
	if len(fields) < 2 {
		return false
	}
	return methods[strings.ToUpper(fields[0])]
}

func isComment(trimmed string) bool {
	return strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, "//")
}

// isInclude accepts "< file" but not an XML body starting with "<".
func isInclude(trimmed string) bool {
	rest, ok := strings.CutPrefix(trimmed, "<@")
	if !ok {
		rest, ok = strings.CutPrefix(trimmed, "<")
	}
	if !ok || trimmed == "" {
		return false
	}
	return rest == "" || strings.HasPrefix(rest, " ") || strings.HasPrefix(rest, "\t")
}

func isUnsupportedBlock(trimmed string) bool {
	if trimmed == "{{" {
		return true
	}
	return strings.HasPrefix(trimmed, "> {%") || strings.HasPrefix(trimmed, "< {%")
}

func isIndented(text string) bool {
	return strings.HasPrefix(text, " ") || strings.HasPrefix(text, "\t")
}

// isURLContinuation reports whether an indented line continues the request URL
// rather than a header value. Header lines contain a colon in their first word.
func isURLContinuation(trimmed string, noHeadersYet bool) bool {
	if strings.HasPrefix(trimmed, "/") || strings.HasPrefix(trimmed, "?") || strings.HasPrefix(trimmed, "&") {
		return true
	}
	if !noHeadersYet {
		return false
	}
	name, _, ok := strings.Cut(trimmed, ":")
	return !ok || name == "" || strings.ContainsAny(name, " \t")
}
