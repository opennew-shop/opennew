/**
 * <ancf-quote> - ANCF Quote Display Component
 *
 * Displays a quote response received from the backend, showing line items,
 * totals, expiry, and a confirm button. All data is display-only — the
 * backend is authoritative for pricing.
 *
 * Attributes:
 *   quote-data - JSON string of the quote response body.
 *
 * Events (dispatched on the element):
 *   ancf:confirm       - User confirmed the quote. detail: { quote_id }
 *   ancf:quote-expired - The quote has expired based on local clock check.
 *
 * Security:
 *   - All titles, SKU IDs, and amounts are HTML-escaped before rendering.
 *   - Price amounts are treated as display strings, not evaluated as numbers
 *     for business logic.
 *   - No scripts from quote data are executed.
 */

interface QuoteLine {
    sku_id: string;
    quantity: number;
    unit_price_minor: string;
    line_total_minor: string;
}

interface QuoteResponse {
    quote_id: string;
    currency: string;
    total_minor: string;
    scale: number;
    expires_at: string;
    lines: QuoteLine[];
}

class ANCFQuote extends HTMLElement {
    private shadow: ShadowRoot;
    private quote: QuoteResponse | null = null;
    private expired: boolean = false;
    private expiryTimer: ReturnType<typeof setInterval> | null = null;
    private timeRemaining: string = '';

    static observedAttributes = ['quote-data'];

    constructor() {
        super();
        this.shadow = this.attachShadow({ mode: 'open' });
    }

    connectedCallback(): void {
        this.parseQuote();
        this.render();
        this.startExpiryTimer();
    }

    disconnectedCallback(): void {
        if (this.expiryTimer) clearInterval(this.expiryTimer);
    }

    attributeChangedCallback(name: string): void {
        if (name === 'quote-data') {
            this.parseQuote();
            this.render();
            this.startExpiryTimer();
        }
    }

    /** Parse the `quote-data` attribute into a typed object. */
    private parseQuote(): void {
        const raw = this.getAttribute('quote-data');
        if (!raw) {
            this.quote = null;
            this.expired = false;
            return;
        }
        try {
            const parsed: unknown = JSON.parse(raw);
            if (parsed && typeof parsed === 'object' && !Array.isArray(parsed)) {
                this.quote = parsed as QuoteResponse;
                this.expired = this.checkExpired(this.quote);
            } else {
                this.quote = null;
            }
        } catch {
            this.quote = null;
        }
    }

    /** Check if the quote has expired per the local clock. */
    private checkExpired(q: QuoteResponse): boolean {
        if (!q.expires_at) return false;
        try {
            return new Date(q.expires_at) <= new Date();
        } catch {
            return false;
        }
    }

    /** Start a 1-second timer to update the time-remaining display. */
    private startExpiryTimer(): void {
        if (this.expiryTimer) clearInterval(this.expiryTimer);
        if (!this.quote || this.expired) return;

        this.expiryTimer = setInterval(() => {
            if (!this.quote) return;
            const now = Date.now();
            const exp = new Date(this.quote.expires_at).getTime();
            const diff = exp - now;
            if (diff <= 0) {
                this.expired = true;
                if (this.expiryTimer) clearInterval(this.expiryTimer);
                this.render();
                this.dispatchEvent(new CustomEvent('ancf:quote-expired', {
                    detail: { quote_id: this.quote.quote_id },
                    bubbles: true,
                    composed: true,
                }));
                return;
            }
            const mins = Math.floor(diff / 60000);
            const secs = Math.floor((diff % 60000) / 1000);
            this.timeRemaining = `${mins}m ${secs}s`;
            this.updateTimerDisplay();
        }, 1000);
    }

    /** Lightweight update of just the timer text to avoid full re-render. */
    private updateTimerDisplay(): void {
        const el = this.shadow.querySelector('.quote-timer');
        if (el) el.textContent = `Expires in: ${this.timeRemaining}`;
    }

    /** Format an amount from minor units + scale. */
    private formatAmount(amountMinor: string, scale: number): string {
        const major = parseInt(amountMinor, 10) / Math.pow(10, scale);
        return major.toFixed(Math.min(scale, 6));
    }

    /** Handle user clicking the Confirm button. */
    private onConfirm(): void {
        if (!this.quote || this.expired) return;
        this.dispatchEvent(new CustomEvent('ancf:confirm', {
            detail: { quote_id: this.quote.quote_id },
            bubbles: true,
            composed: true,
        }));
    }

