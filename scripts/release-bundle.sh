#!/usr/bin/env bash
set -euo pipefail

repo="$(cd "$(dirname "$0")/.." && pwd)"
cd "$repo"

if [[ -n "$(git status --porcelain --untracked-files=all)" ]]; then
  echo "release bundle requires a clean repository" >&2
  exit 1
fi

commit="$(git rev-parse HEAD)"
source_date="$(git show -s --format=%cI HEAD)"
short_commit="${commit:0:12}"
version="${RELEASE_VERSION:-0.0.0-git.${short_commit}}"
output="${RELEASE_OUTPUT_DIR:-${repo}/tmp/release-${short_commit}}"
tmp_root="$(realpath -m "${repo}/tmp")"
resolved_output="$(realpath -m "$output")"
case "$resolved_output" in
  "$tmp_root"/*) ;;
  *)
    echo "release output must be below repository tmp" >&2
    exit 1
    ;;
esac
rm -rf -- "$resolved_output"
mkdir -p "$resolved_output"

go run -mod=readonly ./tools/releasectl build \
  --inventory deploy/release/images.json \
  --output "$resolved_output" \
  --version "$version" \
  --commit "$commit" \
  --source-date "$source_date"
go run -mod=readonly ./tools/releasectl verify --inventory deploy/release/images.json --output "$resolved_output"
echo "Release bundle metadata is ready at ${resolved_output}"
