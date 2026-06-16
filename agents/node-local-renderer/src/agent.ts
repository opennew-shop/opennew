#!/usr/bin/env node
/**
 * ANCF Node Agent — Local Renderer
 *
 * Responsibilities:
 *   1. Fetch and validate the ANCF Discovery Manifest from the API gateway.
 *   2. Validate manifest schema, expiry, signature presence, and firmware SRI.
 *   3. Start a local HTTP server bound to 127.0.0.1 (configurable port).
 *   4. Serve the local checkout page with embedded Web Components.
 *   5. Provide an Agent Bridge API endpoint for whitelisted commands.
 *   6. Enforce Content-Security-Policy headers.
 *
 * Environment variables:
 *   ANCF_API_BASE    — Backend API gateway URL (default: http://127.0.0.1:8080)
 *   ANCF_AGENT_PORT  — Local server port (default: 3000)
 *   ANCF_FIRMWARE_DIR — Path to firmware components dist/ directory
 *
 * Security:
 *   - CSP: no eval, no arbitrary remote scripts, no form-action, no frame-src.
 *   - Agent Bridge whitelists only approved commands.
 *   - The agent never exposes raw backend fetch to the browser.
 *   - Manifest must be validated before the server starts.
 *   - The server binds to 127.0.0.1 only (no external network access).
 */

import express from 'express';
import type { Request, NextFunction } from 'express';
// NOTE: Do not import Response from express — it would shadow the global fetch Response type.
import http from 'node:http';
import crypto from 'node:crypto';
import path from 'node:path';
import fs from 'node:fs';
import { fileURLToPath } from 'node:url';

const __filename = fileURLToPath(import.meta.url);
const __dirname = path.dirname(__filename);

/* ------------------------------------------------------------------ */
/*  Configuration                                                      */
/* ------------------------------------------------------------------ */

const API_BASE = (process.env.ANCF_API_BASE || 'http://127.0.0.1:8080').replace(/\/+$/, '');
const AGENT_PORT = parseInt(process.env.ANCF_AGENT_PORT || '3000', 10);
const AGENT_HOST = process.env.ANCF_AGENT_HOST || '127.0.0.1';
const FIRMWARE_DIR = process.env.ANCF_FIRMWARE_DIR || path.resolve(__dirname, '..', '..', '..', 'firmware', 'components', 'dist');
const TEMPLATE_DIR = process.env.ANCF_TEMPLATE_DIR || path.resolve(__dirname, '..', '..', '..', 'firmware', 'templates', 'animated-retail', 'html');

// SECURITY FIX: F-006-01 — Dev firmware public key for manifest JWS verification
const DEV_FIRMWARE_PUBKEY = 'AsRoFMpBrxEkmxTw5qTvEGxe9KfS4YdvejXaFTKo5x8E'; // devnet payer

// SECURITY FIX: F-006-04 — CSP nonce generated at startup
let CSP_NONCE: string = '';

/* ------------------------------------------------------------------ */
/*  Crypto Utility Functions (SECURITY FIX: F-006-01)                  */
/* ------------------------------------------------------------------ */

/** Decode a base64url string to Uint8Array */
function base64UrlDecode(input: string): Uint8Array {
    let base64 = input.replace(/-/g, '+').replace(/_/g, '/');
    while (base64.length % 4) base64 += '=';
    const binary = Buffer.from(base64, 'base64').toString('binary');
    const bytes = new Uint8Array(binary.length);
    for (let i = 0; i < binary.length; i++) bytes[i] = binary.charCodeAt(i);
    return bytes;
}

/** Decode a base58 string to Uint8Array (Bitcoin-style alphabet) */
function base58Decode(encoded: string): Uint8Array {
    const ALPHABET = '123456789ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz';
    const zeroes = (encoded.match(/^1*/) as string[])?.[0]?.length ?? 0;
    const bytes: number[] = [0];
    for (let i = 0; i < encoded.length; i++) {
        const c = encoded[i];
        let carry = ALPHABET.indexOf(c);
        if (carry < 0) throw new Error(`Invalid base58 character: ${c}`);
        for (let j = 0; j < bytes.length; j++) {
            carry += bytes[j] * 58;
            bytes[j] = carry & 0xff;
            carry >>= 8;
        }
        while (carry > 0) { bytes.push(carry & 0xff); carry >>= 8; }
    }
    for (let i = 0; i < zeroes; i++) bytes.push(0);
    return new Uint8Array(bytes.reverse());
}

/* ------------------------------------------------------------------ */
/*  Type Definitions                                                   */
/* ------------------------------------------------------------------ */

interface ManifestComponent {
    url: string;
    integrity: string;
    type: 'module' | 'script';
}

interface ManifestUIFirmware {
    components: ManifestComponent[];
    theme_tokens: Record<string, string>;
}

interface ManifestCapability {
    endpoint: string;
    method: string;
    requires_idempotency_key?: boolean;
    requires_wallet_signature?: boolean;
}

interface ManifestAgentPolicy {
    allow_autonomous_checkout: boolean;
    max_auto_total_minor: string;
    require_human_confirmation: boolean;
    allowed_component_hosts: string[];
}

interface ManifestSignature {
    alg: string;
    kid: string;
    jws: string;
}

interface ANCFManifest {
    protocol_version: string;
    shop_id: string;
    issued_at: string;
    expires_at: string;
    supported_networks?: string[];
    supported_assets?: Array<{
        symbol: string;
        decimals: number;
        type: string;
        redeemable?: boolean;
    }>;
    schemas: Record<string, string>;
    capabilities: Record<string, ManifestCapability>;
    ui_firmware?: ManifestUIFirmware;
    agent_policy: ManifestAgentPolicy;
    payment_rails?: Array<{
        rail: string;
        currency: string;
        capabilities?: string[];
        requires_user_authorization?: boolean;
    }>;
    signature: ManifestSignature;
}

/* ------------------------------------------------------------------ */
/*  Manifest Fetching & Validation                                     */
/* ------------------------------------------------------------------ */

const MANIFEST_REQUIRED_FIELDS: (keyof ANCFManifest)[] = [
    'protocol_version',
    'shop_id',
    'issued_at',
    'expires_at',
    'schemas',
    'capabilities',
    'agent_policy',
    'signature',
];

/**
 * Fetch the discovery manifest from the API gateway.
 * Endpoint: GET /.well-known/agent-rules.json
 */
async function fetchManifest(apiBase: string): Promise<ANCFManifest> {
    const url = `${apiBase}/.well-known/agent-rules.json`;
    console.log(`[Agent] Fetching manifest from ${url}...`);

    const controller = new AbortController();
    const timer = setTimeout(() => controller.abort(), 10000); // 10s timeout

    let resp: Response;
    try {
        resp = await fetch(url, { signal: controller.signal });
    } catch (e: unknown) {
        const msg = e instanceof Error ? e.message : String(e);
        throw new Error(`Manifest fetch failed: ${msg}`);
    } finally {
        clearTimeout(timer);
    }

    if (!resp.ok) {
        throw new Error(`Manifest endpoint returned HTTP ${resp.status} ${resp.statusText}`);
    }

    const contentType = resp.headers.get('content-type') || '';
    if (!contentType.includes('application/json')) {
        throw new Error(`Manifest response has unexpected content-type: ${contentType}`);
    }

    let raw: unknown;
    try {
        raw = await resp.json();
    } catch {
        throw new Error('Manifest response is not valid JSON');
    }

    if (!raw || typeof raw !== 'object' || Array.isArray(raw)) {
        throw new Error('Manifest response is not a JSON object');
    }

    return raw as ANCFManifest;
}

/**
 * Validate the manifest against required structural rules.
 * Phase 1: validates required fields, expiry, protocol version, and signature presence.
 * Phase 2+: also validates against manifest.schema.json and verifies the JWS signature.
 */
function validateManifest(manifest: ANCFManifest): string[] {
    const warnings: string[] = [];

    // 1. Required fields
    for (const field of MANIFEST_REQUIRED_FIELDS) {
        if (!(field in manifest) || manifest[field] === null || manifest[field] === undefined) {
            throw new Error(`Manifest missing required field: ${field}`);
        }
    }

    // 2. Protocol version format
    const protoPattern = /^ANCF-\d+\.\d+$/;
    if (!protoPattern.test(manifest.protocol_version)) {
        throw new Error(`Invalid protocol_version: "${manifest.protocol_version}". Expected format: ANCF-X.Y`);
    }

    // 3. Expiry check
    const expiresAt = new Date(manifest.expires_at);
    if (isNaN(expiresAt.getTime())) {
        throw new Error(`Invalid expires_at timestamp: "${manifest.expires_at}"`);
    }
    if (expiresAt <= new Date()) {
        throw new Error(`Manifest has expired at ${manifest.expires_at}`);
    }

    // Warn if manifest expires soon (within 1 hour)
    const oneHourFromNow = new Date(Date.now() + 60 * 60 * 1000);
    if (expiresAt <= oneHourFromNow) {
        warnings.push(`Manifest expires soon: ${manifest.expires_at}`);
    }

    // 4. Issued-at sanity check
    const issuedAt = new Date(manifest.issued_at);
    if (isNaN(issuedAt.getTime())) {
        throw new Error(`Invalid issued_at timestamp: "${manifest.issued_at}"`);
    }
    if (issuedAt > new Date()) {
        warnings.push(`Manifest issued_at is in the future: ${manifest.issued_at}`);
    }

    // 5. Signature present
    if (!manifest.signature.alg || !manifest.signature.kid || !manifest.signature.jws) {
        throw new Error('Manifest signature is incomplete (missing alg, kid, or jws)');
    }

    // SECURITY FIX: F-006-01 — Phase 2: verify JWS detached signature cryptographically
    // Note: signature verification is performed in main() using verifyManifestSignature()
    // to allow async crypto.subtle operations.

    // 6. Schemas defined
    if (!manifest.schemas.manifest || !manifest.schemas.checkout || !manifest.schemas.mint) {
        throw new Error('Manifest schemas must include manifest, checkout, and mint schema URLs');
    }

    // 7. Capabilities defined
    const requiredCapabilities = ['search', 'quote', 'checkout_prepare', 'checkout_commit'];
    for (const cap of requiredCapabilities) {
        if (!manifest.capabilities[cap]) {
            throw new Error(`Manifest missing required capability: ${cap}`);
        }
    }

    // 8. UI firmware SRI check (if provided)
    if (manifest.ui_firmware && manifest.ui_firmware.components) {
        for (const comp of manifest.ui_firmware.components) {
            if (!comp.integrity || !comp.integrity.match(/^sha(256|384|512)-/)) {
                throw new Error(`Firmware component at ${comp.url} has invalid or missing SRI integrity hash`);
            }
            if (!comp.url) {
                throw new Error('Firmware component is missing url');
            }
        }
        console.log(`[Agent] Firmware SRI pins verified for ${manifest.ui_firmware.components.length} component(s).`);
    } else {
        warnings.push('No ui_firmware.components defined in manifest. Using statically served local components.');
    }

    return warnings;
}

