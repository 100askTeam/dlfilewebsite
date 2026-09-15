#!/usr/bin/env bash
set -euo pipefail

if [[ $# -ne 1 ]]; then
  echo "usage: DL_REMOTE=user@host $0 RELEASE_DIRECTORY" >&2
  exit 2
fi

release_dir=${1%/}
remote=${DL_REMOTE:?DL_REMOTE is required, for example root@dl.100ask.net}
remote_incoming=${DL_REMOTE_INCOMING:-/home1/dlfile-state/incoming}
remote_dlctl=${DL_REMOTE_DLCTL:-/home1/dladmin-code/current/build/dlctl}
public_base_url=${DL_PUBLIC_BASE_URL:-https://dl.100ask.net}

if [[ ! "$remote_incoming" =~ ^/[A-Za-z0-9_./-]+$ ]] ||
  [[ ! "$remote_dlctl" =~ ^/[A-Za-z0-9_./-]+$ ]]; then
  echo "remote incoming and dlctl paths must be absolute safe paths" >&2
  exit 2
fi
if [[ ! -f "$release_dir/release-set.json" ]] || [[ -L "$release_dir/release-set.json" ]]; then
  echo "release-set.json is missing or unsafe in $release_dir" >&2
  exit 2
fi

for tool in curl jq scp sha256sum ssh; do
  command -v "$tool" >/dev/null || {
    echo "$tool is required to restore a release" >&2
    exit 2
  }
done

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
version=$(jq -er '.version | sub("^v"; "")' "$release_dir/release-set.json")
probe_target=$(jq -er '[.assets[] | select(.kind == "full")][0].target' "$release_dir/release-set.json")
probe_sha256=$(jq -er --arg target "$probe_target" \
  '[.assets[] | select(.kind == "full" and .target == $target)][0].sha256' \
  "$release_dir/release-set.json")
if [[ ! "$product" =~ ^[a-z][a-z0-9_-]{1,63}$ ]] ||
  [[ ! "$channel" =~ ^(stable|beta|nightly)$ ]] ||
  [[ ! "$probe_target" =~ ^[a-z][a-z0-9_-]{1,63}$ ]] ||
  [[ ! "$version" =~ ^[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z][0-9A-Za-z.-]*)?$ ]] ||
  [[ ! "$probe_sha256" =~ ^[0-9a-f]{64}$ ]] ||
  [[ ! "$public_base_url" =~ ^https://[A-Za-z0-9.-]+(:[0-9]+)?$ ]]; then
  echo "release identity or DL_PUBLIC_BASE_URL is unsafe" >&2
  exit 2
fi

release_url="$public_base_url/Tools/$product/$channel/$version"
live_manifest=$(mktemp)
trap 'rm -f "$live_manifest"' EXIT

# A rerun is successful only when the complete public release is already an
# exact copy of this signed set. A partial or different directory is never
# overwritten by the recovery path.
if curl --fail --location --silent --show-error --retry 2 \
  "$release_url/release-set.json" --output "$live_manifest" 2>/dev/null; then
  if ! cmp -s "$release_dir/release-set.json" "$live_manifest"; then
    echo "public release manifest already exists but differs from the recovery set" >&2
    exit 1
  fi
  for name in "${!seen_assets[@]}"; do
    expected=$(jq -er --arg name "$name" \
      '[.assets[] | select(.file == $name)][0].sha256' "$release_dir/release-set.json")
    actual=$(curl --fail --location --silent --show-error --retry 3 --retry-all-errors \
      "$release_url/$name" | sha256sum | awk '{print $1}')
    if [[ "$actual" != "$expected" ]]; then
      echo "public release asset differs: $name" >&2
      exit 1
    fi
  done
  echo "release is already restored and verified: $release_url/"
  exit 0
fi

job_id="recover-${product}-${version}-${GITHUB_RUN_ID:-manual}-${GITHUB_RUN_ATTEMPT:-1}"
job_id=${job_id:0:120}
if [[ ! "$job_id" =~ ^[A-Za-z0-9][A-Za-z0-9._-]{0,119}$ ]]; then
  echo "generated recovery identifier is invalid" >&2
  exit 2
fi

ssh -o BatchMode=yes -o IdentitiesOnly=yes "$remote" \
  "install -d -m 0750 '$remote_incoming/.part-$job_id'"
# Upload one file per SCP process. OpenSSH adds a remote `-d` flag for a
# multi-source transfer; keeping each transfer singular preserves the narrow
# forced-command contract and avoids retransmitting already completed assets.
for upload_file in "${upload_files[@]}"; do
  uploaded=false
  for attempt in 1 2 3; do
    if scp -O -p -o BatchMode=yes -o IdentitiesOnly=yes \
      -o ServerAliveInterval=15 -o ServerAliveCountMax=4 \
      "$upload_file" "$remote:$remote_incoming/.part-$job_id/"; then
      uploaded=true
      break
    fi
    echo "SCP attempt $attempt failed for $(basename "$upload_file"); retrying" >&2
  done
  if [[ "$uploaded" != true ]]; then
    echo "failed to upload $(basename "$upload_file") after 3 attempts" >&2
    exit 1
  fi
done
ssh -o BatchMode=yes -o IdentitiesOnly=yes "$remote" \
  "mv '$remote_incoming/.part-$job_id' '$remote_incoming/$job_id'"
ssh -o BatchMode=yes -o IdentitiesOnly=yes "$remote" \
  "$remote_dlctl restore-published '$job_id'"

api_response=$(curl --fail --location --silent --show-error --retry 3 --retry-all-errors \
  "$public_base_url/api/v1/updates/$product/$channel/$probe_target/0.0.0")
jq -e --arg version "$version" --arg sha256 "$probe_sha256" \
  '.version == $version and .asset.sha256 == $sha256 and
   (.asset.url | startswith("/Tools/"))' <<<"$api_response" >/dev/null

for name in "${!seen_assets[@]}"; do
  expected=$(jq -er --arg name "$name" \
    '[.assets[] | select(.file == $name)][0].sha256' "$release_dir/release-set.json")
  actual=$(curl --fail --location --silent --show-error --retry 3 --retry-all-errors \
    "$release_url/$name" | sha256sum | awk '{print $1}')
  if [[ "$actual" != "$expected" ]]; then
    echo "reverse-download verification failed: $name" >&2
    exit 1
  fi
done
echo "restored and verified: $release_url/"
