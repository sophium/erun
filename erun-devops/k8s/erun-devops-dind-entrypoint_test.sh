#!/bin/sh

# Tests for the dind sidecar's entrypoint wrapper: it derives the daemon's
# bridge MTU from the interface carrying the pod's default route, passes it to
# the stock docker:dind entrypoint ahead of whatever args the chart supplied,
# and degrades to the daemon's own default whenever it cannot read a number it
# trusts.
#
# Drives the real script with a stubbed procfs/sysfs and a stubbed
# dockerd-entrypoint.sh, so what is asserted is the argv dockerd would actually
# receive rather than a re-implementation of the resolver.

set -eu

script_dir="$(cd "$(dirname "$0")" && pwd)"
entrypoint="${script_dir}/erun-devops/files/dind-entrypoint.sh"

[ -r "${entrypoint}" ] || {
    echo "FAIL: ${entrypoint} is missing" >&2
    exit 1
}

work_root="$(mktemp -d 2>/dev/null || mktemp -d -t erun-dind-entrypoint-test)"
trap 'rm -rf "${work_root}"' EXIT INT TERM

fail() {
    echo "FAIL: $1" >&2
    exit 1
}

# stub_net writes a procfs routing table whose default route is on $1 and a
# sysfs MTU of $2 for it, plus a non-default route on a decoy interface with a
# different MTU so a resolver that simply took the first row would be caught.
stub_net() {
    iface="$1"
    mtu="$2"
    root="${work_root}/root"
    rm -rf "${root}"
    mkdir -p "${root}/proc/net" "${root}/sys/class/net/${iface}" "${root}/sys/class/net/decoy0"
    {
        printf 'Iface\tDestination\tGateway \tFlags\tRefCnt\tUse\tMetric\tMask\t\tMTU\tWindow\tIRTT\n'
        printf 'decoy0\t000011AC\t00000000\t0001\t0\t0\t0\t0000FFFF\t0\t0\t0\n'
        printf '%s\t00000000\t0128000A\t0003\t0\t0\t0\t00000000\t0\t0\t0\n' "${iface}"
    } >"${root}/proc/net/route"
    printf '%s\n' "${mtu}" >"${root}/sys/class/net/${iface}/mtu"
    printf '9000\n' >"${root}/sys/class/net/decoy0/mtu"
    printf '%s' "${root}"
}

# stub_own_cgroup writes a fake /proc/self/cgroup naming a cgroup path under
# the given root's sysfs, and a cpu.max file at that path with the given
# content — simulating what the kubelet has already assigned this container
# before the entrypoint ever runs.
stub_own_cgroup() {
    root="$1"
    cpu_max="$2"
    cgroup_path="/kubepods.slice/cri-containerd-test.scope"
    mkdir -p "${root}/proc/self"
    printf '0::%s\n' "${cgroup_path}" >"${root}/proc/self/cgroup"
    mkdir -p "${root}/sys/fs/cgroup${cgroup_path}"
    printf '%s\n' "${cpu_max}" >"${root}/sys/fs/cgroup${cgroup_path}/cpu.max"
}

# run_entrypoint executes the wrapper against a stubbed root with a
# dockerd-entrypoint.sh that records its argv instead of starting a daemon.
run_entrypoint() {
    root="$1"
    shift
    bin="${work_root}/bin"
    argv="${work_root}/argv"
    rm -rf "${bin}"
    mkdir -p "${bin}"
    rm -f "${argv}"
    cat >"${bin}/dockerd-entrypoint.sh" <<EOF
#!/bin/sh
for a in "\$@"; do printf '%s\n' "\$a"; done >"${argv}"
EOF
    chmod 0755 "${bin}/dockerd-entrypoint.sh"
    PATH="${bin}:${PATH}" \
        ERUN_DIND_PROC_ROOT="${root}/proc" \
        ERUN_DIND_SYS_ROOT="${root}/sys" \
        sh "${entrypoint}" "$@" >"${work_root}/stdout" 2>"${work_root}/stderr" ||
        fail "the entrypoint exited non-zero"
    cat "${argv}" 2>/dev/null || true
}

# --- 1. The MTU of the default route's interface is passed to dockerd, and
# the decoy interface's larger MTU is not. ---
root="$(stub_net eth0 1450)"
argv="$(run_entrypoint "${root}")"
[ "${argv}" = "--mtu=1450" ] ||
    fail "expected dockerd to receive --mtu=1450 from the default route's interface, got: ${argv}"

# --- 2. The chart's own args survive, and the derived MTU leads so the stock
# entrypoint still sees a leading '-' and prepends dockerd. ---
root="$(stub_net eth0 1450)"
argv="$(run_entrypoint "${root}" --insecure-registry 10.1.2.3:5000)"
expected="--mtu=1450
--insecure-registry
10.1.2.3:5000"
[ "${argv}" = "${expected}" ] ||
    fail "expected the derived MTU to lead the chart's own args, got: ${argv}"