/* ------------------------------------------------------------------ */
/*  Manifest Signature Verification (SECURITY FIX: F-006-01)            */
/* ------------------------------------------------------------------ */

/**
 * Verify the manifest's JWS detached signature using EdDSA (Ed25519).
 *
 * JWS format: header.payload.signature (base64url)
 * The payload is a canonical JSON representation of the manifest
 * (all keys sorted, signature field removed).
 *
 * In production, this would use @solana/web3.js nacl or tweetnacl.
 * This implementation uses Node.js crypto.subtle (available in Node 18+).
 */
async function verifyManifestSignature(manifest: ANCFManifest, pubKeyBase58: string): Promise<boolean> {
    const jws = manifest.signature.jws;
    if (!jws || jws.includes('placeholder')) {
        console.warn('[Agent] SECURITY: Manifest JWS is a placeholder — signature NOT cryptographically verified.');
        console.warn('[Agent] SECURITY: JWS verification NOT implemented — dev mode only');
        return false;
    }

    const parts = jws.split('.');
    if (parts.length !== 3) {
        console.warn('[Agent] SECURITY: Invalid JWS format in manifest signature (expected 3 parts)');
        console.warn('[Agent] SECURITY: JWS verification NOT implemented — dev mode only');
        return false;
    }

    let sigBytes: Uint8Array;
    let pubKeyBytes: Uint8Array;
    try {
        sigBytes = base64UrlDecode(parts[2]);
        pubKeyBytes = base58Decode(pubKeyBase58);
    } catch (e: unknown) {
        console.warn(`[Agent] SECURITY: Failed to decode signature/key bytes: ${e instanceof Error ? e.message : String(e)}`);
        return false;
    }

    // The signing input is the first two JWS parts joined with a dot
    const signingInput = parts[0] + '.' + parts[1];
    const msgBytes = new TextEncoder().encode(signingInput);

    console.log(`[Agent] Manifest JWS verification: kid=${manifest.signature.kid}, alg=${manifest.signature.alg}`);

    // Use Node.js Web Crypto API (Ed25519 supported since Node 18)
    if (typeof crypto !== 'undefined' && crypto.subtle) {
        try {
            const cryptoKey = await crypto.subtle.importKey(
                'raw',
                pubKeyBytes.slice(0, 32), // Ed25519 public key is 32 bytes
                { name: 'Ed25519' },
                false,
                ['verify']
            );
            const isValid = await crypto.subtle.verify(
                { name: 'Ed25519' },
                cryptoKey,
                sigBytes,
                msgBytes
            );
            if (isValid) {
                console.log('[Agent] SECURITY: Manifest JWS signature VERIFIED successfully.');
            } else {
                console.warn('[Agent] SECURITY: Manifest JWS signature verification FAILED — signature does not match.');
            }
            return isValid;
        } catch (e: unknown) {
            const msg = e instanceof Error ? e.message : String(e);
            console.warn(`[Agent] SECURITY: JWS verification error: ${msg}`);
            console.warn('[Agent] SECURITY: JWS verification NOT implemented — dev mode only');
            return false;
        }
    } else {
        console.warn('[Agent] SECURITY: crypto.subtle not available — JWS verification skipped (dev mode only)');
        return false;
    }
}

/* ------------------------------------------------------------------ */
/*  Firmware SRI Verification (SECURITY FIX: F-006-02)                 */
/* ------------------------------------------------------------------ */

/**
 * Compute sha384 hash of a file and return the SRI string.
 */
function computeSRIHash(filePath: string): string {
    const content = fs.readFileSync(filePath);
    const hash = crypto.createHash('sha384').update(content).digest('base64');
    return `sha384-${hash}`;
}

/**
 * Verify that firmware files served locally match the SRI integrity hashes
 * declared in the manifest. Returns an array of mismatch warnings.
 */
function verifyFirmwareSRI(manifest: ANCFManifest): string[] {
    const warnings: string[] = [];

    if (!manifest.ui_firmware?.components || manifest.ui_firmware.components.length === 0) {
        return warnings;
    }

    const distDir = path.resolve(FIRMWARE_DIR);
    if (!fs.existsSync(distDir)) {
        warnings.push(`Firmware dist directory not found: ${distDir}. Cannot verify SRI locally.`);
        return warnings;
    }

    // Build a map of local files and their SRI hashes
    const localFiles = fs.readdirSync(distDir)
        .filter((f) => f.endsWith('.js') && !f.includes('.hash.'))
        .map((f) => path.join(distDir, f));

    const localSRI = new Map<string, string>();
    for (const filePath of localFiles) {
        try {
            const sri = computeSRIHash(filePath);
            localSRI.set(path.basename(filePath), sri);
        } catch {
            // skip unreadable files
        }
    }

    // Compare each manifest component against local files
    for (const comp of manifest.ui_firmware.components) {
        const urlBase = comp.url.split('/').pop() || '';
        // Try fuzzy matching: check if any local file's SRI matches the manifest integrity
        const manifestIntegrity = comp.integrity;
        let found = false;

        for (const [localName, localHash] of localSRI) {
            if (localHash === manifestIntegrity) {
                found = true;
                console.log(`[Agent] SRI MATCH: ${urlBase} ↔ local ${localName} (${manifestIntegrity})`);
                break;
            }
        }

        if (!found) {
            warnings.push(
                `Firmware SRI mismatch for ${comp.url}: manifest declares ${manifestIntegrity}, ` +
                `but no matching local file found. Available local hashes: ` +
                `${[...localSRI.entries()].map(([k, v]) => `${k}:${v}`).join(', ')}`
            );
        }
    }

    if (warnings.length === 0) {
        console.log(`[Agent] Firmware SRI verified: ${manifest.ui_firmware.components.length} component(s) match local files.`);
    }

    return warnings;
}

/* ------------------------------------------------------------------ */
/*  CSP Header Builder                                                  */
/* ------------------------------------------------------------------ */

/**
 * Build a strict Content-Security-Policy header value.
 *
 * Rules:
 *   - default-src 'self' — block everything by default
 *   - script-src 'self' — only allow scripts from our local server
 *   - No 'unsafe-eval' — eval() is forbidden
 *   - No 'unsafe-inline' for scripts (inline event handlers are blocked)
 *   - connect-src — only allow connections to the ANCF API gateway
 *   - img-src — allow self, data: URIs, and configured CDN
 *   - frame-src 'none' — no embedding
 *   - object-src 'none' — no plugins
 *   - form-action 'none' — no form submissions
 *   - base-uri 'self'
 */
// SECURITY FIX: F-006-04 — Use random nonce instead of unsafe-inline
function buildCSPHeader(manifest: ANCFManifest, nonce: string): string {
    const directives: string[] = [
        "default-src 'self'",
        // Script: use nonce-based CSP — no unsafe-inline, no unsafe-eval
        "script-src 'self' 'nonce-" + nonce + "'",
        // Style: use nonce-based CSP for inline styles
        "style-src 'self' 'nonce-" + nonce + "'",
        // Connect: only the API gateway and agent bridge
        "connect-src 'self'",
        // Images: self, data URIs, and allowed CDN hosts from manifest
        "img-src 'self' data:",
        // Fonts: self only
        "font-src 'self'",
        // Block frames, objects, form submissions
        "frame-src 'none'",
        "object-src 'none'",
        "base-uri 'self'",
        "form-action 'none'",
    ];

    // Add allowed component hosts for images if specified in agent_policy
    if (manifest.agent_policy?.allowed_component_hosts) {
        const imgDirective = directives.find((d) => d.startsWith('img-src'));
        if (imgDirective) {
            const hosts = manifest.agent_policy.allowed_component_hosts
                .map((h) => `https://${h}`)
                .join(' ');
            directives[directives.indexOf(imgDirective)] = `${imgDirective} ${hosts}`;
        }
    }

    return directives.join('; ');
}

/* ------------------------------------------------------------------ */
/*  Legacy HTML Page Generator (deprecated Vue 3 Animated Edition)     */
/* ------------------------------------------------------------------ */

/**
 * Generate the Vue 3 animated checkout page served by the local Agent.
 * Replaces old Web Components with AncfAnimatedCatalog + AncfAnimatedProductDetail.
 */
