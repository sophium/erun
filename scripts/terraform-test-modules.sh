#!/usr/bin/env sh
# Print the repo-root-relative directory of every published Terraform module
# that carries behaviour tests -- a module whose directory holds at least one
# `tests/*.tftest.hcl`.
#
# This is the single source of truth for "which modules `terraform test` runs
# against", read by all three callers so they cannot disagree: the Makefile's
# `terraform-module-tests` target, the erun-devops image test stage that bakes
# the offline provider mirror at build time, and the structural test that
# checks a module with tests is not silently skipped. A second copy of the
# discovery rule is how a new module's tests end up reachable from one caller
# and not another, which is the exact class of gap this target exists to close.
#
# Exits non-zero, printing nothing, when the tree holds no tested module at
# all: an empty list is a scan that stopped matching (a rename, a move, a
# changed test suffix), not a tree that legitimately has nothing to run, and a
# caller that treated it as "nothing to do" would report green over zero
# modules.
set -eu

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
root=$(CDPATH= cd -- "${script_dir}/.." && pwd)

modules_dir="${root}/erun-devops/terraform-erun/modules"
[ -d "${modules_dir}" ] || {
	printf 'terraform-test-modules: no module tree at %s\n' "${modules_dir}" >&2
	exit 1
}

found=0
for module in "${modules_dir}"/*/; do
	[ -d "${module}tests" ] || continue
	# A tests/ directory with no *.tftest.hcl in it is not a tested module:
	# `terraform test` would report "no tests" and exit 0, which reads as a
	# pass. Require an actual test file.
	for _ in "${module}"tests/*.tftest.hcl; do
		[ -e "${_}" ] || continue
		# Strip the trailing slash `*/` leaves on the glob expansion, then the
		# repo root, so every caller gets the repo-root-relative directory it
		# can pass straight to `terraform -chdir=` from any working directory.
		rel=${module%"${module##*[!/]}"}
		printf '%s\n' "${rel#"${root}/"}"
		found=1
		break
	done
done

if [ "${found}" -eq 0 ]; then
	echo "terraform-test-modules: found no module with a tests/*.tftest.hcl under ${modules_dir#"${root}/"}: the scan matched nothing, so every caller would run zero modules and report success" >&2
	exit 1
fi