# --- 3. A jumbo-frame pod network is honoured rather than clamped down: the
# point is to match the path, not to shrink it. ---
root="$(stub_net eth0 8951)"
argv="$(run_entrypoint "${root}")"
[ "${argv}" = "--mtu=8951" ] || fail "expected --mtu=8951 on a jumbo-frame network, got: ${argv}"

# --- 4. An interface name that is not eth0 still resolves; nothing may
# hardcode the conventional name. ---
root="$(stub_net ens5 1400)"
argv="$(run_entrypoint "${root}")"
[ "${argv}" = "--mtu=1400" ] || fail "expected --mtu=1400 on a non-eth0 interface, got: ${argv}"

# --- 5. No default route: the daemon keeps its own default rather than being
# handed a guess, and the chart's args still reach it. ---
root="${work_root}/norootroute"
rm -rf "${root}"
mkdir -p "${root}/proc/net" "${root}/sys/class/net"
printf 'Iface\tDestination\tGateway \tFlags\tRefCnt\tUse\tMetric\tMask\n' >"${root}/proc/net/route"
argv="$(run_entrypoint "${root}" --insecure-registry 10.1.2.3:5000)"
expected="--insecure-registry
10.1.2.3:5000"
[ "${argv}" = "${expected}" ] ||
    fail "with no default route dockerd should keep its default MTU and still get the chart's args, got: ${argv}"
grep -q 'could not read the pod network MTU' "${work_root}/stderr" ||
    fail "an unresolvable MTU should say so on stderr rather than passing silently"

# --- 6. An unreadable procfs is not fatal. A daemon that refuses to start is
# strictly worse than one bridging at the default. ---
root="${work_root}/empty"
rm -rf "${root}"
mkdir -p "${root}/proc" "${root}/sys"
argv="$(run_entrypoint "${root}")"
[ -z "${argv}" ] || fail "an unreadable procfs should add no dockerd args, got: ${argv}"

# --- 7. A garbage or implausible MTU is rejected rather than passed through:
# handing dockerd a nonsense --mtu breaks every container it bridges. ---
for bogus in "" "not-a-number" "0" "68" "1279"; do
    root="$(stub_net eth0 "${bogus}")"
    argv="$(run_entrypoint "${root}")"
    [ -z "${argv}" ] ||
        fail "an implausible MTU (${bogus}) should be ignored, got: ${argv}"
done

# --- 8. Exactly the IPv6 minimum is accepted; the floor is a sanity bound, not
# an exclusion of small-but-real links. ---
root="$(stub_net eth0 1280)"
argv="$(run_entrypoint "${root}")"
[ "${argv}" = "--mtu=1280" ] || fail "expected --mtu=1280 to be accepted, got: ${argv}"

# --- 9. erun#2255: this container's own real cpu.max quota is mirrored into a
# dedicated, per-pod cgroup that `docker build --cgroup-parent` can nest build
# containers under. ---
root="$(stub_net eth0 1450)"
stub_own_cgroup "${root}" "400000 100000"
run_entrypoint "${root}" >/dev/null
pod="$(hostname)"
cap_cgroup="${root}/sys/fs/cgroup/docker/erun-build-cpu-cap-${pod}"
[ "$(cat "${cap_cgroup}/cpu.max" 2>/dev/null)" = "400000 100000" ] ||
    fail "expected the sidecar's own cpu.max to be mirrored into ${cap_cgroup}/cpu.max"
grep -q "erun-build-cpu-cap-${pod}" "${work_root}/stderr" ||
    fail "expected the capped cgroup path to be announced on stderr"

# --- 10. An unlimited own cgroup (no Kubernetes CPU limit declared) mirrors
# nothing rather than fabricating a cap that was never asked for. ---
root="$(stub_net eth0 1450)"
stub_own_cgroup "${root}" "max 100000"
run_entrypoint "${root}" >/dev/null
[ -d "${root}/sys/fs/cgroup/docker/erun-build-cpu-cap-$(hostname)" ] &&
    fail "expected no cap cgroup when this container's own cpu.max is unlimited"

# --- 11. A missing /proc/self/cgroup (the same unreadable-procfs case as
# scenario 6) is non-fatal and creates no cap cgroup either. ---
root="${work_root}/nocgroup"
rm -rf "${root}"
mkdir -p "${root}/proc" "${root}/sys"
run_entrypoint "${root}" >/dev/null
[ -d "${root}/sys/fs/cgroup/docker" ] &&
    fail "expected no docker/ cgroup tree to be created with no readable own cgroup"

echo "ok: erun-devops dind entrypoint tests passed"