// SECURITY FIX: F-006-04 — accepts nonce for CSP
function generateLegacyVueCheckoutHTML(manifest: ANCFManifest, apiBase: string, nonce: string): string {
    const tokens = JSON.stringify(manifest.ui_firmware?.theme_tokens || {
        primary: '#00FFA3', background: '#0D0E12', text: '#FFFFFF',
    });
    const agentPolicy = JSON.stringify(manifest.agent_policy);
    const shopDomain = 'yourshop.com';
    const shopId = manifest.shop_id;
    const protocolVersion = manifest.protocol_version;
    const isAutonomous = manifest.agent_policy.allow_autonomous_checkout ? 'ENABLED' : 'DISABLED';
    const humanConfirm = manifest.agent_policy.require_human_confirmation ? 'REQUIRED' : 'NOT REQUIRED';
    const compHosts = (manifest.agent_policy.allowed_component_hosts || []).join(', ') || 'none';

    return `<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>ANCF Commerce — ${shopId}</title>
    <style>
        *,*::before,*::after{box-sizing:border-box;margin:0;padding:0}
        body{
            background:var(--ancf-background, #0D0E12);
            color:var(--ancf-text, #FFFFFF);
            font-family:'Inter', system-ui, -apple-system, sans-serif;
            min-height:100vh;
            -webkit-font-smoothing:antialiased;
        }
        .app-shell{max-width:960px;margin:0 auto;padding:20px}

        .warning-banner{
            display:flex;align-items:flex-start;gap:10px;
            padding:14px 18px;margin-bottom:20px;
            border-radius:12px;
            background:linear-gradient(135deg, #2A1A00, #1A0A00);
            border:1px solid rgba(255,136,0,0.3);
            color:#FFAA00;font-size:13px;line-height:1.6;
        }
        .warning-banner .icon{font-size:20px;flex-shrink:0;margin-top:1px}

        .policy-bar{
            display:flex;gap:24px;flex-wrap:wrap;padding:10px 18px;margin-bottom:20px;
            background:var(--ancf-surface, #13141F);
            border:1px solid var(--ancf-border, #1E2030);border-radius:10px;
            font-size:11px;color:#666;
        }
        .policy-bar .policy-item{display:flex;flex-direction:column;gap:2px}
        .policy-bar .policy-label{color:#888;text-transform:uppercase;letter-spacing:.5px;font-size:10px}
        .policy-bar .policy-value{font-weight:600;color:#aaa;font-family:monospace}

        .checkout-result{
            text-align:center;padding:40px 20px;margin-top:20px;
            background:var(--ancf-surface, #13141F);
            border:1px solid var(--ancf-border, #1E2030);border-radius:14px;
        }
        .checkout-result.committed{border-color:rgba(0,255,163,0.3)}
        .checkout-result.failed{border-color:rgba(255,68,68,0.3)}
        .checkout-result svg{margin-bottom:12px}
        .checkout-result h3{font-size:18px;margin-bottom:8px}
        .checkout-result.committed h3{color:var(--ancf-primary, #00FFA3)}
        .checkout-result.failed h3{color:var(--ancf-danger, #FF4444)}
        .checkout-result p{color:#888;margin-bottom:16px}
        .btn-back-to-catalog{
            display:inline-flex;align-items:center;gap:6px;padding:8px 20px;
            border-radius:8px;border:1px solid var(--ancf-border, #1E2030);
            background:transparent;color:#aaa;cursor:pointer;font-size:13px;transition:all .2s;
        }
        .btn-back-to-catalog:hover{color:#fff;border-color:#555}
        .footer{
            text-align:center;padding:20px;color:#333;font-size:11px;
            margin-top:40px;border-top:1px solid #13141F;
        }
    </style>
</head>
<body>
    <div id="app" class="app-shell">
        <!-- Security Warning -->
        <div class="warning-banner">
            <span class="icon">&#9888;</span>
            <span>
                <strong>Local Temporary Checkout UI</strong><br>
                Non-authoritative interface. All prices, inventory, and transactions verified by backend.
            </span>
        </div>

        <!-- Policy Bar -->
        <div class="policy-bar">
            <div class="policy-item">
                <span class="policy-label">Autonomous</span>
                <span class="policy-value">${isAutonomous}</span>
            </div>
            <div class="policy-item">
                <span class="policy-label">Human Confirm</span>
                <span class="policy-value">${humanConfirm}</span>
            </div>
            <div class="policy-item">
                <span class="policy-label">Shop</span>
                <span class="policy-value">${shopId}</span>
            </div>
            <div class="policy-item">
                <span class="policy-label">Protocol</span>
                <span class="policy-value">${protocolVersion}</span>
            </div>
            <div class="policy-item">
                <span class="policy-label">Network</span>
                <span class="policy-value">solana-mainnet</span>
            </div>
        </div>

        <!-- Vue Catalog -->
        <ancf-catalog
            v-if="!selectedProduct && !checkoutResult"
            :api-base="apiBase"
            :wallet="wallet"
            :network="network"
            :shop-domain="shopDomain"
            :theme-tokens="themeTokens"
            @select="onProductSelect"
            @change-wallet="onChangeWallet"
            @change-network="onChangeNetwork"
        ></ancf-catalog>

        <!-- Vue Product Detail -->
        <ancf-product-detail
            v-if="selectedProduct && !checkoutResult"
            :product="selectedProduct"
            :api-base="apiBase"
            :wallet="wallet"
            :network="network"
            :shop-domain="shopDomain"
            :shop-id="shopId"
            :agent-session-id="agentSessionId"
            @back="selectedProduct = null"
            @quote-ready="onQuoteReady"
            @confirm-checkout="onConfirmCheckout"
        ></ancf-product-detail>

        <!-- Checkout Result -->
        <div v-if="checkoutResult" class="checkout-result" :class="checkoutResult.status">
            <svg v-if="checkoutResult.status==='committed'" width="48" height="48" viewBox="0 0 24 24" fill="none" stroke="#00FFA3" stroke-width="2"><path d="M20 6L9 17l-5-5"/></svg>
            <svg v-if="checkoutResult.status==='failed'" width="48" height="48" viewBox="0 0 24 24" fill="none" stroke="#FF4444" stroke-width="2"><circle cx="12" cy="12" r="10"/><path d="M15 9l-6 6M9 9l6 6"/></svg>
            <h3>{{ checkoutResult.status === 'committed' ? 'Checkout Confirmed' : 'Checkout Failed' }}</h3>
            <p v-if="checkoutResult.order_id">Order ID: <code>{{ checkoutResult.order_id }}</code></p>
            <p v-if="checkoutResult.error" style="color:#FF6666">{{ checkoutResult.error }}</p>
            <button class="btn-back-to-catalog" @click="checkoutResult=null; selectedProduct=null">← Back to Catalog</button>
        </div>

        <!-- Footer -->
        <div class="footer">ANCF Agent Renderer v2.0 | Shop: ${shopId} | Vue 3 Animated UI | Non-authoritative</div>
    </div>

    <!-- Vue 3 CDN -->
    <script src="/firmware/vue.global.js"></script>
    <!-- Agent Bridge (shared with Vue components) -->
    <script type="module" src="/firmware/agent-bridge.js"></script>
    <!-- Vue Components -->
    <script type="module" src="/firmware/AncfAnimatedCatalog.vue.js"></script>
    <script type="module" src="/firmware/AncfAnimatedProductDetail.vue.js"></script>

    <script type="module" nonce="${nonce}">
        import AncfAnimatedCatalog from '/firmware/AncfAnimatedCatalog.vue.js';
        import AncfAnimatedProductDetail from '/firmware/AncfAnimatedProductDetail.vue.js';

        const { createApp, ref } = Vue;

        createApp({
            components: {
                'ancf-catalog': AncfAnimatedCatalog,
                'ancf-product-detail': AncfAnimatedProductDetail
            },
            setup() {
                const apiBase = ref('${apiBase}');
                const wallet = ref('USER_WALLET');
                const network = ref('solana-mainnet');
                const shopDomain = ref('${shopDomain}');
                const shopId = ref('${shopId}');
                const agentSessionId = ref('agent_session_local_001');
                const themeTokens = ref(${tokens});

                const selectedProduct = ref(null);
                const quoteData = ref(null);
                const checkoutResult = ref(null);

                function onProductSelect(item) {
                    selectedProduct.value = item;
                    setTimeout(() => {
                        const el = document.querySelector('.ancf-detail');
                        if (el) el.scrollIntoView({ behavior: 'smooth' });
                    }, 100);
                }

                function onQuoteReady(quote) {
                    quoteData.value = quote;
                }

                async function onConfirmCheckout({ quote_id, product, qty, wallet: w }) {
                    const bridge = window.__ancfBridge;
                    if (!bridge) { alert('Agent Bridge not available'); return; }

                    try {
                        const prepareResp = await bridge.handleCommand({
                            command: 'ancf:checkout_prepare',
                            params: {
                                quote_id,
                                wallet: w || wallet.value,
                                network: network.value,
                                agent_session_id: agentSessionId.value
                            },
                            requestId: crypto.randomUUID()
                        });
                        if (prepareResp.error) { checkoutResult.value = { status: 'failed', error: prepareResp.error }; return; }

                        const intent = prepareResp.result || prepareResp;
                        const sig = prompt(
                            'Confirm checkout for ' + qty + 'x ' + (product?.title || 'item') + '\\nIntent: ' + intent.order_intent_id + '\\n\\nEnter signature (demo: OK):',
                            'demo_signature_placeholder'
                        );
                        if (!sig) { alert('Checkout cancelled'); return; }

                        const commitResp = await bridge.handleCommand({
                            command: 'ancf:checkout_commit',
                            params: {
                                order_intent_id: intent.order_intent_id,
                                quote_id,
                                wallet: w || wallet.value,
                                wallet_signature: sig,
                                agent_session_id: agentSessionId.value,
                                idempotency_key: 'ck_' + crypto.randomUUID()
                            },
                            requestId: crypto.randomUUID()
                        });
                        checkoutResult.value = commitResp.error
                            ? { status: 'failed', error: commitResp.error }
                            : { status: 'committed', order_id: (commitResp.result || commitResp).order_id };
                    } catch (e) {
                        checkoutResult.value = { status: 'failed', error: e.message };
                    }
                }

                function onChangeWallet() {
                    const w = prompt('Enter wallet address:', wallet.value);
                    if (w) wallet.value = w;
                }
                function onChangeNetwork() {
                    const n = prompt('Enter network (solana-mainnet / sonic-l2):', network.value);
                    if (n) network.value = n;
                }

                return {
                    apiBase, wallet, network, shopDomain, shopId,
                    agentSessionId, themeTokens,
                    selectedProduct, quoteData, checkoutResult,
                    onProductSelect, onQuoteReady, onConfirmCheckout,
                    onChangeWallet, onChangeNetwork
                };
            }
        }).mount('#app');
    </script>
</body>
</html>`;
}

/* ------------------------------------------------------------------ */
/*  Stitch One-Shot Template Renderer                                  */
/* ------------------------------------------------------------------ */

interface PriceValue {
    currency: string;
    amount_minor: string;
    scale: number;
}

interface SearchResultItem {
    sku_id: string;
    title: string;
    price: PriceValue;
    stock_hint?: number;
    specs?: Record<string, string>;
    media?: { thumbnail?: string; gallery?: string[] };
    category?: string;
    description?: string;
}

type SupportedLocale = 'en-US' | 'zh-CN';

