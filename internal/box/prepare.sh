# Readies a new box: dirs, image, and a report of what the dispatcher needs.
# Args: image.
set -euo pipefail
image=$1
base=/srv/runner-hub
sudo install -d -o 1001 -g 1001 -m 755 "$base" "$base/toolcache" "$base/work"
docker pull -q "$image" >/dev/null
echo "arch=$(uname -m) docker=$(docker version -f '{{.Server.Version}}') sock_gid=$(stat -c %g /var/run/docker.sock)"
