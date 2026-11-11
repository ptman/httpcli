package run

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"httpcli/internal/httpfile"
)

func testResponse() *Response {
	return &Response{
		Status: 200,
		Header: http.Header{
			"Content-Type": {"application/json; charset=utf-8"},
			"X-Count":      {"3"},
		},
		Body:     []byte(`{"ok":true,"count":2,"name":"ada","items":[1,2],"nothing":""}`),
		Duration: 150 * time.Millisecond,
	}
}

func TestCheckPasses(t *testing.T) {
	tests := []httpfile.Assertion{
		{Kind: httpfile.AssertStatus, Op: "==", Value: "200"},
		{Kind: httpfile.AssertStatus, Op: "!=", Value: "201"},
		{Kind: httpfile.AssertStatus, Op: ">=", Value: "200"},
		{Kind: httpfile.AssertStatus, Op: "<", Value: "300"},
		{Kind: httpfile.AssertDuration, Op: "<", Value: "1000"},
		{Kind: httpfile.AssertHeader, Name: "content-type", Op: "includes", Value: "json"},
		{Kind: httpfile.AssertHeader, Name: "Content-Type", Op: "startsWith", Value: "application"},
		{Kind: httpfile.AssertHeader, Name: "x-count", Op: "==", Value: "3"},
		{Kind: httpfile.AssertBody, Op: "includes", Value: `"name":"ada"`},
		{Kind: httpfile.AssertBody, Name: "name", Op: "==", Value: "ada"},
		{Kind: httpfile.AssertBody, Name: "name", Op: "matches", Value: "^a"},
		{Kind: httpfile.AssertBody, Name: "count", Op: ">", Value: "1"},
		{Kind: httpfile.AssertBody, Name: "count", Op: "isNumber"},
		{Kind: httpfile.AssertBody, Name: "ok", Op: "isBoolean"},
		{Kind: httpfile.AssertBody, Name: "name", Op: "isString"},
		{Kind: httpfile.AssertBody, Name: "items", Op: "isArray"},
		{Kind: httpfile.AssertBody, Name: "items.1", Op: "==", Value: "2"},
		{Kind: httpfile.AssertBody, Name: "ok", Op: "isTrue"},
		{Kind: httpfile.AssertBody, Name: "nothing", Op: "isFalse"},
		{Kind: httpfile.AssertBody, Name: "name", Op: "endsWith", Value: "da"},
		{Kind: httpfile.AssertHeader, Name: "x-count", Op: "exists"},
	}
	for _, a := range tests {
		if err := check(a, testResponse()); err != nil {
			t.Errorf("check(%s) = %v, want nil", a, err)
		}
	}
}

func TestCheckFails(t *testing.T) {
	tests := []struct {
		a    httpfile.Assertion
		want string
	}{
		{httpfile.Assertion{Kind: httpfile.AssertStatus, Op: "==", Value: "201"}, "got 200"},
		{httpfile.Assertion{Kind: httpfile.AssertStatus, Op: "!=", Value: "200"}, "got 200"},
		{httpfile.Assertion{Kind: httpfile.AssertStatus, Op: ">", Value: "200"}, "got 200"},
		{httpfile.Assertion{Kind: httpfile.AssertDuration, Op: ">", Value: "1000"}, "got 150"},
		{httpfile.Assertion{Kind: httpfile.AssertHeader, Name: "x-missing", Op: "exists"}, "header x-missing exists: not found"},
		{httpfile.Assertion{Kind: httpfile.AssertBody, Name: "missing", Op: "exists"}, "body missing exists: not found"},
		{httpfile.Assertion{Kind: httpfile.AssertBody, Name: "name", Op: "==", Value: "bob"}, "got ada"},
		{httpfile.Assertion{Kind: httpfile.AssertBody, Name: "items", Op: "isString"}, "got [1,2]"},
		{httpfile.Assertion{Kind: httpfile.AssertBody, Name: "ok", Op: "isFalse"}, "got true"},
		{httpfile.Assertion{Kind: httpfile.AssertBody, Name: "count", Op: "matches", Value: "^x"}, "got 2"},
	}
	for _, test := range tests {
		err := check(test.a, testResponse())
		if err == nil {
			t.Errorf("check(%s) = nil, want error", test.a)
			continue
		}
		if !strings.Contains(err.Error(), test.want) {
			t.Errorf("check(%s) = %q, want substring %q", test.a, err, test.want)
		}
		if !strings.HasPrefix(err.Error(), test.a.String()) {
			t.Errorf("error %q does not name the assertion %q", err, test.a)
		}
	}
}

