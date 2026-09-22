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
# Two layers, because either alone is weak: the rendered text is asserted
# directly (so a regression is caught without executing anything), and then the
# rendered script is EXECUTED against a stub pdnsutil (so the reconcile is
# proven to actually repair an existing zone, bump the serial, and leave a
# correct zone alone -- a render-only test would pass on a script that never
# works).
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
placeholder="a.misconfigured.dns.server.invalid."

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
# or to the pdns_server container. The YAML preamble is stripped: what lands in
# $2 is the shell the container actually runs.
bootstrap_script() {
    awk '/- name: zone-bootstrap/ { in_block = 1 }
         in_block && /^ *- \|$/ { in_shell = 1; next }
         in_block && /^ *volumeMounts:$/ { exit }
         in_shell { print }' "$1" >"$2"
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
grep -q "${placeholder}" "${rendered}" &&
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
# The guard must compare dot-insensitively. pdnsutil prints the SOA MNAME
# relative to the zone's $ORIGIN, i.e. WITHOUT the trailing dot the configured
# nameserver carries, so a raw field comparison never matches a real zone: the
# SOA is rewritten -- and its serial bumped -- on every bootstrap, while an
# idempotency assertion run only against a verbatim-round-tripping stub still
# passes. Both sides are stripped.
grep -qF '[ "${mname%.}" != "${want_mname%.}" ]' "${script}" ||
    fail "the SOA rewrite must compare the MNAME with its trailing dot stripped on both sides"
grep -qF 'want_mname="ns1.erunpaas.com"' "${script}" ||
    fail "the expected MNAME must come from the primary platform.nameservers entry"

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

# --- 8. Executed against a stub pdnsutil: the reconcile really repairs zones ---
# The rendered script above is run for real. The stub keeps the zone in a file
# and records every mutating call, so the assertions below are about what the
# bootstrap DID, not about what its text looks like.
stub_bin="${work_root}/bin"
mkdir -p "${stub_bin}"
export FAKE_STATE="${work_root}/zone-state"
export FAKE_LOG="${work_root}/calls.log"
: >"${FAKE_LOG}"

cat >"${stub_bin}/pdnsutil" <<'STUB'
#!/bin/sh
# Stub pdnsutil: state lives in $FAKE_STATE, every mutating call in $FAKE_LOG.
# Only the gpgsql backend behavior the zone-bootstrap depends on is modeled.
shift                 # --config-dir=<dir>
cmd="$1"
shift
case "${cmd}" in
list-zone)
    [ -f "${FAKE_STATE}" ] || exit 1
    # Real layout: a literal $ORIGIN header, then records as
    # name, TTL, class, type, then the 7-field SOA RDATA.
    printf '$ORIGIN .\n'
    printf '%s\t3600\tIN\tSOA\t%s\n' "$1" "$(cat "${FAKE_STATE}")"
    printf '%s\t3600\tIN\tNS\tns1.erunpaas.com.\n' "$1"
    ;;
create-zone)
    printf '%s\n' "${FAKE_PLACEHOLDER}" >"${FAKE_STATE}"
    printf 'create-zone %s\n' "$1" >>"${FAKE_LOG}"
    ;;
add-record)
    printf 'add-record %s\n' "$*" >>"${FAKE_LOG}"
    ;;
replace-rrset)
    printf 'replace-rrset %s %s %s %s\n' "$1" "$3" "$4" "$5" >>"${FAKE_LOG}"
    case "$3" in
    SOA)
        # Real pdnsutil prints the SOA MNAME/RNAME relative to the zone's
        # $ORIGIN, i.e. with their trailing dots stripped. Model that here; a
        # stub that round-trips the content verbatim hides the difference and
        # passes a reconcile that churns on a real server.
        printf '%s\n' "$5" | awk '{ sub(/\.$/, "", $1); sub(/\.$/, "", $2); print }' >"${FAKE_STATE}"
        ;;
    esac
    ;;
import-tsig-key | set-meta)
    printf '%s %s\n' "${cmd}" "$*" >>"${FAKE_LOG}"
    ;;
*)
    exit 0
    ;;
esac
STUB
chmod +x "${stub_bin}/pdnsutil"

# A zone created by the pre-#2259 bootstrap: placeholder MNAME, live serial.
# Undotted, exactly as pdnsutil prints it (it strips the trailing dot).
FAKE_PLACEHOLDER="${placeholder%.} hostmaster.${zone} 2026090202 10800 3600 604800 3600"
export FAKE_PLACEHOLDER
printf '%s\n' "${FAKE_PLACEHOLDER}" >"${FAKE_STATE}"

PATH="${stub_bin}:${PATH}" sh "${script}" ||
    fail "the zone-bootstrap script must run to completion"

repaired=$(cat "${FAKE_STATE}")
[ "${repaired}" = "ns1.erunpaas.com hostmaster.${zone} 2026090203 10800 3600 604800 3600" ] ||
    fail "an EXISTING zone must be repaired in place: expected the real MNAME at serial 2026090203, got '${repaired}'"

# Idempotent: a second bootstrap over the repaired zone must change nothing.
: >"${FAKE_LOG}"
PATH="${stub_bin}:${PATH}" sh "${script}" ||
    fail "a second bootstrap run must succeed"
grep -q '^replace-rrset' "${FAKE_LOG}" &&
    fail "a second bootstrap must not rewrite an already-correct SOA"
[ "$(cat "${FAKE_STATE}")" = "${repaired}" ] ||
    fail "a second bootstrap must leave an already-correct SOA byte-identical"

# A fresh zone (no create-time SOA correction anywhere) must also come out
# correct, so the reconcile covers the create path it replaced.
rm -f "${FAKE_STATE}"
: >"${FAKE_LOG}"
PATH="${stub_bin}:${PATH}" sh "${script}" ||
    fail "bootstrapping a missing zone must run to completion"
fresh=$(cat "${FAKE_STATE}")
case "${fresh}" in
ns1.erunpaas.com\ hostmaster."${zone}"\ *) : ;;
*) fail "a freshly created zone must come out with the real MNAME, got '${fresh}'" ;;
esac

echo "PASS: erun-powerdns chart zone-bootstrap SOA MNAME"
