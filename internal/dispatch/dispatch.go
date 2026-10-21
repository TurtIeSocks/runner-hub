// Package dispatch matches queued GitHub Actions jobs to runner boxes.
package dispatch

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/TurtIeSocks/runner-hub/internal/box"
	"github.com/TurtIeSocks/runner-hub/internal/config"
	"github.com/TurtIeSocks/runner-hub/internal/gh"
)

// workRoot must match base in the box scripts.
const workRoot = "/srv/runner-hub/work/"

type Dispatcher struct {
	cfg   *config.Config
	gh    *gh.Client
	boxes []*box.Box
	log   *slog.Logger

	// The only state kept in memory, and losing it on restart is harmless:
	// it spreads launches and spaces out retries.
	lastLaunch map[string]time.Time
	failures   map[int64]int
	notBefore  map[int64]time.Time
	// For logging each change once instead of every poll.
	downBoxes  map[string]bool
	unservable map[int64]bool
	serving    map[string]bool
	public     map[string]bool
	skipped    map[gh.Skipped]bool
}

func New(cfg *config.Config, client *gh.Client, log *slog.Logger) *Dispatcher {
	d := &Dispatcher{
		cfg:        cfg,
		gh:         client,
		log:        log,
		lastLaunch: map[string]time.Time{},
		failures:   map[int64]int{},
		notBefore:  map[int64]time.Time{},
		downBoxes:  map[string]bool{},
		unservable: map[int64]bool{},
		serving:    map[string]bool{},
		public:     map[string]bool{},
		skipped:    map[gh.Skipped]bool{},
	}
	for _, b := range cfg.Boxes {
		d.boxes = append(d.boxes, box.New(b, cfg))
	}
	return d
}

