#!/usr/bin/env bash
set -Eeuo pipefail
IFS=$'\n\t'

echo "== Operating system =="
if [[ -r /etc/os-release ]]; then
  grep -E '^(ID|ID_LIKE|VERSION_ID|PRETTY_NAME)=' /etc/os-release || true
fi
printf 'architecture=%s\n' "$(uname -m)"
printf 'kernel=%s\n' "$(uname -r)"
printf 'systemd=%s\n' "$(systemctl --version 2>/dev/null | head -1 || echo unavailable)"

echo
echo "== Root filesystem =="
root_source=$(findmnt -nro SOURCE /)
root_type=$(findmnt -nro FSTYPE /)
root_options=$(findmnt -nro OPTIONS /)
printf 'source=%s\nfilesystem=%s\noptions=%s\n' "$root_source" "$root_type" "$root_options"
df -hT /

echo
echo "== Block devices =="
lsblk -e7 -o NAME,PATH,TYPE,SIZE,FSTYPE,MOUNTPOINTS,RM,RO,PKNAME

echo
echo "== Expansion tools =="
for command in growpart resize2fs xfs_growfs btrfs partprobe; do
  if command -v "$command" >/dev/null 2>&1; then
    printf '%-12s %s\n' "$command" "$(command -v "$command")"
  else
    printf '%-12s missing\n' "$command"
  fi
done

echo
echo "== Mining-related services =="
systemctl list-unit-files --type=service --no-legend 2>/dev/null |
  grep -Ei 'min(er|ing)|watchdog|rig' || echo "No matching service units found"

echo
echo "== Mining-related processes =="
ps -eo pid,user,comm,args --sort=comm |
  grep -Ei '[m]iner|[x]mrig|[r]igel|[w]ildrig|[s]rbminer' || echo "No matching processes found"

echo
echo "Inspection only: no files, partitions, services, or processes were changed."
