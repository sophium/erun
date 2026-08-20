#!/bin/sh

# Locks in the --platform=$BUILDPLATFORM contract for Go builder stages that
# explicitly cross-compile (CGO_ENABLED=0 with explicit GOOS/GOARCH): pinning
# lets the compiler run natively on the build host instead of under qemu for
# the foreign target arch, since the cross-compile flags already select the
# output arch. It also locks the counter-case: a stage whose output ships as
# target-arch content in the final image (erun-devops's own `go` toolchain,
# erun-docs's final publish stage) must stay unpinned.

set -eu

docker_dir="$(cd "$(dirname "$0")" && pwd)"

failures=0

fail() {
    printf 'FAIL: %s\n' "$1" >&2
    failures=$((failures + 1))
}

assert_line() {
    file="$1"
    expected="$2"
    if ! grep -qF -e "${expected}" "${file}"; then
        fail "${file} missing expected line: ${expected}"
    fi
}

assert_count() {
    file="$1"
    needle="$2"
    expected="$3"
    actual="$(grep -cF -e "${needle}" "${file}")"
    if [ "${actual}" != "${expected}" ]; then
        fail "${file}: expected ${expected} occurrence(s) of '${needle}', found ${actual}"
    fi
}

# erun-devops: the compiling `builder` stage cross-compiles erun/emcp/golangci-lint
# and must run natively; the `goroot` stage ships the Go toolchain itself into the
# final image and must stay pinned to the target arch, sourced with no RUN steps.
devops_dockerfile="${docker_dir}/erun-devops/Dockerfile"
assert_line "${devops_dockerfile}" 'FROM --platform=$BUILDPLATFORM golang:1.26.0 AS builder'
assert_line "${devops_dockerfile}" 'FROM golang:1.26.0 AS goroot'
assert_line "${devops_dockerfile}" 'COPY --from=goroot /usr/local/go /usr/local/go'
if grep -qF 'COPY --from=builder /usr/local/go' "${devops_dockerfile}"; then
    fail "${devops_dockerfile}: /usr/local/go must be sourced from the unpinned goroot stage, not the BUILDPLATFORM-pinned builder stage"
fi

# erun-backend-api and erun-dns01-webhook: single builder stage, only the
# compiled binary is copied into the final image, so pinning is unconditional.
assert_line "${docker_dir}/erun-backend-api/Dockerfile" 'FROM --platform=$BUILDPLATFORM golang:1.26.0 AS builder'
assert_line "${docker_dir}/erun-dns01-webhook/Dockerfile" 'FROM --platform=$BUILDPLATFORM golang:1.26.0 AS builder'

# erun-docs: only the static-site builder stage (arch-independent output) is
# pinned; the final publish stage ships as the target-arch image and must not be.
docs_dockerfile="${docker_dir}/erun-docs/Dockerfile"
assert_count "${docs_dockerfile}" '--platform=$BUILDPLATFORM' 1

if [ "${failures}" -eq 0 ]; then
    printf 'all tests passed\n'
    exit 0
fi
printf '%d test(s) failed\n' "${failures}"
exit 1