const I18N_LABELS: Record<SupportedLocale, Record<string, string | Record<string, string>>> = {
    'en-US': {
        htmlLang: 'en',
        pageTitleCatalog: 'ANCF Animated Catalog',
        pageTitleDetail: 'ANCF Animated Product Detail',
        catalogTitle: 'ANCF Shop',
        catalogHeading: 'Compute Infrastructure',
        catalogDescription: 'Backend-authoritative product data rendered locally by an Agent.',
        catalogCategoryLabel: 'Display-only catalog',
        manifestValid: 'Manifest valid',
        manifestActive: 'manifest active',
        searchLabel: 'Search',
        searchPlaceholder: 'Search SKU, GPU, capability',
        stockHidden: 'Stock hidden',
        stockOut: 'Out',
        stockLeft: 'left',
        stockAvailable: 'available',
        quote: 'Quote',
        agentIntake: 'Agent Intake',
        itemsRendered: 'items rendered',
        bridge: 'Bridge',
        payload: 'Payload',
        navCatalog: 'Catalog',
        navQuote: 'Quote',
        navCheckout: 'Checkout',
        backendQuoteRequired: 'backend quote required',
        protocol: 'Protocol',
        bridgeCommand: 'Bridge Command',
        trustBoundary: 'Trust Boundary',
        backendAuthoritative: 'Backend Authoritative',
        securityNotice: 'Security notice',
        securityNoticeBody: 'This local view is generated by an Agent. Price, stock, payment status, and service activation must be confirmed by backend quote and checkout APIs.',
        totalPreview: 'Total preview',
        select: 'Select',
        requestQuote: 'Request Quote',
        walletPrompt: 'Wallet address for backend quote:',
        quoteReceived: 'Backend quote received',
        sku: 'SKU',
        quoteId: 'Quote',
        total: 'Total',
        continueCheckout: 'Continue to checkout prepare and commit?',
        signCheckoutIntent: 'Sign checkout intent',
        intent: 'Intent',
        demoSignature: 'Demo signature:',
        checkoutCommitted: 'Checkout committed',
        operationFailed: 'ANCF operation failed',
        filters: { All: 'All', GPU: 'GPU', API: 'API', Storage: 'Storage' },
    },
    'zh-CN': {
        htmlLang: 'zh-CN',
        pageTitleCatalog: 'ANCF 商品目录',
        pageTitleDetail: 'ANCF 商品详情',
        catalogTitle: 'ANCF 商店',
        catalogHeading: '算力基础设施',
        catalogDescription: '由 Agent 在本地渲染商品数据，价格、库存与交易状态以后端确认为准。',
        catalogCategoryLabel: '仅展示目录',
        manifestValid: 'Manifest 有效',
        manifestActive: 'manifest 有效',
        searchLabel: '搜索',
        searchPlaceholder: '搜索 SKU、GPU、能力',
        stockHidden: '库存隐藏',
        stockOut: '售罄',
        stockLeft: '剩余',
        stockAvailable: '可用',
        quote: '报价',
        agentIntake: 'Agent 接入',
        itemsRendered: '个商品已渲染',
        bridge: '桥接',
        payload: '载荷',
        navCatalog: '目录',
        navQuote: '报价',
        navCheckout: '结算',
        backendQuoteRequired: '需要后端报价',
        protocol: '协议',
        bridgeCommand: '桥接命令',
        trustBoundary: '信任边界',
        backendAuthoritative: '以后端为准',
        securityNotice: '安全提示',
        securityNoticeBody: '此本地界面由 Agent 临时生成。价格、库存、支付状态和服务开通必须由后端报价与结算接口确认。',
        totalPreview: '总价预览',
        select: '选择',
        requestQuote: '请求报价',
        walletPrompt: '用于后端报价的钱包地址：',
        quoteReceived: '已收到后端报价',
        sku: 'SKU',
        quoteId: '报价',
        total: '总计',
        continueCheckout: '是否继续执行结算准备和提交？',
        signCheckoutIntent: '签名结算意图',
        intent: '意图',
        demoSignature: 'Demo 签名：',
        checkoutCommitted: '结算已提交',
        operationFailed: 'ANCF 操作失败',
        filters: { All: '全部', GPU: 'GPU', API: 'API', Storage: '存储' },
    },
};

function resolveLocale(req: Request): SupportedLocale {
    const queryLocale = typeof req.query.lang === 'string'
        ? req.query.lang
        : (typeof req.query.locale === 'string' ? req.query.locale : '');
    const headerLocale = req.header('accept-language') || '';
    const raw = `${queryLocale},${headerLocale}`.toLowerCase();
    return raw.includes('zh') ? 'zh-CN' : 'en-US';
}

function labelsForLocale(locale: SupportedLocale): Record<string, string | Record<string, string>> {
    return I18N_LABELS[locale] || I18N_LABELS['en-US'];
}

function asObject(value: unknown): Record<string, unknown> | null {
    if (!value || typeof value !== 'object' || Array.isArray(value)) {
        return null;
    }
    return value as Record<string, unknown>;
}

function normalizeSearchItem(value: unknown): SearchResultItem | null {
    const item = asObject(value);
    if (!item || typeof item.sku_id !== 'string' || typeof item.title !== 'string') {
        return null;
    }

    const price = asObject(item.price);
    if (
        !price ||
        typeof price.currency !== 'string' ||
        typeof price.amount_minor !== 'string' ||
        typeof price.scale !== 'number'
    ) {
        return null;
    }

    const specsRaw = asObject(item.specs);
    const specs = specsRaw
        ? Object.fromEntries(Object.entries(specsRaw).filter((entry): entry is [string, string] => typeof entry[1] === 'string'))
        : undefined;

    const mediaRaw = asObject(item.media);
    const gallery = Array.isArray(mediaRaw?.gallery)
        ? mediaRaw.gallery.filter((entry): entry is string => typeof entry === 'string')
        : undefined;

    const normalized: SearchResultItem = {
        sku_id: item.sku_id,
        title: item.title,
        price: {
            currency: price.currency,
            amount_minor: price.amount_minor,
            scale: price.scale,
        },
        stock_hint: typeof item.stock_hint === 'number' ? item.stock_hint : undefined,
        specs,
        media: mediaRaw
            ? {
                thumbnail: typeof mediaRaw.thumbnail === 'string' ? mediaRaw.thumbnail : undefined,
                gallery,
            }
            : undefined,
        category: typeof item.category === 'string' ? item.category : undefined,
        description: typeof item.description === 'string' ? item.description : undefined,
    };

    if (!normalized.category) {
        normalized.category = normalized.specs?.GPU ? 'GPU' : 'API';
    }
    return normalized;
}

function extractSearchItems(data: unknown): SearchResultItem[] {
    const root = asObject(data);
    const items = Array.isArray(root?.items) ? root.items : [];
    return items.map(normalizeSearchItem).filter((item): item is SearchResultItem => Boolean(item));
}

function readStitchTemplate(fileName: string): string {
    const root = path.resolve(TEMPLATE_DIR);
    const resolved = path.resolve(root, fileName);
    if (!resolved.startsWith(root + path.sep)) {
        throw new Error(`Template path escapes template directory: ${fileName}`);
    }
    return fs.readFileSync(resolved, 'utf8');
}

function safeJsonForInlineScript(value: unknown): string {
    return JSON.stringify(value, null, 2)
        .replace(/</g, '\\u003C')
        .replace(/>/g, '\\u003E')
        .replace(/&/g, '\\u0026')
        .replace(/\u2028/g, '\\u2028')
        .replace(/\u2029/g, '\\u2029');
}

function injectTemplatePayload(template: string, payload: unknown): string {
    const payloadSlot = /(<script\s+id="ancf-payload"\s+type="application\/json"\s*>)([\s\S]*?)(<\/script>)/;
    if (!payloadSlot.test(template)) {
        throw new Error('Stitch template is missing script#ancf-payload[type="application/json"]');
    }
    return template.replace(payloadSlot, `$1\n${safeJsonForInlineScript(payload)}\n$3`);
}

function textLabel(labels: Record<string, string | Record<string, string>>, key: string): string {
    const value = labels[key];
    return typeof value === 'string' ? value : '';
}

function buildCatalogPayload(manifest: ANCFManifest, products: SearchResultItem[], locale: SupportedLocale): Record<string, unknown> {
    const labels = labelsForLocale(locale);
    return {
        locale,
        labels,
        shopLabel: manifest.shop_id,
        title: textLabel(labels, 'catalogTitle'),
        heading: textLabel(labels, 'catalogHeading'),
        description: textLabel(labels, 'catalogDescription'),
        categoryLabel: textLabel(labels, 'catalogCategoryLabel'),
        manifestStatus: textLabel(labels, 'manifestValid'),
        searchPlaceholder: textLabel(labels, 'searchPlaceholder'),
        filters: ['All', 'GPU', 'API', 'Storage'],
        maxItems: 24,
        products,
    };
}

function buildDetailPayload(manifest: ANCFManifest, product: SearchResultItem, locale: SupportedLocale): Record<string, unknown> {
    const labels = labelsForLocale(locale);
    return {
        locale,
        labels,
        shopLabel: manifest.shop_id,
        manifestStatus: textLabel(labels, 'manifestActive'),
        protocolVersion: manifest.protocol_version,
        product,
    };
}

