package catalogue

import "testing"

// Tier 3 coverage push — toInt + toFloat coercion helpers.

func TestScenario_toInt_CoversNumericTypes(t *testing.T) {
	cases := map[any]int{
		int(5):     5,
		int8(8):    8,
		int16(16):  16,
		int32(32):  32,
		int64(64):  64,
		uint(5):    5,
		uint8(8):   8,
		uint16(16): 16,
		uint32(32): 32,
		uint64(64): 64,
		"string":   0,
		nil:        0,
	}
	for in, want := range cases {
		if got := toInt(in); got != want {
			t.Errorf("toInt(%v) = %d; want %d", in, got, want)
		}
	}
}

func TestScenario_toFloat_CoversNumericTypes(t *testing.T) {
	cases := []struct {
		in     any
		want   float64
		wantOK bool
	}{
		{int(5), 5, true},
		{int64(64), 64, true},
		{uint(5), 5, true},
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
