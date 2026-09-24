#!/usr/bin/env bash
set -Eeuo pipefail
IFS=$'\n\t'
umask 077

die() { echo "$*" >&2; exit 1; }
usage() {
  echo "usage: $0 CONTROLLER_IP CONTROLLER_PORT MANAGEMENT_CIDR POOL_CIDRS POOL_PORTS DNS_IP NTP_IP [--apply]" >&2
  echo "lists use commas, e.g. 192.0.2.10/32,192.0.2.11/32 and 3333,4444" >&2
  exit 2
}
[[ ${EUID:-$(id -u)} -eq 0 ]] || die "run as root"
[[ $# -eq 7 || ($# -eq 8 && $8 == --apply) ]] || usage
controller=$1; port=$2; management=$3; pools=$4; pool_ports=$5; dns=$6; ntp=$7
ipv4='([0-9]{1,3}\.){3}[0-9]{1,3}'
valid_ipv4() {
  local address=$1 octet
  [[ "$address" =~ ^$ipv4$ ]] || return 1
  for octet in ${address//./ }; do ((10#$octet <= 255)) || return 1; done
}
valid_cidr() {
  local address=${1%/*} prefix=${1##*/}
  valid_ipv4 "$address" && [[ "$prefix" =~ ^([0-9]|[12][0-9]|3[0-2])$ ]]
}
valid_ipv4 "$controller" && valid_ipv4 "$dns" && valid_ipv4 "$ntp" || die "invalid IPv4 address"
valid_cidr "$management" || die "invalid management CIDR"
IFS=',' read -r -a requested_cidrs <<<"$pools"
for requested_cidr in "${requested_cidrs[@]}"; do valid_cidr "$requested_cidr" || die "invalid pool CIDR list"; done
[[ "$port" =~ ^[0-9]{1,5}$ && "$pool_ports" =~ ^[0-9]{1,5}(,[0-9]{1,5})*$ ]] || die "invalid port list"
((10#$port >= 1 && 10#$port <= 65535)) || die "controller port out of range"
IFS=',' read -r -a requested_ports <<<"$pool_ports"
for requested_port in "${requested_ports[@]}"; do
  ((10#$requested_port >= 1 && 10#$requested_port <= 65535)) || die "pool port out of range"
done

root=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd -P)
template="$root/deploy/linux/nftables-minerdash.conf.template"
output=/etc/nftables-minerdash.conf
pool_set=${pools//,/,\ }
port_set=${pool_ports//,/,\ }
sed -e "s|@CONTROLLER_IPV4@|$controller|g" -e "s|@CONTROLLER_PORT@|$port|g" \
  -e "s|@MANAGEMENT_CIDR@|$management|g" -e "s|@POOL_CIDRS@|$pool_set|g" \
  -e "s|@POOL_PORTS@|{ $port_set }|g" -e "s|@DNS_IPV4@|$dns|g" \
  -e "s|@NTP_IPV4@|$ntp|g" "$template" >"$output.new"
nft -c -f "$output.new" || { rm -f -- "$output.new"; die "generated nftables policy is invalid"; }
install -o root -g root -m 0600 "$output.new" "$output"
rm -f -- "$output.new"
if [[ ${8:-} == --apply ]]; then
  nft -f "$output"
  echo "Policy applied. Persist it explicitly after confirming management access."
else
  echo "Validated policy written to $output but not applied. Review it from a console."
fi
