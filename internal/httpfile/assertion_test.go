package httpfile

import "testing"

func TestParseAssertionForms(t *testing.T) {
	tests := []struct {
		src  string
		want Assertion
	}{
		{"?? status == 200", Assertion{Kind: AssertStatus, Op: "==", Value: "200"}},
		{"?? status != 201", Assertion{Kind: AssertStatus, Op: "!=", Value: "201"}},
		{"?? status >= 200", Assertion{Kind: AssertStatus, Op: ">=", Value: "200"}},
		{"?? header content-type startsWith application", Assertion{Kind: AssertHeader, Name: "content-type", Op: "startsWith", Value: "application"}},
		{"?? header range exists", Assertion{Kind: AssertHeader, Name: "range", Op: "exists"}},
		{"?? body includes slide", Assertion{Kind: AssertBody, Op: "includes", Value: "slide"}},
		{"?? body slideshow.slides.0.title == Wake up", Assertion{Kind: AssertBody, Name: "slideshow.slides.0.title", Op: "==", Value: "Wake up"}},
		{"?? duration < 200", Assertion{Kind: AssertDuration, Op: "<", Value: "200"}},
	}
	for _, test := range tests {
		t.Run(test.src, func(t *testing.T) {
			got, err := parseAssertion(test.src, 7)
			if err != nil {
				t.Fatalf("parseAssertion() error: %v", err)
			}
			test.want.Line = 7
			if got != test.want {
				t.Errorf("got %#v, want %#v", got, test.want)
			}
		})
	}
}

func TestAssertionString(t *testing.T) {
	a := Assertion{Kind: AssertHeader, Name: "content-type", Op: "includes", Value: "json"}
	if got, want := a.String(), "header content-type includes json"; got != want {
		t.Errorf("String() = %q, want %q", got, want)
	}
	noValue := Assertion{Kind: AssertStatus, Op: "exists"}
	if got, want := noValue.String(), "status exists"; got != want {
		t.Errorf("String() = %q, want %q", got, want)
	}
}

func TestParseAssertionErrors(t *testing.T) {
	tests := []struct {
		src  string
		want string
	}{
		{"??", "empty assertion"},
		{"?? status == 200 extra value", ""}, // multi-word values are allowed
		{"?? unknown == 1", "unknown assertion"},
		{"?? body", "missing condition"},
		{"?? body ==  ", `condition "==" needs a value`},
		{"?? status isArray 1", "takes no value"},
	}
	for _, test := range tests {
		t.Run(test.src, func(t *testing.T) {
			_, err := parseAssertion(test.src, 3)
			if test.want == "" {
				if err != nil {
					t.Fatalf("parseAssertion() error = %v, want success", err)
				}
				return
			}
			if err == nil {
				t.Fatal("parseAssertion() error = nil, want error")
			}
		})
	}
}
