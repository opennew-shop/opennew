/**
 * Agent Bridge — Secure communication bridge between the local checkout UI
 * and the ANCF backend API gateway.
 *
 * Architecture:
 *   Local checkout page  <--postMessage-->  AgentBridge (this module)  -->  HTTP API
 *
 * The bridge runs in the Agent's process context (Node.js). The local
 * checkout page in the browser sends commands via window.postMessage or
 * direct function call to the bridge, which validates them against a
 * whitelist before forwarding to the backend.
 *
 * Security rules:
 *   1. Only whitelisted commands are accepted.
 *   2. The bridge never exposes raw fetch capability to the local page.
 *   3. All requests go through the bridge's own fetch (or HTTP client).
 *   4. The bridge validates responses before returning to the page.
 *
 * Whitelisted commands:
 *   ancf:search          - Search products
 *   ancf:quote           - Request a quote
 *   ancf:checkout_prepare - Prepare an order intent
 *   ancf:checkout_commit - Commit a checkout
 *   ancf:ready           - Health check / readiness probe
 *
 * Usage in Node.js (Agent process):
 *   const bridge = new AgentBridge('http://127.0.0.1:8080');
 *   bridge.exposeToBrowser();
 *
 * Usage in browser (checkout page):
 *   // Components auto-detect __ancfBridge on window
 *   const result = await window.__ancfBridge.handleCommand({
 *       command: 'ancf:search',
 *       params: { query: 'H100', limit: 20 },
 *       requestId: crypto.randomUUID(),
 *   });
 */

/* ------------------------------------------------------------------ */
/*  Type Definitions                                                   */
/* ------------------------------------------------------------------ */

interface AgentBridgeCommand {
    command: string;
    params: Record<string, unknown>;
    requestId: string;
}

interface AgentBridgeResponse {
    requestId: string;
    result?: unknown;
    error?: string;
}

type BridgeMessageEvent = MessageEvent<AgentBridgeCommand>;

/* ------------------------------------------------------------------ */
/*  API Response Types (subset used for response validation)          */
/* ------------------------------------------------------------------ */

interface SearchResultItem {
    sku_id: string;
    title: string;
    price: { currency: string; amount_minor: string; scale: number };
    stock_hint?: number;
}

interface SearchResponse {
    items: SearchResultItem[];
}

interface QuoteResponse {
    quote_id: string;
    currency: string;
    total_minor: string;
    scale: number;
    expires_at: string;
    lines: unknown[];
}

interface CheckoutPrepareResponse {
    order_intent_id: string;
    quote_id: string;
    signable_payload: Record<string, unknown>;
}

interface CheckoutCommitResponse {
    order_id: string;
    status: string;
}

/* ------------------------------------------------------------------ */
/*  AgentBridge Class                                                  */
/* ------------------------------------------------------------------ */

class AgentBridge {
    /** Set of allowed command names. Anything not in this set is rejected. */
    private readonly allowedCommands: ReadonlySet<string> = new Set([
        'ancf:search',
        'ancf:quote',
        'ancf:checkout_prepare',
        'ancf:checkout_commit',
        'ancf:ready',
    ]);

    /** Base URL of the ANCF API gateway. */
    private apiBase: string;

    /** Origin(s) from which postMessage events are accepted. */
    private allowedOrigins: Set<string>;

    constructor(apiBase: string, allowedOrigins?: string[]) {
        this.apiBase = apiBase.replace(/\/+$/, ''); // strip trailing slash
        this.allowedOrigins = new Set(allowedOrigins || [
            `http://127.0.0.1:3000`,
            `http://localhost:3000`,
        ]);
    }

