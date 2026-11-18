package box

import (
	"encoding/base64"
	"os"
	"os/exec"
	"path/filepath"
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
	for name, s := range map[string]string{"own-code": ownCodeScript, "status": statusScript, "launch": imageLib + launchScript, "remove": removeScript, "prepare": imageLib + prepareScript} {
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

func TestLaunchArgs(t *testing.T) {
	cases := []struct {
		opts Options
		want []string
	}{
		{Options{Docker: true}, []string{"sock"}},
		{Options{}, []string{"nosock"}},
		{
			Options{CPUs: 6, Mounts: []string{"/srv/data/blob:/data/blob:ro", "/srv/cache:/cache"}},
			[]string{
				"nosock", "--cpus=6",
				"--mount=type=bind,src=/srv/data/blob,dst=/data/blob,readonly",
				"--mount=type=bind,src=/srv/cache,dst=/cache",
			},
		},
		{Options{Docker: true, CPUs: 1.5}, []string{"sock", "--cpus=1.5"}},
		{Options{OwnCodeOnly: true}, []string{"nosock", "--env=ACTIONS_RUNNER_HOOK_JOB_STARTED=/opt/runner-hub/own-code.sh"}},
	}
	for _, c := range cases {
		got := c.opts.launchArgs()
		if strings.Join(got, " ") != strings.Join(c.want, " ") {
			t.Errorf("%+v: got %q, want %q", c.opts, got, c.want)
		}
		for _, a := range got {
			if !safeArg.MatchString(a) {
				t.Errorf("launch arg %q would be refused", a)
			}
		}
	}
}

func TestOwnCodeHook(t *testing.T) {
	if _, err := exec.LookPath("jq"); err != nil {
		t.Skip("no jq")
	}
	dir := t.TempDir()
	hook := filepath.Join(dir, "own-code.sh")
	if err := os.WriteFile(hook, []byte(ownCodeScript), 0o755); err != nil {
		t.Fatal(err)
	}
	pr := func(head string) string {
		return `{"pull_request":{"head":{"repo":{"full_name":"` + head + `"}}}}`
	}
	cases := []struct {
		event, repo, payload string
		ok                   bool
	}{
		{"push", "o/r", `{}`, true},
		{"workflow_dispatch", "o/r", `{}`, true},
		{"pull_request", "o/r", pr("o/r"), true},
		{"pull_request", "O/R", pr("o/r"), true},
		{"pull_request", "o/r", pr("someone/r"), false},
		{"pull_request", "o/r", `{}`, false},
		{"pull_request", "o/r", `not json`, false},
		{"pull_request_target", "o/r", pr("o/r"), false},
		{"workflow_run", "o/r", `{}`, false},
		{"", "o/r", `{}`, false},
		{"push", "", `{}`, false},
	}
	for _, c := range cases {
		payload := filepath.Join(dir, "event.json")
		if err := os.WriteFile(payload, []byte(c.payload), 0o600); err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command("bash", hook)
		cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "GITHUB_EVENT_PATH=" + payload}
		if c.event != "" {
			cmd.Env = append(cmd.Env, "GITHUB_EVENT_NAME="+c.event)
		}
		if c.repo != "" {
			cmd.Env = append(cmd.Env, "GITHUB_REPOSITORY="+c.repo)
		}
		out, err := cmd.CombinedOutput()
		if ok := err == nil; ok != c.ok {
			t.Errorf("%s on %s with %s: ran=%v, want %v (%s)", c.event, c.repo, c.payload, ok, c.ok, out)
		}
	}
	// No payload at all refuses too.
	cmd := exec.Command("bash", hook)
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "GITHUB_EVENT_NAME=pull_request", "GITHUB_REPOSITORY=o/r"}
	if cmd.Run() == nil {
		t.Error("a pull_request with no payload ran")
	}
}

func TestImageHoldsTheOwnCodeHook(t *testing.T) {
	enc := base64.StdEncoding.EncodeToString([]byte(ownCodeScript))
	if !strings.Contains(dockerfile, enc) || !strings.Contains(dockerfile, "> "+OwnCodeHook) {
		t.Error("the image's Dockerfile doesn't write the hook")
	}
	if !strings.HasSuffix(dockerfile, "USER runner\n") {
		t.Error("the image doesn't end back on the runner user")
	}
}
