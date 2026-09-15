#!/usr/bin/env bash
set -euo pipefail

readonly version='v1.2.0'
readonly release_base="https://github.com/100askTeam/dlfilewebsite/releases/download/dladmin-go-${version}"
readonly state_dir='/home1/dlfile-state'
readonly code_build_dir='/home1/dladmin-code/current/build'
readonly wrapper_path='/usr/local/libexec/dl-release-command'
readonly unit_path='/etc/systemd/system/dladmin-go.service'
readonly keyring_dir='/etc/dladmin/release-keys'

if [[ ${EUID:-$(id -u)} -ne 0 ]]; then
  echo 'upgrade-server-v1.2.0.sh must run as root' >&2
  exit 2
fi

for required in curl gzip sha256sum install systemctl pgrep; do
  command -v "$required" >/dev/null || {
    echo "required command is missing: $required" >&2
    exit 2
  }
done

test -d "$state_dir"
test -d "$code_build_dir"
test -s /etc/dladmin/release.pub
test -x "$code_build_dir/dladmin-go"
test -x "$code_build_dir/dlctl"
test -x "$wrapper_path"

upgrade_dir="$(mktemp -d "${state_dir}/server-upgrade-${version}.XXXXXX")"
stamp="$(date +%Y%m%d%H%M%S)"

download() {
  local file=$1
  curl -fL --retry 5 --retry-delay 2 \
    "${release_base}/${file}" -o "${upgrade_dir}/${file}"
}

for file in dladmin-go.gz dlctl.gz dl-release-command dladmin-go.service; do
  download "$file"
done

cat >"${upgrade_dir}/SHA256SUMS.required" <<'SUMS'
6606a5c714a2816aea087917f48e5c4e6083ec75900a8544633728b86840f955  dladmin-go.gz
e866022bdbe1267ac7db4378d324a40c1569af6f8545175c7194b1971f43c6a8  dlctl.gz
492d591660df8517fe62db6928888f6b612a0b6faa1ced6841fa4a3218f6d6a8  dl-release-command
8adbb831c7a24d0464c7e3e152b14a5cf5dc2f77fca0cb5e1c0901b008e3ac1  dladmin-go.service
SUMS
(cd "$upgrade_dir" && sha256sum -c SHA256SUMS.required)

gzip -dc "${upgrade_dir}/dladmin-go.gz" >"${upgrade_dir}/dladmin-go"
gzip -dc "${upgrade_dir}/dlctl.gz" >"${upgrade_dir}/dlctl"
chmod 0755 \
  "${upgrade_dir}/dladmin-go" \
  "${upgrade_dir}/dlctl" \
  "${upgrade_dir}/dl-release-command"
bash "${upgrade_dir}/dl-release-command" --self-test

install -d -o root -g root -m 0755 "$keyring_dir"
install -o root -g root -m 0644 /etc/dladmin/release.pub \
  "${keyring_dir}/lynx.pub.new"
mv "${keyring_dir}/lynx.pub.new" "${keyring_dir}/lynx.pub"
printf '%s\n' \
  'untrusted comment: minisign public key: 571E48733E4D7891' \
  'RWSReE0+c0geV+D7+QQbZ2uDcbB6kVRm/ahjmYt93V6RyHLJYxLaf8gc' \
  >"${keyring_dir}/usbtoolbox.pub.new"
chmod 0644 "${keyring_dir}/usbtoolbox.pub.new"
chown root:root "${keyring_dir}/usbtoolbox.pub.new"
mv "${keyring_dir}/usbtoolbox.pub.new" "${keyring_dir}/usbtoolbox.pub"

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
test "$(SSH_ORIGINAL_COMMAND=release-channel-probe-v3 "$wrapper_path")" = \
  'dl-release-command protocol=3'
curl -fsS -o /dev/null \
  https://dl.100ask.net/Tools/lynx/stable/0.9.1/release-set.json

echo "dladmin-go ${version} is active; PID=$(systemctl show -p MainPID --value dladmin-go)"
echo "LYNX key: $(head -n 1 "${keyring_dir}/lynx.pub")"
echo "USBToolBox key: $(head -n 1 "${keyring_dir}/usbtoolbox.pub")"
echo "Backup timestamp: ${stamp}"
echo "Verified downloads: ${upgrade_dir}"
