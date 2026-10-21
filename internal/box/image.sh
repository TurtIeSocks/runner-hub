# Shared by the scripts that launch or prepare.

# setup-python's prebuilt Pythons only run from this path, so the host's
# cache sits there too, and a container: job's /__t mount finds it.
toolcache=/opt/hostedtoolcache

# The image's runner user is uid 1001.
ensure_dirs() {
  local base=/srv/runner-hub
  if [ -d "$base/toolcache" ] && [ ! -e "$toolcache" ]; then
    sudo mv -T "$base/toolcache" "$toolcache"
  fi
  sudo install -d -o 1001 -g 1001 -m 755 "$base" "$base/work" "$toolcache"
}

# Builds the runner image from the pinned base if this box doesn't have it
# yet, then drops older builds. Prepended to scripts that need the image.
# Args: base tag dockerfile_b64.
ensure_image() {
  local base=$1 tag=$2 df=$3
  if ! docker image inspect "$tag" >/dev/null 2>&1; then
    printf '%s' "$df" | base64 -d | docker build -q -t "$tag" --build-arg "BASE=$base" - >/dev/null
    docker images runner-hub/runner --format '{{.Repository}}:{{.Tag}}' | grep -vxF "$tag" | xargs -r docker rmi >/dev/null 2>&1 || true
  fi
  ensure_template "$tag"
}

# Copies the image's runner install out to the host, where each launch
# hard-links it. Root owns the files, so a runner can't change them in place
# under every other runner. Dropping an old copy is safe: running runners
# hold their own links.
ensure_template() {
  local tag=$1 dir=/srv/runner-hub/runner
  local tpl=$dir/${tag#*:} tmp
  [ -d "$tpl" ] && return
  sudo install -d -m 755 "$dir"
  tmp=$(sudo mktemp -d "$tpl.XXXXXX")
  sudo chmod 755 "$tmp"
  docker run --rm --user 0 -v "$tmp:/out" --entrypoint cp "$tag" -a /home/runner/. /out/
  sudo chown -R root:root "$tmp"
  sudo mv -T "$tmp" "$tpl" 2>/dev/null || sudo rm -rf -- "$tmp"
  for d in "$dir"/*; do
    case $d in "$tpl" | "$tpl".*) ;; *) sudo rm -rf -- "$d" ;; esac
  done
}
