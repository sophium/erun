#!/usr/bin/env sh
# Run one published Terraform module's own behaviour tests.
#
#   terraform-module-test.sh <module-dir>
#
# <module-dir> is the repo-root-relative directory printed by
# scripts/terraform-test-modules.sh (an absolute path works too). The module's
# `tests/*.tftest.hcl` runs against mocked providers -- no cluster, no cloud
# account -- so a pinned terraform binary is the whole of what this needs.
#
# Two properties make the result a contract rather than a convenience:
#
# - The module's `.terraform.lock.hcl` must be committed, and init runs with
#   `-lockfile=readonly`. Without a lock file terraform resolves the newest
#   version satisfying each constraint, so the gate would test whatever the
#   registry happened to publish that day and a provider bump would land
#   unrecorded; with `-lockfile=readonly` a constraint that no longer matches
#   the recorded selection fails here instead of quietly rewriting the pin.
#   Note that `-lockfile=readonly` alone is not enough: terraform only refuses
#   to *change* an existing lock file, so a module with no lock file at all
#   initializes happily and writes one. The existence check below is what
#   makes the pin mandatory.
# - `terraform test` exits non-zero on a failed assertion, so the caller's exit
#   status is the verdict; nothing here parses its output.
set -eu

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
root=$(CDPATH= cd -- "${script_dir}/.." && pwd)

[ "$#" -eq 1 ] || {
	echo "usage: terraform-module-test.sh <module-dir>" >&2
	exit 2
}

case "$1" in
/*) module="$1" ;;
*) module="${root}/$1" ;;
esac

[ -d "${module}" ] || {
	printf 'terraform-module-test: %s is not a directory\n' "$1" >&2
	exit 1
}
[ -f "${module}/.terraform.lock.hcl" ] || {
	printf 'terraform-module-test: %s has behaviour tests but no committed .terraform.lock.hcl.\n' "$1" >&2
	printf 'terraform-module-test: run `terraform -chdir=%s providers lock -platform=linux_amd64 -platform=linux_arm64` and commit the result, so the gate tests a pinned provider set instead of whatever the registry serves that day.\n' "$1" >&2
	exit 1
}
command -v terraform >/dev/null 2>&1 || {
	echo "terraform-module-test: no terraform on PATH. It is installed in the erun-devops image test stage; to run this by hand install the version pinned in erun-devops/docker/erun-devops/Dockerfile (TERRAFORM_VERSION)." >&2
	exit 1
}

# -no-color so the output stays readable in a captured gate log, and
# -backend=false because a test run never touches state: these modules are
# published as reusable child modules and carry no backend configuration.
terraform -chdir="${module}" init -backend=false -lockfile=readonly -no-color
terraform -chdir="${module}" test -no-color
