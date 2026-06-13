/**
 * ANCF Firmware Bundle Script
 *
 * Copies compiled .js files from dist/ and appends a version hash to filenames.
 * Generates a manifest.json with SRI integrity hashes for each component.
 *
 * Usage: node scripts/bundle.js
 * Input:  dist/*.js (compiled TypeScript output)
 * Output: dist/*.{hash}.js  +  dist/manifest.json
 */

import fs from 'node:fs';
import path from 'node:path';
import crypto from 'node:crypto';
import { fileURLToPath } from 'node:url';

const __filename = fileURLToPath(import.meta.url);
const __dirname = path.dirname(__filename);
const distDir = path.resolve(__dirname, '..', 'dist');
const srcDir = path.resolve(__dirname, '..', 'src');

/** Component order for predictable output. */
const COMPONENT_FILES = [
    'ancf-theme.js',
    'ancf-search.js',
    'ancf-quote.js',
    'ancf-checkout.js',
    'agent-bridge.js',
];

/**
 * Compute SHA-384 hash of a file buffer, return as base64 SRI string.
 */
function computeSRI(buffer) {
    const hash = crypto.createHash('sha384');
    hash.update(buffer);
    const digest = hash.digest('base64');
    return `sha384-${digest}`;
}

/**
 * Compute a content-based hash for filename versioning (first 12 hex chars of SHA-256).
 */
function computeShortHash(buffer) {
    const hash = crypto.createHash('sha256');
    hash.update(buffer);
    return hash.digest('hex').slice(0, 12);
}

/**
 * Main bundling routine.
 */
function bundle() {
    // Ensure dist directory exists
    if (!fs.existsSync(distDir)) {
        fs.mkdirSync(distDir, { recursive: true });
    }

    const manifest = {
        generated_at: new Date().toISOString(),
        components: [],
    };

    for (const filename of COMPONENT_FILES) {
        const srcPath = path.join(distDir, filename);

        // Check if compiled output exists (TypeScript may not have run yet)
        if (!fs.existsSync(srcPath)) {
            console.warn(`[bundle] WARNING: ${filename} not found in dist/ — skipping. Run 'tsc' first.`);
            continue;
        }

        const buffer = fs.readFileSync(srcPath);
        const sri = computeSRI(buffer);
        const shortHash = computeShortHash(buffer);

        // Build hash-named filename: ancf-search.abc123.js
        const baseName = filename.replace(/\.js$/, '');
        const hashName = `${baseName}.${shortHash}.js`;
        const hashPath = path.join(distDir, hashName);

        // Copy file with hash-named filename
        fs.copyFileSync(srcPath, hashPath);

        console.log(`[bundle] ${filename} -> ${hashName}  (SRI: ${sri})`);

        manifest.components.push({
            name: baseName,
            original: filename,
            hash_name: hashName,
            integrity: sri,
            type: 'module',
            size_bytes: buffer.length,
        });
    }

    // Write manifest
    const manifestPath = path.join(distDir, 'manifest.json');
    fs.writeFileSync(manifestPath, JSON.stringify(manifest, null, 2), 'utf-8');
    console.log(`[bundle] Manifest written to ${manifestPath}`);
    console.log(`[bundle] Done. ${manifest.components.length} components bundled.`);
}

bundle();