    /**
     * Process a command from the local checkout page.
     * This is the main entry point called directly from JS or via postMessage proxy.
     */
    async handleCommand(cmd: AgentBridgeCommand): Promise<AgentBridgeResponse> {
        const { command, params, requestId } = cmd;

        // 1. Whitelist check
        if (!this.allowedCommands.has(command)) {
            return {
                requestId,
                error: `Command not allowed: ${command}. Allowed: ${[...this.allowedCommands].join(', ')}`,
            };
        }

        // 2. Route to handler
        try {
            let result: unknown;
            switch (command) {
                case 'ancf:search':
                    result = await this.search(params.query as string, (params.limit as number) || 20);
                    break;
                case 'ancf:quote':
                    result = await this.quote(params);
                    break;
                case 'ancf:checkout_prepare':
                    result = await this.checkoutPrepare(params.quote_id as string, params);
                    break;
                case 'ancf:checkout_commit':
                    result = await this.checkoutCommit(params);
                    break;
                case 'ancf:ready':
                    result = { status: 'ready', apiBase: this.apiBase, commands: [...this.allowedCommands] };
                    break;
                default:
                    // TypeScript exhaustiveness — should never reach here
                    return { requestId, error: `Unhandled command: ${command}` };
            }
            return { requestId, result };
        } catch (e: unknown) {
            return {
                requestId,
                error: e instanceof Error ? e.message : 'Bridge internal error',
            };
        }
    }

    // ------------------------------------------------------------------
    // API Proxy Methods
    // ------------------------------------------------------------------

    /** Execute a search against the backend. */
    private async search(query: string, limit: number): Promise<SearchResponse> {
        if (!query || typeof query !== 'string') {
            throw new Error('search requires a non-empty query string');
        }
        const safeQuery = encodeURIComponent(query.trim());
        const safeLimit = Math.min(Math.max(1, Math.floor(limit) || 20), 100);
        const url = `${this.apiBase}/api/v1/cli/search?q=${safeQuery}&limit=${safeLimit}`;
        const resp = await this.fetchWithTimeout(url);
        if (!resp.ok) {
            throw new Error(`Search API returned HTTP ${resp.status}`);
        }
        const data = await resp.json();
        // Validate response shape
        if (!data || !Array.isArray(data.items)) {
            throw new Error('Search response missing items array');
        }
        return data as SearchResponse;
    }

    /** Request a quote from the backend. */
    private async quote(params: Record<string, unknown>): Promise<QuoteResponse> {
        if (!params.wallet || !params.lines || !Array.isArray(params.lines) || params.lines.length === 0) {
            throw new Error('quote requires wallet and a non-empty lines array');
        }
        const url = `${this.apiBase}/api/v1/cli/quote`;
        const resp = await this.fetchWithTimeout(url, {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({
                wallet: params.wallet,
                network: params.network || 'solana-mainnet',
                payment_rail: params.payment_rail,
                lines: params.lines,
            }),
        });
        if (!resp.ok) {
            const errBody = await resp.text().catch(() => '');
            throw new Error(`Quote API returned HTTP ${resp.status}${errBody ? ': ' + errBody : ''}`);
        }
        const data = await resp.json();
        if (!data || !data.quote_id) {
            throw new Error('Quote response missing quote_id');
        }
        return data as QuoteResponse;
    }

    /** Prepare a checkout order intent. */
    private async checkoutPrepare(quoteId: string, params: Record<string, unknown>): Promise<CheckoutPrepareResponse> {
        if (!quoteId) throw new Error('checkout_prepare requires a quote_id');
        const url = `${this.apiBase}/api/v1/cli/checkout/prepare`;
        const resp = await this.fetchWithTimeout(url, {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({
                quote_id: quoteId,
                wallet: params.wallet,
                network: params.network || 'solana-mainnet',
                agent_session_id: params.agent_session_id,
            }),
        });
        if (!resp.ok) {
            throw new Error(`Checkout Prepare API returned HTTP ${resp.status}`);
        }
        const data = await resp.json();
        if (!data || !data.order_intent_id) {
            throw new Error('Checkout prepare response missing order_intent_id');
        }
        return data as CheckoutPrepareResponse;
    }

