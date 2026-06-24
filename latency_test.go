package sim

import (
	"fmt"
	"testing"
)

func TestLatencyModel(t *testing.T) {
	cases := []struct {
		name        string
		promptToks  int
		occupancy   float64
		wantPrefill string
		wantToken   string
	}{
		{"empty-short", 50, 0.0, "~20ms", "~10ms"},
		{"50pct-short", 50, 0.5, "~107ms", "~22ms"},
		{"90pct-short", 50, 0.9, "~300ms", "~80ms"},
		{"empty-long", 500, 0.0, "~200ms", "~10ms"},
		{"90pct-long", 500, 0.9, "~3000ms", "~80ms"},
	}
	for _, tc := range cases {
		prefill := PrefillDelay(tc.promptToks, tc.occupancy)
		token := TokenDelay(tc.occupancy)
		fmt.Printf("%-15s occ=%.1f prefill=%-12s token=%-12s (want prefill %s, token %s)\n",
			tc.name, tc.occupancy, prefill, token, tc.wantPrefill, tc.wantToken)
	}

	// Verify the key constraint: 90% occupancy is much worse than empty
	emptyPrefill := PrefillDelay(50, 0.0)
	fullPrefill := PrefillDelay(50, 0.9)
	ratio := float64(fullPrefill) / float64(emptyPrefill)
	if ratio < 10 {
		t.Errorf("prefill degradation ratio at 90%% occupancy = %.1fx, want >= 10x", ratio)
	}
	fmt.Printf("\nPrefill degradation (empty→90%%): %.1fx\n", ratio)

	emptyToken := TokenDelay(0.0)
	fullToken := TokenDelay(0.9)
	tokenRatio := float64(fullToken) / float64(emptyToken)
	if tokenRatio < 5 {
		t.Errorf("token degradation ratio at 90%% occupancy = %.1fx, want >= 5x", tokenRatio)
	}
	fmt.Printf("Token degradation (empty→90%%): %.1fx\n", tokenRatio)
}
