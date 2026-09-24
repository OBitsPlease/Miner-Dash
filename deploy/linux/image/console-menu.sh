#!/usr/bin/env bash
set -u

show_network() {
  clear
  echo "Miner Dash network information"
  echo
  hostnamectl --static 2>/dev/null || hostname
  ip -brief address show scope global 2>/dev/null || true
  echo
  echo "The controller PC and this rig must be reachable on the same network."
  read -r -p "Press Enter to return..."
}

install_to_disk() {
  clear
  echo "Install Miner Dash to an internal drive"
  echo
  echo "WARNING: The selected drive will be completely erased."
  echo
  lsblk -dnpo NAME,SIZE,MODEL,RM,TYPE |
    awk '$5 == "disk" {printf "%-16s %-10s removable=%s  %s\n", $1, $2, $4, substr($0, index($0,$3))}'
  echo
  read -r -p "Enter the whole internal drive (example /dev/sda), or leave blank to cancel: " target
  [[ -n "$target" ]] || return
  read -r -p "Type ERASE-$target to confirm: " confirmation
  sudo /usr/local/sbin/minerdash-install-to-disk --target "$target" --confirm "$confirmation"
  echo
  read -r -p "Press Enter to return..."
}

while true; do
  clear
  cat <<'EOF'
============================================================
                    MINER DASH RIG OS
============================================================

This USB already contains Linux and the Miner Dash rig agent.

  1) Enroll this rig and run Miner Dash from this USB
  2) Install Miner Dash to an internal drive
  3) Show network information
  4) Open advanced Linux shell
  5) Reboot
  6) Shut down

EOF
  read -r -p "Choose an option [1-6]: " choice
  case "$choice" in
    1)
      sudo /usr/local/sbin/minerdash-enroll-console
      echo
      read -r -p "Press Enter to return..."
      ;;
    2) install_to_disk ;;
    3) show_network ;;
    4)
      echo "Type 'exit' to return to the Miner Dash menu."
      /bin/bash
      ;;
    5) sudo /usr/bin/systemctl reboot ;;
    6) sudo /usr/bin/systemctl poweroff ;;
    *) sleep 1 ;;
  esac
done
