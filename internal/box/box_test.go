package box

import (
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestParseStatus(t *testing.T) {
	out := "claimed 1\nc hub-alex-42 owner/repo 42 1700000000 running\n"
	st, err := parseStatus([]byte(out))
	if err != nil {
		t.Fatal(err)
	}
	if !st.Claimed || len(st.Containers) != 1 {
		t.Fatalf("got %+v", st)
	}
	c := st.Containers[0]
	if c.Name != "hub-alex-42" || c.Repo != "owner/repo" || c.JobID != 42 || !c.Started.Equal(time.Unix(1700000000, 0)) || c.State != "running" {
		t.Errorf("got %+v", c)
	}
}

func TestParseStatusRejects(t *testing.T) {
	for _, out := range []string{
		"",
		"c hub-alex-42 owner/repo 42 1700000000 running\n",
		"claimed 0\nc hub-alex-x owner/repo x 1700000000 running\n",
		"claimed 0\nsomething else\n",
	} {
		if _, err := parseStatus([]byte(out)); err == nil {
			t.Errorf("parseStatus(%q) should fail", out)
		}
	}
}

func TestRemoteCommandRunsScriptWithArgs(t *testing.T) {
	cmd, err := remoteCommand("printf '%s|' \"$0\" \"$@\"", "a", "owner/repo", "ghcr.io/x@sha256:ab", "fleet-*,x*")
	if err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command("sh", "-c", cmd).Output()
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(out), "runner-hub|a|owner/repo|ghcr.io/x@sha256:ab|fleet-*,x*|"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestRemoteCommandRejectsUnsafeArgs(t *testing.T) {
	for _, a := range []string{"a b", "$(id)", "a;b", "'", ""} {
		if _, err := remoteCommand("true", a); err == nil {
			t.Errorf("arg %q should be rejected", a)
		}
	}
}

func TestScriptsParse(t *testing.T) {
	for name, s := range map[string]string{"status": statusScript, "launch": imageLib + launchScript, "remove": removeScript, "prepare": imageLib + prepareScript} {
		if out, err := exec.Command("bash", "-n", "-c", s).CombinedOutput(); err != nil {
			t.Errorf("%s.sh: %v: %s", name, err, strings.TrimSpace(string(out)))
		}
	}
}

func TestImageTag(t *testing.T) {
	a, b := ImageTag("img@sha256:aa"), ImageTag("img@sha256:bb")
	if a == b || !strings.HasPrefix(a, "runner-hub/runner:") || a != ImageTag("img@sha256:aa") {
		t.Errorf("tags %q %q", a, b)
	}
	for _, arg := range imageArgs("ghcr.io/actions/actions-runner:2.338.0@sha256:4ffa") {
		if !safeArg.MatchString(arg) {
			t.Errorf("image arg %q would be refused", arg)
		}
	}
}
