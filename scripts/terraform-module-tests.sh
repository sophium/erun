#!/bin/sh

# Every published Terraform module's own behaviour suite, run with
# `terraform test` against mocked providers: no cluster, no cloud account, no
# state. The modules under erun-devops/terraform-erun are consumed by pinned
# `?ref=` from tenant roots, so an edit that drops an invariant they pin still
# applies cleanly -- the suite is the only thing that notices, and until this
# script was wired into `make check` nothing ever invoked it. helm-chart-tests
# does the same job for the k8s charts.
#
# Needs only the `terraform` CLI, which the erun-devops image test stage
# installs (see that Dockerfile) and every runtime pod already carries, so the
# same command runs in both venues. It does need the network on a cold start:
# `terraform init` fetches each module's providers from registry.terraform.io
# (~105MB for the cluster-edge module's helm+kubernetes pair, measured at 6-8s
# per module), and nothing here bakes a provider mirror into the image.
#
# Modules are discovered by scanning modules/*/tests for suites rather than
# listed, so a new module's suite is gated the moment it lands and no Makefile
# or Dockerfile edit is needed to pick it up. A module carrying no suite is
# skipped rather than initialized: `terraform test` against it prints
# "Success! 0 passed, 0 failed" after paying for that module's provider
# download, which is gate cost spent on a green line that covers nothing.

set -eu

# Terraform's own upgrade/version ping is not the gate's business, and a build
# host behind a restrictive egress rule must not fail on it.
CHECKPOINT_DISABLE=1
export CHECKPOINT_DISABLE

# `terraform init` is this gate's one and only network step, and it is the one
# step here that can fail for a reason that has nothing to do with the change
# under test. On a loaded build host its discovery request to
# registry.terraform.io has timed out while the same pod reached the same host
# with a plain `curl` in 0.28s, and while the other modules' init in the same
# run succeeded -- reddening branches that changed no terraform file at all and
# pulling them out of the merge queue. Worse, the abort lands before the rest of
# the gate runs, so the branch's own changed specs are never exercised and a
# whole gate's worth of evidence is lost to an unrelated timeout.
#
# So `init` alone is retried, a bounded number of times with a short linear
# backoff. The retry is honest because the request it re-runs is an idempotent,
# side-effect-free GET -- nothing is being asserted away, only a network blip
# ridden out -- which is a different thing from widening a test timeout, and
# `init` is the only place here with a reason to be retried at all. The suites
# themselves run against mocked providers with no registry in reach, so
# retrying them would buy nothing and could only hide a real assertion failure:
# the one thing this gate exists to catch.
#
# The worst case this can add is (1 + 2) * the base step per module, which is
# less than a single attempt's own cost -- so a genuine registry outage still
# reds the gate promptly rather than turning it into a hang. The Go half of this
# same decision is erun-common/published_artifact_verify.go's
# readBackPublishedArtifact, which retries a subprocess read-back on a transient
# failure classified from its captured output; this mirrors that shape.
TF_INIT_MAX_ATTEMPTS=3
TF_INIT_RETRY_BASE_SECONDS=2

script_dir="$(cd "$(dirname "$0")" && pwd)"
modules_root="$(cd "${script_dir}/../erun-devops/terraform-erun/modules" && pwd)"

# terraform_init_failure_is_transient reports whether a failed `terraform init`
# capture ($1) failed for a transport reason rather than a verdict about the
# module. It is deliberately a marker list of transport signatures only.
#
# Two things it must never match, both of which the report that produced this
# fix demonstrates. "Could not retrieve the list of available versions for
# provider X" is the outer wrapper around BOTH kinds: a timed-out discovery
# request and a constraint no release satisfies ("no available releases match
# the given constraints") carry it word for word. And a bare "not found" or a
# bare status code would catch a genuinely broken module. Matching the outer
# message would retry a provider that does not exist; matching nothing would
# leave the blip. So the markers below are the inner transport failures, and a
# definitive answer from the registry -- a version constraint, an unknown
# provider, a bad source address, a malformed config, a checksum mismatch --
# stays terminal and fails on its first attempt.
terraform_init_failure_is_transient() {
    # Terraform hard-wraps diagnostics at 80 columns, so a marker can be split
    # across two lines in the capture: the reported failure itself prints
    # "failed to request\ndiscovery document". Fold the capture onto one line
    # before matching, or the very failure this exists for goes unrecognized.
    tf_folded="$(tr '\n' ' ' <"$1" | tr -s '[:space:]' ' ' | tr '[:upper:]' '[:lower:]')"
    for tf_marker in \
        'failed to request discovery document' \
        'context deadline exceeded' \
        'i/o timeout' \
        'connection reset by peer' \
        'connection refused' \
        'tls handshake timeout' \
        'no such host' \
        'temporary failure in name resolution' \
        'network is unreachable' \
        '502 bad gateway' \
        '503 service unavailable' \
        '429 too many requests'
    do
        case "${tf_folded}" in
        *"${tf_marker}"*) return 0 ;;
        esac
    done
    return 1
}

