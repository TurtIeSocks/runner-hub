# Starts one ephemeral runner container. Args: name repo job base tag dockerfile_b64.
# The JIT config arrives on stdin and goes straight into the container
# through docker exec, so it is never on a command line or on the host's disk.
set -euo pipefail
name=$1 repo=$2 job=$3
ensure_image "$4" "$5" "$6" </dev/null
image=$5
base=/srv/runner-hub
work=$base/work/$name

# The image's runner user is uid 1001.
sudo install -d -o 1001 -g 1001 -m 755 "$base" "$base/toolcache" "$base/work" </dev/null
sudo install -d -o 1001 -g 1001 -m 700 "$work" </dev/null
sock_gid=$(stat -c %g /var/run/docker.sock)

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

# The work dir has the same path inside and out, so a job's own
# `docker run -v "$PWD:..."` against the host's daemon finds its files.
docker run -d --rm --name "$name" \
  --label runner-hub=1 \
  --label "runner-hub.repo=$repo" \
  --label "runner-hub.job=$job" \
  --label "runner-hub.started=$(date +%s)" \
  --network host \
  --group-add "$sock_gid" \
  -v /var/run/docker.sock:/var/run/docker.sock \
  -v "$base/toolcache:/opt/hostedtoolcache" \
  -e RUNNER_TOOL_CACHE=/opt/hostedtoolcache \
  -e AGENT_TOOLSDIRECTORY=/opt/hostedtoolcache \
  -v "$work:$work" \
  --entrypoint bash \
  "$image" -c "$entry" </dev/null >/dev/null

docker exec -i "$name" sh -c 'umask 077; cat > ~/.jit.tmp && mv ~/.jit.tmp ~/.jit'
