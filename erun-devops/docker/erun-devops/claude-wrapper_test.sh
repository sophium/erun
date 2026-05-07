#!/bin/sh

# Tests for claude-wrapper.sh. Stubs claude-real, exercises arg combinations,
# and asserts the recorded invocation matches expectations.

set -eu

script_dir="$(cd "$(dirname "$0")" && pwd)"
wrapper="${script_dir}/claude-wrapper.sh"
if [ ! -x "${wrapper}" ]; then
    chmod +x "${wrapper}"
fi

work_root="$(mktemp -d 2>/dev/null || mktemp -d -t claude-wrapper-test)"
trap 'rm -rf "${work_root}"' EXIT INT TERM

stub_dir="${work_root}/bin"
fake_home="${work_root}/home"
fake_cwd="${work_root}/cwd"
record_file="${work_root}/record"
mkdir -p "${stub_dir}" "${fake_home}" "${fake_cwd}"

cat >"${stub_dir}/claude-real" <<EOF
#!/bin/sh
{
    printf '%s' "claude-real"
    for a in "\$@"; do
        printf ' [%s]' "\${a}"
    done
    printf '\n'
} >>"${record_file}"
EOF
chmod 0755 "${stub_dir}/claude-real"

failures=0

run_case() {
    label="$1"
    expected="$2"
    shift 2
    : >"${record_file}"
    (
        cd "${fake_cwd}"
        HOME="${fake_home}" PATH="${stub_dir}:${PATH}" "${wrapper}" "$@"
    )
    actual="$(cat "${record_file}")"
    if [ "${actual}" = "${expected}" ]; then
        printf 'ok  %s\n' "${label}"
    else
        printf 'FAIL %s\n  expected: %s\n  actual:   %s\n' "${label}" "${expected}" "${actual}"
        failures=$((failures + 1))
    fi
}

# Project directory absent → fresh session.
rm -rf "${fake_home}/.claude/projects"
run_case "no project dir runs fresh" "claude-real"

# Project directory absent → flags still pass through.
run_case "fresh session with positional prompt" "claude-real [hello]" hello

# Project directory present → resume injected.
project_key="$(printf '%s' "${fake_cwd}" | tr / -)"
mkdir -p "${fake_home}/.claude/projects/${project_key}"
run_case "project dir triggers resume" "claude-real [--continue]"

# Resume with extra args.
run_case "resume preserves user args" "claude-real [--continue] [hello]" hello

# Bypass flags must not get --continue.
for flag in --continue -c --resume -r --print -p --new --no-resume \
            --help -h --version -v; do
    run_case "bypass flag ${flag}" "claude-real [${flag}]" "${flag}"
done

# Bypass flag with extra args preserves order.
run_case "bypass flag with arg" "claude-real [--resume] [abc123]" --resume abc123

# Subcommands bypass.
for sub in mcp update migrate-installer setup-token config doctor; do
    run_case "bypass subcommand ${sub}" "claude-real [${sub}]" "${sub}"
done

# Subcommand with arg.
run_case "config set passes through" "claude-real [config] [set] [theme] [dark]" config set theme dark

if [ "${failures}" -eq 0 ]; then
    printf '\nall tests passed\n'
    exit 0
fi
printf '\n%d test(s) failed\n' "${failures}"
exit 1
