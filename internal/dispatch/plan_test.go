package dispatch

import (
	"reflect"
	"testing"
	"time"

	"github.com/TurtIeSocks/runner-hub/internal/box"
	"github.com/TurtIeSocks/runner-hub/internal/gh"
)

var now = time.Unix(1_800_000_000, 0)

const repo = "owner/repo"

func job(id int64, labels ...string) gh.Job {
	return gh.Job{Repo: repo, ID: id, Labels: labels}
}

func up(name string, cs ...box.Container) BoxState {
	return BoxState{Name: name, Arch: "ARM64", Slots: 1, Up: true, Containers: cs}
}

func running(name string, age time.Duration) box.Container {
	_, id, _ := ParseRunnerName(name)
	return box.Container{Name: name, Repo: repo, JobID: id, Started: now.Add(-age), State: "running"}
}

func input(boxes []BoxState, jobs []gh.Job, runners ...gh.Runner) Input {
	return Input{
		Now:         now,
		IdleTimeout: 10 * time.Minute,
		Boxes:       boxes,
		Jobs:        jobs,
		Runners:     map[string][]gh.Runner{repo: runners},
	}
}

func launched(p Plan) map[int64]string {
	out := map[int64]string{}
	for _, l := range p.Launches {
		out[l.Job.ID] = l.Box
	}
	return out
}

func TestTwoJobsLandOnTwoBoxes(t *testing.T) {
	p := Build(input(
		[]BoxState{up("alex"), up("kat")},
		[]gh.Job{job(2, "self-hosted", "ek"), job(1, "self-hosted", "ek")},
	))
	if got, want := launched(p), map[int64]string{1: "alex", 2: "kat"}; !reflect.DeepEqual(got, want) {
		t.Errorf("launches %v, want %v", got, want)
	}
	if p.Launches[0].Name != "hub-alex-1" {
		t.Errorf("name %q", p.Launches[0].Name)
	}
	if want := []string{"self-hosted", "Linux", "ARM64", "ek"}; !reflect.DeepEqual(p.Launches[0].Labels, want) {
		t.Errorf("labels %v, want %v", p.Launches[0].Labels, want)
	}
}

func TestBusyClaimedAndDownBoxesAreSkipped(t *testing.T) {
	claimed := up("alex")
	claimed.Claimed = true
	down := up("kat")
	down.Up = false
	busy := up("liz", running("hub-liz-9", time.Minute))
	p := Build(input([]BoxState{claimed, down, busy}, []gh.Job{job(1, "self-hosted")}))
	if len(p.Launches) != 0 {
		t.Errorf("launched %v with no free box", launched(p))
	}
	if len(p.Unservable) != 0 {
		t.Errorf("a job waiting for a free box is not unservable")
	}
}

func TestLeastRecentlyUsedBoxWins(t *testing.T) {
	a, k := up("alex"), up("kat")
	a.LastLaunch = now.Add(-time.Minute)
	k.LastLaunch = now.Add(-time.Hour)
	p := Build(input([]BoxState{a, k}, []gh.Job{job(1, "self-hosted")}))
	if got := launched(p)[1]; got != "kat" {
		t.Errorf("picked %q, want kat", got)
	}
}

// After a restart the dispatcher knows only what the boxes and GitHub tell
// it. A job whose runner is still starting must not get a second one.
func TestRestartDoesNotDuplicate(t *testing.T) {
	jobs := []gh.Job{job(7, "self-hosted")}
	// The container is up but the runner hasn't taken the job yet.
	p := Build(input([]BoxState{up("alex", running("hub-alex-7", 30*time.Second)), up("kat")}, jobs))
	if len(p.Launches) != 0 {
		t.Errorf("launched again for a job with a container: %v", launched(p))
	}
	// The box holding it is unreachable, but GitHub still lists its runner.
	down := up("alex")
	down.Up = false
	p = Build(input([]BoxState{down, up("kat")}, jobs, gh.Runner{ID: 3, Name: "hub-alex-7", Status: "online"}))
	if len(p.Launches) != 0 || len(p.Deregisters) != 0 {
		t.Errorf("acted on a job whose box is down: %+v", p)
	}
}

func TestJobStillQueuedAfterItsRunnerLeftGetsAnother(t *testing.T) {
	// Job 7's runner took another job and finished; nothing for 7 remains.
	p := Build(input([]BoxState{up("alex")}, []gh.Job{job(7, "self-hosted")}))
	if got := launched(p)[7]; got != "alex" {
		t.Errorf("want a new runner for job 7, got %v", launched(p))
	}
}

