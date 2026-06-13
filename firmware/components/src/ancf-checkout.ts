/**
 * <ancf-checkout> - ANCF Checkout Confirmation Component
 *
 * Displays the final order summary before the user commits a transaction.
 * Shows domain, shop_id, wallet address, SKU list, total price, and
 * payment method. After confirmation, dispatches the checkout event.
 *
 * Attributes:
 *   order-intent-data - JSON string of the order intent response (from checkout/prepare).
 *   quote-data        - JSON string of the quote response (for line-item details).
 *   wallet-address    - The user's wallet address (display-only).
 *   shop-domain       - The shop domain (e.g., "yourshop.com").
 *   shop-id           - The shop identifier from the manifest.
 *
 * Events:
 *   ancf:checkout-confirm - User confirmed checkout.
 *       detail: { order_intent_id, quote_id, wallet }
 *
 * Security:
 *   - All display values are HTML-escaped.
 *   - Prices are display-only; the backend is authoritative.
 *   - No scripts from external data are executed.
 *   - The component does NOT initiate any network requests.
 *   - The warning banner explicitly states this is a non-authoritative UI.
 */

interface SignablePayload {
    domain: string;
    shop_id: string;
    network: string;
    wallet: string;
    quote_id: string;
    total_minor: string;
    currency: string;
    expires_at: string;
    nonce: string;
}

interface OrderIntentResponse {
    order_intent_id: string;
    quote_id: string;
    signable_payload: SignablePayload;
}

interface QuoteLine {
    sku_id: string;
    quantity: number;
    unit_price_minor: string;
    line_total_minor: string;
}

interface QuoteData {
    quote_id: string;
    currency: string;
    total_minor: string;
    scale: number;
    expires_at: string;
    lines: QuoteLine[];
}

class ANCFCheckout extends HTMLElement {
    private shadow: ShadowRoot;
    private orderIntent: OrderIntentResponse | null = null;
    private quote: QuoteData | null = null;
    private confirmed: boolean = false;

    static observedAttributes = ['order-intent-data', 'quote-data', 'wallet-address', 'shop-domain', 'shop-id'];

    constructor() {
        super();
        this.shadow = this.attachShadow({ mode: 'open' });
    }

    connectedCallback(): void {
        this.parseData();
        this.render();
    }

    attributeChangedCallback(): void {
        this.parseData();
        this.render();
    }

    private parseData(): void {
        // Parse order-intent-data
        const intentRaw = this.getAttribute('order-intent-data');
        if (intentRaw) {
            try {
                this.orderIntent = JSON.parse(intentRaw) as OrderIntentResponse;
            } catch { this.orderIntent = null; }
        }

        // Parse quote-data
        const quoteRaw = this.getAttribute('quote-data');
        if (quoteRaw) {
            try {
                this.quote = JSON.parse(quoteRaw) as QuoteData;
            } catch { this.quote = null; }
        }
    }

    /** Format amount from minor units. */
    private formatAmount(amountMinor: string, scale: number): string {
        const major = parseInt(amountMinor, 10) / Math.pow(10, scale);
        return major.toFixed(Math.min(scale, 6));
    }

    /** Get the wallet address from attribute or parsed data. */
    private getWallet(): string {
        return this.getAttribute('wallet-address')
            || this.orderIntent?.signable_payload?.wallet
            || 'Not provided';
    }

    /** Get the shop domain. */
    private getDomain(): string {
        return this.getAttribute('shop-domain')
            || this.orderIntent?.signable_payload?.domain
            || 'Unknown';
    }

    /** Get the shop ID. */
    private getShopId(): string {
        return this.getAttribute('shop-id')
            || this.orderIntent?.signable_payload?.shop_id
            || 'Unknown';
    }

    /** Get the network. */
    private getNetwork(): string {
        return this.orderIntent?.signable_payload?.network || 'Unknown';
    }