    // --------------------------------------------------------------------
    // Render
    // --------------------------------------------------------------------

    private render(): void {
        const style = this.renderStyles();
        const html = this.renderHTML();
        this.shadow.innerHTML = `${style}${html}`;

        // Bind confirm button after render
        const btn = this.shadow.querySelector('.confirm-btn');
        btn?.addEventListener('click', () => this.onConfirm());
    }

    private renderStyles(): string {
        return `<style>
:host {
    display: block;
    font-family: var(--ancf-font, system-ui, sans-serif);
    color: var(--ancf-text, #FFFFFF);
}
.quote-card {
    border: 1px solid var(--ancf-border, #2A2A4A);
    border-radius: var(--ancf-radius, 8px);
    background: var(--ancf-surface, #1A1A2E);
    overflow: hidden;
}
.quote-header {
    padding: 16px;
    border-bottom: 1px solid #1E1E32;
    display: flex;
    justify-content: space-between;
    align-items: center;
}
.quote-id {
    font-size: 12px;
    color: #888;
    font-family: monospace;
}
.quote-timer {
    font-size: 12px;
    color: var(--ancf-warning, #FFAA00);
    font-family: monospace;
}
.quote-expired-badge {
    font-size: 12px;
    color: var(--ancf-danger, #FF4444);
    font-weight: 600;
    padding: 2px 8px;
    border: 1px solid var(--ancf-danger, #FF4444);
    border-radius: 4px;
}
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
tr:last-child td {
    border-bottom: none;
}
.col-sku { font-family: monospace; font-size: 13px; color: #aaa; }
.col-qty { text-align: center; }
.col-unit { text-align: right; white-space: nowrap; }
.col-total { text-align: right; white-space: nowrap; font-weight: 500; }
.quote-footer {
    padding: 16px;
    border-top: 2px solid var(--ancf-border, #2A2A4A);
    display: flex;
    justify-content: space-between;
    align-items: center;
    background: rgba(0,0,0,0.2);
}
.grand-total {
    font-size: 18px;
    font-weight: 700;
    color: var(--ancf-primary, #00FFA3);
}
.grand-total-label {
    font-size: 11px;
    color: #888;
    text-transform: uppercase;
    letter-spacing: 0.5px;
}
.confirm-btn {
    padding: 10px 24px;
    background: var(--ancf-primary, #00FFA3);
    color: #0D0E12;
    border: none;
    border-radius: var(--ancf-radius, 8px);
    font-size: 14px;
    font-weight: 600;
    cursor: pointer;
    transition: background 0.15s;
}
.confirm-btn:hover {
    background: #00CC82;
}
.confirm-btn:disabled {
    background: #333;
    color: #666;
    cursor: not-allowed;
}
.empty-state {
    padding: 24px;
    text-align: center;
    color: #666;
    font-size: 14px;
}
</style>`;
    }

    private renderHTML(): string {
        if (!this.quote) {
            return `<div class="empty-state">No quote data available. Request a quote first.</div>`;
        }

        const q = this.quote;

        let body = `<div class="quote-card">
    <div class="quote-header">
        <span class="quote-id">Quote: ${this.escapeHtml(q.quote_id)}</span>`;

        if (this.expired) {
            body += `<span class="quote-expired-badge">EXPIRED</span>`;
        } else {
            body += `<span class="quote-timer">Expires in: --</span>`;
        }

        body += `</div>
    <table>
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
                <td class="col-unit">${this.formatAmount(line.unit_price_minor, q.scale)} ${this.escapeHtml(q.currency)}</td>
                <td class="col-total">${this.formatAmount(line.line_total_minor, q.scale)} ${this.escapeHtml(q.currency)}</td>
            </tr>`;
        }

        body += `</tbody>
    </table>
    <div class="quote-footer">
        <div>
            <div class="grand-total-label">Total</div>
            <div class="grand-total">${this.formatAmount(q.total_minor, q.scale)} ${this.escapeHtml(q.currency)}</div>
        </div>
        <button class="confirm-btn"${this.expired ? ' disabled' : ''}>
            ${this.expired ? 'Quote Expired' : 'Confirm Quote'}
        </button>
    </div>
</div>`;

        return body;
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

customElements.define('ancf-quote', ANCFQuote);
