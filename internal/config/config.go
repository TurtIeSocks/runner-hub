// Package config loads the dispatcher's TOML config.
package config

import (
	"fmt"
	"os"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
)

type Config struct {
	AppID          int64         `toml:"app_id"`
	PrivateKeyPath string        `toml:"private_key_path"`
	RunnerImage    string        `toml:"runner_image"`
	RunnerGroupID  int64         `toml:"runner_group_id"`
	PollInterval   time.Duration `toml:"poll_interval"`
	IdleTimeout    time.Duration `toml:"idle_timeout"`
	// Units that, while active on a box, mean something else has taken it.
	ClaimUnits     []string `toml:"claim_units"`
	ClaimUserUnits []string `toml:"claim_user_units"`
	// Accounts whose App installations are served. Anyone else's
	// installation is ignored.
	Owners []string `toml:"owners"`
	// Public repos served despite being public, each with its own guard
	// rails. Any other public repo is never served.
	PublicRepos []PublicRepo `toml:"public_repo"`
	SSH         SSH          `toml:"ssh"`
	Boxes       []Box        `toml:"box"`
}

// PublicRepo opts one public repo in. Its jobs run only from its own
// branches (never a fork's), only on boxes of Arch, and in a container
// shaped by the rest.
type PublicRepo struct {
	Name string `toml:"name"`
	Arch string `toml:"arch"`
	// Docker mounts the host's Docker socket, which is root on the box.
	Docker bool `toml:"docker"`
	// CPUs caps the container (docker run --cpus); 0 leaves it uncapped.
	CPUs float64 `toml:"cpus"`
	// Mounts are "host:container" or "host:container:ro", both absolute.
	// The host path must exist on the box.
	Mounts []string `toml:"mounts"`
}

// Public returns the opt-in for a public repo, or nil when it has none.
func (c *Config) Public(repo string) *PublicRepo {
	for i := range c.PublicRepos {
		if strings.EqualFold(c.PublicRepos[i].Name, repo) {
			return &c.PublicRepos[i]
		}
	}
	return nil
}

type SSH struct {
	KeyPath        string `toml:"key_path"`
	KnownHostsPath string `toml:"known_hosts_path"`
	// ControlDir holds ssh ControlMaster sockets, so a poll reuses one
	// connection per box instead of handshaking every few seconds.
	ControlDir string `toml:"control_dir"`
}

type Box struct {
	Name  string `toml:"name"`
	Host  string `toml:"host"`
	User  string `toml:"user"`
	Arch  string `toml:"arch"`
	Slots int    `toml:"slots"`
	// Priority orders boxes for a job: a free box of higher priority is
	// always picked before one of lower, whatever their last launches.
	Priority int `toml:"priority"`
}

var (
	// Box names go into runner and container names, so keep them to what
	// both accept and what the name parser can split on.
	boxNameRe = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)
	ownerRe   = regexp.MustCompile(`^[A-Za-z0-9-]+$`)
	unitRe    = regexp.MustCompile(`^[A-Za-z0-9@._*:-]+$`)
	hostRe    = regexp.MustCompile(`^[A-Za-z0-9.:-]+$`)
	userRe    = regexp.MustCompile(`^[a-z_][a-z0-9_-]*$`)
	repoRe    = regexp.MustCompile(`^([A-Za-z0-9-]+)/[A-Za-z0-9._-]+$`)
	mountRe   = regexp.MustCompile(`^/[A-Za-z0-9._/-]*:/[A-Za-z0-9._/-]*(:ro)?$`)
)

// Archs maps the arch labels GitHub uses to whether a box can carry them.
var Archs = map[string]bool{"X64": true, "ARM64": true, "ARM": true}

func Load(path string) (*Config, error) {
	var c Config
	md, err := toml.DecodeFile(path, &c)
	if err != nil {
		return nil, fmt.Errorf("load config %s: %w", path, err)
	}
	if undec := md.Undecoded(); len(undec) > 0 {
		return nil, fmt.Errorf("load config %s: unknown keys %v", path, undec)
	}
	c.applyDefaults()
	if err := c.validate(); err != nil {
		return nil, fmt.Errorf("config %s: %w", path, err)
	}
	return &c, nil
}

