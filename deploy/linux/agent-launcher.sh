#!/usr/bin/env bash
set -u

current=/opt/minerdash/agent/current
previous=/opt/minerdash/agent/previous

"$current/minerdash-agent" "$@"
status=$?
if ((status == 0)) || [[ ! -L "$previous" || ! -x "$previous/minerdash-agent" ]]; then
  exit "$status"
fi

failed=$(readlink -f "$current")
fallback=$(readlink -f "$previous")
ln -sfn -- "$fallback" "$current.rollback"
mv -Tf -- "$current.rollback" "$current"
ln -sfn -- "$failed" "$previous.rollback"
mv -Tf -- "$previous.rollback" "$previous"
echo "Miner Dash agent startup failed; restored $(basename "$fallback")" >&2
exec "$current/minerdash-agent" "$@"
