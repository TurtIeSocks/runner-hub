package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const base = `
app_id = 1
private_key_path = "/k.pem"
runner_image = "ghcr.io/actions/actions-runner@sha256:abc"
repos = ["owner/repo-a"]

[ssh]
key_path = "/id"
known_hosts_path = "/kh"
`

func write(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "c.toml")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLoadDefaults(t *testing.T) {
	c, err := Load(write(t, base+"\n[[box]]\nname = \"alex\"\nhost = \"10.0.0.1\"\narch = \"arm64\"\n"))
	if err != nil {
		t.Fatal(err)
	}
	if c.PollInterval != 20*time.Second || c.IdleTimeout != 10*time.Minute || c.RunnerGroupID != 1 {
		t.Errorf("defaults not applied: %+v", c)
	}
	b := c.Boxes[0]
	if b.User != "ubuntu" || b.Arch != "ARM64" || b.Slots != 1 {
		t.Errorf("box defaults not applied: %+v", b)
	}
}

func TestLoadDurations(t *testing.T) {
	c, err := Load(write(t, "poll_interval = \"30s\"\nidle_timeout = \"15m\"\n"+base))
	if err != nil {
		t.Fatal(err)
	}
	if c.PollInterval != 30*time.Second || c.IdleTimeout != 15*time.Minute {
		t.Errorf("got poll %s idle %s", c.PollInterval, c.IdleTimeout)
	}
}

func TestLoadRejects(t *testing.T) {
	cases := map[string]string{
		"unknown keys":     base + "\nbogus = 1\n",
		"not owner/name":   strings.Replace(base, `"owner/repo-a"`, `"repo-a"`, 1),
		"listed twice":     strings.Replace(base, `"owner/repo-a"`, `"owner/repo-a", "Owner/Repo-A"`, 1),
		"lower-case":       base + "\n[[box]]\nname = \"Alex\"\nhost = \"h\"\n",
		"not one of":       base + "\n[[box]]\nname = \"alex\"\nhost = \"h\"\narch = \"riscv\"\n",
		"box \"alex\" is":  base + "\n[[box]]\nname = \"alex\"\nhost = \"h\"\n[[box]]\nname = \"alex\"\nhost = \"g\"\n",
		"not a hostname":   base + "\n[[box]]\nname = \"alex\"\nhost = \"h;rm\"\n",
		"under 2m":         "idle_timeout = \"30s\"\n" + base,
		"app_id":           strings.Replace(base, "app_id = 1", "", 1),
		"ssh.key_path and": strings.Replace(base, `key_path = "/id"`, "", 1),
	}
	for want, body := range cases {
		_, err := Load(write(t, body))
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("want error containing %q, got %v", want, err)
		}
	}
}
