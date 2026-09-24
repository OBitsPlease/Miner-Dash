#!/usr/bin/env bash
set -Eeuo pipefail
IFS=$'\n\t'
umask 077

die() { echo "$*" >&2; exit 1; }
usage() {
  echo "usage: $0 --controller https://HOST:PORT --fingerprint SHA256 --name RIG --token-file FILE" >&2
  exit 2
}
[[ ${EUID:-$(id -u)} -eq 0 ]] || die "run as root"
controller= fingerprint= name= token_file=
while (($#)); do
  case "$1" in
    --controller) [[ $# -ge 2 ]] || usage; controller=$2; shift 2 ;;
    --fingerprint) [[ $# -ge 2 ]] || usage; fingerprint=${2//:/}; shift 2 ;;
    --name) [[ $# -ge 2 ]] || usage; name=$2; shift 2 ;;
    --token-file) [[ $# -ge 2 ]] || usage; token_file=$2; shift 2 ;;
    *) usage ;;
  esac
done
[[ "$controller" =~ ^https://([A-Za-z0-9.-]+|\[[0-9A-Fa-f:]+\]):[0-9]{1,5}$ ]] ||
  die "controller must be an explicit HTTPS host and port"
controller_port=${controller##*:}
controller_port=$((10#$controller_port))
((controller_port >= 1 && controller_port <= 65535)) || die "controller port out of range"
[[ "${fingerprint,,}" =~ ^[0-9a-f]{64}$ ]] || die "fingerprint must contain 64 hexadecimal digits"
[[ "$name" =~ ^[A-Za-z0-9][A-Za-z0-9._-]{0,62}$ ]] || die "invalid rig name"
[[ -f "$token_file" && ! -L "$token_file" ]] || die "token file must be a regular, non-symlink file"
mode=$(stat -c '%a' "$token_file")
[[ "$mode" == 600 || "$mode" == 400 ]] || die "token file mode must be 0600 or 0400"
token=$(tr -d '\r\n' <"$token_file")
[[ "$token" =~ ^[A-Za-z0-9._~-]{16,512}$ ]] || die "token has an invalid format"
[[ -x /opt/minerdash/agent/current/minerdash-agent ]] || die "activate an agent release first"
[[ ! -e /var/lib/minerdash/identity.json ]] || die "rig is already enrolled"

write_config() {
  local destination=$1 enrollment_token=${2:-}
  {
    printf '{\n'
    printf '  "controller": "%s",\n' "$controller"
    printf '  "tls_fingerprint": "%s",\n' "${fingerprint,,}"
    if [[ -n "$enrollment_token" ]]; then
      printf '  "enrollment_token": "%s",\n' "$enrollment_token"
    fi
    printf '  "name": "%s",\n' "$name"
    printf '  "state_file": "/var/lib/minerdash/identity.json",\n'
    printf '  "package_dir": "/opt/minerdash/miners",\n'
    if [[ -x /usr/bin/xmrig ]]; then
      printf '  "profiles": {\n'
      printf '    "xmrig": {\n'
      printf '      "command": "/usr/bin/xmrig",\n'
      printf '      "args": ["--http-host", "127.0.0.1", "--http-port", "18080"],\n'
      printf '      "working_dir": "/var/lib/minerdash",\n'
      printf '      "stats_type": "xmrig",\n'
      printf '      "stats_url": "http://127.0.0.1:18080/2/summary"\n'
      printf '    }\n'
      printf '  }\n'
    else
      printf '  "profiles": {}\n'
    fi
    printf '}\n'
  } >"$destination"
}

tmp=$(mktemp /etc/minerdash/.agent.json.XXXXXX)
trap 'rm -f -- "${tmp:-}"; unset token' EXIT
write_config "$tmp" "$token"
chown root:root "$tmp"; chmod 0600 "$tmp"
mv -f -- "$tmp" /etc/minerdash/agent.json
unset token
systemctl enable --now minerdash-agent.service

for _ in {1..60}; do
  [[ -s /var/lib/minerdash/identity.json ]] && break
  sleep 1
done
if [[ ! -s /var/lib/minerdash/identity.json ]]; then
  tmp=$(mktemp /etc/minerdash/.agent.json.XXXXXX)
  write_config "$tmp"
  chown root:root "$tmp"; chmod 0600 "$tmp"
  mv -f -- "$tmp" /etc/minerdash/agent.json
  systemctl stop minerdash-agent.service
  die "enrollment timed out; token removed; inspect journalctl --namespace=minerdash -u minerdash-agent"
fi

tmp=$(mktemp /etc/minerdash/.agent.json.XXXXXX)
write_config "$tmp"
chown root:root "$tmp"; chmod 0600 "$tmp"
mv -f -- "$tmp" /etc/minerdash/agent.json
shred -u -- "$token_file" 2>/dev/null || rm -f -- "$token_file"
systemctl restart minerdash-agent.service
echo "Enrollment complete; the shared token was removed."
