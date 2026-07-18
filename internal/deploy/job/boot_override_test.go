package job

import (
	"testing"

	"github.com/mattcburns/shoal/internal/common/redfish"
)

func TestBootOverrideCleared(t *testing.T) {
	cases := []struct {
		en, tgt string
		want    bool
	}{
		{"Disabled", "None", true},
		{"disabled", "Hdd", true},
		{"", "", true},
		{"Continuous", "Hdd", true}, // sushy clear fallback
		{"Continuous", "None", true},
		{"Continuous", "Cd", false},
		{"Once", "Cd", false},
		{"Once", "Hdd", false},
	}
	for _, tc := range cases {
		got := bootOverrideCleared(redfish.BootInfo{OverrideEnabled: tc.en, OverrideTarget: tc.tgt})
		if got != tc.want {
			t.Errorf("cleared(%q/%q)=%v want %v", tc.en, tc.tgt, got, tc.want)
		}
	}
}
