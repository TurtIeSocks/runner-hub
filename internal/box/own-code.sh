#!/bin/bash
# Job-started hook for a public repo's runners: a job runs only on the
# repo's own code. GitHub hands a runner any queued job whose labels match,
# so the dispatcher's check on the job it launched for isn't enough. A job
# this refuses fails before its first step. Anything unexpected refuses.
set -uo pipefail
event=${GITHUB_EVENT_NAME:-}
repo=${GITHUB_REPOSITORY:-}
head=$(jq -r '.pull_request.head.repo.full_name // empty' "${GITHUB_EVENT_PATH:-/nonexistent}" 2>/dev/null)
case $event in
  # Only someone who can write to the repo can push to it or dispatch.
  push | workflow_dispatch) [ -n "$repo" ] && exit 0 ;;
  pull_request) [ -n "$repo" ] && [ "${head,,}" = "${repo,,}" ] && exit 0 ;;
esac
echo "runner-hub: refusing a '${event:-unknown}' job on ${repo:-an unknown repo}: its code is from '${head:-somewhere else}', not the repo's own branches" >&2
exit 1