    /** Commit a checkout. Requires idempotency key and wallet signature. */
    private async checkoutCommit(params: Record<string, unknown>): Promise<CheckoutCommitResponse> {
        const { order_intent_id, quote_id, wallet, wallet_signature, agent_session_id, idempotency_key } = params;
        if (!order_intent_id || !quote_id || !wallet || !wallet_signature) {
            throw new Error('checkout_commit requires order_intent_id, quote_id, wallet, and wallet_signature');
        }
        const idemKey = (idempotency_key as string) || this.generateIdempotencyKey();
        const url = `${this.apiBase}/api/v1/cli/checkout/commit`;
        const resp = await this.fetchWithTimeout(url, {
            method: 'POST',
            headers: {
                'Content-Type': 'application/json',
                'Idempotency-Key': idemKey,
            },
            body: JSON.stringify({
                order_intent_id,
                quote_id,
                wallet,
                wallet_signature,
                agent_session_id: agent_session_id || '',
            }),
        });
        if (!resp.ok) {
            throw new Error(`Checkout Commit API returned HTTP ${resp.status}`);
        }
        const data = await resp.json();
        return data as CheckoutCommitResponse;
    }

    // ------------------------------------------------------------------
    // Utilities
    // ------------------------------------------------------------------

    /** Fetch with a configurable timeout (default 15s). */
    private async fetchWithTimeout(input: RequestInfo, init?: RequestInit, timeoutMs = 15000): Promise<Response> {
        const controller = new AbortController();
        const timer = setTimeout(() => controller.abort(), timeoutMs);
        try {
            const response = await fetch(input, { ...init, signal: controller.signal });
            return response;
        } finally {
            clearTimeout(timer);
        }
    }

    /** Generate a random idempotency key. */
    private generateIdempotencyKey(): string {
        const prefix = 'ck_';
        if (typeof crypto !== 'undefined' && crypto.randomUUID) {
            return prefix + crypto.randomUUID();
        }
        // Fallback
        const arr = new Uint8Array(16);
        if (typeof crypto !== 'undefined' && crypto.getRandomValues) {
            crypto.getRandomValues(arr);
        } else {
            for (let i = 0; i < 16; i++) arr[i] = Math.floor(Math.random() * 256);
        }
        const hex = Array.from(arr).map((b) => b.toString(16).padStart(2, '0')).join('');
        return `${prefix}${hex}`;
    }

    // ------------------------------------------------------------------
    // Browser Integration (when running in checkout page context)
    // ------------------------------------------------------------------

    /**
     * When called from a browser context, this sets up a postMessage listener
     * so that Web Components can communicate through the bridge.
     *
     * The bridge is also exposed as window.__ancfBridge for direct calls.
     */
    exposeToBrowser(): void {
        if (typeof window === 'undefined') return;

        // Expose bridge globally for direct calls from Web Components
        (window as unknown as Record<string, AgentBridge>).__ancfBridge = this;

        // Listen for postMessage commands (e.g., from iframes or cross-origin)
        window.addEventListener('message', (event: BridgeMessageEvent) => {
            // Validate origin
            if (!this.allowedOrigins.has(event.origin) && !this.allowedOrigins.has('*')) {
                return; // silently ignore unauthorized origins
            }

            const cmd = event.data;
            if (!cmd || !cmd.command || !cmd.requestId) return;

            // Process command and post back the result
            this.handleCommand(cmd).then((response) => {
                if (event.source && 'postMessage' in event.source) {
                    (event.source as WindowProxy).postMessage(response, event.origin);
                }
            }).catch((err: Error) => {
                if (event.source && 'postMessage' in event.source) {
                    (event.source as WindowProxy).postMessage(
                        { requestId: cmd.requestId, error: err.message },
                        event.origin,
                    );
                }
            });
        });

        console.log('[AgentBridge] Exposed to browser. Allowed commands:', [...this.allowedCommands]);
    }
}

// --------------------------------------------------------------------
// Export for module environments (Node.js Agent process)
// --------------------------------------------------------------------
export { AgentBridge };
export type {
    AgentBridgeCommand,
    AgentBridgeResponse,
    SearchResponse,
    QuoteResponse,
    CheckoutPrepareResponse,
    CheckoutCommitResponse,
};

// --------------------------------------------------------------------
// Auto-initialize in browser context (checkout page)
// --------------------------------------------------------------------
if (typeof window !== 'undefined') {
    // When loaded as a <script> in the checkout page, auto-init
    const apiBase = (window as unknown as Record<string, string>).__ancfApiBase || 'http://127.0.0.1:8080';
    const bridge = new AgentBridge(apiBase);
    bridge.exposeToBrowser();
}
