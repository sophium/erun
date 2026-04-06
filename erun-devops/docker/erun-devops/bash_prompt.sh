#!/bin/sh

[ -n "${BASH_VERSION:-}" ] || return 0

__erun_color_blue=$'\001\033[38;5;39m\002'
__erun_color_host=$'\001\033[38;5;81m\002'
__erun_color_path=$'\001\033[38;5;70m\002'
__erun_color_branch=$'\001\033[38;5;214m\002'
__erun_color_error=$'\001\033[38;5;196m\002'
__erun_color_reset=$'\001\033[0m\002'

__erun_git_branch() {
    command -v git >/dev/null 2>&1 || return 0
    git rev-parse --is-inside-work-tree >/dev/null 2>&1 || return 0

    branch=$(
        git symbolic-ref --quiet --short HEAD 2>/dev/null ||
        git rev-parse --short HEAD 2>/dev/null
    ) || return 0

    dirty=""
    if ! git diff --no-ext-diff --quiet --ignore-submodules 2>/dev/null || \
       ! git diff --no-ext-diff --quiet --ignore-submodules --cached 2>/dev/null; then
        dirty="*"
    fi

    printf ' %s(%s%s)%s' "${__erun_color_branch}" "${branch}" "${dirty}" "${__erun_color_reset}"
}

__erun_set_prompt() {
    last_status=$?
    host_name="${ERUN_SHELL_HOST:-$(hostname -s 2>/dev/null || printf '%s' shell)}"
    prompt_symbol='$'
    if [ "${EUID:-$(id -u)}" -eq 0 ]; then
        prompt_symbol='#'
    fi

    status_segment=""
    if [ "${last_status}" -ne 0 ]; then
        status_segment="${__erun_color_error}[${last_status}]${__erun_color_reset} "
    fi

    PS1="${status_segment}${__erun_color_blue}\u${__erun_color_reset}@${__erun_color_host}${host_name}${__erun_color_reset}:${__erun_color_path}\w${__erun_color_reset}\$(__erun_git_branch) ${prompt_symbol} "
}

PROMPT_COMMAND=__erun_set_prompt
PROMPT_DIRTRIM=3