    /** Handle user clicking Confirm Checkout. */
    private onConfirm(): void {
        if (this.confirmed || !this.orderIntent) return;
        this.confirmed = true;
        this.dispatchEvent(new CustomEvent('ancf:checkout-confirm', {
            detail: {
                order_intent_id: this.orderIntent.order_intent_id,
                quote_id: this.orderIntent.quote_id,
                wallet: this.getWallet(),
            },
            bubbles: true,
            composed: true,
        }));
        this.render(); // Update button state
    }

    // --------------------------------------------------------------------
    // Render
    // --------------------------------------------------------------------

    private render(): void {
        const style = this.renderStyles();
        const html = this.renderHTML();
        this.shadow.innerHTML = `${style}${html}`;

        const btn = this.shadow.querySelector('.confirm-checkout-btn');
        btn?.addEventListener('click', () => this.onConfirm());
    }

    private renderStyles(): string {
        return `<style>
:host {
    display: block;
    font-family: var(--ancf-font, system-ui, sans-serif);
    color: var(--ancf-text, #FFFFFF);
}
.checkout-card {
    border: 1px solid var(--ancf-border, #2A2A4A);
    border-radius: var(--ancf-radius, 8px);
    background: var(--ancf-surface, #1A1A2E);
    overflow: hidden;
}

/* Warning banner */
.warning-banner {
    background: #2A1A00;
    border-bottom: 1px solid #FF8800;
    padding: 12px 16px;
    color: #FFAA00;
    font-size: 13px;
    line-height: 1.5;
    display: flex;
    align-items: flex-start;
    gap: 8px;
}
.warning-icon {
    font-size: 18px;
    flex-shrink: 0;
}

/* Shop info section */
.shop-info {
    padding: 16px;
    border-bottom: 1px solid #1E1E32;
}
.shop-domain {
    font-size: 12px;
    color: #888;
    margin-bottom: 4px;
}
.shop-id {
    font-size: 14px;
    color: var(--ancf-primary, #00FFA3);
    font-weight: 600;
    font-family: monospace;
}
.shop-network {
    font-size: 12px;
    color: #666;
    margin-top: 4px;
}

/* Wallet section */
.wallet-section {
    padding: 12px 16px;
    border-bottom: 1px solid #1E1E32;
}
.wallet-label {
    font-size: 11px;
    text-transform: uppercase;
    letter-spacing: 0.5px;
    color: #888;
    margin-bottom: 4px;
}
.wallet-address {
    font-size: 12px;
    font-family: monospace;
    color: #aaa;
    word-break: break-all;
}

/* SKU / lines table */
table {
    width: 100%;
    border-collapse: collapse;
}
th {
    text-align: left;
    padding: 10px 16px;
    font-size: 11px;
    font-weight: 600;
    text-transform: uppercase;
    letter-spacing: 0.5px;
    color: #888;
    border-bottom: 1px solid #1E1E32;
}
td {
    padding: 12px 16px;
    font-size: 14px;
    border-bottom: 1px solid #1A1A2E;
}
tr:last-child td { border-bottom: none; }
.col-sku { font-family: monospace; font-size: 13px; color: #aaa; }
.col-qty { text-align: center; }
.col-unit { text-align: right; white-space: nowrap; }
.col-total { text-align: right; white-space: nowrap; font-weight: 500; }

/* Footer / total + confirm */
.checkout-footer {
    padding: 16px;
    border-top: 2px solid var(--ancf-border, #2A2A4A);
    display: flex;
    justify-content: space-between;
    align-items: center;
    background: rgba(0,0,0,0.2);
}
.grand-total {
    font-size: 20px;
    font-weight: 700;
    color: var(--ancf-primary, #00FFA3);
}
.total-label {
    font-size: 11px;
    color: #888;
    text-transform: uppercase;
    letter-spacing: 0.5px;
}
.confirm-checkout-btn {
    padding: 12px 32px;
    background: var(--ancf-primary, #00FFA3);
    color: #0D0E12;
    border: none;
    border-radius: var(--ancf-radius, 8px);
    font-size: 15px;
    font-weight: 700;
    cursor: pointer;
    transition: background 0.15s;
}
.confirm-checkout-btn:hover:not(:disabled) {
    background: #00CC82;
}
.confirm-checkout-btn:disabled {
    background: #333;
    color: #666;
    cursor: not-allowed;
}

/* Order intent ID display */
.order-intent-row {
    padding: 8px 16px;
    font-size: 11px;
    color: #555;
    display: flex;
    justify-content: space-between;
    background: rgba(0,0,0,0.3);
}
.intent-id {
    font-family: monospace;
}

/* Empty state */
.empty-state {
    padding: 24px;
    text-align: center;
    color: #666;
    font-size: 14px;
}
</style>`;
    }

