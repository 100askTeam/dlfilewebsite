#!/usr/bin/env bash
set -euo pipefail

[[ $# -eq 1 ]] || { echo "Usage: $0 JOB_ID" >&2; exit 2; }
job_id=$1
[[ "$job_id" =~ ^[0-9]{8}T[0-9]{6}Z-[0-9a-f]{16}$ ]] || { echo "Invalid APT upload job id" >&2; exit 2; }
script_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
site_root=${DSHANPI_APT_SITE_ROOT:-/home1/dlfile}
trusted_key=${DSHANPI_APT_TRUSTED_KEY:-/etc/dladmin/apt-keys/dshanpi.gpg}
site_root=${site_root%/}
[[ "$site_root" == /* && "$site_root" != / ]] || { echo "Invalid site root" >&2; exit 1; }
[[ -f "$trusted_key" ]] || { echo "Trusted APT public key is missing: $trusted_key" >&2; exit 1; }
upload="$site_root/_apt-upload/$job_id"
repository="$site_root/apt"
metadata_root="$site_root/_apt-metadata"
[[ -d "$upload" && ! -L "$upload" ]] || { echo "APT upload is missing: $job_id" >&2; exit 1; }

mkdir -p "$repository" "$metadata_root/dists" "$metadata_root/catalog"
exec 9> "$site_root/.apt-import.lock"
flock -x 9
python3 "$script_dir/verify-apt-upload.py" "$upload" "$trusted_key" "$repository"

while IFS= read -r -d '' source; do
	relative=${source#"$upload/"}
	target="$repository/$relative"
	if [[ -f "$target" ]]; then
		cmp -s "$source" "$target" || { echo "Immutable APT pool collision: $relative" >&2; exit 1; }
		continue
	fi
	install -d -m 0755 "$(dirname "$target")"
	temporary="${target}.new.$job_id"
	install -m 0444 "$source" "$temporary"
	mv -T "$temporary" "$target"
done < <(find "$upload/pool" -type f -print0)

for source_suite in "$upload"/dists/*; do
	[[ -d "$source_suite" ]] || continue
	suite=$(basename "$source_suite")
	[[ "$suite" =~ ^[a-z0-9][a-z0-9.+-]*$ ]] || { echo "Unsafe suite: $suite" >&2; exit 1; }
	version_dir="$metadata_root/dists/$suite/$job_id"
	[[ ! -e "$version_dir" ]] || { echo "Metadata version already exists: $suite/$job_id" >&2; exit 1; }
	install -d -m 0755 "$(dirname "$version_dir")"
	cp -a --no-preserve=ownership "$source_suite" "$version_dir"
	find "$version_dir" -type f -exec chmod 0444 {} +
	find "$version_dir" -type d -exec chmod 0555 {} +
	install -d -m 0755 "$repository/dists"
	temporary_link="$repository/dists/.${suite}.new.$job_id"
	ln -s "../../_apt-metadata/dists/$suite/$job_id" "$temporary_link"
	mv -Tf "$temporary_link" "$repository/dists/$suite"
done

catalog_version="$metadata_root/catalog/$job_id"
cp -a --no-preserve=ownership "$upload/catalog" "$catalog_version"
find "$catalog_version" -type f -exec chmod 0444 {} +
find "$catalog_version" -type d -exec chmod 0555 {} +
catalog_link="$repository/.catalog.new.$job_id"
ln -s "../_apt-metadata/catalog/$job_id" "$catalog_link"
mv -Tf "$catalog_link" "$repository/catalog"

for name in PUBLICATION.json SHA256SUMS SHA256SUMS.asc; do
	temporary="$repository/.${name}.new.$job_id"
	install -m 0444 "$upload/$name" "$temporary"
	mv -T "$temporary" "$repository/$name"
done

find "$upload" -depth -delete
echo "Activated signed APT publication $job_id"
