package pathglob

import "testing"

func TestMatch_Equality(t *testing.T) {
	if !Match("src/main.go", "src/main.go") {
		t.Error("equality should match")
	}
}

func TestMatch_SimpleStar(t *testing.T) {
	if !Match("src/*.go", "src/main.go") {
		t.Error("single-star should match")
	}
	if Match("src/*.go", "src/sub/main.go") {
		t.Error("single-star must not cross /")
	}
}

func TestMatch_Doublestar(t *testing.T) {
	cases := []struct {
		pattern, name string
		want          bool
	}{
		{"src/**", "src/main.go", true},
		{"src/**", "src/sub/foo.go", true},
		{"src/**", "src", true}, // ** matches zero segments after the prefix
		{"src/**", "lib/main.go", false},
		{"**", "anywhere/deep/file.go", true},
		{"**/foo.go", "a/b/foo.go", true},
		{"**/foo.go", "foo.go", true}, // ** matches zero segments
	}
	for _, c := range cases {
		t.Run(c.pattern+"->"+c.name, func(t *testing.T) {
			got := Match(c.pattern, c.name)
			if got != c.want {
				t.Errorf("Match(%q, %q) = %v; want %v", c.pattern, c.name, got, c.want)
			}
		})
	}
}

func TestMatch_MultipleDoublestar(t *testing.T) {
	// validation-pass-3 F13 (and F3 from agent 1's review):
	// multi-`**` patterns must work end-to-end.
	if !Match("a/**/foo/**/bar", "a/x/foo/y/bar") {
		t.Error("multi-** failed for a/x/foo/y/bar")
	}
	if !Match("a/**/foo/**/bar", "a/foo/bar") {
		t.Error("multi-** failed for the zero-segment expansion case")
	}
	if Match("a/**/foo/**/bar", "a/foo/baz") {
		t.Error("multi-** must not match non-bar suffix")
	}
}

func TestMatch_MalformedPatternFailsClosed(t *testing.T) {
	if Match("src/[unclosed", "src/main.go") {
		t.Error("malformed pattern should not match (safety gate)")
	}
}

func TestMatch_EmptyPattern(t *testing.T) {
	if !Match("", "") {
		t.Error("empty pattern should match empty name")
	}
	if Match("", "src/main.go") {
		t.Error("empty pattern must not match non-empty name")
	}
}
