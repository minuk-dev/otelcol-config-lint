#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
script="$PWD/build/release.sh"
work="$(mktemp -d "${TMPDIR:-/tmp}/otelcol-release-test.XXXXXX")"
trap 'rm -rf "$work"' EXIT

git init --bare "$work/origin.git"
git init -b main "$work/repo"
cp Makefile Dockerfile "$work/repo/"
cd "$work/repo"
git config user.name 'Release test'
git config user.email 'release-test@example.com'
git add .
git commit -m 'Initial commit'
git remote add origin "$work/origin.git"
git push origin main

reject() {
  if bash "$script" "$1"; then
    echo "Expected release $1 to be rejected" >&2
    exit 1
  fi
}

initial="$(git rev-parse HEAD)"
for version in '' v1 v1.2 1.2.3 v01.2.3 v1.2.3-rc.1 'v1.2.3;echo unsafe'; do
  reject "$version"
done
test "$(git rev-parse HEAD)" = "$initial"
test -z "$(git tag)"

touch untracked
reject v1.2.3
rm untracked
test "$(git rev-parse HEAD)" = "$initial"

bash "$script" v1.2.3
test "$(git rev-parse HEAD)" = "$(git rev-parse 'v1.2.3^{commit}')"
test "$(git --git-dir="$work/origin.git" rev-parse main)" = "$(git rev-parse HEAD)"
test "$(git --git-dir="$work/origin.git" rev-parse v1.2.3)" = "$(git rev-parse v1.2.3)"
git show v1.2.3:Dockerfile | grep -Fx 'FROM ghcr.io/minuk-dev/otelcol-config-lint:1.2.3 AS bin'
test -z "$(git status --porcelain)"
reject v1.2.3

# An already pinned checkout can be tagged without an empty commit.
make release-pin RELEASE=v1.2.4
git add Dockerfile
git commit -m 'Pin next release'
pinned="$(git rev-parse HEAD)"
bash "$script" v1.2.4
test "$(git rev-parse HEAD)" = "$pinned"

# A competing main update must leave the remote without a partial release tag.
git clone -b main "$work/origin.git" "$work/other"
git -C "$work/other" -c user.name=Other -c user.email=other@example.com \
  commit --allow-empty -m 'Concurrent change'
git -C "$work/other" push origin main
reject v1.2.5
test -z "$(git --git-dir="$work/origin.git" tag -l v1.2.5)"
echo 'Release preparation checks passed'
