# Readies a new box: dirs, image, and a report of what the dispatcher needs.
# Args: base tag dockerfile_b64.
set -euo pipefail
base=/srv/runner-hub
sudo install -d -o 1001 -g 1001 -m 755 "$base" "$base/toolcache" "$base/work"
docker pull -q "$1" >/dev/null
ensure_image "$1" "$2" "$3"
echo "arch=$(uname -m) docker=$(docker version -f '{{.Server.Version}}') sock_gid=$(stat -c %g /var/run/docker.sock)"
