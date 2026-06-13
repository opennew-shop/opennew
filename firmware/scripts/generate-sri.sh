#!/usr/bin/env bash
#
# generate-sri.sh — Generate SRI (Subresource Integrity) hashes for ANCF firmware components
#
# Reads all .js files from the dist/ directory, computes their SHA-384 hash,
# and outputs SRI integrity strings in the format sha384-<base64hash>.
# Also generates a firmware-manifest.json file compatible with the
# ANCF Discovery Manifest ui_firmware.components field.
#
# Usage:
#   bash generate-sri.sh [dist-dir] [output-file]
#
# Arguments:
#   dist-dir      Path to dist/ directory (default: ../components/dist)
#   output-file   Path for firmware-manifest.json (default: <dist-dir>/firmware-manifest.json)

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
DIST_DIR="${1:-${SCRIPT_DIR}/../components/dist}"
OUTPUT_FILE="${2:-${DIST_DIR}/firmware-manifest.json}"

# Resolve to absolute paths
DIST_DIR="$(cd "$DIST_DIR" 2>/dev/null && pwd || echo "$DIST_DIR")"

echo "ANCF Firmware SRI Generator"
echo "==========================="
echo "Dist directory: ${DIST_DIR}"
echo "Output file:    ${OUTPUT_FILE}"
echo ""

if [ ! -d "$DIST_DIR" ]; then
    echo "ERROR: Dist directory not found: ${DIST_DIR}" >&2
    echo "Run 'tsc' first to compile TypeScript components." >&2
    exit 1
fi

# Collect .js files
shopt -s nullglob
js_files=("$DIST_DIR"/*.js)
shopt -u nullglob

if [ ${#js_files[@]} -eq 0 ]; then
    echo "ERROR: No .js files found in ${DIST_DIR}" >&2
    echo "Run 'tsc' first to compile TypeScript components." >&2
    exit 1
fi

echo "Found ${#js_files[@]} component file(s):"

manifest_components="["
first=true

for file in "${js_files[@]}"; do
    filename=$(basename "$file")
    echo -n "  Processing: ${filename} ..."

    # Compute SHA-384 (SRI)
    sha384_raw=$(openssl dgst -sha384 -binary "$file" | openssl base64 -A)
    sri="sha384-${sha384_raw}"

    # Compute SHA-256 for short content hash
    short_hash=$(openssl dgst -sha256 -binary "$file" | openssl base64 -A | head -c 12 | xxd -p -c 100)

    # File size
    size_bytes=$(stat -c%s "$file" 2>/dev/null || stat -f%z "$file" 2>/dev/null || echo 0)

    echo " SRI: ${sri}"
    echo "       Short hash: ${short_hash}"
    echo "       Size: ${size_bytes} bytes"

    # Build component entry
    base_name="${filename%.js}"
    hash_name="${base_name}.${short_hash}.js"

    # Copy with hash-named filename
    cp "$file" "${DIST_DIR}/${hash_name}"
    echo "       Copied to: ${hash_name}"

    # JSON entry
    comma=""
    if [ "$first" = false ]; then comma=","; fi
    first=false

    manifest_components+="${comma}
    {
      \"name\": \"${base_name}\",
      \"url\": \"https://cdn.yourshop.com/firmware/v1/${hash_name}\",
      \"integrity\": \"${sri}\",
      \"type\": \"module\",
      \"size_bytes\": ${size_bytes},
      \"short_hash\": \"${short_hash}\"
    }"
done

# Build manifest JSON
manifest_json=$(cat <<EOFM
{
  "generated_at": "$(date -u +"%Y-%m-%dT%H:%M:%SZ")",
  "components": ${manifest_components}
]
}
EOFM
)

echo "$manifest_json" > "$OUTPUT_FILE"

echo ""
echo "Firmware manifest written to: ${OUTPUT_FILE}"
echo ""

# Print summary
echo "SRI Integrity Hashes:"
printf "%-30s %s\n" "Component" "SRI (sha384)"
printf "%-30s %s\n" "---------" "--------------"
for file in "${js_files[@]}"; do
    filename=$(basename "$file")
    sha384_raw=$(openssl dgst -sha384 -binary "$file" | openssl base64 -A)
    printf "%-30s sha384-%s...\n" "$filename" "${sha384_raw:0:20}"
done

echo ""
echo "Done. Copy the integrity values into your manifest's ui_firmware.components[].integrity fields."
