#!/bin/sh

set -eu

# Resume the most recent conversation by default so a fresh terminal in a
# fresh pod picks up where the previous pod left off. Subcommands and
# explicit-mode flags bypass the resume injection.
#
# The background-session lifecycle (--bg/--background and its agents/attach/
# stop/respawn/logs/rm subcommands) addresses a running instance by its own
# short id, never the cwd's most recent conversation, so injecting --continue
# ahead of it is always wrong: `claude logs <id>` became `claude-real
# --continue logs <id>`, which discarded the id and resumed whatever
# conversation this cwd last ran — observed live resuming another session's
# transcript instead of printing the requested id's output.
for arg in "$@"; do
    case "${arg}" in
        --continue|-c|--resume|-r|--print|-p|--new|--no-resume| \
        --help|-h|--version|-v|--bg|--background| \
        mcp|update|migrate-installer|setup-token|config|doctor| \
        agents|attach|stop|respawn|logs|rm)
            exec claude-real "$@"
            ;;
    esac
done

project_key="$(pwd | tr / -)"
if [ -d "${HOME}/.claude/projects/${project_key}" ]; then
    exec claude-real --continue "$@"
fi
exec claude-real "$@"
