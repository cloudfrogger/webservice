#!/bin/sh
set -eu

# Regenerate the embedded assets from the official npm distribution.
version=${1:?usage: update.sh VERSION}
assets=$(cd "$(dirname "$0")" && pwd)/dist
tmpdir=$(mktemp -d)
trap 'rm -rf "$tmpdir"' EXIT
cd "$tmpdir"
archive=$(npm pack --ignore-scripts --silent "swagger-ui-dist@$version")
tar -xzf "$archive"
mkdir -p "$assets"
cd package
for file in swagger-ui-bundle.js swagger-ui-standalone-preset.js swagger-ui.css oauth2-redirect.html oauth2-redirect.js; do
    gzip -n -9 -c "$file" > "$assets/$file.gz"
done
cp LICENSE NOTICE swagger-ui-bundle.js.LICENSE.txt swagger-ui-standalone-preset.js.LICENSE.txt favicon-16x16.png favicon-32x32.png "$assets/"
