#!/usr/bin/env bash
set -euo pipefail

source_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
test_root=$(mktemp -d)
cleanup() {
	chmod -R u+w "$test_root" 2>/dev/null || true
	rm -rf -- "$test_root"
}
trap cleanup EXIT
site="$test_root/site"
upload="$test_root/publication"
export GNUPGHOME="$test_root/gnupg"
mkdir -m 0700 "$GNUPGHOME"
mkdir -p "$site/_apt-upload" "$upload/pool/common/d/dspi-config" \
	"$upload/dists/noble-testing/common/binary-all" \
	"$upload/dists/noble-testing/common/binary-arm64" \
	"$upload/catalog/suites/noble-testing" \
	"$upload/catalog/releases/dshanpi-a1-cm5"

gpg --batch --passphrase '' --quick-gen-key 'APT Import Test <apt-test@dshanpi.invalid>' rsa2048 sign 0 >/dev/null 2>&1
fingerprint=$(gpg --batch --with-colons --list-secret-keys | awk -F: '$1 == "fpr" {print $10; exit}')
gpg --batch --export "$fingerprint" > "$test_root/trusted.gpg"

package_root="$test_root/package"
mkdir -p "$package_root/DEBIAN" "$package_root/usr/share/dspi-config"
cat > "$package_root/DEBIAN/control" <<'EOF'
Package: dspi-config
Version: 1.0.0-1
Architecture: all
Maintainer: DShanPI <support@dshanpi.com>
Section: admin
Priority: optional
Description: APT importer fixture
EOF
echo fixture > "$package_root/usr/share/dspi-config/fixture"
deb="$upload/pool/common/d/dspi-config/dspi-config_1.0.0-1_all.deb"
dpkg-deb --root-owner-group --build "$package_root" "$deb" >/dev/null

(
	cd "$upload"
	dpkg-scanpackages --multiversion pool/common /dev/null > dists/noble-testing/common/binary-all/Packages 2>/dev/null
	: > dists/noble-testing/common/binary-arm64/Packages
	for arch in all arm64; do
		gzip -n -c "dists/noble-testing/common/binary-$arch/Packages" > "dists/noble-testing/common/binary-$arch/Packages.gz"
		xz -9 -c "dists/noble-testing/common/binary-$arch/Packages" > "dists/noble-testing/common/binary-$arch/Packages.xz"
	done
)
release="$upload/dists/noble-testing/Release"
cat > "$release" <<'EOF'
Origin: DShanPI
Label: DShanPI
Suite: noble-testing
Codename: noble-testing
Architectures: all arm64
Components: common
Description: importer fixture
NotAutomatic: yes
ButAutomaticUpgrades: no
SHA256:
EOF
(
	cd "$upload/dists/noble-testing"
	find common -type f -print0 | sort -z | while IFS= read -r -d '' path; do
		printf ' %s %16d %s\n' "$(sha256sum "$path" | awk '{print $1}')" "$(stat -c %s "$path")" "$path"
	done
) >> "$release"
gpg --batch --yes --local-user "$fingerprint" --clearsign --output "$upload/dists/noble-testing/InRelease" "$release"
gpg --batch --yes --local-user "$fingerprint" --armor --detach-sign --output "$upload/dists/noble-testing/Release.gpg" "$release"

deb_sha=$(sha256sum "$deb" | awk '{print $1}')
printf 'dspi-config\t1.0.0-1\tall\tpool/common/d/dspi-config/%s\t%s\n' "$(basename "$deb")" "$deb_sha" \
	> "$upload/catalog/suites/noble-testing/common.tsv"
cat > "$upload/catalog/releases/dshanpi-a1-cm5/2026.09.30-1.json" <<- EOF
{"schema_version":1,"product":"dshanpi-a1-cm5","version":"2026.09.30-1","channel":"testing","suite":"noble-testing","packages":[]}
EOF
gpg --batch --yes --local-user "$fingerprint" --armor --detach-sign \
	--output "$upload/catalog/releases/dshanpi-a1-cm5/2026.09.30-1.json.asc" \
	"$upload/catalog/releases/dshanpi-a1-cm5/2026.09.30-1.json"
cat > "$upload/PUBLICATION.json" <<'EOF'
{
  "schema_version": 1,
  "channel": "testing",
  "suite": "noble-testing",
  "product": "dshanpi-a1-cm5",
  "component": "dshanpi-a1-cm5",
  "version": "2026.09.30-1",
  "expected_base_state_sha256": "none"
}
EOF
(
	cd "$upload"
	find PUBLICATION.json catalog dists pool -type f -print0 | sort -z | xargs -0 sha256sum > SHA256SUMS
)
gpg --batch --yes --local-user "$fingerprint" --armor --detach-sign \
	--output "$upload/SHA256SUMS.asc" "$upload/SHA256SUMS"

job=20260930T120000Z-0123456789abcdef
stale=20260930T120001Z-0123456789abcdef
tampered=20260930T120002Z-0123456789abcdef
cp -a "$upload" "$site/_apt-upload/$job"
cp -a "$upload" "$site/_apt-upload/$stale"
cp -a "$upload" "$site/_apt-upload/$tampered"
printf 'tamper\n' >> "$site/_apt-upload/$tampered/pool/common/d/dspi-config/$(basename "$deb")"

DSHANPI_APT_SITE_ROOT="$site" DSHANPI_APT_TRUSTED_KEY="$test_root/trusted.gpg" \
	bash "$source_root/deploy/apt/activate-apt-upload.sh" "$job" >/dev/null
[[ -L "$site/apt/dists/noble-testing" && -L "$site/apt/catalog" ]]
[[ -f "$site/apt/pool/common/d/dspi-config/$(basename "$deb")" ]]
[[ ! -e "$site/_apt-upload/$job" ]]
if DSHANPI_APT_SITE_ROOT="$site" DSHANPI_APT_TRUSTED_KEY="$test_root/trusted.gpg" \
	bash "$source_root/deploy/apt/activate-apt-upload.sh" "$stale" >/dev/null 2>&1; then
	echo "stale repository publication unexpectedly passed" >&2
	exit 1
fi
empty="$test_root/empty"
mkdir -p "$empty"
if python3 "$source_root/deploy/apt/verify-apt-upload.py" \
	"$site/_apt-upload/$tampered" "$test_root/trusted.gpg" "$empty" >/dev/null 2>&1; then
	echo "tampered repository publication unexpectedly passed" >&2
	exit 1
fi

echo "APT import tests passed"
