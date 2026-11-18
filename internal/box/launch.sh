# Starts one ephemeral runner container. Args: name repo job base tag
# dockerfile_b64 sock|nosock, then any docker run flags the repo adds.
# The JIT config arrives on stdin and goes straight into the container
# through docker exec, so it is never on a command line or on the host's disk.
set -euo pipefail
name=$1 repo=$2 job=$3
ensure_dirs </dev/null
ensure_image "$4" "$5" "$6" </dev/null
image=$5
sock=$7
shift 7
extra=("$@")
work=/srv/runner-hub/work/$name

# The runner runs from a hard-linked copy of the template, at the same path
# inside and out. A container: job gets its externals, work dir and tool
# cache mounted by the paths the runner sees, so they must exist on the host.
sudo rm -rf -- "$work" </dev/null
sudo cp -al "/srv/runner-hub/runner/${image#*:}" "$work" </dev/null
sudo find "$work" -type d -exec chown 1001:1001 {} + </dev/null
sudo chmod 700 "$work" </dev/null
socket=()
if [ "$sock" = sock ]; then
  socket=(--group-add "$(stat -c %g /var/run/docker.sock)" -v /var/run/docker.sock:/var/run/docker.sock)
fi

# Waits for the JIT config, then hands it to the runner through the
# environment variable the runner reads its --jitconfig from.
# shellcheck disable=SC2016 # expands inside the container
entry='
for _ in $(seq 600); do [ -s ~/.jit ] && break; sleep 0.2; done
[ -s ~/.jit ] || { echo "no JIT config after 120s" >&2; exit 1; }
ACTIONS_RUNNER_INPUT_JITCONFIG=$(cat ~/.jit)
rm -f ~/.jit
export ACTIONS_RUNNER_INPUT_JITCONFIG
exec ./run.sh'

# The same paths also let a job's own `docker run -v "$PWD:..."` against
# the host's daemon find its files.
docker run -d --rm --name "$name" \
  --label runner-hub=1 \
  --label "runner-hub.repo=$repo" \
  --label "runner-hub.job=$job" \
  --label "runner-hub.started=$(date +%s)" \
  --network host \
  "${socket[@]}" \
  "${extra[@]}" \
  -v "$toolcache:$toolcache" \
  -e "RUNNER_TOOL_CACHE=$toolcache" \
  -e "AGENT_TOOLSDIRECTORY=$toolcache" \
  -v "$work:$work" \
  -w "$work" \
  --entrypoint bash \
  "$image" -c "$entry" </dev/null >/dev/null

docker exec -i "$name" sh -c 'umask 077; cat > ~/.jit.tmp && mv ~/.jit.tmp ~/.jit'
