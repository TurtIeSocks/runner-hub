// Package box runs the dispatcher's scripts on a runner box over ssh.
package box

import (
	"bufio"
	"bytes"
	"context"
	_ "embed"
	"encoding/base64"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/TurtIeSocks/runner-hub/internal/config"
)

var (
	//go:embed status.sh
	statusScript string
	//go:embed launch.sh
	launchScript string
	//go:embed remove.sh
	removeScript string
	//go:embed prepare.sh
	prepareScript string
)

type Box struct {
	config.Box
	ssh            config.SSH
	claimUnits     string
	claimUserUnits string
}

func New(b config.Box, cfg *config.Config) *Box {
	return &Box{
		Box:            b,
		ssh:            cfg.SSH,
		claimUnits:     unitList(cfg.ClaimUnits),
		claimUserUnits: unitList(cfg.ClaimUserUnits),
	}
}

func unitList(units []string) string {
	if len(units) == 0 {
		return "-"
	}
	return strings.Join(units, ",")
}

// Container is a runner container the dispatcher started on a box.
type Container struct {
	Name    string
	Repo    string
	JobID   int64
	Started time.Time
	State   string
}

type Status struct {
	Claimed    bool
	Containers []Container
}

func (b *Box) Status(ctx context.Context) (Status, error) {
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	out, err := b.run(ctx, statusScript, nil, b.claimUserUnits, b.claimUnits)
	if err != nil {
		return Status{}, err
	}
	return parseStatus(out)
}

func parseStatus(out []byte) (Status, error) {
	var st Status
	sawClaimed := false
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		switch {
		case len(f) == 2 && f[0] == "claimed":
			st.Claimed = f[1] == "1"
			sawClaimed = true
		case len(f) == 6 && f[0] == "c":
			job, err := strconv.ParseInt(f[3], 10, 64)
			if err != nil {
				return Status{}, fmt.Errorf("container %s: job id %q: %w", f[1], f[3], err)
			}
			started, err := strconv.ParseInt(f[4], 10, 64)
			if err != nil {
				return Status{}, fmt.Errorf("container %s: start time %q: %w", f[1], f[4], err)
			}
			st.Containers = append(st.Containers, Container{
				Name: f[1], Repo: f[2], JobID: job, Started: time.Unix(started, 0), State: f[5],
			})
		default:
			return Status{}, fmt.Errorf("unexpected status line %q", sc.Text())
		}
	}
	if !sawClaimed {
		return Status{}, fmt.Errorf("status output has no claimed line")
	}
	return st, sc.Err()
}

// Launch starts a runner container. jit is the encoded JIT config; it goes
// to the box on ssh's stdin only.
func (b *Box) Launch(ctx context.Context, name, repo string, jobID int64, image, jit string) error {
	// The first launch on a box pulls the image.
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	_, err := b.run(ctx, launchScript, strings.NewReader(jit), name, repo, strconv.FormatInt(jobID, 10), image)
	return err
}

func (b *Box) Remove(ctx context.Context, name string) error {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	_, err := b.run(ctx, removeScript, nil, name)
	return err
}

// Prepare readies a new box and returns its one-line report.
func (b *Box) Prepare(ctx context.Context, image string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Minute)
	defer cancel()
	out, err := b.run(ctx, prepareScript, nil, image)
	return strings.TrimSpace(string(out)), err
}

func (b *Box) run(ctx context.Context, script string, stdin io.Reader, args ...string) ([]byte, error) {
	remote, err := remoteCommand(script, args...)
	if err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(ctx, "ssh", append(b.sshArgs(), remote)...)
	if stdin == nil {
		stdin = strings.NewReader("")
	}
	cmd.Stdin = stdin
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("box %s: %w: %s", b.Name, err, lastLine(stderr.String()))
	}
	return stdout.Bytes(), nil
}

func (b *Box) sshArgs() []string {
	return []string{
		"-i", b.ssh.KeyPath,
		"-o", "IdentitiesOnly=yes",
		"-o", "BatchMode=yes",
		"-o", "UserKnownHostsFile=" + b.ssh.KnownHostsPath,
		// Pins each box's host key on first contact.
		"-o", "StrictHostKeyChecking=accept-new",
		"-o", "ConnectTimeout=10",
		"-o", "ServerAliveInterval=15",
		"-o", "ControlMaster=auto",
		"-o", "ControlPath=" + filepath.Join(b.ssh.ControlDir, "rh-%C"),
		"-o", "ControlPersist=5m",
		"-l", b.User,
		b.Host,
	}
}

var safeArg = regexp.MustCompile(`^[A-Za-z0-9._/:@=+*,-]+$`)

// remoteCommand builds the single string ssh hands to the remote shell. The
// script travels base64-encoded, so no quoting layer can mangle it. Each
// argument is single-quoted, and must also pass an allow-list, so neither a
// quote nor a glob can reach the shell.
func remoteCommand(script string, args ...string) (string, error) {
	for _, a := range args {
		if !safeArg.MatchString(a) {
			return "", fmt.Errorf("unsafe remote argument %q", a)
		}
	}
	enc := base64.StdEncoding.EncodeToString([]byte(script))
	cmd := `bash -c "$(echo ` + enc + ` | base64 -d)" runner-hub`
	if len(args) > 0 {
		cmd += " '" + strings.Join(args, "' '") + "'"
	}
	return cmd, nil
}

func lastLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.LastIndexByte(s, '\n'); i >= 0 {
		return s[i+1:]
	}
	return s
}
