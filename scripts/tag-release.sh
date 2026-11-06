#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
version="$(python3 scripts/release.py validate)"
jj git fetch --remote origin
if [[ -n "$(jj diff --summary)" ]]; then
  echo "Commit working-copy changes before releasing" >&2
  exit 1
fi
local_main="$(jj log --no-graph -r main -T 'commit_id')"
remote_main="$(jj log --no-graph -r main@origin -T 'commit_id')"
parent="$(jj log --no-graph -r @- -T 'commit_id')"
if [[ "$local_main" != "$remote_main" || "$parent" != "$local_main" ]]; then
  echo "Release from an empty working-copy change directly above synchronized main" >&2
  exit 1
fi
if [[ -n "$(jj tag list "v$version")" ]]; then
  echo "Release tag v$version already exists; bump VERSION and commit it first" >&2
  exit 1
fi
jj tag set "v$version" -r main
jj git push --remote origin --tag "v$version"
