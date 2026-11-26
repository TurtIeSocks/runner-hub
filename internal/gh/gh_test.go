package gh

import (
	"testing"
	"time"
)

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

func TestRunJobsStillGood(t *testing.T) {
	t0 := time.Date(2026, 11, 26, 0, 0, 0, 0, time.UTC)
	r := runJobs{fetched: t0}
	if !r.stillGood(t0.Add(jobsMaxAge - time.Second)) {
		t.Error("a fetch younger than jobsMaxAge isn't reused")
	}
	if r.stillGood(t0.Add(jobsMaxAge)) {
		t.Error("a fetch jobsMaxAge old is reused")
	}
}
