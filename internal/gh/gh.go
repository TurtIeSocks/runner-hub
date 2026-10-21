// Package gh is the dispatcher's view of the GitHub API, authenticated as
// the GitHub App.
package gh

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/bradleyfalzon/ghinstallation/v2"
	"github.com/google/go-github/v92/github"
)

type Repo struct {
	FullName string
	Private  bool
}

type Job struct {
	Repo   string
	ID     int64
	RunID  int64
	Name   string
	Labels []string
}

type Runner struct {
	ID     int64
	Name   string
	Status string // "online" or "offline"
	Busy   bool
}

const requestTimeout = 30 * time.Second

// ErrRunnerBusy is returned when GitHub refuses to delete a runner that has
// just taken a job.
var ErrRunnerBusy = errors.New("runner is busy")

type Client struct {
	apps *github.Client
	atr  *ghinstallation.AppsTransport

	mu sync.Mutex
	// One client per account the App is installed on.
	byOwner map[string]*github.Client
}

func New(appID int64, privateKeyPath string) (*Client, error) {
	atr, err := ghinstallation.NewAppsTransportKeyFromFile(http.DefaultTransport, appID, privateKeyPath)
	if err != nil {
		return nil, fmt.Errorf("github app key: %w", err)
	}
	apps, err := github.NewClient(github.WithTransport(atr), github.WithTimeout(requestTimeout))
	if err != nil {
		return nil, err
	}
	return &Client{apps: apps, atr: atr, byOwner: map[string]*github.Client{}}, nil
}

// Repos lists every repo the App is installed on. Installing the App on a
// repo is what opts it in.
func (c *Client) Repos(ctx context.Context) ([]Repo, error) {
	var out []Repo
	for inst, err := range c.apps.Apps.ListInstallationsIter(ctx, &github.ListOptions{PerPage: 100}) {
		if err != nil {
			return nil, fmt.Errorf("list app installations: %w", err)
		}
		cl, err := c.installationClient(inst.GetAccount().GetLogin(), inst.GetID())
		if err != nil {
			return nil, err
		}
		for r, err := range cl.Apps.ListReposIter(ctx, &github.ListOptions{PerPage: 100}) {
			if err != nil {
				return nil, fmt.Errorf("list repos of installation %d: %w", inst.GetID(), err)
			}
			out = append(out, Repo{FullName: r.GetFullName(), Private: r.GetPrivate()})
		}
	}
	return out, nil
}

func (c *Client) installationClient(owner string, id int64) (*github.Client, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if cl, ok := c.byOwner[owner]; ok {
		return cl, nil
	}
	tr := ghinstallation.NewFromAppsTransport(c.atr, id)
	cl, err := github.NewClient(github.WithTransport(tr), github.WithTimeout(requestTimeout))
	if err != nil {
		return nil, err
	}
	c.byOwner[owner] = cl
	return cl, nil
}

// client returns the installation client for owner. A container can
// outlive its repo's installation, so an owner Repos hasn't seen is looked
// up directly.
func (c *Client) client(ctx context.Context, owner string) (*github.Client, error) {
	c.mu.Lock()
	cl, ok := c.byOwner[owner]
	c.mu.Unlock()
	if ok {
		return cl, nil
	}
	inst, _, err := c.apps.Apps.GetUserInstallation(ctx, owner)
	if err != nil {
		return nil, fmt.Errorf("find app installation for %s: %w", owner, err)
	}
	return c.installationClient(owner, inst.GetID())
}

func split(repo string) (string, string) {
	owner, name, _ := strings.Cut(repo, "/")
	return owner, name
}

// QueuedJobs lists the repo's jobs waiting for a runner. A run already in
// progress can still hold queued jobs, so both run states are read.
func (c *Client) QueuedJobs(ctx context.Context, repo string) ([]Job, error) {
	owner, name := split(repo)
	cl, err := c.client(ctx, owner)
	if err != nil {
		return nil, err
	}
	var jobs []Job
	for _, status := range []string{"queued", "in_progress"} {
		runs, _, err := cl.Actions.ListRepositoryWorkflowRuns(ctx, owner, name, &github.ListWorkflowRunsOptions{
			Status:      status,
			ListOptions: github.ListOptions{PerPage: 100},
		})
		if err != nil {
			return nil, fmt.Errorf("list %s runs of %s: %w", status, repo, err)
		}
		for _, run := range runs.WorkflowRuns {
			js, _, err := cl.Actions.ListWorkflowJobs(ctx, owner, name, run.GetID(), &github.ListWorkflowJobsOptions{
				Filter:      "latest",
				ListOptions: github.ListOptions{PerPage: 100},
			})
			if err != nil {
				return nil, fmt.Errorf("list jobs of %s run %d: %w", repo, run.GetID(), err)
			}
			for _, j := range js.Jobs {
				if j.GetStatus() != "queued" {
					continue
				}
				jobs = append(jobs, Job{Repo: repo, ID: j.GetID(), RunID: run.GetID(), Name: j.GetName(), Labels: j.Labels})
			}
		}
	}
	return jobs, nil
}

func (c *Client) Runners(ctx context.Context, repo string) ([]Runner, error) {
	owner, name := split(repo)
	cl, err := c.client(ctx, owner)
	if err != nil {
		return nil, err
	}
	var out []Runner
	opts := &github.ListRunnersOptions{ListOptions: github.ListOptions{PerPage: 100}}
	for {
		rs, resp, err := cl.Actions.ListRunners(ctx, owner, name, opts)
		if err != nil {
			return nil, fmt.Errorf("list runners of %s: %w", repo, err)
		}
		for _, r := range rs.Runners {
			out = append(out, Runner{ID: r.GetID(), Name: r.GetName(), Status: r.GetStatus(), Busy: r.GetBusy()})
		}
		if resp.NextPage == 0 {
			return out, nil
		}
		opts.Page = resp.NextPage
	}
}

// MintJIT registers a single-use runner and returns its encoded JIT config,
// which is a credential: never log it.
func (c *Client) MintJIT(ctx context.Context, repo, runnerName string, groupID int64, labels []string, workFolder string) (string, error) {
	owner, name := split(repo)
	cl, err := c.client(ctx, owner)
	if err != nil {
		return "", err
	}
	cfg, _, err := cl.Actions.CreateRepoJITConfig(ctx, owner, name, github.CreateJITConfigRequest{
		Name:          runnerName,
		RunnerGroupID: groupID,
		Labels:        labels,
		WorkFolder:    &workFolder,
	})
	if err != nil {
		return "", fmt.Errorf("mint JIT config for %s on %s: %w", runnerName, repo, err)
	}
	if cfg.GetEncodedJITConfig() == "" {
		return "", fmt.Errorf("mint JIT config for %s on %s: empty config", runnerName, repo)
	}
	return cfg.GetEncodedJITConfig(), nil
}

// DeleteRunner removes a runner registration. A runner already gone counts
// as deleted.
func (c *Client) DeleteRunner(ctx context.Context, repo string, id int64) error {
	owner, name := split(repo)
	cl, err := c.client(ctx, owner)
	if err != nil {
		return err
	}
	resp, err := cl.Actions.RemoveRunner(ctx, owner, name, id)
	if resp != nil {
		switch resp.StatusCode {
		case http.StatusNotFound:
			return nil
		case http.StatusUnprocessableEntity:
			return fmt.Errorf("delete runner %d on %s: %w", id, repo, ErrRunnerBusy)
		}
	}
	if err != nil {
		return fmt.Errorf("delete runner %d on %s: %w", id, repo, err)
	}
	return nil
}
