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
# not a descendant of it, so that limit never reaches them -- a build container
# is free to use the whole node regardless of what the pod declares. This
# container's own cgroup, in contrast, is one dockerd cannot escape: it is
# assigned by the kubelet before this script ever runs, and its cpu.max already
# carries the real, enforced quota. Mirroring that exact value into a dedicated,
# per-pod cgroup (keyed by this pod's own hostname, so two environments' dind
# sidecars sharing one node's cgroup tree never collide on the same path) gives
# `docker build --cgroup-parent` (erun-common/build_cpu_cap.go) somewhere real
# to nest every build container under, so the quota is enforced hierarchically
# instead of bypassed structurally.

# Says why builds cannot be capped. The step stays non-fatal, but it must never
# pass silently: a cap that reads as applied while enforcing nothing is worse
# than a build that was openly left uncapped, because nothing downstream can
# tell the difference.
report_uncapped() {
	echo "erun-dind: WARNING: build containers are not CPU-capped: $1" >&2
}

# has_controller FILE NAME succeeds when NAME is listed in a cgroup controller
# file (cgroup.controllers, cgroup.subtree_control), where names are
# whitespace-separated and an empty file lists none.
has_controller() {
	awk -v want="$2" '
		{ for (i = 1; i <= NF; i++) if ($i == want) found = 1 }
		END { exit !found }
	' "$1" 2>/dev/null
}

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

	cap_parent="${sys_root}/fs/cgroup/docker"
	mkdir -p "${cap_parent}" 2>/dev/null || {
		report_uncapped "${cap_parent} could not be created"
		return 0
	}
	# cgroup v2 creates a controller's interface files in a child only once the
	# parent delegates that controller to its children. Until cpu is delegated,
	# the cap cgroup below has cpu.stat but no cpu.max at all, and writing it
	# fails with a misleading EACCES that looks like a capability problem.
	has_controller "${cap_parent}/cgroup.controllers" cpu || {
		report_uncapped "${cap_parent}/cgroup.controllers does not list cpu"
		return 0
	}
	if ! has_controller "${cap_parent}/cgroup.subtree_control" cpu; then
		# Additive, so whatever else the parent already delegates stays
		# delegated, and idempotent, so restarts are free. A delegation the
		# kernel refuses shows up immediately below, as a cpu.max that cannot
		# be written or does not read back.
		echo '+cpu' >"${cap_parent}/cgroup.subtree_control" 2>/dev/null || {
			report_uncapped "cpu could not be delegated via ${cap_parent}/cgroup.subtree_control"
			return 0
		}
	fi

	cap_cgroup="${cap_parent}/erun-build-cpu-cap-${pod}"
	mkdir -p "${cap_cgroup}" 2>/dev/null || {
		report_uncapped "${cap_cgroup} could not be created"
		return 0
	}
	echo "${own_cpu_max}" >"${cap_cgroup}/cpu.max" 2>/dev/null || {
		report_uncapped "${cap_cgroup}/cpu.max could not be written"
		return 0
	}
	# A write the kernel accepted is not proof the quota is in force, and this
	# function exists because an unverified cap was reported as a working one.
	# Read the value back before claiming anything.
	applied_cpu_max=$(cat "${cap_cgroup}/cpu.max" 2>/dev/null) || applied_cpu_max=''
	[ "${applied_cpu_max}" = "${own_cpu_max}" ] || {
		report_uncapped "${cap_cgroup}/cpu.max reads back '${applied_cpu_max}', not '${own_cpu_max}'"
		return 0
	}
	echo "erun-dind: capping build containers via cgroup docker/erun-build-cpu-cap-${pod} (cpu.max ${own_cpu_max})" >&2
}

# Never fatal: same reasoning as the MTU resolver above. A build that cannot
# be capped still runs exactly as it did before this existed -- but it says so.
cap_build_container_cpu || true

# Where BuildKit's cache identity lives, and why nothing here has to anchor it.
#
# Every cache record and every cache mount on this volume is attributed to the
# BuildKit worker the daemon starts, and on the docker driver that worker takes
# its id from the daemon's own engine id (moby's builder passes
# `ID: opt.EngineID`). The daemon writes that id to <data-root>/engine-id and
# reuses it on every later start, so the identity is a property of this volume,
# not of the pod: measured on a rolled environment, the file was created with
# the volume and every record in buildkit/cache.db is still keyed
# "<engine-id>::<ref>" across repeated rolls. That is what makes a roll keep
# serving the cache the volume holds.
#
# So do not anchor a worker id here. buildkit's <buildkit-root>/workerid is
# read only by standalone buildkitd's runc and containerd workers (base.ID);
# the docker driver never consults it, and writing one changes no key. An id
# that *is* lost while this volume keeps its records is the shape worth
# recovering, and the records themselves are the only place the lost id
# survives -- but reading it back means scanning cache.db, which grows far
# past what a container start may spend.

exec dockerd-entrypoint.sh "$@"
