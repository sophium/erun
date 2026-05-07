#!/bin/sh

set -eu

# Resume the most recent conversation by default so a fresh terminal in a
# fresh pod picks up where the previous pod left off. Subcommands and
# explicit-mode flags bypass the resume injection.
for arg in "$@"; do
    case "${arg}" in
        --continue|-c|--resume|-r|--print|-p|--new|--no-resume| \
        --help|-h|--version|-v| \
        mcp|update|migrate-installer|setup-token|config|doctor)
            exec claude-real "$@"
            ;;
    esac
done

project_key="$(pwd | tr / -)"
if [ -d "${HOME}/.claude/projects/${project_key}" ]; then
    exec claude-real --continue "$@"
fi
exec claude-real "$@"
