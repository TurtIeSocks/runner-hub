# runner-hub

Spreads GitHub Actions jobs from private repos on a personal account across a pool of Linux boxes, without registering a runner per repo per box.

Personal accounts can't have account-level runners, only repo-level ones. runner-hub is a GitHub App plus a small polling service. Installing the App on a repo opts it in. For each queued self-hosted job in a repo the App is installed on, it picks a free box, mints a single-use JIT runner for that repo with the job's labels, and starts it in a container on the box over ssh. The runner takes one job and exits. Workflows don't change: a job asking for `[self-hosted, my-repo]` gets a runner carrying exactly those labels.

## How it works

Every `poll_interval` (20s by default) it:

- reads each box over ssh: its runner containers, and whether something else has claimed it
- lists the repos the App is installed on, then reads each one's queued jobs and runner list
- launches a runner for each queued job that has none, on the free box of the right arch that launched least recently
- removes runners idle past `idle_timeout` (10m by default): a cancelled job, or one a static runner took first, leaves one behind
- deregisters runners whose container is gone

It keeps no state of its own. A runner's name is `hub-<box>-<job id>`, so after a restart the containers and the runner list say which jobs already have a runner coming, and nothing is launched twice.

Polling rather than a webhook means no public endpoint. A job waits up to one poll longer for its runner.

On the box, each runner is a container of `ghcr.io/actions/actions-runner` plus `build-essential` and a few archive tools (`internal/box/runner.Dockerfile`), which static runners would get from their host. Each box builds that image the first time a launch needs it. The container has:

- the host's Docker socket and host networking, so jobs can run `docker` and reach what they start
- its work dir at the same path inside and out, so a job's own `docker run -v "$PWD:..."` works
- a shared tool cache at `/srv/runner-hub/toolcache`, so `setup-go` and friends download once per box

The JIT config goes over ssh's stdin and into the container through `docker exec`. It's never on a command line or on the box's disk. The runner reads it from an environment variable and masks it in its logs. It stays in that process's environment, but it's single-use and already spent by then.

Only private repos get runners. A public repo's fork PRs would run their code on the boxes, so runner-hub skips them even with the App installed.

## Setup

### The GitHub App

Create it at https://github.com/settings/apps/new:

- Webhook: untick Active
- Repository permissions: Actions read-only, Administration read and write (runner registration lives under Administration)
- Where can this app be installed: only on this account

Then generate a private key, note the App ID, and install the App with "Only select repositories".

runner-hub serves only installations on the accounts in `owners`, so a changed install setting can't hand your boxes to someone else's repos. It also refuses an installation on "All repositories", which would serve every private repo with self-hosted jobs, including ones whose jobs can't run on the pool's arch.

### The service

```sh
go build -o bin/runner-hub ./cmd/runner-hub
```

Config lives in `/etc/runner-hub/config.toml`, see `config.example.toml`. The App's private key and the ssh key go next to it, readable only by the service's user.

`Flockfile.toml` runs it under [shep](https://github.com/shep-pm/shep).

```sh
runner-hub status         # boxes, claims, containers, and each installed repo's runners
runner-hub once           # one reconcile, then exit
runner-hub -debug run     # the service
```

## Adding a box

The box needs Docker, a user in the `docker` group with passwordless sudo, and outbound HTTPS. No inbound ports.

1. Add the service's ssh public key to that user's `authorized_keys`.
2. Add a `[[box]]` to the config.
3. Run `runner-hub prepare <box>`. It makes the dirs and builds the runner image, so the first job doesn't wait on it. The host key is pinned on first contact.
4. Restart the service.

## Opting a repo in

Add the repo to the App's installation (App settings, Install App, configure). The next poll picks it up and logs `serving repo`. Removing it stops new runners, and its leftover ones are reaped once idle.

Nothing changes on the boxes or in the config. A static runner already serving the repo will race runner-hub for its jobs. Whichever runner loses sits idle until the reaper removes it.

## Updating the runner image

GitHub stops sending jobs to runner versions that are too old, and an ephemeral runner can't update itself. Bump `runner_image` to a newer release's tag and digest and restart. Each box rebuilds on its next launch, or ahead of time with `runner-hub prepare`.

## Limits

Jobs with `container:` don't work yet. The runner mounts its own files into the job's container by host path, and those paths only exist inside the runner's container.
