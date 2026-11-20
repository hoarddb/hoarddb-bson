#!/usr/bin/env bash
# scripts/check-synced.sh - verifies the files hoardDB's scripts/sync-bson-library.sh
# copied into this repository: every line of testdata/vectors/SHA256SUMS (the
# vectors, scripts/lint-tool.sh and .server-go-version) must match, and no file
# may sit in testdata/vectors without a line. Needs only bash and sha256sum, so
# it runs in the public CI without the hoardDB tree.
#
# To repair a drift, run in a hoardDB checkout:
#   scripts/sync-bson-library.sh <path to this repository>
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
vectors="$root/testdata/vectors"
fix="run 'scripts/sync-bson-library.sh $root' in a hoardDB checkout, and commit the result"

if [ ! -f "$vectors/SHA256SUMS" ]; then
    echo "check-synced.sh: $vectors/SHA256SUMS is missing. To create it, $fix." >&2
    exit 1
fi
if ! (cd "$vectors" && sha256sum --check --strict --quiet SHA256SUMS); then
    echo "check-synced.sh: a synced file differs from SHA256SUMS. If the change was deliberate in hoardDB, $fix; otherwise restore the file with git." >&2
    exit 1
fi

# A file nobody hashed would be trusted silently.
unlisted=0
while IFS= read -r found; do
    if ! grep -qF -- "  $found" "$vectors/SHA256SUMS"; then
        echo "check-synced.sh: testdata/vectors/$found is not listed in SHA256SUMS. To list it, $fix." >&2
        unlisted=1
    fi
done < <(cd "$vectors" && find . -type f ! -name SHA256SUMS | sed 's|^\./||' | LC_ALL=C sort)
[ "$unlisted" -eq 0 ] || exit 1

if ! grep -Eqx '[0-9]+\.[0-9]+(\.[0-9]+)?' "$root/.server-go-version"; then
    echo "check-synced.sh: .server-go-version is not a Go version such as 1.26.9. To restore it, $fix." >&2
    exit 1
fi
echo "check-synced.sh: ok"