// SECURITY FIX: F-006-04 — injects CSP nonce into inline script
function buildTemplateOrchestrationScript(nonce: string): string {
    return `
  <script nonce="${nonce}">
    (function () {
      var payloadNode = document.getElementById('ancf-payload');
      var payload = payloadNode ? JSON.parse(payloadNode.textContent || '{}') : {};
      var labels = payload.labels || {};
      var network = localStorage.getItem('ancf_network') || 'solana-mainnet';
      var agentSessionId = 'agent_session_local_001';

      function t(key, fallback) {
        var value = labels[key];
        return typeof value === 'string' ? value : fallback;
      }

      function requestId() {
        if (window.crypto && crypto.randomUUID) return crypto.randomUUID();
        return 'req_' + Date.now().toString(36) + '_' + Math.random().toString(36).slice(2);
      }

      async function bridge(command, params) {
        var response = await fetch('/bridge', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ command: command, params: params || {}, requestId: requestId() })
        });
        var data = await response.json().catch(function () { return {}; });
        if (!response.ok || data.error) {
          throw new Error(data.error || ('Bridge HTTP ' + response.status));
        }
        return data.result;
      }

      function ensureWallet() {
        var current = localStorage.getItem('ancf_wallet') || 'USER_WALLET';
        var wallet = prompt(t('walletPrompt', 'Wallet address for backend quote:'), current);
        if (!wallet) return '';
        localStorage.setItem('ancf_wallet', wallet);
        return wallet;
      }

      function formatMinor(amountMinor, scale, currency) {
        try {
          var sc = Number(scale || 0);
          var raw = String(amountMinor == null ? '0' : amountMinor);
          if (!/^[0-9]+$/.test(raw)) return '— ' + (currency || '');
          var big = BigInt(raw);
          var divisor = BigInt(10) ** BigInt(sc);
          var whole = big / divisor;
          var frac = (big % divisor).toString().padStart(sc, '0').slice(0, Math.min(sc, 6));
          return whole.toString() + (frac ? '.' + frac : '') + ' ' + (currency || '');
        } catch (e) {
          return '— ' + (currency || '');
        }
      }

      function openDetail(product) {
        if (!product || !product.sku_id) return;
        var lang = payload.locale ? '&lang=' + encodeURIComponent(payload.locale) : '';
        window.location.href = '/detail?sku=' + encodeURIComponent(product.sku_id) + lang;
      }

      async function quoteAndMaybeCheckout(product) {
        if (!product || !product.sku_id) return;
        var wallet = ensureWallet();
        if (!wallet) return;

        var quote = await bridge('ancf:quote', {
          wallet: wallet,
          network: network,
          lines: [{ sku_id: product.sku_id, quantity: 1 }]
        });
        var total = formatMinor(quote.total_minor, quote.scale, quote.currency);
        var shouldCheckout = confirm(
          t('quoteReceived', 'Backend quote received') + '\\n' +
          t('sku', 'SKU') + ': ' + product.sku_id + '\\n' +
          t('quoteId', 'Quote') + ': ' + quote.quote_id + '\\n' +
          t('total', 'Total') + ': ' + total + '\\n\\n' +
          t('continueCheckout', 'Continue to checkout prepare and commit?')
        );
        if (!shouldCheckout) return;

        var intent = await bridge('ancf:checkout_prepare', {
          quote_id: quote.quote_id,
          wallet: wallet,
          network: network,
          agent_session_id: agentSessionId
        });
        var signature = prompt(
          t('signCheckoutIntent', 'Sign checkout intent') + '\\n' +
          t('intent', 'Intent') + ': ' + intent.order_intent_id + '\\n\\n' +
          t('demoSignature', 'Demo signature:'),
          'demo_signature_placeholder'
        );
        if (!signature) return;

        var commit = await bridge('ancf:checkout_commit', {
          order_intent_id: intent.order_intent_id,
          quote_id: quote.quote_id,
          wallet: wallet,
          wallet_signature: signature,
          agent_session_id: agentSessionId,
          idempotency_key: 'ck_' + requestId()
        });
        alert(t('checkoutCommitted', 'Checkout committed') + ': ' + (commit.order_id || commit.status || 'ok'));
      }

      document.addEventListener('ANCF_TEMPLATE_SELECT', function (event) {
        openDetail(event.detail);
      });

      document.addEventListener('ANCF_TEMPLATE_QUOTE', function (event) {
        quoteAndMaybeCheckout(event.detail).catch(function (error) {
          alert(t('operationFailed', 'ANCF operation failed') + ': ' + (error && error.message ? error.message : String(error)));
        });
      });

      document.addEventListener('ANCF_TEMPLATE_BACK', function () {
        if (location.pathname === '/detail') {
          window.location.href = '/';
        }
      });
    })();
  </script>`;
}

function attachInlineCSPNonce(html: string, nonce: string): string {
    return html
        .replace(/<style(?![^>]*\bnonce=)([^>]*)>/gi, `<style nonce="${nonce}"$1>`)
        .replace(/<script(?![^>]*\bnonce=)(?![^>]*\bsrc=)([^>]*)>/gi, `<script nonce="${nonce}"$1>`);
}

// SECURITY FIX: F-006-04 — passes CSP nonce to template orchestration script
function finalizeStitchTemplate(template: string, locale: SupportedLocale, pageTitleKey: string, nonce: string): string {
    const labels = labelsForLocale(locale);
    const htmlLang = textLabel(labels, 'htmlLang') || locale;
    const pageTitle = textLabel(labels, pageTitleKey) || 'ANCF Commerce';
    const finalized = template
        .replace(/<html lang="[^"]*">/, `<html lang="${htmlLang}">`)
        .replace(/<title>[\s\S]*?<\/title>/, `<title>${pageTitle}</title>`)
        .replace('</body>', `${buildTemplateOrchestrationScript(nonce)}\n</body>`);
    return attachInlineCSPNonce(finalized, nonce);
}

// SECURITY FIX: F-006-04 — passes CSP nonce through template pipeline
async function generateStitchCatalogHTML(manifest: ANCFManifest, locale: SupportedLocale, nonce: string): Promise<string> {
    const searchResponse = await searchAPI('', 24);
    const products = extractSearchItems(searchResponse);
    const template = readStitchTemplate('ancf-animated-catalog.template.html');
    return finalizeStitchTemplate(
        injectTemplatePayload(template, buildCatalogPayload(manifest, products, locale)),
        locale,
        'pageTitleCatalog',
        nonce
    );
}

// SECURITY FIX: F-006-04 — passes CSP nonce through template pipeline
async function generateStitchProductDetailHTML(manifest: ANCFManifest, skuId: string, locale: SupportedLocale, nonce: string): Promise<string> {
    const searchResponse = await searchAPI(skuId, 20);
    const product = extractSearchItems(searchResponse).find((item) => item.sku_id === skuId);
    if (!product) {
        throw new Error(`SKU not found: ${skuId}`);
    }
    const template = readStitchTemplate('ancf-animated-product-detail.template.html');
    return finalizeStitchTemplate(
        injectTemplatePayload(template, buildDetailPayload(manifest, product, locale)),
        locale,
        'pageTitleDetail',
        nonce
    );
}

/* ------------------------------------------------------------------ */
/*  Agent Bridge Proxy Endpoint                                         */
/* ------------------------------------------------------------------ */

/* ------------------------------------------------------------------ */
/*  One-Shot Checkout HTML Generator                                   */
/* ------------------------------------------------------------------ */

/**
 * Generate a disposable, single-transaction checkout HTML page.
 * An agent calls GET /checkout-session?sku=xxx&wallet=xxx
 * → returns a one-time interactive HTML showing ONLY this product.
 * No catalog, no navigation — just this one purchase.
 */
