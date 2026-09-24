#!/usr/bin/env bash
set -Eeuo pipefail
IFS=$'\n\t'
umask 077

[[ ${EUID:-$(id -u)} -eq 0 ]] || {
  echo "run with: sudo minerdash-enroll-console" >&2
  exit 1
}

read -r -p "Controller URL (https://HOST:PORT): " controller
read -r -p "Controller certificate SHA-256: " fingerprint
read -r -p "Rig name: " rig_name
read -r -s -p "Enrollment token: " enrollment_token
printf '\n'

token_file=$(mktemp /run/minerdash-enrollment.XXXXXX)
cleanup() {
  unset enrollment_token
  shred -u -- "$token_file" 2>/dev/null || rm -f -- "$token_file"
}
trap cleanup EXIT
printf '%s\n' "$enrollment_token" >"$token_file"
unset enrollment_token
chmod 0600 "$token_file"

/usr/local/sbin/minerdash-enroll \
  --controller "$controller" \
  --fingerprint "$fingerprint" \
  --name "$rig_name" \
  --token-file "$token_file"

echo "Rig enrollment is complete."
