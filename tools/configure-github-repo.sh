#!/usr/bin/env bash
set -euo pipefail

REPOSITORY="${FLOE_GITHUB_REPOSITORY:-JerryRun/floe}"
DESCRIPTION="Manage and move files across Linux servers from one Windows workspace."
TOPICS=(
  sftp
  ssh
  file-manager
  server-management
  server-to-server
  windows
  devops
  remote-files
)

if ! command -v gh >/dev/null 2>&1; then
  echo "GitHub CLI is required: https://cli.github.com/" >&2
  exit 1
fi

gh auth status

arguments=(repo edit "$REPOSITORY" --description "$DESCRIPTION")
for topic in "${TOPICS[@]}"; do
  arguments+=(--add-topic "$topic")
done
gh "${arguments[@]}"

latest_tag="$(gh release view --repo "$REPOSITORY" --json tagName --jq .tagName 2>/dev/null || true)"
if [[ -n "$latest_tag" ]]; then
  current_body="$(gh release view "$latest_tag" --repo "$REPOSITORY" --json body --jq .body)"
  if [[ "$current_body" != *"## Download for Windows"* ]]; then
    release_notes="$(mktemp)"
    trap 'rm -f "$release_notes"' EXIT
    cat .github/release-preamble.md > "$release_notes"
    if [[ -n "$current_body" ]]; then
      printf '\n---\n\n%s\n' "$current_body" >> "$release_notes"
    fi
    gh release edit "$latest_tag" --repo "$REPOSITORY" --notes-file "$release_notes"
  fi
fi

gh repo view "$REPOSITORY" \
  --json description,repositoryTopics,url \
  --jq '{url, description, topics: [.repositoryTopics[].name]}'
