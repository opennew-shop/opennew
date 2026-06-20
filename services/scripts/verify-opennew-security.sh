#!/usr/bin/env bash
set -euo pipefail

cd "$(dirname "$0")/../.."

fail=0

pass() {
	printf 'PASS %s\n' "$1"
}

fail_check() {
	printf 'FAIL %s\n' "$1"
	fail=1
}

check_rg() {
	local desc="$1"
	local pattern="$2"
	local path="$3"
	if rg -n "$pattern" "$path" >/dev/null; then
		pass "$desc"
	else
		fail_check "$desc"
	fi
}

check_absent() {
	local desc="$1"
	local pattern="$2"
	local path="$3"
	if rg -n "$pattern" "$path" >/dev/null; then
		fail_check "$desc"
		rg -n "$pattern" "$path"
	else
		pass "$desc"
	fi
}

check_fixed() {
	local desc="$1"
	local text="$2"
	local path="$3"
	if rg -n -F "$text" "$path" >/dev/null; then
		pass "$desc"
	else
		fail_check "$desc"
	fi
}

check_ignored() {
	local desc="$1"
	local path="$2"
	if git check-ignore -q "$path"; then
		pass "$desc"
	else
		fail_check "$desc"
	fi
}

check_rg "key directory is ignored" '^keys/$' .gitignore
check_rg "pem files are ignored" '^\*\.pem$' .gitignore
check_rg "private key files are ignored" '^\*\.key$' .gitignore
check_ignored "sample admin pem would be ignored" keys/ancf-admin.pem
check_ignored "admin setup script under keys would be ignored" keys/setup-admin.cjs

check_rg "checkout verifies wallet signatures" 'function verifyWalletSignature' test-mock-server.cjs
check_rg "checkout uses EdDSA verify" 'nacl\.sign\.detached\.verify' test-mock-server.cjs
check_rg "checkout rejects missing or none signatures" "signatureB64 === 'none'" test-mock-server.cjs
check_rg "checkout rejects demo signature placeholder" "signatureB64 === 'demo_signature_placeholder'" test-mock-server.cjs
check_rg "checkout idempotency compares body hash" 'cached\.body_hash !== bodyHash' test-mock-server.cjs
check_rg "checkout persists idempotency body hash" 'body_hash: bodyHash' test-mock-server.cjs
check_rg "checkout binds wallet to quote owner" 'wallet does not match quote/intent owner' test-mock-server.cjs
check_rg "checkout enforces per-order quantity cap" 'MAX_QUOTE_QUANTITY' test-mock-server.cjs
check_rg "checkout decrements stock after commit" 'decrementStock\(line\.sku_id' test-mock-server.cjs
check_rg "quote validates stock before issuing quote" 'stockOf\(sku\) < qty' test-mock-server.cjs

check_rg "CORS uses origin allowlist" 'function allowedOrigin' test-mock-server.cjs
check_rg "CORS responses vary by Origin" "'Vary': 'Origin'" test-mock-server.cjs
check_absent "CORS wildcard is not emitted" "Access-Control-Allow-Origin['\"]?: ['\"]\\*" test-mock-server.cjs

check_rg "bad requests return generic messages" 'function badRequest' test-mock-server.cjs
check_absent "mock API does not expose caught error messages in JSON" 'message: e\.message' test-mock-server.cjs
check_absent "mock API does not concatenate caught error messages into client errors" 'Invalid .* \+ e\.message' test-mock-server.cjs
check_rg "positive minor amount validation exists" 'function isPositiveMinor' test-mock-server.cjs
check_rg "quote quantity validation is strict" 'function parsePositiveInteger' test-mock-server.cjs
check_rg "product price validation is strict" 'function parseUnsignedInteger' test-mock-server.cjs

check_rg "renderer disables x-powered-by" "app\.disable\('x-powered-by'\)" agents/node-local-renderer/src/agent.ts
check_absent "renderer has no dead prepResp branch" 'prepResp' agents/node-local-renderer/src/agent.ts
check_absent "renderer no longer pre-fills demo signature placeholders" 'demo_signature_placeholder|demo_sig_' agents/node-local-renderer/src/agent.ts

check_rg "catalog HTML guards payload JSON parse" 'payload = JSON\.parse' firmware/templates/animated-retail/html/ancf-animated-catalog.template.html
check_fixed "catalog HTML guards BigInt parse" 'if (!/^[0-9]+$/.test(raw))' firmware/templates/animated-retail/html/ancf-animated-catalog.template.html
check_rg "detail HTML guards payload JSON parse" 'payload = JSON\.parse' firmware/templates/animated-retail/html/ancf-animated-product-detail.template.html
check_fixed "detail HTML guards BigInt parse" 'if (!/^[0-9]+$/.test(raw))' firmware/templates/animated-retail/html/ancf-animated-product-detail.template.html
check_fixed "catalog Vue guards BigInt parse" 'if (!/^\d+$/.test(raw))' firmware/templates/animated-retail/vue/AncfAnimatedCatalog.vue
check_fixed "detail Vue guards BigInt parse" 'if (!/^\d+$/.test(raw))' firmware/templates/animated-retail/vue/AncfAnimatedProductDetail.vue
check_rg "component total price has BigInt try/catch" 'const totalPrice = computed\(\(\) => \{' firmware/components/src/AncfAnimatedProductDetail.vue
check_fixed "component formatPrice guards BigInt parse" 'if (!/^\d+$/.test(raw))' firmware/components/src/AncfAnimatedProductDetail.vue

if [ "$fail" -ne 0 ]; then
	printf '\nopennew security verification failed.\n'
	exit 1
fi

printf '\nopennew security verification passed.\n'