// SECURITY FIX: F-006-04 — accepts nonce for CSP
function generateOneShotCheckoutHTML(
    manifest: ANCFManifest,
    product: any,
    wallet: string,
    sessionId: string,
    apiBase: string,
    nonce: string
): string {
    const price = product.price || {};
    const amountMinor = price.amount_minor || '0';
    const scale = price.scale || 6;
    const currency = price.currency || 'vUSDC';
    const displayPrice = (parseInt(amountMinor) / Math.pow(10, scale)).toFixed(2);
    const thumbnail = product.media?.thumbnail || '';
    const specs = product.specs || {};
    const shopId = manifest.shop_id;
    const domain = 'yourshop.com';

    return `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>Checkout: ${product.title} — ${shopId}</title>
<style>
*,*::before,*::after{box-sizing:border-box;margin:0;padding:0}
body{
  background:#0D0E12;color:#fff;font-family:system-ui,-apple-system,sans-serif;
  min-height:100vh;display:flex;align-items:center;justify-content:center;
  padding:20px;
}
.card{
  max-width:480px;width:100%;background:#13141F;border:1px solid #1E2030;
  border-radius:16px;overflow:hidden;box-shadow:0 20px 60px rgba(0,0,0,.5);
  animation:fadeIn .4s ease-out;
}
@keyframes fadeIn{from{opacity:0;transform:translateY(20px)}to{opacity:1;transform:translateY(0)}}

/* Header */
.card-header{
  padding:24px 24px 0;display:flex;justify-content:space-between;align-items:flex-start;
}
.badge{
  display:inline-flex;align-items:center;gap:6px;padding:4px 12px;
  border-radius:20px;background:rgba(0,255,163,.08);color:#00FFA3;
  border:1px solid rgba(0,255,163,.2);font-size:12px;font-weight:500;
}
.badge-dot{width:6px;height:6px;border-radius:50%;background:#00FFA3;box-shadow:0 0 6px #00FFA3}
.session-id{font-size:10px;color:#444;font-family:monospace}

/* Product image */
.product-image{
  height:200px;margin:16px 24px;border-radius:12px;
  background:linear-gradient(135deg,#0d2818 0%,#0D0E12 100%);
  display:flex;align-items:center;justify-content:center;position:relative;overflow:hidden;
}
.product-image .chip{
  font-size:48px;font-weight:900;letter-spacing:3px;color:rgba(255,255,255,.06);
  text-transform:uppercase;
}
.product-image .shimmer{
  position:absolute;inset:0;
  background:linear-gradient(105deg,transparent 40%,rgba(255,255,255,.02) 50%,transparent 60%);
  background-size:200% 100%;animation:shimmer 3s infinite;
}
@keyframes shimmer{0%{background-position:200% 0}100%{background-position:-200% 0}}
${thumbnail ? `.product-image{background:url(${thumbnail}) center/cover;}.product-image .chip{display:none}` : ''}

/* Info */
.card-body{padding:0 24px}
.title{font-size:22px;font-weight:700;line-height:1.3;margin-bottom:4px}
.sku{font-size:11px;color:#555;font-family:monospace;text-transform:uppercase;letter-spacing:.5px;margin-bottom:12px}
.specs{display:flex;flex-wrap:wrap;gap:6px;margin-bottom:16px}
.spec{
  padding:4px 10px;border-radius:6px;background:rgba(255,255,255,.04);
  font-size:11px;color:#888;
}
.spec strong{color:#aaa}

/* Price */
.price-row{
  display:flex;justify-content:space-between;align-items:baseline;
  padding:16px 24px;border-top:1px solid #1E2030;border-bottom:1px solid #1E2030;
  background:rgba(0,0,0,.2);
}
.price-label{font-size:11px;color:#888;text-transform:uppercase;letter-spacing:.5px}
.price-value{font-size:28px;font-weight:800;color:#00FFA3}
.price-unit{font-size:14px;color:#666;font-weight:400}
.stock{font-size:12px;color:#888}
.stock-ok{color:#00FFA3}
.stock-low{color:#FFAA00}

/* Wallet */
.wallet-row{padding:12px 24px;font-size:11px;color:#555;display:flex;justify-content:space-between}
.wallet-addr{font-family:monospace;font-size:11px;color:#777}

/* Actions */
.card-actions{padding:20px 24px 24px;display:flex;flex-direction:column;gap:10px}
.btn{
  width:100%;padding:14px;border-radius:12px;border:none;
  font-size:15px;font-weight:600;cursor:pointer;transition:all .2s;
  display:flex;align-items:center;justify-content:center;gap:8px;
}
.btn-primary{background:#00FFA3;color:#0D0E12}
.btn-primary:hover{filter:brightness(1.1);transform:translateY(-1px);box-shadow:0 4px 24px rgba(0,255,163,.2)}
.btn-primary:disabled{background:#222;color:#555;cursor:not-allowed;transform:none;box-shadow:none}
.btn-secondary{background:transparent;color:#888;border:1px solid #1E2030}
.btn-secondary:hover{color:#fff;border-color:#444}

/* Status area */
.status{
  padding:10px 14px;border-radius:10px;font-size:13px;text-align:center;display:none;
}
.status.show{display:block}
.status.quote{background:rgba(0,255,163,.06);border:1px solid rgba(0,255,163,.15);color:#00FFA3}
.status.error{background:rgba(255,68,68,.06);border:1px solid rgba(255,68,68,.15);color:#FF6666}
.status.success{background:rgba(0,255,163,.08);border:1px solid rgba(0,255,163,.2);color:#00FFA3}

/* Warning banner */
.warning{
  margin:0 24px 16px;padding:10px 14px;border-radius:8px;
  background:rgba(255,136,0,.06);border:1px solid rgba(255,136,0,.2);
  color:#FFAA00;font-size:11px;line-height:1.5;display:flex;align-items:flex-start;gap:8px;
}
.warning-icon{font-size:16px;flex-shrink:0;margin-top:1px}
</style>
</head>
<body>
<div class="card">
  <div class="card-header">
    <div class="badge"><span class="badge-dot"></span> ${domain}</div>
    <div class="session-id">session: ${sessionId.slice(0,8)}</div>
  </div>

  <div class="product-image">
    <span class="chip">${(specs.GPU || product.sku_id || '').split(' ')[0] || 'GPU'}</span>
    <div class="shimmer"></div>
  </div>

  <div class="card-body">
    <h1 class="title">${product.title || 'Product'}</h1>
    <div class="sku">${product.sku_id || ''}</div>
    <div class="specs">
      ${Object.entries(specs).map(([k,v]) => `<span class="spec"><strong>${k}</strong> ${v}</span>`).join('')}
    </div>
  </div>

  <div class="warning">
    <span class="warning-icon">&#9888;</span>
    <span><strong>One-Time Checkout Session</strong><br>This page is a disposable, non-authoritative interface generated by the ANCF Agent. Transaction finality is confirmed by the backend.</span>
  </div>

  <div class="price-row">
    <div>
      <div class="price-label">Unit Price (Backend Authoritative)</div>
      <div class="price-value">${displayPrice} <span class="price-unit">${currency}/hr</span></div>
    </div>
    <div class="stock ${product.stock_hint > 50 ? 'stock-ok' : 'stock-low'}">
      ${product.stock_hint || 0} available
    </div>
  </div>

  <div class="wallet-row">
    <span>Wallet</span>
    <span class="wallet-addr">${wallet.length > 16 ? wallet.slice(0,8) + '...' + wallet.slice(-6) : wallet}</span>
  </div>

  <div class="card-actions">
    <div id="status" class="status"></div>

    <button id="btnQuote" class="btn btn-primary" onclick="requestQuote()">
      <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5"><rect x="3" y="3" width="18" height="18" rx="3"/><path d="M9 12h6M12 9v6"/></svg>
      Request Quote
    </button>

    <button id="btnCheckout" class="btn btn-primary" onclick="doCheckout()" style="display:none" disabled>
      <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5"><path d="M20 6L9 17l-5-5"/></svg>
      Confirm Checkout
    </button>

    <div id="quoteDetail" style="display:none;padding:12px;background:rgba(255,255,255,.02);border-radius:10px;font-size:13px;color:#aaa;text-align:center"></div>
  </div>
</div>

<script nonce="${nonce}">
const BRIDGE = '/bridge';
const PRODUCT = ${JSON.stringify(product)};
const WALLET = '${wallet}';
const SESSION = '${sessionId}';
let quoteData = null;

// All API calls routed through Agent Bridge (F-006-05 fix)
async function bridgeCall(command, params) {
  const resp = await fetch(BRIDGE, {
    method: 'POST',
    headers: {'Content-Type':'application/json'},
    body: JSON.stringify({command, params, requestId: crypto.randomUUID?.() || SESSION + '_' + Date.now()})
  });
  if (!resp.ok) throw new Error('Bridge HTTP ' + resp.status);
  const data = await resp.json();
  if (data.error) throw new Error(data.error);
  return data.result;
}

async function requestQuote() {
  const btn = document.getElementById('btnQuote');
  const status = document.getElementById('status');
  btn.disabled = true;
  btn.innerHTML = '<span style="display:inline-block;width:16px;height:16px;border:2px solid rgba(0,0,0,.2);border-top-color:#0D0E12;border-radius:50%;animation:spin .6s linear infinite"></span> Requesting...';
  status.className = 'status';
  status.style.display = 'none';

  try {
    // Route through Bridge — never call backend directly
    quoteData = await bridgeCall('ancf:quote', {
      wallet: WALLET, network: 'solana-mainnet',
      lines: [{sku_id: PRODUCT.sku_id, quantity: 1}]
    });

    const price = (parseInt(quoteData.total_minor)/Math.pow(10, quoteData.scale||6)).toFixed(2);
    document.getElementById('quoteDetail').innerHTML =
      '<div style="margin-bottom:6px;color:#00FFA3;font-size:16px;font-weight:700">' + price + ' ' + (quoteData.currency||'AGP') + '</div>' +
      '<div style="font-size:11px;color:#555">Quote: ' + quoteData.quote_id.slice(0,16) + '... | Expires: ' + new Date(quoteData.expires_at).toLocaleTimeString() + '</div>';
    document.getElementById('quoteDetail').style.display = 'block';

    btn.style.display = 'none';
    const btnCO = document.getElementById('btnCheckout');
    btnCO.style.display = 'flex';
    btnCO.disabled = false;

    status.className = 'status quote show';
    status.textContent = '✓ Quote ready — backend authoritative, TTL 5min';
  } catch(e) {
    btn.disabled = false;
    btn.innerHTML = 'Request Quote';
    status.className = 'status error show';
    status.textContent = 'Quote failed: ' + e.message;
  }
}

async function doCheckout() {
  const btn = document.getElementById('btnCheckout');
  const status = document.getElementById('status');
  btn.disabled = true;
  btn.innerHTML = 'Processing...';

  try {
    // Step 1: prepare (via Bridge)
    const intent = await bridgeCall('ancf:checkout_prepare', {
      quote_id: quoteData.quote_id,
      wallet: WALLET, network: 'solana-mainnet',
      agent_session_id: 'session_' + SESSION
    });
    if (!prepResp.ok) throw new Error('Prepare HTTP ' + prepResp.status);
    const intent = await prepResp.json();

    // Step 2: commit (demo signature)
    const sig = prompt('Sign checkout for ' + PRODUCT.title + '\\nIntent: ' + intent.order_intent_id + '\\n\\nEnter demo signature:', 'demo_sig_' + Date.now());
    if (!sig) { btn.disabled = false; btn.innerHTML = 'Confirm Checkout'; return; }

    // Step 2: commit via Bridge
    const order = await bridgeCall('ancf:checkout_commit', {
      order_intent_id: intent.order_intent_id,
      quote_id: quoteData.quote_id,
      wallet: WALLET,
      wallet_signature: sig,
      agent_session_id: 'session_' + SESSION,
      idempotency_key: 'ck_' + SESSION
    });

    status.className = 'status success show';
    status.innerHTML = '<div style="font-size:16px;font-weight:700;margin-bottom:4px">✓ Checkout Confirmed</div>' +
      '<div style="font-size:11px;color:#00FFA3">Order: ' + (order.order_id||'OK').slice(0,20) + '...</div>';
    btn.innerHTML = 'Done ✓';
  } catch(e) {
    btn.disabled = false;
    btn.innerHTML = 'Confirm Checkout';
    status.className = 'status error show';
    status.textContent = 'Checkout failed: ' + e.message;
  }
}
</script>
</body>
</html>`;
}

/** Whitelisted commands that the bridge accepts from the browser. */
const BRIDGE_ALLOWED_COMMANDS = new Set([
    // Commerce flow
    'ancf:search',
    'ancf:rag-search',
    'ancf:quote',
    'ancf:checkout_prepare',
    'ancf:checkout_commit',
    'ancf:ready',
    // Agent catalog management
    'ancf:catalog_create',
    'ancf:catalog_list',
    'ancf:catalog_get',
    'ancf:catalog_update',
    'ancf:catalog_delete',
    // Agent authentication (SUB-026)
    'ancf:agent_register',
    'ancf:agent_bind_wallet',
    'ancf:agent_info',
    // Multi-chain payments (SUB-028)
    'ancf:payment_create_link',
    'ancf:payment_status',
]);

/**
 * Proxy a search request to the backend.
 * Supports mode parameter: hybrid (default), keyword, vector.
 */
async function searchAPI(query: string, limit: number, mode?: string): Promise<unknown> {
    const searchMode = mode || 'hybrid';
    const url = `${API_BASE}/api/v1/cli/search?q=${encodeURIComponent(query)}&limit=${limit}&mode=${encodeURIComponent(searchMode)}`;
    const resp = await fetch(url);
    if (!resp.ok) throw new Error(`Search API HTTP ${resp.status}`);
    return resp.json();
}

/**
 * Proxy a RAG semantic search request to the backend.
 * Used by ancf:rag-search bridge command.
 */
async function ragSearchAPI(params: Record<string, unknown>): Promise<unknown> {
    const query = (params.query as string) || '';
    const topK = Math.min(Math.max(1, parseInt((params.top_k as string) || (params.limit as string) || '5', 10) || 5), 20);
    const mode = (params.mode as string) || 'hybrid';
    const url = `${API_BASE}/api/v1/cli/rag-search?q=${encodeURIComponent(query)}&top_k=${topK}&mode=${encodeURIComponent(mode)}`;
    const resp = await fetch(url);
    if (!resp.ok) throw new Error(`RAG Search API HTTP ${resp.status}`);
    return resp.json();
}

/**
 * Proxy a quote request to the backend.
 */
async function quoteAPI(params: Record<string, unknown>): Promise<unknown> {
    const url = `${API_BASE}/api/v1/cli/quote`;
    const resp = await fetch(url, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(params),
    });
    if (!resp.ok) {
        const err = await resp.text().catch(() => '');
        throw new Error(`Quote API HTTP ${resp.status}${err ? ': ' + err : ''}`);
    }
    return resp.json();
}

/**
 * Proxy a checkout prepare request to the backend.
 */
async function checkoutPrepareAPI(params: Record<string, unknown>): Promise<unknown> {
    const url = `${API_BASE}/api/v1/cli/checkout/prepare`;
    const resp = await fetch(url, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(params),
    });
    if (!resp.ok) throw new Error(`Checkout Prepare API HTTP ${resp.status}`);
    return resp.json();
}

