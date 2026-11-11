// Package httpfile parses .http files into requests and variables.
package httpfile

import "strings"

// File is the parsed content of a .http file.
type File struct {
	Requests []*Request
	Vars     []Var
}

// Var is an "@name = value" definition.
type Var struct {
	Name  string
	Value string
	Line  int
}

// Header preserves the order and spelling of a request header.
type Header struct {
	Name  string
	Value string
}

// Request is a single request region of a .http file.
type Request struct {
	Title      string
	Name       string
	Refs       []string
	Method     string
	URL        string
	Headers    []Header
	Body       string
	BodyFile   string // relative path from a "< file" body
	BodySubst  bool   // true for "<@ file": variables are substituted
	Vars       []Var
	Assertions []Assertion
	Line       int
}

// AssertKind is the response field an assertion inspects.
type AssertKind string

const (
	AssertStatus   AssertKind = "status"
	AssertHeader   AssertKind = "header"
	AssertBody     AssertKind = "body"
	AssertDuration AssertKind = "duration"
)

// Assertion is a single "?? ..." line.
type Assertion struct {
	Kind  AssertKind
	Name  string // header name or JSON path inside the body
	Op    string
	Value string
	Line  int
}

func (a Assertion) String() string {
	var b strings.Builder
	b.WriteString(string(a.Kind))
	if a.Name != "" {
		b.WriteString(" ")
		b.WriteString(a.Name)
	}
	b.WriteString(" ")
	b.WriteString(a.Op)
	if a.Value != "" {
		b.WriteString(" ")
		b.WriteString(a.Value)
	}
	return b.String()
}
