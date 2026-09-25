#!/bin/sh

# Tests for terraform-module-tests.sh's init retry: a transient registry failure
# is ridden out and the module still runs, while a failure that is not transient
# still reds the gate on its first attempt and pays no backoff.
#
# A stub `terraform` on PATH stands in for the real CLI, so every case is
# deterministic and none of them touches registry.terraform.io: the failure
# texts below are captured verbatim from real `terraform init` runs (an
# unreachable host for the transient ones, an unsatisfiable version constraint
# for the permanent one), including terraform's 80-column hard wrap, which is
# what splits "failed to request / discovery document" across two lines.
#
# Run directly (not wired into `make check`, same reasoning as
# scripts/agent-gate_test.sh and erun-devops/docker/erun-devops/entrypoint_test.sh):
# the stub asserts the retry's control flow, not terraform's own behaviour.

set -eu

script_dir="$(cd "$(dirname "$0")" && pwd)"
subject="${script_dir}/terraform-module-tests.sh"
modules_root="$(cd "${script_dir}/../erun-devops/terraform-erun/modules" && pwd)"

work_root="$(mktemp -d 2>/dev/null || mktemp -d -t terraform-module-tests-test)"
trap 'rm -rf "${work_root}"' EXIT INT TERM

fail() {
    echo "FAIL: $1" >&2
    exit 1
}