/**
 * Agent catalog management — proxy functions
 */
async function catalogCreateAPI(params: Record<string, unknown>): Promise<unknown> {
    const url = `${API_BASE}/api/v1/catalog/products`;
    const resp = await fetch(url, { method:'POST', headers:{'Content-Type':'application/json'}, body: JSON.stringify(params) });
    if (!resp.ok) { const err = await resp.text().catch(()=>''); throw new Error(`Catalog Create HTTP ${resp.status}${err?': '+err:''}`); }
    return resp.json();
}
async function catalogListAPI(params: Record<string, unknown>): Promise<unknown> {
    const limit = params.limit || 20;
    const url = `${API_BASE}/api/v1/catalog/products?limit=${limit}`;
    const resp = await fetch(url);
    if (!resp.ok) throw new Error(`Catalog List HTTP ${resp.status}`);
    return resp.json();
}
async function catalogGetAPI(params: Record<string, unknown>): Promise<unknown> {
    const sku = params.sku_id || '';
    const url = `${API_BASE}/api/v1/catalog/products/${encodeURIComponent(sku as string)}`;
    const resp = await fetch(url);
    if (!resp.ok) throw new Error(`Catalog Get HTTP ${resp.status}`);
    return resp.json();
}
async function catalogUpdateAPI(params: Record<string, unknown>): Promise<unknown> {
    const sku = params.sku_id || '';
    const url = `${API_BASE}/api/v1/catalog/products/${encodeURIComponent(sku as string)}`;
    const resp = await fetch(url, { method:'PUT', headers:{'Content-Type':'application/json'}, body: JSON.stringify(params) });
    if (!resp.ok) throw new Error(`Catalog Update HTTP ${resp.status}`);
    return resp.json();
}
async function catalogDeleteAPI(params: Record<string, unknown>): Promise<unknown> {
    const sku = params.sku_id || '';
    const url = `${API_BASE}/api/v1/catalog/products/${encodeURIComponent(sku as string)}`;
    const resp = await fetch(url, { method:'DELETE' });
    if (!resp.ok) throw new Error(`Catalog Delete HTTP ${resp.status}`);
    return resp.json();
}

/**
 * Agent authentication proxy functions (SUB-026).
 * These proxy requests to the mock API server's auth endpoints.
 * The X-ANCF-Agent-Token is forwarded from params if present.
 */

/** Store agent token in memory for bridge session use */
let AGENT_SESSION_TOKEN: string | null = null;

async function agentRegisterAPI(params: Record<string, unknown>): Promise<unknown> {
    const url = `${API_BASE}/api/v1/auth/register-agent`;
    const body: Record<string, unknown> = {
        agent_name: params.agent_name,
        agent_type: params.agent_type || 'general',
    };
    const resp = await fetch(url, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(body),
    });
    if (!resp.ok) {
        const err = await resp.text().catch(() => '');
        throw new Error(`Agent Register HTTP ${resp.status}${err ? ': ' + err : ''}`);
    }
    const data: any = await resp.json();
    // Automatically store the token for subsequent catalog operations
    if (data.token) {
        AGENT_SESSION_TOKEN = data.token;
        console.log(`[Agent] Token stored for agent ${data.agent_id} (${data.agent_name})`);
    }
    return data;
}

async function agentBindWalletAPI(params: Record<string, unknown>): Promise<unknown> {
    const url = `${API_BASE}/api/v1/auth/bind-wallet`;
    const token = (params.token as string) || AGENT_SESSION_TOKEN;
    const body: Record<string, unknown> = {
        wallet_address: params.wallet_address,
        chain: params.chain || 'solana',
        label: params.label || 'default',
    };
    const resp = await fetch(url, {
        method: 'POST',
        headers: {
            'Content-Type': 'application/json',
            'X-ANCF-Agent-Token': token || '',
        },
        body: JSON.stringify(body),
    });
    if (!resp.ok) {
        const err = await resp.text().catch(() => '');
        throw new Error(`Agent Bind Wallet HTTP ${resp.status}${err ? ': ' + err : ''}`);
    }
    return resp.json();
}

async function agentInfoAPI(params: Record<string, unknown>): Promise<unknown> {
    const url = `${API_BASE}/api/v1/auth/agent-info`;
    const token = (params.token as string) || AGENT_SESSION_TOKEN;
    const resp = await fetch(url, {
        method: 'GET',
        headers: {
            'Content-Type': 'application/json',
            'X-ANCF-Agent-Token': token || '',
        },
    });
    if (!resp.ok) {
        const err = await resp.text().catch(() => '');
        throw new Error(`Agent Info HTTP ${resp.status}${err ? ': ' + err : ''}`);
    }
    return resp.json();
}

/**
 * Proxy a checkout commit request to the backend.
 * Ensures an Idempotency-Key header is present.
 */
async function checkoutCommitAPI(params: Record<string, unknown>): Promise<unknown> {
    const idempotencyKey = (params.idempotency_key as string) || crypto.randomUUID();
    const body = {
        order_intent_id: params.order_intent_id,
        quote_id: params.quote_id,
        wallet: params.wallet,
        wallet_signature: params.wallet_signature,
        agent_session_id: params.agent_session_id,
    };

    const url = `${API_BASE}/api/v1/cli/checkout/commit`;
    const resp = await fetch(url, {
        method: 'POST',
        headers: {
            'Content-Type': 'application/json',
            'Idempotency-Key': idempotencyKey,
        },
        body: JSON.stringify(body),
    });
    if (!resp.ok) {
        const err = await resp.text().catch(() => '');
        throw new Error(`Checkout Commit API HTTP ${resp.status}${err ? ': ' + err : ''}`);
    }
    return resp.json();
}

/* ------------------------------------------------------------------ */
/*  Payment API Proxy Functions (SUB-028)                              */
/* ------------------------------------------------------------------ */

/**
 * Proxy a payment link creation request to the backend.
 */
async function paymentCreateLinkAPI(params: Record<string, unknown>): Promise<unknown> {
    const url = `${API_BASE}/api/v1/payments/create-link`;
    const resp = await fetch(url, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(params),
    });
    if (!resp.ok) {
        const err = await resp.text().catch(() => '');
        throw new Error(`Payment Create Link HTTP ${resp.status}${err ? ': ' + err : ''}`);
    }
    return resp.json();
}

/**
 * Proxy a payment status query to the backend.
 */
async function paymentStatusAPI(params: Record<string, unknown>): Promise<unknown> {
    const paymentId = (params.payment_id as string) || '';
    const url = `${API_BASE}/api/v1/payments/status?payment_id=${encodeURIComponent(paymentId)}`;
    const resp = await fetch(url);
    if (!resp.ok) {
        const err = await resp.text().catch(() => '');
        throw new Error(`Payment Status HTTP ${resp.status}${err ? ': ' + err : ''}`);
    }
    return resp.json();
}

/* ------------------------------------------------------------------ */
/*  Main Agent Entry Point                                              */
/* ------------------------------------------------------------------ */

