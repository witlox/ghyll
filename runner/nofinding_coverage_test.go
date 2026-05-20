package runner

import "testing"

// Tier 3 coverage push — coerceSeverityArg + parseSeverityRank.

func TestScenario_coerceSeverityArg_AcceptedTypes(t *testing.T) {
	cases := []struct {
		in   any
		want int
		err  bool
	}{
		{"high", SeverityHigh, false},
		{"  CRITICAL  ", SeverityCritical, false},
		{3, 3, false},
		{int64(2), 2, false},
		{float64(1), 1, false},
		{float64(1.5), 0, true},
		{5, 0, true}, // out of 0..4
		{-1, 0, true},
		{true, 0, true},
		{nil, 0, true},
	}
	for _, tc := range cases {
		got, err := coerceSeverityArg(tc.in)
		if (err != nil) != tc.err {
			t.Errorf("coerceSeverityArg(%v) err = %v; want err=%v", tc.in, err, tc.err)
		}
		if !tc.err && got != tc.want {
			t.Errorf("coerceSeverityArg(%v) = %d; want %d", tc.in, got, tc.want)
		}
	}
}

func TestScenario_parseSeverityRank_KnownAndUnknown(t *testing.T) {
	known := map[string]int{
		"info":     SeverityInfo,
		"low":      SeverityLow,
		"medium":   SeverityMedium,
		"high":     SeverityHigh,
		"critical": SeverityCritical,
	}
	for s, want := range known {
		got, err := parseSeverityRank(s)
		if err != nil {
			t.Errorf("parseSeverityRank(%q): %v", s, err)
		}
		if got != want {
			t.Errorf("parseSeverityRank(%q) = %d; want %d", s, got, want)
		}
	}
	if _, err := parseSeverityRank("nope"); err == nil {
		t.Error("parseSeverityRank(\"nope\") expected error")
	}
}