func (d *Dispatcher) Run(ctx context.Context) {
	t := time.NewTicker(d.cfg.PollInterval)
	defer t.Stop()
	for {
		d.Reconcile(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// Reconcile reads the boxes and GitHub, then launches, reaps and
// deregisters to match. Every decision comes from what it reads here, not
// from memory, so a restart picks up where the last process stopped.
func (d *Dispatcher) Reconcile(ctx context.Context) {
	states := d.boxStates(ctx)
	served := d.servedRepos(ctx)

	repos := map[string]bool{}
	for _, r := range served {
		repos[r] = true
	}
	// A repo the App left may still have containers to clean up.
	for _, s := range states {
		for _, c := range s.Containers {
			repos[c.Repo] = true
		}
	}
	runners := map[string][]gh.Runner{}
	for r := range repos {
		rs, err := d.gh.Runners(ctx, r)
		if err != nil && !d.serving[r] {
			// The App can't see this repo's runners any more, and won't
			// again. Treating the list as empty lets its containers be
			// reaped once idle_timeout passes, busy or not.
			d.log.Debug("list runners of a repo no longer served", "repo", r, "err", err)
			runners[r] = nil
			continue
		}
		if err != nil {
			d.log.Error("list runners", "repo", r, "err", err)
			continue
		}
		runners[r] = rs
	}

	var jobs []gh.Job
	for _, r := range served {
		js, err := d.gh.QueuedJobs(ctx, r)
		if err != nil {
			d.log.Error("list queued jobs", "repo", r, "err", err)
			continue
		}
		jobs = append(jobs, js...)
	}
	d.forgetFinished(jobs)

	now := time.Now()
	plan := Build(Input{
		Now:         now,
		IdleTimeout: d.cfg.IdleTimeout,
		Boxes:       states,
		Jobs:        jobs,
		Runners:     runners,
		NotBefore:   d.notBefore,
	})

	for _, j := range plan.Unservable {
		if !d.unservable[j.ID] {
			d.unservable[j.ID] = true
			d.log.Warn("no box has the arch this job needs", "repo", j.Repo, "job", j.ID, "name", j.Name, "labels", j.Labels)
		}
	}
	for _, r := range plan.Reaps {
		d.reap(ctx, r)
	}
	for _, r := range plan.Deregisters {
		if err := d.gh.DeleteRunner(ctx, r.Repo, r.RunnerID); err != nil {
			d.log.Error("deregister orphan runner", "repo", r.Repo, "runner", r.Name, "err", err)
			continue
		}
		d.log.Info("deregistered orphan runner", "repo", r.Repo, "runner", r.Name)
	}
	d.launchAll(ctx, plan.Launches, now)
}

// servedRepos returns the private repos the App is installed on, logging
// each one as it starts or stops being served. On an API error it serves
// nothing this round: launches wait, cleanup still runs.
func (d *Dispatcher) servedRepos(ctx context.Context) []string {
	installed, skipped, err := d.gh.Repos(ctx)
	if err != nil {
		d.log.Error("list the app's repos", "err", err)
		return nil
	}
	seen := map[gh.Skipped]bool{}
	for _, s := range skipped {
		seen[s] = true
		if !d.skipped[s] {
			d.log.Error("not serving an app installation", "account", s.Account, "reason", s.Reason)
		}
	}
	d.skipped = seen
	now := map[string]bool{}
	var served []string
	for _, r := range installed {
		if !r.Private {
			// A public repo's fork PRs would run their code on the boxes.
			if !d.public[r.FullName] {
				d.public[r.FullName] = true
				d.log.Error("app is installed on a public repo, not serving it", "repo", r.FullName)
			}
			continue
		}
		delete(d.public, r.FullName)
		now[r.FullName] = true
		served = append(served, r.FullName)
		if !d.serving[r.FullName] {
			d.log.Info("serving repo", "repo", r.FullName)
		}
	}
	for r := range d.serving {
		if !now[r] {
			d.log.Info("stopped serving repo", "repo", r)
		}
	}
	d.serving = now
	return served
}

func (d *Dispatcher) boxStates(ctx context.Context) []BoxState {
	states := make([]BoxState, len(d.boxes))
	errs := make([]error, len(d.boxes))
	var wg sync.WaitGroup
	for i, b := range d.boxes {
		states[i] = BoxState{Name: b.Name, Arch: b.Arch, Slots: b.Slots, LastLaunch: d.lastLaunch[b.Name]}
		wg.Add(1)
		go func() {
			defer wg.Done()
			st, err := b.Status(ctx)
			if err != nil {
				errs[i] = err
				return
			}
			states[i].Up = true
			states[i].Claimed = st.Claimed
			states[i].Containers = st.Containers
		}()
	}
	wg.Wait()
	for i, s := range states {
		switch {
		case errs[i] != nil && !d.downBoxes[s.Name]:
			d.downBoxes[s.Name] = true
			d.log.Warn("box unreachable, skipping it", "box", s.Name, "err", errs[i])
		case errs[i] == nil && d.downBoxes[s.Name]:
			delete(d.downBoxes, s.Name)
			d.log.Info("box is back", "box", s.Name)
		}
	}
	return states
}

func (d *Dispatcher) reap(ctx context.Context, r Reap) {
	if r.RunnerID != 0 {
		err := d.gh.DeleteRunner(ctx, r.Repo, r.RunnerID)
		if errors.Is(err, gh.ErrRunnerBusy) {
			d.log.Info("idle runner took a job before it was reaped", "box", r.Box, "runner", r.Name)
			return
		}
		if err != nil {
			d.log.Error("deregister idle runner", "box", r.Box, "runner", r.Name, "err", err)
			return
		}
	}
	if err := d.box(r.Box).Remove(ctx, r.Name); err != nil {
		d.log.Error("remove idle runner", "box", r.Box, "runner", r.Name, "err", err)
		return
	}
	d.log.Info("reaped idle runner", "box", r.Box, "runner", r.Name, "repo", r.Repo)
}

func (d *Dispatcher) launchAll(ctx context.Context, launches []Launch, now time.Time) {
	errs := make([]error, len(launches))
	var wg sync.WaitGroup
	for i, l := range launches {
		d.lastLaunch[l.Box] = now
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs[i] = d.launch(ctx, l)
		}()
	}
	wg.Wait()

	for i, l := range launches {
		if err := errs[i]; err != nil {
			d.failures[l.Job.ID]++
			wait := backoff(d.failures[l.Job.ID])
			d.notBefore[l.Job.ID] = now.Add(wait)
			d.log.Error("launch failed", "box", l.Box, "repo", l.Job.Repo, "job", l.Job.ID, "retry_in", wait, "err", err)
			continue
		}
		d.log.Info("launched runner", "box", l.Box, "runner", l.Name, "repo", l.Job.Repo, "job", l.Job.ID, "name", l.Job.Name, "labels", l.Labels)
	}
}

func (d *Dispatcher) launch(ctx context.Context, l Launch) error {
	b := d.box(l.Box)
	jit, err := d.gh.MintJIT(ctx, l.Job.Repo, l.Name, d.cfg.RunnerGroupID, l.Labels, workRoot+l.Name)
	if err != nil {
		return err
	}
	if err := b.Launch(ctx, l.Name, l.Job.Repo, l.Job.ID, d.cfg.RunnerImage, jit); err != nil {
		// The registration exists now. Remove the container so the next
		// poll sees the registration with nothing behind it and
		// deregisters it.
		if rmErr := b.Remove(ctx, l.Name); rmErr != nil {
			err = fmt.Errorf("%w (and cleanup failed: %v)", err, rmErr)
		}
		return err
	}
	return nil
}

func (d *Dispatcher) box(name string) *box.Box {
	for _, b := range d.boxes {
		if b.Name == name {
			return b
		}
	}
	panic("dispatch: plan named unknown box " + name)
}

// forgetFinished drops retry and log state for jobs no longer queued.
func (d *Dispatcher) forgetFinished(queued []gh.Job) {
	live := map[int64]bool{}
	for _, j := range queued {
		live[j.ID] = true
	}
	for id := range d.notBefore {
		if !live[id] {
			delete(d.notBefore, id)
			delete(d.failures, id)
		}
	}
	for id := range d.unservable {
		if !live[id] {
			delete(d.unservable, id)
		}
	}
}

func backoff(failures int) time.Duration {
	wait := 30 * time.Second
	for i := 1; i < failures && wait < 10*time.Minute; i++ {
		wait *= 2
	}
	return min(wait, 10*time.Minute)
}
