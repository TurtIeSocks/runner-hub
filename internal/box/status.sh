# Args: claim_user_units claim_units.
# Prints the box's state for the dispatcher, one fact per line:
#   claimed 0|1
#   c <name> <repo> <job id> <started unix> <state>
# and removes work dirs whose container is gone. The dispatcher runs one
# reconcile at a time, so no launch can be between mkdir and docker run here.
set -euo pipefail
base=/srv/runner-hub

# Another tool can take a box for heavy work of its own. While any unit
# matching claim_user_units (the login user's systemd) or claim_units (the
# system's) is active, the box is taken. Each is a comma-separated list of
# unit patterns, or "-" for none.
user_units=${1:--} sys_units=${2:--}
claimed=0
if [ "$user_units" != - ]; then
  IFS=, read -ra pats <<<"$user_units"
  systemctl --user list-units --state=active --no-legend -- "${pats[@]}" 2>/dev/null | grep -q . && claimed=1
fi
if [ "$sys_units" != - ]; then
  IFS=, read -ra pats <<<"$sys_units"
  systemctl list-units --state=active --no-legend -- "${pats[@]}" 2>/dev/null | grep -q . && claimed=1
fi
echo "claimed $claimed"

names=$(docker ps -a --filter label=runner-hub=1 \
  --format '{{.Names}} {{.Label "runner-hub.repo"}} {{.Label "runner-hub.job"}} {{.Label "runner-hub.started"}} {{.State}}')
[ -n "$names" ] && printf '%s\n' "$names" | sed 's/^/c /'

if [ -d "$base/work" ]; then
  for d in "$base"/work/*; do
    [ -e "$d" ] || continue
    n=${d##*/}
    printf '%s\n' "$names" | grep -q "^$n " || sudo rm -rf -- "$d"
  done
fi
