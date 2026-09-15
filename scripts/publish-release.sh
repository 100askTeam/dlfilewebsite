#!/usr/bin/env bash
set -euo pipefail

if [[ $# -ne 1 ]]; then
  echo "usage: DL_REMOTE=user@host $0 RELEASE_DIRECTORY" >&2
  exit 2
fi

release_dir=${1%/}
remote=${DL_REMOTE:?DL_REMOTE is required, for example deploy@dl.100ask.net}
remote_incoming=${DL_REMOTE_INCOMING:-/home1/dlfile-state/incoming}
remote_dlctl=${DL_REMOTE_DLCTL:-/home1/dladmin-code/current/build/dlctl}
publish_now=${DL_PUBLISH_NOW:-false}
public_base_url=${DL_PUBLIC_BASE_URL:-https://dl.100ask.net}

if [[ ! "$remote_incoming" =~ ^/[A-Za-z0-9_./-]+$ ]] || [[ ! "$remote_dlctl" =~ ^/[A-Za-z0-9_./-]+$ ]]; then
  echo "remote incoming and dlctl paths must be absolute safe paths" >&2
  exit 2
fi

if [[ ! -f "$release_dir/release-set.json" ]]; then
  echo "release-set.json is missing from $release_dir" >&2
  exit 2
fi

command -v jq >/dev/null || {
  echo "jq is required to select only manifest-declared assets" >&2
  exit 2
}
mapfile -t asset_names < <(jq -er '.assets[].file' "$release_dir/release-set.json")
upload_files=("$release_dir/release-set.json")
declare -A seen_assets=()
for name in "${asset_names[@]}"; do
  if [[ ! "$name" =~ ^[A-Za-z0-9][A-Za-z0-9._+-]{0,199}$ ]] ||
    [[ ! -f "$release_dir/$name" ]] || [[ -L "$release_dir/$name" ]]; then
    echo "unsafe or missing manifest asset: $name" >&2
    exit 2
  fi
  if [[ -n "${seen_assets[$name]:-}" ]]; then
    continue
  fi
  seen_assets[$name]=1
  upload_files+=("$release_dir/$name")
done

product=$(jq -er '.product' "$release_dir/release-set.json")
channel=$(jq -er '.channel' "$release_dir/release-set.json")
version=$(jq -er '.version' "$release_dir/release-set.json")
probe_target=$(jq -er '[.assets[] | select(.kind == "full")][0].target' "$release_dir/release-set.json")
probe_sha256=$(jq -er --arg target "$probe_target" \
  '[.assets[] | select(.kind == "full" and .target == $target)][0].sha256' \
  "$release_dir/release-set.json")
if [[ ! "$product" =~ ^[a-z][a-z0-9_-]{1,63}$ ]] ||
  [[ ! "$channel" =~ ^(stable|beta|nightly)$ ]] ||
  [[ ! "$probe_target" =~ ^[a-z][a-z0-9_-]{1,63}$ ]] ||
  [[ ! "$version" =~ ^[0-9]+\.[0-9]+\.[0-9]+([.-][0-9A-Za-z.-]+)?$ ]] ||
  [[ ! "$probe_sha256" =~ ^[0-9a-f]{64}$ ]] ||
  [[ ! "$public_base_url" =~ ^https://[A-Za-z0-9.-]+(:[0-9]+)?$ ]]; then
  echo "release identity or DL_PUBLIC_BASE_URL is unsafe" >&2
  exit 2
fi

if command -v curl >/dev/null; then
  probe_url="$public_base_url/api/v1/updates/$product/$channel/$probe_target/0.0.0"
  live_response=$(curl --fail --silent --show-error --retry 2 "$probe_url" 2>/dev/null || true)
  live_version=$(jq -r '.version // ""' <<<"$live_response" 2>/dev/null || true)
  if [[ "$live_version" == "$version" ]]; then
    live_sha256=$(jq -r '.asset.sha256 // ""' <<<"$live_response" 2>/dev/null || true)
    if [[ "$live_sha256" != "$probe_sha256" ]]; then
      echo "release $version already exists with a different full asset hash" >&2
      exit 1
    fi
    echo "release $version is already published and matches $probe_sha256"
    exit 0
  fi
fi

job_id="ci-$(date -u +%Y%m%dT%H%M%SZ)-${GITHUB_RUN_ID:-manual}-${GITHUB_RUN_ATTEMPT:-1}"
if [[ ! "$job_id" =~ ^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$ ]]; then
  echo "generated job id is invalid" >&2
  exit 2
fi

ssh "$remote" "install -d -m 0750 '$remote_incoming/.part-$job_id'"
scp -O -p "${upload_files[@]}" "$remote:$remote_incoming/.part-$job_id/"
ssh "$remote" "mv '$remote_incoming/.part-$job_id' '$remote_incoming/$job_id'"

if [[ "$publish_now" == "true" ]]; then
  ssh "$remote" "$remote_dlctl import --publish '$job_id'"
  echo "published: $public_base_url/Tools/$product/releases/$channel/$version/"
else
  ssh "$remote" "$remote_dlctl import '$job_id'"
fi
