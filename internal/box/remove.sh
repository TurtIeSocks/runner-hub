# Removes one runner container and its work dir. Args: name.
set -euo pipefail
name=$1
docker rm -f "$name" >/dev/null 2>&1 || true
sudo rm -rf -- "/srv/runner-hub/work/$name"
