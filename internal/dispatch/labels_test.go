package dispatch

import (
	"reflect"
	"testing"
)

func TestRunnerNameRoundTrip(t *testing.T) {
	name := RunnerName("smart-ball", 123)
	b, id, ok := ParseRunnerName(name)
	if !ok || b != "smart-ball" || id != 123 {
		t.Errorf("ParseRunnerName(%q) = %q %d %v", name, b, id, ok)
	}
	for _, bad := range []string{"static-runner", "hub-", "hub-alex", "hub--5", "hub-alex-x", "hub-alex-0"} {
		if _, _, ok := ParseRunnerName(bad); ok {
			t.Errorf("ParseRunnerName(%q) should fail", bad)
		}
	}
}

func TestRequiredArch(t *testing.T) {
	cases := []struct {
		labels []string
		arch   string
		ok     bool
	}{
		{[]string{"self-hosted", "ek"}, "", true},
		{[]string{"Self-Hosted", "Linux", "arm64"}, "ARM64", true},
		{[]string{"self-hosted", "X64"}, "X64", true},
		{[]string{"ubuntu-latest"}, "", false},
		{[]string{"self-hosted", "Windows"}, "", false},
		{[]string{"self-hosted", "X64", "ARM64"}, "", false},
	}
	for _, c := range cases {
		arch, ok := RequiredArch(c.labels)
		if arch != c.arch || ok != c.ok {
			t.Errorf("RequiredArch(%v) = %q %v, want %q %v", c.labels, arch, ok, c.arch, c.ok)
		}
	}
}

func TestRunnerLabelsDedupes(t *testing.T) {
	got := RunnerLabels([]string{"self-hosted", "linux", "ARM64", "my-repo"}, "ARM64")
	if want := []string{"self-hosted", "Linux", "ARM64", "my-repo"}; !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}
