package bootstrap

import "testing"

// Tier 3 coverage push — bootstrap.toFloat + equalAny coercion.

func TestScenario_bootstrap_toFloat_CoversNumericTypes(t *testing.T) {
	cases := []struct {
		in     any
		want   float64
		wantOK bool
	}{
		{int(5), 5, true},
		{int8(8), 8, true},
		{int16(16), 16, true},
		{int32(32), 32, true},
		{int64(64), 64, true},
		{uint(5), 5, true},
		{uint8(8), 8, true},
		{uint16(16), 16, true},
		{uint32(32), 32, true},
		{uint64(64), 64, true},
		{float32(1.5), 1.5, true},
		{float64(3.14), 3.14, true},
		{"nope", 0, false},
		{nil, 0, false},
	}
	for _, tc := range cases {
		got, ok := toFloat(tc.in)
		if ok != tc.wantOK {
			t.Errorf("toFloat(%v) ok = %v; want %v", tc.in, ok, tc.wantOK)
		}
		if ok && got != tc.want {
			t.Errorf("toFloat(%v) = %v; want %v", tc.in, got, tc.want)
		}
	}
}

func TestScenario_bootstrap_equalAny(t *testing.T) {
	cases := []struct {
		a, b any
		want bool
	}{
		{nil, nil, true},
		{nil, 1, false},
		{1, nil, false},
		{1, 1.0, true},
		{int64(5), float64(5), true},
		{"x", "x", true},
		{"x", "y", false},
		{[]int{1, 2}, []int{1, 2}, true},
		{[]int{1, 2}, []int{2, 1}, false},
		{map[string]int{"a": 1}, map[string]int{"a": 1}, true},
	}
	for _, tc := range cases {
		if got := equalAny(tc.a, tc.b); got != tc.want {
			t.Errorf("equalAny(%v, %v) = %v; want %v", tc.a, tc.b, got, tc.want)
		}
	}
}
