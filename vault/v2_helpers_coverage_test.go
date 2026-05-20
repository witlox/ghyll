package vault

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// Tier 3 coverage push — small helpers in v2_endpoints.go.

func TestScenario_queryBool_HappyAndErrorPaths(t *testing.T) {
	cases := []struct {
		q       string
		wantNil bool
		wantOK  bool
		wantVal bool
	}{
		{"", true, true, false},
		{"true", false, true, true},
		{"FALSE", false, true, false},
		{"1", false, true, true},
		{"0", false, true, false},
		{"yes", true, false, false}, // bad input, written to w + ok=false
	}
	for _, tc := range cases {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, "/?b="+tc.q, nil)
		got, ok := queryBool(w, r, "b")
		if ok != tc.wantOK {
			t.Errorf("queryBool(%q) ok = %v; want %v", tc.q, ok, tc.wantOK)
		}
		if tc.wantNil && got != nil {
			t.Errorf("queryBool(%q) = %v; want nil", tc.q, *got)
		}
		if !tc.wantNil && got != nil && *got != tc.wantVal {
			t.Errorf("queryBool(%q) = %v; want %v", tc.q, *got, tc.wantVal)
		}
	}
}

func TestScenario_Server_writeJSON_Roundtrip(t *testing.T) {
	s := NewServer(nil, "")
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	s.writeJSON(w, r, map[string]string{"hello": "world"})
	if got := w.Header().Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type = %q; want application/json", got)
	}
	if !contains(w.Body.String(), "hello") {
		t.Errorf("body = %q; want hello", w.Body.String())
	}
}

func TestScenario_Server_writeServerError_GenericMessage(t *testing.T) {
	s := NewServer(nil, "")
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	s.writeServerError(r, w, errInternalProbe)
	if w.Code != http.StatusInternalServerError {
		t.Errorf("status = %d; want 500", w.Code)
	}
	if contains(w.Body.String(), "errInternalProbe") {
		t.Errorf("body leaks error detail: %q", w.Body.String())
	}
}

// contains is a small substring helper to keep the test
// independent of strings import in this file.
func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// errInternalProbe is a sentinel used to assert writeServerError
// does NOT leak the underlying error string.
type errProbe struct{}

func (errProbe) Error() string { return "errInternalProbe-detail-should-not-leak" }

var errInternalProbe = errProbe{}