func (c *Config) applyDefaults() {
	if c.RunnerGroupID == 0 {
		c.RunnerGroupID = 1
	}
	if c.PollInterval == 0 {
		c.PollInterval = 20 * time.Second
	}
	if c.IdleTimeout == 0 {
		c.IdleTimeout = 10 * time.Minute
	}
	if c.SSH.ControlDir == "" {
		c.SSH.ControlDir = os.TempDir()
	}
	for i := range c.Boxes {
		b := &c.Boxes[i]
		if b.User == "" {
			b.User = "ubuntu"
		}
		if b.Arch == "" {
			b.Arch = "ARM64"
		}
		b.Arch = strings.ToUpper(b.Arch)
		if b.Slots == 0 {
			b.Slots = 1
		}
	}
	for i := range c.PublicRepos {
		c.PublicRepos[i].Arch = strings.ToUpper(c.PublicRepos[i].Arch)
	}
}

func (c *Config) validate() error {
	switch {
	case c.AppID <= 0:
		return fmt.Errorf("app_id is required")
	case c.PrivateKeyPath == "":
		return fmt.Errorf("private_key_path is required")
	case len(c.Owners) == 0:
		return fmt.Errorf("owners is required: the accounts whose App installations are served")
	case c.RunnerImage == "":
		return fmt.Errorf("runner_image is required")
	case c.SSH.KeyPath == "" || c.SSH.KnownHostsPath == "":
		return fmt.Errorf("ssh.key_path and ssh.known_hosts_path are required")
	// ssh appends "/rh-" and a 40-character hash, and a Unix socket path
	// stops at 104 bytes on macOS (108 on Linux).
	case len(c.SSH.ControlDir) > 56:
		return fmt.Errorf("ssh.control_dir %q is over 56 bytes, too long for ssh's control socket", c.SSH.ControlDir)
	case c.PollInterval < 5*time.Second:
		return fmt.Errorf("poll_interval %s is under 5s", c.PollInterval)
	case c.IdleTimeout < 2*time.Minute:
		return fmt.Errorf("idle_timeout %s is under 2m, too short for a runner to start", c.IdleTimeout)
	}
	for _, o := range c.Owners {
		if !ownerRe.MatchString(o) {
			return fmt.Errorf("owner %q is not a GitHub account name", o)
		}
	}
	for _, u := range append(append([]string{}, c.ClaimUnits...), c.ClaimUserUnits...) {
		if !unitRe.MatchString(u) {
			return fmt.Errorf("claim unit pattern %q has characters a unit name can't", u)
		}
	}
	names := map[string]bool{}
	for _, b := range c.Boxes {
		switch {
		case !boxNameRe.MatchString(b.Name):
			return fmt.Errorf("box name %q must be lower-case letters, digits and single hyphens", b.Name)
		case names[b.Name]:
			return fmt.Errorf("box %q is listed twice", b.Name)
		case !hostRe.MatchString(b.Host):
			return fmt.Errorf("box %s: host %q is not a hostname or IP", b.Name, b.Host)
		case !userRe.MatchString(b.User):
			return fmt.Errorf("box %s: user %q is not a valid user name", b.Name, b.User)
		case !Archs[b.Arch]:
			return fmt.Errorf("box %s: arch %q is not one of X64, ARM64, ARM", b.Name, b.Arch)
		case b.Slots < 1:
			return fmt.Errorf("box %s: slots must be at least 1", b.Name)
		}
		names[b.Name] = true
	}
	return c.validatePublic()
}

func (c *Config) validatePublic() error {
	seen := map[string]bool{}
	for _, p := range c.PublicRepos {
		m := repoRe.FindStringSubmatch(p.Name)
		switch {
		case m == nil:
			return fmt.Errorf("public_repo %q is not owner/repo", p.Name)
		case !slices.ContainsFunc(c.Owners, func(o string) bool { return strings.EqualFold(o, m[1]) }):
			return fmt.Errorf("public_repo %s: %s is not in owners", p.Name, m[1])
		case seen[strings.ToLower(p.Name)]:
			return fmt.Errorf("public_repo %s is listed twice", p.Name)
		case !slices.ContainsFunc(c.Boxes, func(b Box) bool { return b.Arch == p.Arch }):
			return fmt.Errorf("public_repo %s: no box has arch %q", p.Name, p.Arch)
		case p.CPUs < 0:
			return fmt.Errorf("public_repo %s: cpus is negative", p.Name)
		}
		for _, mount := range p.Mounts {
			if !mountRe.MatchString(mount) {
				return fmt.Errorf("public_repo %s: mount %q is not /host:/container or /host:/container:ro", p.Name, mount)
			}
		}
		seen[strings.ToLower(p.Name)] = true
	}
	return nil
}
