#!/usr/bin/env sh
# The test of the test: prove `terraform-module-tests` actually fails when a
# module's pinned invariant is broken.
#
# The gap #2642 reports is not "no tests exist" -- the modules have real
# behaviour tests, and they pass when someone remembers to run them by hand.
# The gap is that *nothing ran them*, so an edit that drops an invariant they
# pin applies cleanly and every gate stays green. A runner added to the gate
# can close that gap and still be worthless in the same silent way: if it
# swallows terraform's exit status, or runs in the wrong directory, or skips a
# module it cannot find, it reports success without having tested anything.
# So this drives the runner over a module copy whose pinned behaviour is
# deliberately removed and requires a non-zero verdict -- the reproduction of
# the reported failure is the mutated run below, and the unmutated control run
# is what keeps it from passing for an unrelated reason (a missing provider, a
# typo in the module path).
#
# The mutation is the shape the issue names: the apex record is pointed at a
# subdomain instead of the base domain, which is exactly what
# `apex_records.tftest.hcl` asserts against ("the apex record must target
# base_domain_name itself, not a subdomain of it"). cloudflare-apex is the
# module used because it is the cheap one (~5s a run against cluster-edge's
# ~30s) and this script proves a property of the *runner*, not of either
# module's own coverage -- cluster-edge's tests run in the gate as their own
# job, at full cost, where they belong.
set -eu

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
root=$(CDPATH= cd -- "${script_dir}/.." && pwd)

module_rel="erun-devops/terraform-erun/modules/terraform-erun-cloudflare-apex"
anchor='  name    = var.base_domain_name'
mutated='  name    = "www.${var.base_domain_name}"'

work=$(mktemp -d)
trap 'rm -rf "${work}"' EXIT INT TERM

cp -r "${root}/${module_rel}" "${work}/module"

fail() {
	printf 'terraform-module-tests self-test: %s\n' "$1" >&2
	exit 1
}

# Control. If this fails, the run below proves nothing about the mutation --
# the runner is broken, or the environment is (no terraform, no provider
# mirror) -- so it is reported as its own failure rather than folded into the
# assertion that the mutation is caught.
if ! sh "${script_dir}/terraform-module-test.sh" "${work}/module" >"${work}/control.log" 2>&1; then
	sed 's/^/    /' "${work}/control.log" >&2
	fail "the runner failed on the unmutated copy, so its result on a mutated copy would not mean anything"
fi

# The anchor has to be there before it can be broken: a rename would otherwise
# leave the mutation a no-op that the runner correctly passes, and this script
# would report a false alarm about a working gate.
[ "$(grep -c "^${anchor}\$" "${work}/module/main.tf")" -eq 1 ] || fail \
	"the anchor '${anchor}' is not exactly once in ${module_rel}/main.tf, so this script mutated nothing -- update the anchor to the line the apex record's own name assertion pins"

sed -i "s|^${anchor}\$|${mutated}|" "${work}/module/main.tf"
grep -q "^${mutated}\$" "${work}/module/main.tf" || fail "the mutation did not apply, so the run below would not have tested it"

if sh "${script_dir}/terraform-module-test.sh" "${work}/module" >"${work}/mutated.log" 2>&1; then
	printf 'terraform-module-tests self-test: the runner reported success for a module whose apex record no longer targets base_domain_name.\n' >&2
	printf 'terraform-module-tests self-test: terraform-module-tests would report a green gate over a broken invariant, which is the failure #2642 exists to remove.\n' >&2
	sed 's/^/    /' "${work}/mutated.log" >&2
	exit 1
fi

echo "terraform-module-tests self-test: the runner passes the unmutated module and fails the one whose pinned invariant is broken"
