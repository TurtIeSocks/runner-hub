package dispatch

import (
	"reflect"
	"testing"

	"github.com/TurtIeSocks/runner-hub/internal/box"
	"github.com/TurtIeSocks/runner-hub/internal/config"
	"github.com/TurtIeSocks/runner-hub/internal/gh"
)

var optIn = config.PublicRepo{Name: repo, Arch: "X64", CPUs: 6, Mounts: []string{"/srv/a:/a:ro"}}

// A public repo's job from one of its own branches.
func own(id int64, event string, labels ...string) gh.Job {
	j := job(id, labels...)
	j.HeadRepo, j.Event = repo, event
	return j
}

func publicInput(boxes []BoxState, jobs ...gh.Job) Input {
	in := input(boxes, jobs)
	in.Public = map[string]config.PublicRepo{repo: optIn}
	return in
}

func x64(name string) BoxState {
	b := up(name)
	b.Arch = "X64"
	return b
}

func TestPublicRepoRefusesAForksCode(t *testing.T) {
	fork := own(1, "pull_request", "self-hosted")
	fork.HeadRepo = "someone/repo"
	p := Build(publicInput([]BoxState{x64("big")}, fork))
	if len(p.Launches) != 0 || len(p.Refused) != 1 || p.Refused[0].ID != 1 {
		t.Errorf("a fork's pull request: launches %v, refused %+v", launched(p), p.Refused)
	}
}

func TestPublicRepoRefusesPullRequestTarget(t *testing.T) {
	p := Build(publicInput([]BoxState{x64("big")}, own(1, "pull_request_target", "self-hosted")))
	if len(p.Launches) != 0 || len(p.Refused) != 1 {
		t.Errorf("pull_request_target: launches %v, refused %+v", launched(p), p.Refused)
	}
}

func TestPublicRepoRunsItsOwnCodeOnItsArchOnly(t *testing.T) {
	for _, event := range []string{"push", "pull_request", "workflow_dispatch"} {
		// The ARM box is free and higher priority, and the job names no
		// arch, yet only the opt-in's arch may take it.
		arm := up("alex")
		arm.Priority = 9
		p := Build(publicInput([]BoxState{arm, x64("big")}, own(1, event, "self-hosted", "repo")))
		if got := launched(p); !reflect.DeepEqual(got, map[int64]string{1: "big"}) {
			t.Fatalf("%s: launches %v, want job 1 on big", event, got)
		}
		want := box.Options{CPUs: 6, Mounts: []string{"/srv/a:/a:ro"}}
		if got := p.Launches[0].Container; !reflect.DeepEqual(got, want) {
			t.Errorf("%s: container %+v, want %+v", event, got, want)
		}
	}
}

func TestPublicRepoWaitsForItsArchRatherThanTakeAnother(t *testing.T) {
	busy := x64("big")
	busy.Claimed = true
	p := Build(publicInput([]BoxState{up("alex"), busy}, own(1, "push", "self-hosted")))
	if len(p.Launches) != 0 || len(p.Unservable) != 0 {
		t.Errorf("launches %v, unservable %v: the job should wait for big", launched(p), p.Unservable)
	}
	p = Build(publicInput([]BoxState{up("alex"), x64("big")}, own(2, "push", "self-hosted", "ARM64")))
	if len(p.Launches) != 0 || len(p.Unservable) != 1 {
		t.Errorf("a job asking another arch: launches %v, unservable %v", launched(p), p.Unservable)
	}
}

func TestPrivateRepoKeepsTheDockerSocket(t *testing.T) {
	p := Build(input([]BoxState{up("alex")}, []gh.Job{job(1, "self-hosted")}))
	if got := p.Launches[0].Container; !reflect.DeepEqual(got, box.Options{Docker: true}) {
		t.Errorf("container %+v, want the socket and nothing else", got)
	}
}

func TestServes(t *testing.T) {
	cfg := &config.Config{PublicRepos: []config.PublicRepo{optIn}}
	cases := map[gh.Repo]bool{
		{FullName: "owner/private", Private: true}: true,
		{FullName: "Owner/Repo"}:                   true,
		{FullName: "owner/other"}:                  false,
	}
	for r, want := range cases {
		if got := Serves(cfg, r); got != want {
			t.Errorf("Serves(%+v) = %v, want %v", r, got, want)
		}
	}
}
