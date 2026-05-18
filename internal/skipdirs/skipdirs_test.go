package skipdirs

import "testing"

func TestIsBuildOrHarness(t *testing.T) {
	cases := map[string]bool{
		".git":         true,
		".ghyll":       true,
		"vendor":       true,
		"node_modules": true,
		"target":       true,
		"build":        true,
		"specs":        false,
		"docs":         false,
		"src":          false,
		"":             false,
	}
	for in, want := range cases {
		if got := IsBuildOrHarness(in); got != want {
			t.Errorf("IsBuildOrHarness(%q) = %v; want %v", in, got, want)
		}
	}
}

func TestIsSpecOrDoc(t *testing.T) {
	for _, name := range []string{"specs", "docs", "tests", "test"} {
		if !IsSpecOrDoc(name) {
			t.Errorf("IsSpecOrDoc(%q) = false; want true", name)
		}
	}
	for _, name := range []string{"src", ".git", "vendor"} {
		if IsSpecOrDoc(name) {
			t.Errorf("IsSpecOrDoc(%q) = true; want false", name)
		}
	}
}

func TestIsSourceWalkSkip(t *testing.T) {
	for _, name := range []string{".git", "vendor", "specs", "docs"} {
		if !IsSourceWalkSkip(name) {
			t.Errorf("IsSourceWalkSkip(%q) = false; want true", name)
		}
	}
	if IsSourceWalkSkip("src") {
		t.Error("IsSourceWalkSkip(src) = true; want false")
	}
}
