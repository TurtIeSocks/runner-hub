package gh

import "testing"

func TestRefuse(t *testing.T) {
	c := &Client{owners: map[string]bool{"some-user": true}}
	cases := []struct {
		account, selection string
		served             bool
	}{
		{"some-user", "selected", true},
		{"Some-User", "selected", true},
		{"some-user", "all", false},
		{"stranger", "selected", false},
		{"", "selected", false},
	}
	for _, tc := range cases {
		if got := c.refuse(tc.account, tc.selection) == ""; got != tc.served {
			t.Errorf("refuse(%q, %q) served=%v, want %v", tc.account, tc.selection, got, tc.served)
		}
	}
}