# terraform_init_with_retry runs `terraform init` for the module in $1, leaving
# its output in the log file $2. It returns 0 on success and 1 on failure, with
# the log from the final attempt intact so the caller's existing failure report
# is unchanged. A transient failure is retried up to TF_INIT_MAX_ATTEMPTS
# times; a failure that is not transient returns after the first attempt, with
# no backoff paid.
terraform_init_with_retry() {
    tf_init_dir="$1"
    tf_init_log="$2"
    tf_init_attempt=1

    while :; do
        if (cd "${tf_init_dir}" && terraform init -backend=false -input=false -no-color) \
            >"${tf_init_log}" 2>&1; then
            return 0
        fi
        if [ "${tf_init_attempt}" -ge "${TF_INIT_MAX_ATTEMPTS}" ] ||
            ! terraform_init_failure_is_transient "${tf_init_log}"; then
            return 1
        fi
        tf_init_delay=$((TF_INIT_RETRY_BASE_SECONDS * tf_init_attempt))
        echo "-- terraform init in $(basename "${tf_init_dir}") hit a transient" \
            "registry failure; retrying in ${tf_init_delay}s" \
            "(attempt $((tf_init_attempt + 1)) of ${TF_INIT_MAX_ATTEMPTS})" >&2
        sleep "${tf_init_delay}"
        tf_init_attempt=$((tf_init_attempt + 1))
    done
}

command -v terraform >/dev/null 2>&1 || {
    echo "FAIL: the terraform CLI is required to run the terraform module tests" >&2
    exit 1
}

work_dir="$(mktemp -d)"
trap 'rm -rf "${work_dir}"' EXIT INT TERM

# Run every module's suite even when an earlier one fails, then fail once at
# the end naming every module that failed -- the same aggregate rather than
# fail-fast contract helm-chart-tests' own fan-out reports under.
failed=""
suites=0

for module_dir in "${modules_root}"/*/; do
    [ -d "${module_dir}" ] || continue
    module="$(basename "${module_dir}")"

    # Any suite at all is enough to run the module; $1 is the first match, or
    # the unmatched pattern itself, which then fails the -f test. The skip is
    # printed rather than silent so the gate log distinguishes "ran and passed"
    # from "nothing here to run" -- two modules under this tree carry no suite
    # at all today, and a run that never mentions them reads as coverage.
    set -- "${module_dir}"tests/*.tftest.hcl
    if [ ! -f "$1" ]; then
        echo "-- terraform test ${module}: skipped, no tests/*.tftest.hcl suite"
        continue
    fi

    echo ">> terraform test ${module}"

    init_log="${work_dir}/${module}.init.log"
    if ! terraform_init_with_retry "${module_dir}" "${init_log}"; then
        echo "FAIL: terraform init failed in ${module}:" >&2
        cat "${init_log}" >&2
        failed="${failed} ${module}"
        continue
    fi

    if ! (cd "${module_dir}" && terraform test -no-color); then
        failed="${failed} ${module}"
    fi
    suites=$((suites + 1))
done

if [ -n "${failed}" ]; then
    echo "terraform-module-tests failed in:${failed}" >&2
    exit 1
fi

# The vacuous-pass guard: this target exists because these suites were never
# run by anything, so an empty scan reporting success would reproduce exactly
# the defect it was added for. Naming the count also puts the evidence in the
# gate log rather than leaving "the suites ran" to be inferred from silence.
if [ "${suites}" -eq 0 ]; then
    echo "FAIL: no module under ${modules_root} carries a tests/*.tftest.hcl suite," \
        "so terraform-module-tests verified nothing" >&2
    exit 1
fi

echo "terraform-module-tests: ${suites} module suite(s) passed"
