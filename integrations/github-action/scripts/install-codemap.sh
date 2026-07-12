#!/usr/bin/env bash
# Downloads the pinned codemap release archive resolved by resolve-version.sh,
# verifies it against the release's checksums.txt, extracts it, and puts the
# binary on PATH. Skips the network entirely when actions/cache already
# restored the binary for this exact tag+os+arch (see action.yml's cache step,
# keyed on the same coordinates this script reads).
#
# Called by: action.yml step "Install codemap"; gitlab/codemap-review.yml
# (curled down and invoked unmodified — GitLab has no actions/cache-equivalent
# step wired up here today, so it always downloads).
set -euo pipefail

: "${CODEMAP_TAG:?resolve-version.sh must run before install-codemap.sh}"
: "${CODEMAP_VERSION_NUM:?}"
: "${CODEMAP_ARCHIVE_OS:?}"
: "${CODEMAP_ARCHIVE_ARCH:?}"
: "${CODEMAP_ARCHIVE_EXT:?}"
: "${CODEMAP_BIN_DIR:?}"

REPO="abdul-hamid-achik/codemap"

bin_name="codemap"
if [[ "$CODEMAP_ARCHIVE_EXT" == "zip" ]]; then
  bin_name="codemap.exe"
fi
bin_path="${CODEMAP_BIN_DIR}/${bin_name}"

if [[ -x "$bin_path" ]]; then
  echo "codemap-action: ${CODEMAP_TAG} already present at ${bin_path} (actions/cache hit) — skipping download"
  echo "${CODEMAP_BIN_DIR}" >> "${GITHUB_PATH:?}"
  exit 0
fi

mkdir -p "$CODEMAP_BIN_DIR"

archive="codemap_${CODEMAP_VERSION_NUM}_${CODEMAP_ARCHIVE_OS}_${CODEMAP_ARCHIVE_ARCH}.${CODEMAP_ARCHIVE_EXT}"
base_url="https://github.com/${REPO}/releases/download/${CODEMAP_TAG}"

work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

echo "codemap-action: downloading ${archive} (${CODEMAP_TAG})"
if ! curl -sSL --fail -o "${work}/${archive}" "${base_url}/${archive}"; then
  echo "::error::failed to download ${base_url}/${archive} — does release ${CODEMAP_TAG} exist and carry this platform's archive?" >&2
  exit 1
fi
if ! curl -sSL --fail -o "${work}/checksums.txt" "${base_url}/checksums.txt"; then
  echo "::error::failed to download ${base_url}/checksums.txt" >&2
  exit 1
fi

expected="$(grep -F " ${archive}" "${work}/checksums.txt" | awk '{print $1}' | head -n1)"
if [[ -z "$expected" ]]; then
  echo "::error::${archive} is not listed in ${CODEMAP_TAG}'s checksums.txt" >&2
  exit 1
fi

if command -v sha256sum >/dev/null 2>&1; then
  actual="$(sha256sum "${work}/${archive}" | awk '{print $1}')"
elif command -v shasum >/dev/null 2>&1; then
  actual="$(shasum -a 256 "${work}/${archive}" | awk '{print $1}')"
else
  echo "::error::neither sha256sum nor shasum is available on this runner to verify the download" >&2
  exit 1
fi

if [[ "$expected" != "$actual" ]]; then
  echo "::error::checksum mismatch for ${archive}: checksums.txt says ${expected}, downloaded file hashes to ${actual}" >&2
  exit 1
fi

if [[ "$CODEMAP_ARCHIVE_EXT" == "zip" ]]; then
  unzip -q -o "${work}/${archive}" -d "$CODEMAP_BIN_DIR"
else
  tar -xzf "${work}/${archive}" -C "$CODEMAP_BIN_DIR"
fi

chmod +x "$bin_path" 2>/dev/null || true

if [[ ! -x "$bin_path" ]]; then
  echo "::error::extraction succeeded but ${bin_path} is missing or not executable" >&2
  exit 1
fi

echo "${CODEMAP_BIN_DIR}" >> "${GITHUB_PATH:?}"
echo "codemap-action: installed ${CODEMAP_TAG} to ${bin_path} (checksum verified)"