    private renderHTML(): string {
        let body = '';

        // Warning banner (always shown — per security requirement)
        body += `<div class="warning-banner">
            <span class="warning-icon">&#9888;</span>
            <span>This is a temporary local checkout interface. Prices, inventory, and transaction status are non-authoritative. Final confirmation is processed by the backend.</span>
        </div>`;

        if (!this.orderIntent) {
            body += `<div class="empty-state">No order intent available. Complete quote and prepare steps first.</div>`;
            return `<div class="checkout-card">${body}</div>`;
        }

        const intent = this.orderIntent;
        const q = this.quote;
        const payload = intent.signable_payload;
        const scale = q?.scale ?? 6;
        const currency = payload.currency || q?.currency || 'vUSDC';
        const totalMinor = payload.total_minor || q?.total_minor || '0';

        // Shop info
        body += `<div class="shop-info">
            <div class="shop-domain">Domain: ${this.escapeHtml(this.getDomain())}</div>
            <div class="shop-id">Shop: ${this.escapeHtml(this.getShopId())}</div>
            <div class="shop-network">Network: ${this.escapeHtml(this.getNetwork())}</div>
        </div>`;

        // Wallet
        body += `<div class="wallet-section">
            <div class="wallet-label">Wallet Address</div>
            <div class="wallet-address">${this.escapeHtml(this.getWallet())}</div>
        </div>`;

        // Order intent ID
        body += `<div class="order-intent-row">
            <span>Order Intent</span>
            <span class="intent-id">${this.escapeHtml(intent.order_intent_id)}</span>
        </div>`;

        // Line items table
        if (q && q.lines && q.lines.length > 0) {
            body += `<table>
                <thead>
                    <tr>
                        <th>SKU</th>
                        <th class="col-qty">Qty</th>
                        <th class="col-unit">Unit Price</th>
                        <th class="col-total">Line Total</th>
                    </tr>
                </thead>
                <tbody>`;
            for (const line of q.lines) {
                body += `<tr>
                    <td class="col-sku">${this.escapeHtml(line.sku_id)}</td>
                    <td class="col-qty">${line.quantity}</td>
                    <td class="col-unit">${this.formatAmount(line.unit_price_minor, scale)} ${this.escapeHtml(currency)}</td>
                    <td class="col-total">${this.formatAmount(line.line_total_minor, scale)} ${this.escapeHtml(currency)}</td>
                </tr>`;
            }
            body += `</tbody></table>`;
        }

        // Footer with total and confirm button
        body += `<div class="checkout-footer">
            <div>
                <div class="total-label">Total (Backend Authoritative)</div>
                <div class="grand-total">${this.formatAmount(totalMinor, scale)} ${this.escapeHtml(currency)}</div>
            </div>
            <button class="confirm-checkout-btn"${this.confirmed ? ' disabled' : ''}>
                ${this.confirmed ? 'Confirmed' : 'Confirm Checkout'}
            </button>
        </div>`;

        return `<div class="checkout-card">${body}</div>`;
    }

    // --------------------------------------------------------------------
    // Utilities
    // --------------------------------------------------------------------

    private escapeHtml(str: string): string {
        const map: Record<string, string> = {
            '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#x27;',
        };
        return str.replace(/[&<>"']/g, (ch) => map[ch] || ch);
    }
}

customElements.define('ancf-checkout', ANCFCheckout);
