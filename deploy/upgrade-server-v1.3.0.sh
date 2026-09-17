#!/usr/bin/env bash
set -euo pipefail

readonly version='v1.3.0'
readonly release_base="https://github.com/100askTeam/dlfilewebsite/releases/download/dladmin-go-${version}"
readonly source_commit='fb6783f713c1d649e569c029b7b91908f3225fe2'
readonly source_base="https://raw.githubusercontent.com/100askTeam/dlfilewebsite/${source_commit}"
readonly state_dir='/home1/dlfile-state'
readonly code_build_dir='/home1/dladmin-code/current/build'
readonly wrapper_path='/usr/local/libexec/dl-release-command'
readonly unit_path='/etc/systemd/system/dladmin-go.service'
readonly keyring_dir='/etc/dladmin/release-keys'

if [[ ${EUID:-$(id -u)} -ne 0 ]]; then
  echo 'upgrade-server-v1.3.0.sh must run as root' >&2
  exit 2
fi

for required in curl gzip sha256sum install systemctl pgrep sleep seq; do
  command -v "$required" >/dev/null || {
    echo "required command is missing: $required" >&2
    exit 2
  }
done

test -d "$state_dir"
test -d "$code_build_dir"
test -x "$code_build_dir/dladmin-go"
test -x "$code_build_dir/dlctl"
test -x "$wrapper_path"
test -s "${keyring_dir}/lynx.pub"
test -s "${keyring_dir}/usbtoolbox.pub"

upgrade_dir="$(mktemp -d "${state_dir}/server-upgrade-${version}.XXXXXX")"
stamp="$(date +%Y%m%d%H%M%S)"

fetch_with_resume() {
  local url=$1
  local partial=$2
  local attempt

  for attempt in {1..6}; do
    if curl -fL --speed-limit 1024 --speed-time 60 \
      --continue-at - "$url" -o "$partial"; then
      return 0
    fi
    echo "download attempt ${attempt}/6 failed; retrying in 2 seconds: ${url}" >&2
    sleep 2
  done
  return 1
}

download() {
  local file=$1
  local destination="${upgrade_dir}/${file}"
  local partial="${destination}.part"

  if ! fetch_with_resume "${release_base}/${file}" "$partial"; then
    if [[ "$file" != 'dladmin-go.service' ]]; then
      return 1
    fi
    echo 'release download failed; retrying dladmin-go.service from the pinned source commit' >&2
    rm -f "$partial"
    fetch_with_resume \
      "${source_base}/deploy/systemd/dladmin-go.service" "$partial"
  fi

  mv "$partial" "$destination"
}

for file in dladmin-go.gz dlctl.gz dl-release-command dladmin-go.service; do
  download "$file"
done

cat >"${upgrade_dir}/SHA256SUMS.required" <<'SUMS'
1a4894901b49cc5fc2424ff645f5c74cd6c7ce9a903ab5ced932a2082b710c5e  dladmin-go.gz
48a838aabd5eabe8f5e5fbc4f139f93450dea17be36e7aaa9808b97111d1bf0d  dlctl.gz
3e5d21b0d8d80a6ccbfcba3d7d3a051d86ae324510dd9d2e37d4d839061a86be  dl-release-command
8adbb831c7a24d0464c7e3e152b14a5cf5dc2f77fca0cb5e1c0901b008e3ac1b  dladmin-go.service
SUMS
(cd "$upgrade_dir" && sha256sum -c SHA256SUMS.required)

gzip -dc "${upgrade_dir}/dladmin-go.gz" >"${upgrade_dir}/dladmin-go"
gzip -dc "${upgrade_dir}/dlctl.gz" >"${upgrade_dir}/dlctl"
chmod 0755 \
  "${upgrade_dir}/dladmin-go" \
  "${upgrade_dir}/dlctl" \
  "${upgrade_dir}/dl-release-command"
bash "${upgrade_dir}/dl-release-command" --self-test

cp -a "${code_build_dir}/dladmin-go" \
  "${code_build_dir}/dladmin-go.before-${version}.${stamp}"
cp -a "${code_build_dir}/dlctl" \
  "${code_build_dir}/dlctl.before-${version}.${stamp}"
cp -a "$wrapper_path" "${wrapper_path}.before-${version}.${stamp}"
if [[ -f "$unit_path" ]]; then
  cp -a "$unit_path" "${unit_path}.before-${version}.${stamp}"
fi

install -o root -g root -m 0755 "${upgrade_dir}/dladmin-go" \
  "${code_build_dir}/dladmin-go.new"
mv "${code_build_dir}/dladmin-go.new" "${code_build_dir}/dladmin-go"
install -o root -g root -m 0755 "${upgrade_dir}/dlctl" \
  "${code_build_dir}/dlctl.new"
mv "${code_build_dir}/dlctl.new" "${code_build_dir}/dlctl"
install -o root -g root -m 0755 "${upgrade_dir}/dl-release-command" \
  "${wrapper_path}.new"
mv "${wrapper_path}.new" "$wrapper_path"
install -o root -g root -m 0644 "${upgrade_dir}/dladmin-go.service" "$unit_path"

old_pid="$(pgrep -f '^/home1/dladmin-code/current/build/dladmin-go$' | head -n 1 || true)"
if [[ -n "$old_pid" ]]; then
  kill -TERM "$old_pid"
  for _ in $(seq 1 20); do
    kill -0 "$old_pid" 2>/dev/null || break
    sleep 1
  done
  if kill -0 "$old_pid" 2>/dev/null; then
    echo 'old dladmin-go did not stop in 20 seconds; it was not force-killed' >&2
    exit 4
  fi
fi

systemctl daemon-reload
systemctl enable --now dladmin-go
for _ in $(seq 1 30); do
  if curl -fsS -o /dev/null http://127.0.0.1:5001/; then
    break
  fi
  sleep 1
done

systemctl is-active --quiet dladmin-go
test "$(SSH_ORIGINAL_COMMAND=release-channel-probe-v4 "$wrapper_path")" = \
  'dl-release-command protocol=4'
curl -fsS -o /dev/null \
  https://dl.100ask.net/Tools/lynx/stable/0.9.1/release-set.json

main_pid_property="$(systemctl show -p MainPID dladmin-go)"
main_pid="${main_pid_property#MainPID=}"
echo "dladmin-go ${version} is active; PID=${main_pid}"
echo "Protocol: $(SSH_ORIGINAL_COMMAND=release-channel-probe-v4 "$wrapper_path")"
echo "LYNX key: $(head -n 1 "${keyring_dir}/lynx.pub")"
echo "USBToolBox key: $(head -n 1 "${keyring_dir}/usbtoolbox.pub")"
echo "Backup timestamp: ${stamp}"
echo "Verified downloads: ${upgrade_dir}"
