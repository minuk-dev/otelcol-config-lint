#!/usr/bin/env bash
set -euo pipefail

version="${1:-}"
if [[ ! "$version" =~ ^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$ ]]; then
  echo 'Release version must be vMAJOR.MINOR.PATCH, for example v0.2.1' >&2
  exit 1
fi
if git rev-parse --verify --quiet "refs/tags/$version" >/dev/null; then
  echo "Release tag $version already exists; choose a new version" >&2
  exit 1
fi
if [ -n "$(git status --porcelain)" ]; then
  echo 'Release preparation requires a clean checkout' >&2
  exit 1
fi

make release-pin RELEASE="$version"
git config user.name 'github-actions[bot]'
git config user.email '41898282+github-actions[bot]@users.noreply.github.com'
git add Dockerfile
if ! git diff --cached --quiet; then
  git commit -m "chore(release): pin the Action at $version"
fi
git tag -a "$version" -m "Release $version"
# A concurrent update to main must reject both the commit and the tag.
git push --atomic origin HEAD:refs/heads/main "refs/tags/$version"
