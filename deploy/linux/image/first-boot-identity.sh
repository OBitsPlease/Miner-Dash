#!/usr/bin/env bash
set -Eeuo pipefail
IFS=$'\n\t'
umask 077

marker=/etc/minerdash/image-first-boot
provisioning=/boot/efi/minerdash-provision.conf
[[ -e "$marker" || -f "$provisioning" ]] || exit 0

if [[ -e "$marker" ]]; then
  # Images and disk clones must never share an agent credential or host identity.
  echo "MinerDash first boot: disabling the unconfigured agent"
  rm -f -- /etc/systemd/system/multi-user.target.wants/minerdash-agent.service
  rm -f -- /etc/minerdash/agent.json /var/lib/minerdash/identity.json
  rm -f -- /etc/ssh/ssh_host_ecdsa_key /etc/ssh/ssh_host_ecdsa_key.pub
  rm -f -- /etc/ssh/ssh_host_ed25519_key /etc/ssh/ssh_host_ed25519_key.pub
  rm -f -- /etc/ssh/ssh_host_rsa_key /etc/ssh/ssh_host_rsa_key.pub

  if [[ ! -s /etc/machine-id ]]; then
    echo "MinerDash first boot: creating machine identity"
    systemd-machine-id-setup
  fi
  machine_id=$(tr -d '\n' </etc/machine-id)
  [[ "$machine_id" =~ ^[0-9a-f]{32}$ ]] || {
    echo "failed to establish a unique machine ID" >&2
    exit 1
  }
  if [[ $(cat /etc/hostname 2>/dev/null || true) == minerdash-template ]]; then
    echo "MinerDash first boot: assigning a unique hostname"
    hostname="minerdash-${machine_id:0:8}"
    printf '%s\n' "$hostname" >/etc/hostname
    hostname "$hostname"
  fi

  echo "MinerDash first boot: completing identity setup"
  rm -f -- "$marker"
fi

if [[ -f "$provisioning" && ! -L "$provisioning" ]]; then
  version= controller= fingerprint= name= token=
  while IFS='=' read -r key value; do
    value=${value%$'\r'}
    case "$key" in
      version) version=$value ;;
      controller) controller=$value ;;
      fingerprint) fingerprint=$value ;;
      name) name=$value ;;
      token) token=$value ;;
      "") ;;
      *) echo "unknown provisioning field: $key" >&2; exit 1 ;;
    esac
  done <"$provisioning"
  [[ "$version" == 1 ]] || { echo "invalid provisioning version" >&2; exit 1; }
  [[ "$controller" =~ ^https://([A-Za-z0-9.-]+|\[[0-9A-Fa-f:]+\]):[0-9]{1,5}$ ]] ||
    { echo "invalid provisioned controller" >&2; exit 1; }
  [[ "${fingerprint,,}" =~ ^[0-9a-f]{64}$ ]] ||
    { echo "invalid provisioned fingerprint" >&2; exit 1; }
  [[ "$name" =~ ^[A-Za-z0-9][A-Za-z0-9._-]{0,62}$ ]] ||
    { echo "invalid provisioned rig name" >&2; exit 1; }
  [[ "$token" =~ ^[A-Za-z0-9._~-]{16,512}$ ]] ||
    { echo "invalid provisioned enrollment token" >&2; exit 1; }

  token_file=$(mktemp /run/minerdash-enrollment.XXXXXX)
  cleanup() {
    unset token
    shred -u -- "$token_file" 2>/dev/null || rm -f -- "$token_file"
  }
  trap cleanup EXIT
  printf '%s\n' "$token" >"$token_file"
  unset token
  chmod 0600 "$token_file"
  echo "MinerDash first boot: enrolling with the paired controller"
  /usr/local/sbin/minerdash-enroll \
    --controller "$controller" \
    --fingerprint "$fingerprint" \
    --name "$name" \
    --token-file "$token_file"
  rm -f -- "$provisioning"
  echo "Automatic MinerDash enrollment complete."
else
  echo "Unique host identity created. Use the console menu to enroll this rig."
fi
