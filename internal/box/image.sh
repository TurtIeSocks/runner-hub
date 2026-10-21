# Builds the runner image from the pinned base if this box doesn't have it
# yet, then drops older builds. Prepended to scripts that need the image.
# Args: base tag dockerfile_b64.
ensure_image() {
  local base=$1 tag=$2 df=$3
  docker image inspect "$tag" >/dev/null 2>&1 && return
  printf '%s' "$df" | base64 -d | docker build -q -t "$tag" --build-arg "BASE=$base" - >/dev/null
  docker images runner-hub/runner --format '{{.Repository}}:{{.Tag}}' | grep -vxF "$tag" | xargs -r docker rmi >/dev/null 2>&1 || true
}
