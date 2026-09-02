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

exec dockerd-entrypoint.sh "$@"