func TestIdleRunnerIsReaped(t *testing.T) {
	c := running("hub-alex-7", 11*time.Minute)
	p := Build(input([]BoxState{up("alex", c)}, nil, gh.Runner{ID: 3, Name: "hub-alex-7", Status: "online"}))
	if want := []Reap{{Box: "alex", Name: "hub-alex-7", Repo: repo, RunnerID: 3}}; !reflect.DeepEqual(p.Reaps, want) {
		t.Errorf("reaps %+v, want %+v", p.Reaps, want)
	}
}

func TestReapSkipsBusyYoungAndUnknown(t *testing.T) {
	busy := running("hub-alex-7", time.Hour)
	young := running("hub-kat-8", 5*time.Minute)
	in := input([]BoxState{up("alex", busy), up("kat", young)}, nil,
		gh.Runner{ID: 3, Name: "hub-alex-7", Status: "online", Busy: true})
	if p := Build(in); len(p.Reaps) != 0 {
		t.Errorf("reaped %+v", p.Reaps)
	}
	// Without the repo's runner list, busy can't be ruled out.
	old := running("hub-alex-7", time.Hour)
	in = input([]BoxState{up("alex", old)}, nil)
	in.Runners = map[string][]gh.Runner{}
	if p := Build(in); len(p.Reaps) != 0 {
		t.Errorf("reaped %+v without a runner list", p.Reaps)
	}
}

func TestRunnerThatNeverRegisteredIsReaped(t *testing.T) {
	p := Build(input([]BoxState{up("alex", running("hub-alex-7", 11*time.Minute))}, nil))
	if want := []Reap{{Box: "alex", Name: "hub-alex-7", Repo: repo}}; !reflect.DeepEqual(p.Reaps, want) {
		t.Errorf("reaps %+v, want %+v", p.Reaps, want)
	}
}

func TestStuckContainerIsReapedAfterGrace(t *testing.T) {
	c := running("hub-alex-7", 3*time.Minute)
	c.State = "created"
	if p := Build(input([]BoxState{up("alex", c)}, nil)); len(p.Reaps) != 1 {
		t.Errorf("stuck container not reaped: %+v", p.Reaps)
	}
}

func TestOrphanRegistrationIsDeregistered(t *testing.T) {
	p := Build(input([]BoxState{up("alex")}, nil,
		gh.Runner{ID: 3, Name: "hub-alex-7", Status: "offline"},
		gh.Runner{ID: 4, Name: "hub-gone-8", Status: "offline"},
		gh.Runner{ID: 5, Name: "hub-alex-9", Status: "online"},
		gh.Runner{ID: 6, Name: "static-runner", Status: "offline"},
	))
	want := []Deregister{{Repo: repo, Name: "hub-alex-7", RunnerID: 3}, {Repo: repo, Name: "hub-gone-8", RunnerID: 4}}
	if !reflect.DeepEqual(p.Deregisters, want) {
		t.Errorf("deregisters %+v, want %+v", p.Deregisters, want)
	}
}

func TestJobsLeftAlone(t *testing.T) {
	p := Build(input([]BoxState{up("alex")}, []gh.Job{
		job(1, "ubuntu-latest"),
		job(2, "self-hosted", "macOS"),
		job(3, "self-hosted", "X64"),
	}))
	if len(p.Launches) != 0 {
		t.Errorf("launched %v", launched(p))
	}
	if len(p.Unservable) != 1 || p.Unservable[0].ID != 3 {
		t.Errorf("want job 3 unservable, got %+v", p.Unservable)
	}
}

func TestBackoff(t *testing.T) {
	in := input([]BoxState{up("alex")}, []gh.Job{job(1, "self-hosted")})
	in.NotBefore = map[int64]time.Time{1: now.Add(time.Minute)}
	if p := Build(in); len(p.Launches) != 0 {
		t.Errorf("launched during backoff")
	}
}

func TestSlots(t *testing.T) {
	b := up("alex", running("hub-alex-1", time.Minute))
	b.Slots = 2
	p := Build(input([]BoxState{b}, []gh.Job{job(2, "self-hosted"), job(3, "self-hosted")}))
	if got, want := launched(p), map[int64]string{2: "alex"}; !reflect.DeepEqual(got, want) {
		t.Errorf("launches %v, want %v", got, want)
	}
}
