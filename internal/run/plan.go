package run

import (
	"errors"
	"fmt"
	"strings"

	"httpcli/internal/httpfile"
)

// Selection chooses which requests of a file to run. A zero Line means no
// line selection.
type Selection struct {
	All  bool
	Name string
	Line int
}

// Plan resolves the selected requests together with everything they reference
// and returns them in dependency order.
func Plan(f *httpfile.File, sel Selection) ([]*httpfile.Request, error) {
	if len(f.Requests) == 0 {
		return nil, errors.New("file contains no requests")
	}

	byName := map[string]*httpfile.Request{}
	for _, req := range f.Requests {
		if req.Name == "" {
			continue
		}
		if previous, ok := byName[req.Name]; ok {
			return nil, fmt.Errorf("duplicate request name %q at lines %d and %d", req.Name, previous.Line, req.Line)
		}
		byName[req.Name] = req
	}

	var roots []*httpfile.Request
	switch {
	case sel.All:
		roots = f.Requests
	case sel.Name != "":
		root := byName[sel.Name]
		if root == nil {
			return nil, fmt.Errorf("no request named %q%s", sel.Name, namesHint(byName))
		}
		roots = []*httpfile.Request{root}
	case sel.Line > 0:
		root := lineAt(f.Requests, sel.Line)
		if root == nil {
			return nil, fmt.Errorf("no request at or before line %d", sel.Line)
		}
		roots = []*httpfile.Request{root}
	default:
		roots = f.Requests[:1]
	}

	const (
		unseen = iota
		visiting
		finished
	)
	state := map[*httpfile.Request]int{}
	var plan []*httpfile.Request

	var visit func(*httpfile.Request) error
	visit = func(req *httpfile.Request) error {
		switch state[req] {
		case finished:
			return nil
		case visiting:
			return fmt.Errorf("request %s has a reference cycle", label(req))
		}
		state[req] = visiting
		for _, ref := range req.Refs {
			dep, ok := byName[ref]
			if !ok {
				return fmt.Errorf("request %s references unknown request %q", label(req), ref)
			}
			if err := visit(dep); err != nil {
				return err
			}
		}
		state[req] = finished
		plan = append(plan, req)
		return nil
	}

	for _, root := range roots {
		if err := visit(root); err != nil {
			return nil, err
		}
	}
	return plan, nil
}

func lineAt(requests []*httpfile.Request, line int) *httpfile.Request {
	var found *httpfile.Request
	for _, req := range requests {
		if req.Line <= line {
			found = req
		}
	}
	return found
}

func label(req *httpfile.Request) string {
	if req.Name != "" {
		return fmt.Sprintf("%q", req.Name)
	}
	return fmt.Sprintf("at line %d", req.Line)
}

func namesHint(byName map[string]*httpfile.Request) string {
	if len(byName) == 0 {
		return ""
	}
	names := make([]string, 0, len(byName))
	for name := range byName {
		names = append(names, name)
	}
	return " (named requests: " + strings.Join(names, ", ") + ")"
}
