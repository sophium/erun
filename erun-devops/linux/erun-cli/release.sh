#!/usr/bin/env bash
set -euo pipefail

script_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
repo_root=$(cd "$script_dir/../../.." && pwd)
version=${ERUN_BUILD_VERSION:-}

if [[ -z "$version" ]]; then
  echo "ERUN_BUILD_VERSION must be set" >&2
  exit 1
fi

resolve_gh_ssh_key_title() {
  if [[ -n "${ERUN_SHELL_HOST:-}" ]]; then
    printf '%s\n' "${ERUN_SHELL_HOST}"
    return
  fi

  if [[ -n "${ERUN_TENANT:-}" && -n "${ERUN_ENVIRONMENT:-}" ]]; then
    printf '%s-%s\n' "${ERUN_TENANT}" "${ERUN_ENVIRONMENT}"
    return
  fi

  hostname -s 2>/dev/null || printf 'erun\n'
}

find_local_ssh_public_key() {
  local candidate

  for candidate in \
    "$HOME/.ssh/id_ed25519.pub" \
    "$HOME/.ssh/id_ecdsa.pub" \
    "$HOME/.ssh/id_rsa.pub"
  do
    if [[ -r "$candidate" ]]; then
      printf '%s\n' "$candidate"
      return
    fi
  done

  return 1
}

ensure_local_ssh_key() {
  local key_title ssh_dir private_key_path public_key_path

  if public_key_path=$(find_local_ssh_public_key); then
    printf '%s\n' "$public_key_path"
    return
  fi

  key_title=$(resolve_gh_ssh_key_title)
  ssh_dir="$HOME/.ssh"
  private_key_path="$ssh_dir/id_ed25519"
  public_key_path="$private_key_path.pub"

  mkdir -p "$ssh_dir"
  chmod 700 "$ssh_dir"

  echo "No local SSH key found. Generating $public_key_path for GitHub..." >&2
  ssh-keygen -q -t ed25519 -C "$key_title" -f "$private_key_path" -N ""
  printf '%s\n' "$public_key_path"
}

ensure_github_public_key_scope() {
  echo "Refreshing GitHub CLI credentials to manage SSH keys..." >&2
  gh auth refresh --hostname github.com --scopes admin:public_key
}

ensure_github_ssh_key_uploaded() {
  local public_key_path public_key_data key_title uploaded_keys

  public_key_path=$(ensure_local_ssh_key)
  public_key_data=$(<"$public_key_path")
  if ! uploaded_keys=$(gh ssh-key list 2>/dev/null); then
    ensure_github_public_key_scope
    uploaded_keys=$(gh ssh-key list)
  fi
  if grep -Fq "$public_key_data" <<<"$uploaded_keys"; then
    return
  fi

  key_title=$(resolve_gh_ssh_key_title)
  echo "Adding SSH key \"$key_title\" to GitHub..." >&2
  if ! gh ssh-key add "$public_key_path" --title "$key_title"; then
    ensure_github_public_key_scope
    gh ssh-key add "$public_key_path" --title "$key_title"
  fi
}

ensure_github_cli_auth() {
  if ! gh auth status >/dev/null 2>&1; then
    echo "GitHub CLI is not authenticated. Starting gh auth login..." >&2
    gh auth login --hostname github.com --git-protocol ssh --web --skip-ssh-key --scopes admin:public_key
  fi

  ensure_github_ssh_key_uploaded
}

ensure_remote_release_tag() {
  local tag=$1

  if ! git -C "$repo_root" rev-parse --verify "refs/tags/$tag" >/dev/null 2>&1; then
    return
  fi

  if git -C "$repo_root" ls-remote --exit-code --tags origin "refs/tags/$tag" >/dev/null 2>&1; then
    return
  fi

  echo "Pushing release tag $tag to origin..." >&2
  git -C "$repo_root" push origin "$tag"
}

ensure_github_cli_auth

artifact_path=$("$script_dir/build.sh")
tag="v${version}"

if gh release view "$tag" >/dev/null 2>&1; then
  gh release upload "$tag" "$artifact_path" --clobber
  exit 0
fi

ensure_remote_release_tag "$tag"

release_args=("$tag" "$artifact_path" "--title" "Release $version")
if [[ "$version" == *-rc.* || "$version" == *-pr.* ]]; then
  release_args+=("--prerelease")
fi
release_args+=("--generate-notes")

(cd "$repo_root" && gh release create "${release_args[@]}")
