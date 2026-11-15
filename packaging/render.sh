#!/bin/sh
# render.sh fills in the packaging templates for one release:
#
#   packaging/render.sh 1.2.0 dist/SHA256SUMS out/
#
# Each template's @VERSION@ becomes the version, @DATE@ today's date, and
# @SHA256_<NAME>@ the checksum SHA256SUMS lists for viewrab-<version>-<name>,
# with <name> in capitals and its dashes as underscores: LINUX_AMD64 for
# viewrab-1.2.0-linux-amd64.tar.gz, WINDOWS_AMD64_SETUP for the installer.
# The filled-in files land in out/, in the layout of packaging/.
set -eu

if [ $# -ne 3 ]; then
	echo "usage: $0 version SHA256SUMS outdir" >&2
	exit 2
fi
version=$1 sums=$2 out=$3
here=$(dirname "$0")

script="s|@VERSION@|$version|g; s|@DATE@|$(date -u +%Y-%m-%d)|g;"
while read -r hash file; do
	name=${file#viewrab-"$version"-}
	name=${name%.tar.gz}
	name=${name%.zip}
	name=${name%.exe}
	name=$(printf '%s' "$name" | tr 'a-z-' 'A-Z_')
	script="$script s|@SHA256_${name}@|$hash|g;"
done <"$sums"

# The templates are the files that name the version.
for t in $(cd "$here" && grep -rl --exclude=render.sh '@VERSION@' .); do
	t=${t#./}
	mkdir -p "$out/$(dirname "$t")"
	sed "$script" "$here/$t" >"$out/$t"
	if grep -n '@[A-Z0-9_]*@' "$out/$t" >&2; then
		echo "$0: $t: no value for the placeholders above" >&2
		exit 1
	fi
done
