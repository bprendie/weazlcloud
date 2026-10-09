package desk

import "testing"

func TestMobileFinalizerLimits(t *testing.T) {
	for _, tc := range []struct {
		cpus          int
		global, owner string
		want          mobileFinalizeLimits
	}{
		{1, "", "", mobileFinalizeLimits{1, 1}},
		{2, "", "", mobileFinalizeLimits{1, 1}},
		{4, "", "", mobileFinalizeLimits{2, 1}},
		{8, "", "", mobileFinalizeLimits{4, 2}},
		{16, "", "", mobileFinalizeLimits{8, 8}},
		{128, "", "", mobileFinalizeLimits{8, 8}},
		{1, "8", "4", mobileFinalizeLimits{8, 4}},
		{16, "2", "4", mobileFinalizeLimits{2, 2}},
		{8, "0", "-1", mobileFinalizeLimits{4, 2}},
		{8, "9", "9", mobileFinalizeLimits{4, 2}},
		{8, "abc", "999999999999999999999", mobileFinalizeLimits{4, 2}},
	} {
		got := resolveMobileFinalizerLimits(tc.cpus, 32<<30, func(name string) string {
			if name == "WEAZLCLOUD_MOBILE_FINALIZE_WORKERS" {
				return tc.global
			}
			return tc.owner
		})
		if got != tc.want {
			t.Errorf("cpus=%d overrides=%q/%q: got %+v want %+v", tc.cpus, tc.global, tc.owner, got, tc.want)
		}
	}
}
