#!/usr/bin/env bash
set -uo pipefail

mode="${1:-randomx}"
target_huge_pages=2048
state_dir="/var/lib/minerdash/cpu-setup"
baseline_file="$state_dir/original-hugepages"
log() {
    printf 'minerdash-cpu-setup: %s\n' "$*" >&2
}

if [[ "$mode" != "randomx" && "$mode" != "standard" ]]; then
    log "unsupported setup mode: $mode"
    exit 2
fi

kernel_release="$(uname -r)"
if [[ "$mode" == "randomx" ]]; then
    module_path="/lib/modules/$kernel_release/kernel/arch/x86/kernel/msr.ko"
    if [[ -f "$module_path" ]]; then
        if modprobe msr; then
            log "loaded the MSR driver matching kernel $kernel_release"
        else
            log "could not load the MSR driver for kernel $kernel_release"
        fi
    else
        log "no MSR driver is installed for kernel $kernel_release"
    fi
fi

if [[ ! -w /proc/sys/vm/nr_hugepages ]]; then
    log "huge-page reservation is unavailable; leaving the existing setting unchanged"
    exit 0
fi

install -d -m 0700 "$state_dir"
current_huge_pages="$(< /proc/sys/vm/nr_hugepages)"
if [[ ! "$current_huge_pages" =~ ^[0-9]+$ ]]; then
    log "could not read the current huge-page reservation"
    exit 0
fi

if [[ ! -s "$baseline_file" ]]; then
    printf '%s\n' "$current_huge_pages" > "$baseline_file"
fi
baseline_huge_pages="$(< "$baseline_file")"
if [[ ! "$baseline_huge_pages" =~ ^[0-9]+$ ]]; then
    log "saved huge-page baseline is invalid; leaving the existing setting unchanged"
    exit 0
fi

if [[ "$mode" == "standard" ]]; then
    target_huge_pages="$baseline_huge_pages"
fi

if ((current_huge_pages != target_huge_pages)); then
    if ! printf '%s\n' "$target_huge_pages" > /proc/sys/vm/nr_hugepages; then
        log "could not set huge pages to $target_huge_pages; leaving mining available"
        exit 0
    fi
fi

actual_huge_pages="$(< /proc/sys/vm/nr_hugepages)"
if [[ "$actual_huge_pages" =~ ^[0-9]+$ ]] && ((actual_huge_pages != target_huge_pages)); then
    log "requested $target_huge_pages huge pages but the kernel set $actual_huge_pages"
elif [[ "$mode" == "randomx" ]]; then
    log "reserved $actual_huge_pages huge pages of 2 MiB for RandomX workloads"
else
    log "restored the standard huge-page pool to $actual_huge_pages pages"
fi
