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

script_dir="$(cd "$(dirname "$0")" && pwd)"
modules_root="$(cd "${script_dir}/../erun-devops/terraform-erun/modules" && pwd)"

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
    if ! (cd "${module_dir}" && terraform init -backend=false -input=false -no-color) \
        >"${init_log}" 2>&1; then
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
