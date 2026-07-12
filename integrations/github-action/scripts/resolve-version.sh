#!/usr/bin/env bash
# Resolves the codemap version input + this runner's OS/arch to a concrete,
# pinned release tag and GoReleaser archive coordinates, and exports them via
# $GITHUB_ENV so the cache step (actions/cache, action.yml) and
# install-codemap.sh (next step) can use them without re-deriving anything.
#
# "latest" is resolved to an EXACT tag here, once, at job start — never left to
# float mid-job (a long job could otherwise install a different "latest" than
# what it started with if a release ships mid-run).
set -euo pipefail

: "${CODEMAP_VERSION:=latest}"
REPO="abdul-hamid-achik/codemap"

if [[ "$CODEMAP_VERSION" == "latest" ]]; then
  api_url="https://api.github.com/repos/${REPO}/releases/latest"
  resp="$(curl -sSL -H "Accept: application/vnd.github+json" "$api_url")"
  tag="$(printf '%s' "$resp" | jq -r '.tag_name // empty')"
  if [[ -z "$tag" ]]; then
    echo "::error::failed to resolve the latest codemap release from ${api_url}" >&2
    exit 1
  fi
else
  tag="$CODEMAP_VERSION"
  if [[ "$tag" != v* ]]; then
    tag="v${tag}"
  fi
fi

version_num="${tag#v}"

# GoReleaser's archive name_template is `{{ .ProjectName }}_{{ .Version }}_{{ .Os }}_{{ .Arch }}`
# (.goreleaser.yaml in the codemap repo). Verified against a real published
# release (v0.40.0 checksums.txt): lowercase `linux`/`darwin`/`windows`,
# lowercase `amd64`/`arm64`, `.Version` WITHOUT the leading `v`
# (codemap_0.40.0_darwin_arm64.tar.gz, not codemap_v0.40.0_...).
case "${RUNNER_OS:-}" in
  Linux) os=linux ;;
  macOS) os=darwin ;;
  Windows) os=windows ;;
  *)
    echo "::error::unsupported RUNNER_OS '${RUNNER_OS:-<empty>}' — codemap only ships linux/darwin/windows builds" >&2
    exit 1
    ;;
esac

case "${RUNNER_ARCH:-}" in
  X64) arch=amd64 ;;
  ARM64) arch=arm64 ;;
  *)
    echo "::error::unsupported RUNNER_ARCH '${RUNNER_ARCH:-<empty>}' — codemap only ships amd64/arm64 builds" >&2
    exit 1
    ;;
esac

# .goreleaser.yaml explicitly ignores goos:windows/goarch:arm64 — fail loudly
# and specifically here instead of a confusing 404 during download.
if [[ "$os" == "windows" && "$arch" == "arm64" ]]; then
  echo "::error::codemap has no windows/arm64 release build (see .goreleaser.yaml's 'ignore' list) — this runner combination is unsupported" >&2
  exit 1
fi

ext="tar.gz"
if [[ "$os" == "windows" ]]; then
  ext="zip"
fi

bin_dir="${RUNNER_TEMP:-/tmp}/codemap-action/${tag}/${os}-${arch}"

{
  echo "CODEMAP_TAG=${tag}"
  echo "CODEMAP_VERSION_NUM=${version_num}"
  echo "CODEMAP_ARCHIVE_OS=${os}"
  echo "CODEMAP_ARCHIVE_ARCH=${arch}"
  echo "CODEMAP_ARCHIVE_EXT=${ext}"
  echo "CODEMAP_BIN_DIR=${bin_dir}"
} >> "${GITHUB_ENV:?resolve-version.sh must run inside a GitHub Actions job}"

echo "codemap-action: resolved ${tag} for ${os}/${arch} -> ${bin_dir}"
