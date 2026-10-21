package dispatch

import (
	"sort"
	"time"

	"github.com/TurtIeSocks/runner-hub/internal/box"
	"github.com/TurtIeSocks/runner-hub/internal/gh"
)

// BoxState is one box as the last poll saw it.
type BoxState struct {
	Name  string
	Arch  string
	Slots int
	// Up is false when the box's status couldn't be read; its containers are
	// then unknown, so nothing on it is launched, reaped or deregistered.
	Up         bool
	Claimed    bool
	Containers []box.Container
	LastLaunch time.Time
}

// Input is everything one reconcile knows. Runners is keyed by repo; a repo
// missing from it had its runner list fail to load.
type Input struct {
	Now         time.Time
	IdleTimeout time.Duration
	Boxes       []BoxState
	Jobs        []gh.Job
	Runners     map[string][]gh.Runner
	NotBefore   map[int64]time.Time
}

type Launch struct {
	Job    gh.Job
	Box    string
	Name   string
	Labels []string
}

// Reap removes a container that sat idle past the timeout, along with its
// runner registration (RunnerID 0 when it never registered).
type Reap struct {
	Box      string
	Name     string
	Repo     string
	RunnerID int64
}

// Deregister removes a runner registration whose container is gone.
type Deregister struct {
	Repo     string
	Name     string
	RunnerID int64
}

type Plan struct {
	Launches    []Launch
	Reaps       []Reap
	Deregisters []Deregister
	// Unservable jobs are self-hosted Linux jobs no configured box's arch
	// can take.
	Unservable []gh.Job
}

// A container that isn't running can't be doing a job; this is how long one
// gets to start before it's removed.
const startGrace = 2 * time.Minute

func Build(in Input) Plan {
	var p Plan
	boxes := map[string]*BoxState{}
	for i := range in.Boxes {
		boxes[in.Boxes[i].Name] = &in.Boxes[i]
	}

	// Jobs that already have a runner coming, from either side: a container
	// on a box, or a registration on GitHub (its box may be unreachable).
	covered := map[int64]bool{}
	containerNames := map[string]bool{}
	for _, b := range in.Boxes {
		for _, c := range b.Containers {
			covered[c.JobID] = true
			containerNames[c.Name] = true
		}
	}
	for repo, rs := range in.Runners {
		for _, r := range rs {
			boxName, jobID, ok := ParseRunnerName(r.Name)
			if !ok {
				continue
			}
			covered[jobID] = true
			b, known := boxes[boxName]
			if known && !b.Up {
				continue
			}
			if !containerNames[r.Name] && r.Status == "offline" {
				p.Deregisters = append(p.Deregisters, Deregister{Repo: repo, Name: r.Name, RunnerID: r.ID})
			}
		}
	}

	for _, b := range in.Boxes {
		if !b.Up {
			continue
		}
		for _, c := range b.Containers {
			if reap, ok := idle(in, b.Name, c); ok {
				p.Reaps = append(p.Reaps, reap)
			}
		}
	}

	used := map[string]int{}
	for _, b := range in.Boxes {
		used[b.Name] = len(b.Containers)
	}
	jobs := append([]gh.Job(nil), in.Jobs...)
	sort.Slice(jobs, func(i, j int) bool { return jobs[i].ID < jobs[j].ID })
	for _, j := range jobs {
		arch, ok := RequiredArch(j.Labels)
		if !ok || covered[j.ID] || in.Now.Before(in.NotBefore[j.ID]) {
			continue
		}
		b := pickBox(in.Boxes, arch, used)
		if b == nil {
			if !anyArch(in.Boxes, arch) {
				p.Unservable = append(p.Unservable, j)
			}
			continue
		}
		used[b.Name]++
		covered[j.ID] = true
		p.Launches = append(p.Launches, Launch{
			Job:    j,
			Box:    b.Name,
			Name:   RunnerName(b.Name, j.ID),
			Labels: RunnerLabels(j.Labels, b.Arch),
		})
	}
	return p
}

func idle(in Input, boxName string, c box.Container) (Reap, bool) {
	age := in.Now.Sub(c.Started)
	reap := Reap{Box: boxName, Name: c.Name, Repo: c.Repo}
	if c.State != "running" {
		return reap, age > startGrace
	}
	if age < in.IdleTimeout {
		return reap, false
	}
	rs, known := in.Runners[c.Repo]
	if !known {
		// Can't tell whether it's busy.
		return reap, false
	}
	for _, r := range rs {
		if r.Name == c.Name {
			if r.Busy {
				return reap, false
			}
			reap.RunnerID = r.ID
		}
	}
	return reap, true
}

// pickBox takes the free box of the right arch that launched least recently,
// which spreads jobs, and so wear and warm caches, across the pool.
func pickBox(boxes []BoxState, arch string, used map[string]int) *BoxState {
	var best *BoxState
	for i := range boxes {
		b := &boxes[i]
		if !b.Up || b.Claimed || (arch != "" && b.Arch != arch) || used[b.Name] >= b.Slots {
			continue
		}
		if best == nil || b.LastLaunch.Before(best.LastLaunch) ||
			(b.LastLaunch.Equal(best.LastLaunch) && b.Name < best.Name) {
			best = b
		}
	}
	return best
}

func anyArch(boxes []BoxState, arch string) bool {
	for _, b := range boxes {
		if arch == "" || b.Arch == arch {
			return true
		}
	}
	return false
}
