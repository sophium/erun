#!/bin/sh
# Starts the sidecar's dockerd with a bridge MTU the pod's network can carry.
# docker defaults to 1500; a CNI that encapsulates hands the pod less (a VXLAN
# overlay costs 50 bytes), and the difference silently black-holes every
# full-size packet a build fetches. The sidecar shares the pod's network
# namespace, so the real number is readable right here.
set -eu

proc_root="${ERUN_DIND_PROC_ROOT:-/proc}"
sys_root="${ERUN_DIND_SYS_ROOT:-/sys}"

# Reads procfs/sysfs rather than iproute2, which this image has no reason to
# carry: in /proc/net/route an all-zero destination and mask is the default
# route, and field 1 is the interface carrying it.
resolve_pod_mtu() {
	route_table="${proc_root}/net/route"
	[ -r "${route_table}" ] || return 1
	iface=$(awk '$2 == "00000000" && $8 == "00000000" { print $1; exit }' "${route_table}")
	[ -n "${iface}" ] || return 1

	mtu_file="${sys_root}/class/net/${iface}/mtu"
	[ -r "${mtu_file}" ] || return 1
	mtu=$(cat "${mtu_file}")
	case "${mtu}" in
	'' | *[!0-9]*) return 1 ;;
	esac
	# Nothing below the IPv6 minimum is a real link MTU; treat it as a bad read
	# rather than crippling every build on it.
	[ "${mtu}" -ge 1280 ] || return 1

	printf '%s\n' "${mtu}"
}

if mtu=$(resolve_pod_mtu); then
	echo "erun-dind: bridging containers at the pod network's MTU ${mtu}" >&2
	set -- --mtu="${mtu}" "$@"
else
	# Never fatal: an unreadable MTU leaves the daemon exactly as it behaved
	# before this wrapper existed, which is correct wherever 1500 already fits.
	echo "erun-dind: could not read the pod network MTU; leaving dockerd on its default" >&2
fi

# Kubernetes declares this sidecar's own CPU limit, but every container a real
# `docker build` creates lands as a *sibling* of this container's own cgroup,
# not a descendant of it, so that limit never reaches them (erun#2255) — a
# build container is free to use the whole node regardless of what the pod
# declares. This container's own cgroup, in contrast, is one dockerd cannot
# escape: it is assigned by the kubelet before this script ever runs, and its
# cpu.max already carries the real, enforced quota. Mirroring that exact value
# into a dedicated, per-pod cgroup (keyed by this pod's own hostname, so two
# environments' dind sidecars sharing one node's cgroup tree never collide on
# the same path) gives `docker build --cgroup-parent` (erun-common/
# build_cpu_cap.go) somewhere real to nest every build container under, so the
# quota is enforced hierarchically instead of bypassed structurally.
cap_build_container_cpu() {
	own_cgroup=$(awk -F: '$1 == "0" { print $3 }' "${proc_root}/self/cgroup" 2>/dev/null)
	[ -n "${own_cgroup}" ] || return 0
	own_cpu_max_file="${sys_root}/fs/cgroup${own_cgroup}/cpu.max"
	[ -r "${own_cpu_max_file}" ] || return 0
	own_cpu_max=$(cat "${own_cpu_max_file}" 2>/dev/null)
	case "${own_cpu_max}" in
	'max '* | '') return 0 ;; # unlimited or unreadable: nothing to mirror
	esac

	pod=$(hostname 2>/dev/null)
	[ -n "${pod}" ] || return 0
	cap_cgroup="${sys_root}/fs/cgroup/docker/erun-build-cpu-cap-${pod}"
	mkdir -p "${cap_cgroup}" 2>/dev/null || return 0
	echo "${own_cpu_max}" >"${cap_cgroup}/cpu.max" 2>/dev/null &&
		echo "erun-dind: capping build containers via cgroup docker/erun-build-cpu-cap-${pod} (cpu.max ${own_cpu_max})" >&2
}

# Never fatal: same reasoning as the MTU resolver above. A build that cannot
# be capped still runs exactly as it did before this existed.
cap_build_container_cpu || true

exec dockerd-entrypoint.sh "$@"
