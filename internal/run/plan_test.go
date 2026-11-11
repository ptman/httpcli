package run

import (
	"strings"
	"testing"

	"httpcli/internal/httpfile"
)

const chainedFile = `### Login
# @name login
POST https://example.com/login

### Profile
# @name profile
# @ref login
GET https://example.com/profile

### Other
# @name other
GET https://example.com/other
`

func parse(t *testing.T, src string) *httpfile.File {
	t.Helper()
	f, err := httpfile.Parse(src)
	if err != nil {
		t.Fatalf("Parse() error: %v", err)
	}
	return f
}

func names(reqs []*httpfile.Request) []string {
	result := make([]string, len(reqs))
	for i, req := range reqs {
		result[i] = req.Name
	}
	return result
}

func TestPlanDefaultRunsFirstRequest(t *testing.T) {
	f := parse(t, chainedFile)
	plan, err := Plan(f, Selection{})
	if err != nil {
		t.Fatalf("Plan() error: %v", err)
	}
	if got := names(plan); len(got) != 1 || got[0] != "login" {
		t.Errorf("plan = %v, want [login]", got)
	}
}

func TestPlanAll(t *testing.T) {
	f := parse(t, chainedFile)
	plan, err := Plan(f, Selection{All: true})
	if err != nil {
		t.Fatalf("Plan() error: %v", err)
	}
	if got := names(plan); strings.Join(got, ",") != "login,profile,other" {
		t.Errorf("plan = %v", got)
	}
}

func TestPlanByNameRunsReferencesFirst(t *testing.T) {
	f := parse(t, chainedFile)
	plan, err := Plan(f, Selection{Name: "profile"})
	if err != nil {
		t.Fatalf("Plan() error: %v", err)
	}
	if got := names(plan); strings.Join(got, ",") != "login,profile" {
		t.Errorf("plan = %v, want [login profile]", got)
	}
}

func TestPlanByLine(t *testing.T) {
	f := parse(t, chainedFile)
	plan, err := Plan(f, Selection{Line: f.Requests[1].Line})
	if err != nil {
		t.Fatalf("Plan() error: %v", err)
	}
	if got := names(plan); strings.Join(got, ",") != "login,profile" {
		t.Errorf("plan = %v, want [login profile]", got)
	}
}

func TestPlanLineBeforeFirstRequest(t *testing.T) {
	f := parse(t, chainedFile)
	if _, err := Plan(f, Selection{Line: 1}); err == nil {
		t.Fatal("Plan() error = nil, want error")
	}
}

func TestPlanUnknownName(t *testing.T) {
	_, err := Plan(parse(t, chainedFile), Selection{Name: "missing"})
	if err == nil {
		t.Fatal("Plan() error = nil, want error")
	}
	for _, want := range []string{`"missing"`, "login", "profile", "other"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q missing %q", err, want)
		}
	}
}

func TestPlanRunsOutOfOrderReferenceFirst(t *testing.T) {
	f := parse(t, `### Protected
# @name protected
# @ref login
GET https://example.com/protected

### Login
# @name login
POST https://example.com/login
`)
	plan, err := Plan(f, Selection{Name: "protected"})
	if err != nil {
		t.Fatalf("Plan() error: %v", err)
	}
	if got := names(plan); strings.Join(got, ",") != "login,protected" {
		t.Errorf("plan = %v, want [login protected]", got)
	}
}

func TestPlanReferenceCycle(t *testing.T) {
	f := parse(t, `### A
# @name a
# @ref b
GET https://example.com/a

### B
# @name b
# @ref a
GET https://example.com/b
`)
	_, err := Plan(f, Selection{All: true})
	if err == nil || !strings.Contains(err.Error(), "cycle") {
		t.Fatalf("error = %v, want reference cycle", err)
	}
}

func TestPlanUnknownReference(t *testing.T) {
	f := parse(t, `### A
# @name a
# @ref nope
GET https://example.com/a
`)
	_, err := Plan(f, Selection{})
	if err == nil || !strings.Contains(err.Error(), `unknown request "nope"`) {
		t.Fatalf("error = %v, want unknown reference", err)
	}
}

func TestPlanEmptyFile(t *testing.T) {
	if _, err := Plan(&httpfile.File{}, Selection{}); err == nil {
		t.Fatal("Plan() error = nil, want error")
	}
}

func TestPlanDuplicateName(t *testing.T) {
	f := parse(t, `### A
# @name dup
GET https://example.com/a

### B
# @name dup
GET https://example.com/b
`)
	_, err := Plan(f, Selection{All: true})
	if err == nil || !strings.Contains(err.Error(), "duplicate request name") {
		t.Fatalf("error = %v, want a duplicate name error", err)
	}
}