func TestCheckInvalidComparisons(t *testing.T) {
	tests := []struct {
		a    httpfile.Assertion
		want string
	}{
		{httpfile.Assertion{Kind: httpfile.AssertBody, Name: "name", Op: ">", Value: "1"}, "numerically"},
		{httpfile.Assertion{Kind: httpfile.AssertStatus, Op: "==", Value: "abc"}, ""},
		{httpfile.Assertion{Kind: httpfile.AssertStatus, Op: ">", Value: "abc"}, "invalid number"},
		{httpfile.Assertion{Kind: httpfile.AssertBody, Name: "name", Op: "matches", Value: "["}, "invalid pattern"},
		{httpfile.Assertion{Kind: "cookie", Op: "==", Value: "1"}, "unknown assertion"},
	}
	for _, test := range tests {
		err := check(test.a, testResponse())
		if test.want == "" {
			if err == nil {
				t.Errorf("check(%s) = nil, want a failure for an impossible comparison", test.a)
			}
			continue
		}
		if err == nil || !strings.Contains(err.Error(), test.want) {
			t.Errorf("check(%s) = %v, want substring %q", test.a, err, test.want)
		}
	}
}

func TestCheckBodyPathRequiresJSON(t *testing.T) {
	resp := &Response{Status: 200, Body: []byte("plain text")}
	err := check(httpfile.Assertion{Kind: httpfile.AssertBody, Name: "field", Op: "==", Value: "x"}, resp)
	if err == nil || !strings.Contains(err.Error(), "not JSON") {
		t.Fatalf("error = %v, want JSON error", err)
	}
}

func TestCheckStringEqualityWithJSONNumber(t *testing.T) {
	resp := &Response{Status: 200, Body: []byte(`{"id":7}`)}
	if err := check(httpfile.Assertion{Kind: httpfile.AssertBody, Name: "id", Op: "==", Value: "7"}, resp); err != nil {
		t.Fatalf("numeric equality failed: %v", err)
	}
}

func TestCheckRawBodyExists(t *testing.T) {
	resp := &Response{Status: 200, Body: []byte(`hello world`)}
	err := check(httpfile.Assertion{Kind: httpfile.AssertBody, Op: "exists"}, resp)
	if err != nil {
		t.Fatalf("raw body exists: %v", err)
	}
}

func TestCheckIsNumberWithJsonNumber(t *testing.T) {
	resp := &Response{Status: 200, Body: []byte(`{"id": 42}`)}
	err := check(httpfile.Assertion{Kind: httpfile.AssertBody, Name: "id", Op: "isNumber"}, resp)
	if err != nil {
		t.Fatalf("isNumber with json.Number: %v", err)
	}
}

func TestCheckNullEquality(t *testing.T) {
	resp := &Response{Status: 200, Body: []byte(`{"v":null}`)}
	if err := check(httpfile.Assertion{Kind: httpfile.AssertBody, Name: "v", Op: "==", Value: "null"}, resp); err != nil {
		t.Fatalf("null equality: %v", err)
	}
	if err := check(httpfile.Assertion{Kind: httpfile.AssertBody, Name: "v", Op: "!=", Value: "null"}, resp); err == nil {
		t.Fatal("v != null passed, want failure")
	}
}

func TestCheckSubMillisecondDuration(t *testing.T) {
	resp := &Response{Status: 200, Duration: 400 * time.Microsecond}
	if err := check(httpfile.Assertion{Kind: httpfile.AssertDuration, Op: ">", Value: "0"}, resp); err != nil {
		t.Fatalf("duration > 0: %v", err)
	}
}

func TestCheckIsTrueIsFalseWithNumbers(t *testing.T) {
	resp := &Response{Status: 200, Body: []byte(`{"a": 42, "b": 0}`)}
	if err := check(httpfile.Assertion{Kind: httpfile.AssertBody, Name: "a", Op: "isTrue"}, resp); err != nil {
		t.Fatalf("isTrue with nonzero number: %v", err)
	}
	if err := check(httpfile.Assertion{Kind: httpfile.AssertBody, Name: "b", Op: "isFalse"}, resp); err != nil {
		t.Fatalf("isFalse with zero: %v", err)
	}
}
