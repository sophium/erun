#!/bin/sh

# Locks the erun-powerdns zone-bootstrap's SOA hygiene: the services zone's
# MNAME must name the primary nameserver the delegation actually serves, never
# pdnsutil's placeholder primary NS, and the correction must reconcile on every
# bootstrap rather than only when the zone is created. The distinction is the
# whole point (sophium/erun#2259): create-zone's placeholder survived a
# creation-only correction for weeks on a zone that was actively maintained,
# because an existing zone is never recreated and so never re-enters the create
# branch. The CAA/TSIG policies below it already reconciled every run; the SOA
# did not, and nothing in the render said so.
#
# Lives beside the chart rather than inside it, like the other k8s chart tests:
# helm renders every file under templates/, and `make helm-chart-tests` picks up
# any erun-devops/k8s/*_test.sh with no Makefile edit.

set -eu

script_dir="$(cd "$(dirname "$0")" && pwd)"
chart_dir="${script_dir}/erun-powerdns"

command -v helm >/dev/null 2>&1 || {
    echo "FAIL: helm is required to render the chart" >&2
    exit 1
}

work_root="$(mktemp -d 2>/dev/null || mktemp -d -t erun-powerdns-chart-test)"
trap 'rm -rf "${work_root}"' EXIT INT TERM

fail() {
    echo "FAIL: $1" >&2
    exit 1
}

zone="services.erunpaas.com"

render() {
    out="$1"
    shift
    helm template test "${chart_dir}" \
        --set tenant=erun \
        --set-string "platform.servicesZone=${zone}" \
        "$@" >"${out}" || fail "helm template failed"
}

# The zone-bootstrap initContainer's shell script, so an assertion about
# ordering cannot accidentally match a line belonging to another initContainer
# or to the pdns_server container.
bootstrap_script() {
    awk '/- name: zone-bootstrap/ { in_block = 1 } in_block && /^ *volumeMounts:$/ { exit } in_block { print }' "$1" >"$2"
    [ -s "$2" ] || fail "the rendered chart must contain a zone-bootstrap initContainer"
}

# Line number of the first line matching the given ERE, optionally only after a
# given line number (0 = from the top of the file).
line_of() {
    awk -v pat="$1" -v after="${2:-0}" 'NR > after && $0 ~ pat { print NR; exit }' "$3"
}

# --- 1. The bootstrap rewrites the SOA MNAME to the primary nameserver ---
rendered="${work_root}/render.yaml"
render "${rendered}" \
    --set-string 'platform.nameservers[0]=ns1.erunpaas.com' \
    --set-string 'platform.nameservers[1]=ns2.erunpaas.com'
script="${work_root}/bootstrap.sh"
bootstrap_script "${rendered}" "${script}"

grep -q "replace-rrset \"${zone}\" @ SOA" "${script}" ||
    fail "the zone-bootstrap must set the services zone's SOA"

grep -q "ns1\.erunpaas\.com\. hostmaster\.${zone}\." "${script}" ||
    fail "the SOA MNAME must name the primary nameserver (first of platform.nameservers)"

# --- 2. pdnsutil's placeholder primary NS never reaches a rendered manifest ---
# The literal is asserted absent rather than merely unused, so the placeholder
# cannot survive anywhere in the deploy path -- not as an MNAME, not in a
# command that a future edit reintroduces.
grep -q 'a\.misconfigured\.dns\.server\.invalid' "${rendered}" &&
    fail "pdnsutil's placeholder SOA MNAME must not appear in any rendered manifest"

# --- 3. The MNAME correction runs on every bootstrap, not only at creation ---
# The guard that skips an existing zone closes at the first `fi` after
# create-zone; the SOA rewrite must sit after it, or an existing zone keeps the
# placeholder forever (erun#2259).
create_line=$(line_of 'create-zone' 0 "${script}")
[ -n "${create_line}" ] || fail "the bootstrap must create the zone when it is missing"
guard_fi=$(line_of '^ *fi$' "${create_line}" "${script}")
[ -n "${guard_fi}" ] || fail "the create-only guard must close with fi"
soa_line=$(line_of 'replace-rrset .* @ SOA' 0 "${script}")
[ -n "${soa_line}" ] || fail "the bootstrap must rewrite the SOA"
[ "${guard_fi}" -lt "${soa_line}" ] ||
    fail "the SOA MNAME rewrite must sit OUTSIDE the create-only guard (guard closes at line ${guard_fi}, rewrite at ${soa_line}) so existing zones are reconciled too"

# --- 4. The correction is conditional, so an already-correct SOA is untouched ---
grep -q 'mname' "${script}" ||
    fail "the bootstrap must read the zone's current MNAME before rewriting"
grep -q '\[ "\$mname" != "ns1\.erunpaas\.com\." \]' "${script}" ||
    fail "the SOA rewrite must be gated on the MNAME differing from the primary nameserver"

# --- 5. The serial moves forward rather than back ---
grep -q 'serial=\$((serial + 1))' "${script}" ||
    fail "rewriting the SOA must move its serial forward, not reset it"

# --- 6. The remaining nameservers are still added as NS records ---
grep -q "add-record \"${zone}\" @ NS \"ns2\.erunpaas\.com\"" "${script}" ||
    fail "nameservers after the first must be added as NS records"

# --- 7. With no configured nameservers there is no name to publish, so no SOA ---
bare="${work_root}/bare.yaml"
render "${bare}"
bare_script="${work_root}/bare-bootstrap.sh"
bootstrap_script "${bare}" "${bare_script}"
grep -q 'replace-rrset .* @ SOA' "${bare_script}" &&
    fail "the bootstrap must not rewrite the SOA when no nameserver is configured"

echo "PASS: erun-powerdns chart zone-bootstrap SOA MNAME"