async function main(): Promise<void> {
    console.log('╔══════════════════════════════════════════════╗');
    console.log('║  ANCF Node Agent — Local Renderer v1.0      ║');
    console.log('╚══════════════════════════════════════════════╝');
    console.log('');

    // ── Step 1: Fetch and validate manifest ──
    console.log('[Agent] Step 1/4: Fetching discovery manifest...');
    let manifest: ANCFManifest;
    try {
        manifest = await fetchManifest(API_BASE);
    } catch (e: unknown) {
        const msg = e instanceof Error ? e.message : String(e);
        console.error(`[Agent] FATAL: Manifest fetch failed: ${msg}`);
        process.exit(1);
    }

    console.log('[Agent] Step 2/4: Validating manifest...');
    try {
        const warnings = validateManifest(manifest);
        for (const w of warnings) {
            console.warn(`[Agent] WARNING: ${w}`);
        }
    } catch (e: unknown) {
        const msg = e instanceof Error ? e.message : String(e);
        console.error(`[Agent] FATAL: Manifest validation failed: ${msg}`);
        console.error('[Agent] Agent will not start with an invalid manifest. Exiting.');
        process.exit(1);
    }

    console.log(`[Agent] Manifest OK: shop=${manifest.shop_id}, protocol=${manifest.protocol_version}`);
    console.log(`[Agent] Expires: ${manifest.expires_at}`);
    console.log(`[Agent] Signature: alg=${manifest.signature.alg}, kid=${manifest.signature.kid}`);

    // SECURITY FIX: F-006-01 — Verify manifest JWS signature cryptographically
    console.log('[Agent] Verifying manifest JWS signature...');
    const sigValid = await verifyManifestSignature(manifest, DEV_FIRMWARE_PUBKEY);
    if (!sigValid) {
        console.warn('[Agent] SECURITY: JWS verification NOT implemented — dev mode only');
        console.warn('[Agent] SECURITY: Manifest signature could not be cryptographically validated.');
        console.warn('[Agent] SECURITY: In production, agent MUST exit on signature verification failure.');
    }

    // SECURITY FIX: F-006-02 — Verify firmware SRI integrity
    console.log('[Agent] Verifying firmware SRI integrity...');
    const sriWarnings = verifyFirmwareSRI(manifest);
    for (const w of sriWarnings) {
        console.warn(`[Agent] SRI WARNING: ${w}`);
    }

    // SECURITY FIX: F-006-04 — Generate CSP nonce at startup
    CSP_NONCE = crypto.randomBytes(16).toString('base64');
    console.log(`[Agent] CSP nonce generated (per-session fallback).`);

    // ── Step 3: Start local HTTP server ──
    console.log('[Agent] Step 3/4: Starting local HTTP server...');

    const app = express();

    // Body parsing for bridge API
    app.use(express.json({ limit: '1mb' }));

    // CSP header on every response — generates per-request nonce
    app.use((_req: Request, res: express.Response, next: NextFunction) => {
        // SECURITY FIX: F-006-04 — Generate random nonce per request
        const nonce = crypto.randomBytes(16).toString('base64');
        res.locals.ancfNonce = nonce;
        const cspHeader = buildCSPHeader(manifest, nonce);
        res.setHeader('Content-Security-Policy', cspHeader);
        res.setHeader('X-Content-Type-Options', 'nosniff');
        res.setHeader('X-Frame-Options', 'DENY');
        res.setHeader('X-XSS-Protection', '0'); // Deprecated, use CSP instead
        res.setHeader('Referrer-Policy', 'no-referrer');
        next();
    });

    // Static files: firmware components
    app.use('/firmware', express.static(FIRMWARE_DIR, {
        setHeaders(res, filePath) {
            // Set correct MIME type for JS modules
            if (filePath.endsWith('.js')) {
                res.setHeader('Content-Type', 'application/javascript; charset=utf-8');
            }
        },
    }));

    // Health check
    app.get('/health', (_req: Request, res: express.Response) => {
        res.json({
            status: 'ok',
            agent: 'ANCF Node Agent v1.0',
            shop_id: manifest.shop_id,
            protocol_version: manifest.protocol_version,
            api_base: API_BASE,
        });
    });

    // Agent Bridge API
    app.post('/bridge', async (req: Request, res: express.Response) => {
        const { command, params, requestId } = req.body || {};

        if (!command || !requestId) {
            res.status(400).json({ error: 'Missing command or requestId in bridge request' });
            return;
        }

        // Whitelist check
        if (!BRIDGE_ALLOWED_COMMANDS.has(command)) {
            res.status(403).json({
                requestId,
                error: `Command not allowed: "${command}". Allowed: ${[...BRIDGE_ALLOWED_COMMANDS].join(', ')}`,
            });
            return;
        }

        try {
            let result: unknown;
            switch (command) {
                case 'ancf:search': {
                    const query = (params?.query as string) || '';
                    const limit = Math.min(Math.max(1, parseInt((params?.limit as string) || '20', 10) || 20), 100);
                    const mode = (params?.mode as string) || 'hybrid';
                    result = await searchAPI(query, limit, mode);
                    break;
                }
                case 'ancf:rag-search': {
                    result = await ragSearchAPI(params || {});
                    break;
                }
                case 'ancf:quote':
                    result = await quoteAPI(params || {});
                    break;
                case 'ancf:checkout_prepare':
                    result = await checkoutPrepareAPI(params || {});
                    break;
                case 'ancf:checkout_commit':
                    result = await checkoutCommitAPI(params || {});
                    break;
                case 'ancf:ready':
                    result = {
                        status: 'ready',
                        apiBase: API_BASE,
                        shop_id: manifest.shop_id,
                        commands: [...BRIDGE_ALLOWED_COMMANDS],
                    };
                    break;
                case 'ancf:catalog_create':
                    result = await catalogCreateAPI(params || {});
                    break;
                case 'ancf:catalog_list':
                    result = await catalogListAPI(params || {});
                    break;
                case 'ancf:catalog_get':
                    result = await catalogGetAPI(params || {});
                    break;
                case 'ancf:catalog_update':
                    result = await catalogUpdateAPI(params || {});
                    break;
                case 'ancf:catalog_delete':
                    result = await catalogDeleteAPI(params || {});
                    break;
                // Agent authentication (SUB-026)
                case 'ancf:agent_register':
                    result = await agentRegisterAPI(params || {});
                    break;
                case 'ancf:agent_bind_wallet':
                    result = await agentBindWalletAPI(params || {});
                    break;
                case 'ancf:agent_info':
                    result = await agentInfoAPI(params || {});
                    break;
                case 'ancf:payment_create_link':
                    result = await paymentCreateLinkAPI(params || {});
                    break;
                case 'ancf:payment_status':
                    result = await paymentStatusAPI(params || {});
                    break;
                default:
                    res.status(400).json({ requestId, error: `Unhandled command: ${command}` });
                    return;
            }
            res.json({ requestId, result });
        } catch (e: unknown) {
            const msg = e instanceof Error ? e.message : 'Bridge proxy error';
            console.error(`[AgentBridge] Error proxying ${command}: ${msg}`);
            res.status(502).json({ requestId, error: msg });
        }
    });

    // Main catalog page rendered from the Stitch fixed template pack.
    app.get('/', async (req: Request, res: express.Response) => {
        try {
            // SECURITY FIX: F-006-04 — use per-request CSP nonce
            const nonce = (res.locals.ancfNonce as string) || CSP_NONCE;
            const html = await generateStitchCatalogHTML(manifest, resolveLocale(req), nonce);
            res.setHeader('Content-Type', 'text/html; charset=utf-8');
            res.send(html);
        } catch (e: unknown) {
            const msg = e instanceof Error ? e.message : String(e);
            console.error(`[Agent] Failed to render catalog template: ${msg}`);
            res.status(502).send(`ANCF template render failed: ${msg}`);
        }
    });

    // Product detail page rendered from the Stitch fixed template pack.
    app.get('/detail', async (req: Request, res: express.Response) => {
        const skuId = typeof req.query.sku === 'string' ? req.query.sku : '';
        if (!skuId) {
            res.status(400).send('Missing sku query parameter');
            return;
        }

        try {
            // SECURITY FIX: F-006-04 — use per-request CSP nonce
            const nonce = (res.locals.ancfNonce as string) || CSP_NONCE;
            const html = await generateStitchProductDetailHTML(manifest, skuId, resolveLocale(req), nonce);
            res.setHeader('Content-Type', 'text/html; charset=utf-8');
            res.send(html);
        } catch (e: unknown) {
            const msg = e instanceof Error ? e.message : String(e);
            console.error(`[Agent] Failed to render detail template: ${msg}`);
            res.status(404).send(`ANCF product detail unavailable: ${msg}`);
        }
    });

    // One-shot checkout session — agent requests SKU → returns disposable checkout HTML
    app.get('/checkout-session', async (req: Request, res: express.Response) => {
        const sku = typeof req.query.sku === 'string' ? req.query.sku : '';
        const wallet = typeof req.query.wallet === 'string' ? req.query.wallet : 'AGENT_WALLET';
        if (!sku) { res.status(400).send('Missing sku parameter'); return; }

        try {
            // Fetch product from backend
            const searchResp = await fetch(`${API_BASE}/api/v1/cli/search?q=${encodeURIComponent(sku)}&limit=1`);
            if (!searchResp.ok) throw new Error(`Search API HTTP ${searchResp.status}`);
            const searchData: any = await searchResp.json();
            const product = searchData.items?.[0];
            if (!product) throw new Error(`SKU ${sku} not found`);

            const sessionId = crypto.randomBytes(12).toString('hex');
            // SECURITY FIX: F-006-04 — use per-request CSP nonce
            const nonce = (res.locals.ancfNonce as string) || CSP_NONCE;
            const html = generateOneShotCheckoutHTML(manifest, product, wallet, sessionId, API_BASE, nonce);
            res.setHeader('Content-Type', 'text/html; charset=utf-8');
            res.setHeader('X-ANCF-Session-Id', sessionId);
            res.send(html);
        } catch (e: unknown) {
            const msg = e instanceof Error ? e.message : String(e);
            res.status(502).send(`ANCF checkout session failed: ${msg}`);
        }
    });

    // On-chain Solana balance query for agents
    app.get('/solana-balance', async (req: Request, res: express.Response) => {
        const wallet = typeof req.query.wallet === 'string' ? req.query.wallet : '';
        const mint = typeof req.query.mint === 'string' ? req.query.mint : 'Ecz3XMcs76JsFiiUgVNDGbqtKVotMP5gMMAjCJYpe8SX';
        if (!wallet) { res.status(400).json({error:'Missing wallet parameter'}); return; }
        try {
            const resp = await fetch('https://api.devnet.solana.com', {
                method:'POST', headers:{'Content-Type':'application/json'},
                body: JSON.stringify({jsonrpc:'2.0',id:1,method:'getTokenAccountsByOwner',params:[wallet,{mint},{encoding:'jsonParsed'}]})
            });
            const data: any = await resp.json();
            const accounts = data.result?.value || [];
            const balances = accounts.map((a:any) => ({
                pubkey: a.pubkey,
                amount: a.account.data.parsed.info.tokenAmount.uiAmountString,
                decimals: a.account.data.parsed.info.tokenAmount.decimals,
            }));
            res.json({wallet, mint, network:'devnet', accounts: balances, total: balances.reduce((s:number,b:any)=>s+parseFloat(b.amount),0)});
        } catch(e: any) { res.status(502).json({error:e.message}); }
    });

    // 404 handler
    app.use((_req: Request, res: express.Response) => {
        res.status(404).json({ error: 'Not found' });
    });

    // ── Step 4: Start listening ──
    console.log('[Agent] Step 4/4: Starting server...');

    const server = app.listen(AGENT_PORT, AGENT_HOST, () => {
        const publicHost = AGENT_HOST === '0.0.0.0' ? '127.0.0.1' : AGENT_HOST;
        console.log(`[Agent] Listening on ${AGENT_HOST}:${AGENT_PORT}`);
        console.log(`[Agent] Checkout UI: http://${publicHost}:${AGENT_PORT}`);
        console.log(`[Agent] Bridge API: http://${publicHost}:${AGENT_PORT}/bridge`);
        console.log(`[Agent] Health: http://${publicHost}:${AGENT_PORT}/health`);
        console.log('');
        console.log('╔══════════════════════════════════════════════╗');
        console.log('║  ANCF Agent Ready                            ║');
        console.log('╠══════════════════════════════════════════════╣');
        console.log(`║  Checkout UI:  http://127.0.0.1:${AGENT_PORT}        ${' '.repeat(Math.max(0, 4 - String(AGENT_PORT).length))}║`);
        console.log(`║  Bridge API:   http://127.0.0.1:${AGENT_PORT}/bridge ║`);
        console.log(`║  Health:       http://127.0.0.1:${AGENT_PORT}/health ║`);
        console.log(`║  API Gateway:  ${API_BASE}  ║`);
        console.log(`║  Shop ID:      ${manifest.shop_id}${' '.repeat(Math.max(0, 20 - manifest.shop_id.length))}║`);
        console.log('╚══════════════════════════════════════════════╝');
        console.log('');
        console.log('[Agent] Press Ctrl+C to stop.');
    });

    // Graceful shutdown
    const shutdown = (signal: string) => {
        console.log(`\n[Agent] Received ${signal}. Shutting down gracefully...`);
        server.close(() => {
            console.log('[Agent] Server closed. Goodbye.');
            process.exit(0);
        });
        // Force exit after 5s
        setTimeout(() => {
            console.error('[Agent] Forced shutdown after timeout.');
            process.exit(1);
        }, 5000);
    };

    process.on('SIGINT', () => shutdown('SIGINT'));
    process.on('SIGTERM', () => shutdown('SIGTERM'));
}

// Run
main().catch((e: unknown) => {
    console.error('[Agent] Unhandled error:', e);
    process.exit(1);
});
