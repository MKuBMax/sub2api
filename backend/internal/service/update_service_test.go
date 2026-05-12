package service

import "testing"

func TestCompareVersionsSupportsForkRevision(t *testing.T) {
	tests := []struct {
		name    string
		current string
		latest  string
		want    int
	}{
		{name: "same upstream version without fork revision", current: "0.1.126", latest: "0.1.126.0", want: 0},
		{name: "fork revision newer than upstream base", current: "0.1.126", latest: "0.1.126.1", want: -1},
		{name: "same fork revision with v prefix", current: "v0.1.126.1", latest: "0.1.126.1", want: 0},
		{name: "next upstream patch beats fork revision", current: "0.1.126.9", latest: "0.1.127", want: -1},
		{name: "installed fork revision is newer", current: "0.1.126.2", latest: "0.1.126.1", want: 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := compareVersions(tt.current, tt.latest); got != tt.want {
				t.Fatalf("compareVersions(%q, %q) = %d, want %d", tt.current, tt.latest, got, tt.want)
			}
		})
	}
}