# The module every failing case is aimed at: the first one the subject's own
# scan reaches that carries a suite, resolved the same way the subject resolves
# it, so renaming or reordering the module tree cannot silently turn these cases
# into assertions about a module the scan skips.
module=""
for candidate in "${modules_root}"/*/; do
    [ -d "${candidate}" ] || continue
    set -- "${candidate}"tests/*.tftest.hcl
    if [ -f "$1" ]; then
        module="$(basename "${candidate}")"
        break
    fi
done
[ -n "${module}" ] || fail "no module under ${modules_root} carries a tests/*.tftest.hcl suite"

# Captured from a real `terraform init` whose provider host does not answer:
# the shape the gate was reddened by, wrap and all.
TRANSIENT_TEXT='Error: Failed to query available provider packages

Could not retrieve the list of available versions for provider
hashicorp/null: could not connect to registry.terraform.io: failed to request
discovery document: Get
"https://registry.terraform.io/.well-known/terraform.json": context deadline
exceeded'

# Captured from a real `terraform init` against a constraint no release
# satisfies. It carries the transient capture's outer "Could not retrieve the
# list of available versions for provider" wording word for word, which is why
# the classifier may not match on the outer message: a module asking for a
# provider version that does not exist must fail on its first attempt.
PERMANENT_TEXT='Error: Failed to query available provider packages

Could not retrieve the list of available versions for provider
hashicorp/null: no available releases match the given constraints >= 99.99.99'

# stub_terraform writes a fake `terraform` on PATH that records every
# invocation and fails the first TFSTUB_FAIL_INIT_TIMES `init` calls made in
# the directory named by TFSTUB_FAILING_MODULE, printing TFSTUB_FAIL_TEXT.
# Every other call succeeds, so the subject's own loop, module discovery, and
# aggregate failure report all run for real.
stub_terraform() {
    bin_dir="$1"
    mkdir -p "${bin_dir}"
    cat >"${bin_dir}/terraform" <<'EOF'
#!/bin/sh
printf '%s\t%s\n' "$(basename "${PWD}")" "$*" >>"${TFSTUB_CALLS}"
[ "${1:-}" = "init" ] || exit 0
[ "$(basename "${PWD}")" = "${TFSTUB_FAILING_MODULE}" ] || exit 0
count_file="${TFSTUB_STATE}/$(basename "${PWD}").count"
count=0
[ -f "${count_file}" ] && count="$(cat "${count_file}")"
count=$((count + 1))
printf '%s' "${count}" >"${count_file}"
if [ "${count}" -le "${TFSTUB_FAIL_INIT_TIMES}" ]; then
    printf '%s\n' "${TFSTUB_FAIL_TEXT}" >&2
    exit 1
fi
exit 0
EOF
    chmod +x "${bin_dir}/terraform"
}

# run_subject runs the subject once under a fresh stub with the given failure
# configuration, capturing stdout, stderr, its exit status, and how many times
# each module's `init` was invoked. Sets RUN_STATUS, RUN_STDOUT, RUN_STDERR,
# RUN_INITS, and RUN_ELAPSED.
run_subject() {
    case_name="$1"
    fail_times="$2"
    fail_text="$3"

    case_root="${work_root}/${case_name}"
    mkdir -p "${case_root}/bin" "${case_root}/state"
    stub_terraform "${case_root}/bin"

    calls_file="${case_root}/calls"
    : >"${calls_file}"

    started="$(date +%s)"
    set +e
    TFSTUB_CALLS="${calls_file}" \
        TFSTUB_STATE="${case_root}/state" \
        TFSTUB_FAILING_MODULE="${module}" \
        TFSTUB_FAIL_INIT_TIMES="${fail_times}" \
        TFSTUB_FAIL_TEXT="${fail_text}" \
        PATH="${case_root}/bin:${PATH}" \
        sh "${subject}" >"${case_root}/stdout" 2>"${case_root}/stderr"
    RUN_STATUS=$?
    set -e
    RUN_ELAPSED=$(($(date +%s) - started))

    RUN_STDOUT="$(cat "${case_root}/stdout")"
    RUN_STDERR="$(cat "${case_root}/stderr")"
    RUN_INITS="$(awk -F'\t' -v m="${module}" '$1 == m && $2 ~ /^init/ { n++ } END { print n + 0 }' "${calls_file}")"
    RUN_TOTAL_INITS="$(awk -F'\t' '$2 ~ /^init/ { n++ } END { print n + 0 }' "${calls_file}")"
    RUN_RETRY_NOTICES="$(grep -c 'hit a transient' "${case_root}/stderr" || true)"
}

# --- a transient failure is ridden out and the module still runs -------------
# The stub fails the targeted module's first init with the real captured text;
# the subject must retry, succeed on the second attempt, and report the whole
# run green. This is the case that fails on the pre-fix script: one attempt,
# no retry, exit 1.
run_subject transient-recovers 1 "${TRANSIENT_TEXT}"
[ "${RUN_STATUS}" -eq 0 ] ||
    fail "a transient init failure must not fail the gate; got status ${RUN_STATUS}:
${RUN_STDERR}"
[ "${RUN_INITS}" -eq 2 ] ||
    fail "expected the targeted module to be inited twice (fail, retry), got ${RUN_INITS}"
[ "${RUN_TOTAL_INITS}" -gt 0 ] ||
    fail "expected the scan to init at least one module, got none"
[ "${RUN_RETRY_NOTICES}" -eq 1 ] ||
    fail "expected exactly one retry notice, got ${RUN_RETRY_NOTICES}:
${RUN_STDERR}"
case "${RUN_STDERR}" in
*"attempt 2 of 3"*) ;;
*) fail "the retry notice must name the next attempt, got:
${RUN_STDERR}" ;;
esac
# Recovered means the whole run is green, not merely that this module moved on:
# the subject's own aggregate report is the observable that says so.
case "${RUN_STDOUT}" in
*"module suite(s) passed"*) ;;
*) fail "a recovered run must report the suites as passed, got:
${RUN_STDOUT}" ;;
esac
echo "ok: transient failure retried and recovered (${RUN_ELAPSED}s)"

# --- a permanent failure fails on the first attempt, with no backoff ---------
# Same stub, same targeted module, but the text is the one a real unsatisfiable
# constraint produces. The classifier must not fire, so there is exactly one
# attempt -- and because no backoff is paid, this case is fast by construction.
run_subject permanent-fails-fast 99 "${PERMANENT_TEXT}"
[ "${RUN_STATUS}" -eq 1 ] ||
    fail "a non-transient init failure must still fail the gate; got status ${RUN_STATUS}"
[ "${RUN_INITS}" -eq 1 ] ||
    fail "a non-transient init failure must not be retried, got ${RUN_INITS} attempts"
[ "${RUN_RETRY_NOTICES}" -eq 0 ] ||
    fail "a non-transient failure must print no retry notice, got ${RUN_RETRY_NOTICES}"
case "${RUN_STDERR}" in
*"FAIL: terraform init failed in ${module}:"*) ;;
*) fail "the failure report must be unchanged, got:
${RUN_STDERR}" ;;
esac
# The capture must reach the operator intact, wrap and all.
case "${RUN_STDERR}" in
*"no available releases match the given constraints"*) ;;
*) fail "the failing module's own diagnostics must still be printed, got:
${RUN_STDERR}" ;;
esac
echo "ok: non-transient failure fails on the first attempt (${RUN_ELAPSED}s)"

# --- a persistent transient failure is still bounded -------------------------
# A real registry outage: the transient signature every time. The gate must
# still go red rather than retrying forever, and must stop after exactly the
# configured bound -- the attempt count is the bound, so a hung gate is
# impossible by construction.
run_subject transient-persists 99 "${TRANSIENT_TEXT}"
[ "${RUN_STATUS}" -eq 1 ] ||
    fail "a persistent transient failure must still fail the gate; got status ${RUN_STATUS}"
[ "${RUN_INITS}" -eq 3 ] ||
    fail "expected exactly TF_INIT_MAX_ATTEMPTS (3) attempts, got ${RUN_INITS}"
[ "${RUN_RETRY_NOTICES}" -eq 2 ] ||
    fail "expected 2 retry notices for 3 attempts, got ${RUN_RETRY_NOTICES}"
case "${RUN_STDERR}" in
*"FAIL: terraform init failed in ${module}:"*) ;;
*) fail "a bounded-out retry must still report the module as failed, got:
${RUN_STDERR}" ;;
esac
echo "ok: persistent transient failure bounded at 3 attempts and failed (${RUN_ELAPSED}s)"

# The bound is only honest if it is small in wall-clock too: two backoff steps
# of a small base, paid by one module. Assert the ceiling rather than the
# measured value so a test host's jitter cannot make this flaky, but keep the
# ceiling tight enough that an unbounded or generous ladder would blow it.
[ "${RUN_ELAPSED}" -le 30 ] ||
    fail "the retry bound must not stall the gate; took ${RUN_ELAPSED}s for 3 attempts"

echo "terraform-module-tests_test: all cases passed"
