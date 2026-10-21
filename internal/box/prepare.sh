# Readies a new box: dirs, image, and a report of what the dispatcher needs.
# Args: base tag dockerfile_b64.
set -euo pipefail
ensure_dirs
docker pull -q "$1" >/dev/null
ensure_image "$1" "$2" "$3"
echo "arch=$(uname -m) docker=$(docker version -f '{{.Server.Version}}') sock_gid=$(stat -c %g /var/run/docker.sock)"
