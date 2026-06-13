/**
 * <ancf-search> - ANCF Product Search Component
 *
 * Renders a search input with autocomplete-style result list.
 * All network requests go through the Agent Bridge (if available) or
 * directly to the configured API base.
 *
 * Attributes:
 *   api-base    - Base URL for the ANCF API gateway (default: http://127.0.0.1:8080)
 *
 * Events (dispatched on the element):
 *   ancf:select - Fired when a user clicks a search result.
 *                 detail: { sku_id, title, price, specks, media, stock_hint }
 *   ancf:search-start - Fired when a search begins.
 *   ancf:search-end   - Fired when a search completes (with or without error).
 *                 detail: { query, itemCount, error? }
 *
 * Internal state:
 *   loading: boolean
 *   results:  SearchResultItem[]
 *   error:    string | null
 *   query:    string
 */

interface SearchResultItem {
    sku_id: string;
    title: string;
    price: {
        currency: string;
        amount_minor: string;
        scale: number;
    };
    stock_hint?: number;
    specs?: Record<string, string>;
    media?: {
        thumbnail?: string;
        gallery?: string[];
    };
}

interface AgentBridgeInterface {
    handleCommand(cmd: { command: string; params: Record<string, unknown>; requestId: string }): Promise<unknown>;
}

class ANCFSearch extends HTMLElement {
    private shadow: ShadowRoot;
    private apiBase: string = 'http://127.0.0.1:8080';
    private results: SearchResultItem[] = [];
    private loading: boolean = false;
    private error: string | null = null;
    private selectedIndex: number = -1;
    private inputEl!: HTMLInputElement;

    // Debounce timer for keystroke search
    private debounceTimer: ReturnType<typeof setTimeout> | null = null;
    private static readonly DEBOUNCE_MS = 300;

    // Reference to Agent Bridge (injected globally by agent-bridge.js)
    private bridge: AgentBridgeInterface | null = null;

    constructor() {
        super();
        this.shadow = this.attachShadow({ mode: 'open' });
    }

    connectedCallback(): void {
        this.apiBase = this.getAttribute('api-base') || this.apiBase;
        // Try to obtain the Agent Bridge from the global scope
        if (typeof (window as unknown as Record<string, unknown>).__ancfBridge !== 'undefined') {
            this.bridge = (window as unknown as Record<string, AgentBridgeInterface>).__ancfBridge;
        }
        this.render();
        this.inputEl = this.shadow.querySelector('.search-input') as HTMLInputElement;
        this.inputEl?.addEventListener('input', this.onInput.bind(this));
        this.inputEl?.addEventListener('keydown', this.onKeydown.bind(this));
    }

    disconnectedCallback(): void {
        if (this.debounceTimer) clearTimeout(this.debounceTimer);
        this.inputEl?.removeEventListener('input', this.onInput.bind(this));
        this.inputEl?.removeEventListener('keydown', this.onKeydown.bind(this));
    }

    /** Format price from minor units + scale to a display string. */
    private formatPrice(price: SearchResultItem['price']): string {
        const major = parseInt(price.amount_minor, 10) / Math.pow(10, price.scale);
        return `${major.toFixed(price.scale)} ${price.currency}`;
    }

    /** Format stock hint for display. */
    private formatStock(stock: number | undefined): string {
        if (stock === undefined || stock === null) return '';
        if (stock === 0) return 'Out of stock';
        if (stock < 100) return `${stock} available`;
        return `${stock}+ available`;
    }

    /** Debounced input handler — triggers search after DEBOUNCE_MS of no typing. */
    private onInput(): void {
        if (this.debounceTimer) clearTimeout(this.debounceTimer);
        const query = this.inputEl.value.trim();
        if (!query) {
            this.results = [];
            this.error = null;
            this.loading = false;
            this.selectedIndex = -1;
            this.render();
            return;
        }
        this.debounceTimer = setTimeout(() => this.doSearch(query), ANCFSearch.DEBOUNCE_MS);
    }

    /** Keyboard navigation within results. */
    private onKeydown(e: KeyboardEvent): void {
        if (e.key === 'ArrowDown') {
            e.preventDefault();
            this.selectedIndex = Math.min(this.selectedIndex + 1, this.results.length - 1);
            this.render();
        } else if (e.key === 'ArrowUp') {
            e.preventDefault();
            this.selectedIndex = Math.max(this.selectedIndex - 1, 0);
            this.render();
        } else if (e.key === 'Enter') {
            e.preventDefault();
            if (this.selectedIndex >= 0 && this.selectedIndex < this.results.length) {
                this.selectItem(this.results[this.selectedIndex]);
            }
        } else if (e.key === 'Escape') {
            this.results = [];
            this.selectedIndex = -1;
            this.inputEl.value = '';
            this.render();
        }
    }

    /** Execute a search via the Agent Bridge or direct fetch. */
    private async doSearch(query: string): Promise<void> {
        this.loading = true;
        this.error = null;
        this.selectedIndex = -1;

        this.dispatchEvent(new CustomEvent('ancf:search-start', {
            detail: { query },
            bubbles: true,
            composed: true,
        }));
        this.render();

        try {
            if (this.bridge) {
                // Route through Agent Bridge (preferred path for CSP compliance)
                const result = await this.bridge.handleCommand({
                    command: 'ancf:search',
                    params: { query, limit: 20 },
                    requestId: crypto.randomUUID ? crypto.randomUUID() : this.generateId(),
                });
                const data = result as { items?: SearchResultItem[] };
                this.results = data.items || [];
            } else {
                // Direct fetch fallback
                const url = `${this.apiBase}/api/v1/cli/search?q=${encodeURIComponent(query)}&limit=20`;
                const resp = await fetch(url);
                if (!resp.ok) throw new Error(`Search failed: HTTP ${resp.status}`);
                const data = await resp.json();
                this.results = data.items || [];
            }
        } catch (e: unknown) {
            this.error = e instanceof Error ? e.message : 'Unknown search error';
            this.results = [];
        } finally {
            this.loading = false;
            this.render();
            this.dispatchEvent(new CustomEvent('ancf:search-end', {
                detail: { query, itemCount: this.results.length, error: this.error },
                bubbles: true,
                composed: true,
            }));
        }
    }

    /** User selected a result item. */
    private selectItem(item: SearchResultItem): void {
        this.dispatchEvent(new CustomEvent('ancf:select', {
            detail: { ...item },
            bubbles: true,
            composed: true,
        }));
    }

    /** Simple ID generator fallback. */
    private generateId(): string {
        return `${Date.now()}-${Math.random().toString(36).slice(2, 11)}`;
    }

    // --------------------------------------------------------------------
    // Render
    // --------------------------------------------------------------------

    private render(): void {
        const style = this.renderStyles();
        const html = this.renderHTML();
        this.shadow.innerHTML = `${style}${html}`;
    }

    private renderStyles(): string {
        return `<style>
:host {
    display: block;
    font-family: var(--ancf-font, system-ui, sans-serif);
    color: var(--ancf-text, #FFFFFF);
}
.wrapper {
    position: relative;
}
.search-input {
    width: 100%;
    padding: 12px 16px;
    border: 1px solid var(--ancf-border, #2A2A4A);
    border-radius: var(--ancf-radius, 8px);
    background: var(--ancf-surface, #1A1A2E);
    color: var(--ancf-text, #FFFFFF);
    font-size: 14px;
    box-sizing: border-box;
    outline: none;
    transition: border-color 0.2s;
}
.search-input:focus {
    border-color: var(--ancf-primary, #00FFA3);
    box-shadow: 0 0 0 1px var(--ancf-primary, #00FFA3);
}
.search-input::placeholder {
    color: #666;
}
.results-list {
    list-style: none;
    margin: 4px 0 0 0;
    padding: 0;
    border: 1px solid var(--ancf-border, #2A2A4A);
    border-radius: var(--ancf-radius, 8px);
    background: var(--ancf-surface, #1A1A2E);
    max-height: 360px;
    overflow-y: auto;
}
.result-item {
    padding: 12px 16px;
    border-bottom: 1px solid #1E1E32;
    cursor: pointer;
    display: flex;
    justify-content: space-between;
    align-items: center;
    transition: background 0.15s;
}
.result-item:last-child {
    border-bottom: none;
}
.result-item:hover,
.result-item.selected {
    background: #1E1E3A;
}
.result-item.selected .item-title {
    color: var(--ancf-primary, #00FFA3);
}
.item-info {
    flex: 1;
    min-width: 0;
}
.item-title {
    font-size: 14px;
    font-weight: 500;
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
    margin-bottom: 4px;
}
.item-meta {
    font-size: 12px;
    color: #888;
}
.item-thumb {
    width: 40px;
    height: 40px;
    border-radius: 4px;
    background: #1A1A2E;
    object-fit: cover;
    margin-left: 12px;
    flex-shrink: 0;
}
.item-price {
    color: var(--ancf-primary, #00FFA3);
    font-weight: 600;
    font-size: 14px;
    white-space: nowrap;
    margin-left: 12px;
}
.loading {
    text-align: center;
    padding: 24px;
    color: #666;
    font-size: 14px;
}
.spinner {
    display: inline-block;
    width: 20px;
    height: 20px;
    border: 2px solid #333;
    border-top-color: var(--ancf-primary, #00FFA3);
    border-radius: 50%;
    animation: ancf-spin 0.6s linear infinite;
    margin-right: 8px;
    vertical-align: middle;
}
@keyframes ancf-spin {
    to { transform: rotate(360deg); }
}
.empty {
    text-align: center;
    padding: 24px;
    color: #666;
    font-size: 14px;
}
.error-box {
    text-align: center;
    padding: 16px;
    color: var(--ancf-danger, #FF4444);
    font-size: 14px;
}
.retry-btn {
    margin-top: 8px;
    padding: 6px 16px;
    border: 1px solid var(--ancf-danger, #FF4444);
    border-radius: var(--ancf-radius, 8px);
    background: transparent;
    color: var(--ancf-danger, #FF4444);
    cursor: pointer;
    font-size: 12px;
}
.retry-btn:hover {
    background: rgba(255,68,68,0.1);
}
</style>`;
    }

    private renderHTML(): string {
        let body = '';

        // Search input
        body += `<div class="wrapper">
    <input type="search" class="search-input" placeholder="Search products..." autocomplete="off">`;

        // Loading state
        if (this.loading) {
            body += `<div class="loading"><span class="spinner"></span> Searching...</div>`;
        }
        // Error state
        else if (this.error) {
            body += `<div class="error-box">
                ${this.escapeHtml(this.error)}
                <br><button class="retry-btn">Retry</button>
            </div>`;
        }
        // Empty state (user typed but no results)
        else if (this.results.length === 0 && this.inputEl?.value?.trim()) {
            body += `<div class="empty">No products found. Try a different search term.</div>`;
        }
        // Results list
        else if (this.results.length > 0) {
            body += `<ul class="results-list">`;
            for (let i = 0; i < this.results.length; i++) {
                const item = this.results[i];
                const selectedClass = i === this.selectedIndex ? ' selected' : '';
                const thumbHtml = item.media?.thumbnail
                    ? `<img class="item-thumb" src="${this.escapeAttr(item.media.thumbnail)}" alt="" loading="lazy">`
                    : '';
                body += `<li class="result-item${selectedClass}" data-index="${i}">
                    <div class="item-info">
                        <div class="item-title">${this.escapeHtml(item.title)}</div>
                        <div class="item-meta">
                            ${item.sku_id} ${this.formatStock(item.stock_hint)}
                        </div>
                    </div>
                    ${thumbHtml}
                    <div class="item-price">${this.formatPrice(item.price)}</div>
                </li>`;
            }
            body += `</ul>`;
        }

        body += `</div>`; // .wrapper
        return body;
    }

    // --------------------------------------------------------------------
    // HTML escaping utilities (security: prevent XSS from search results)
    // --------------------------------------------------------------------

    private escapeHtml(str: string): string {
        const map: Record<string, string> = {
            '&': '&amp;',
            '<': '&lt;',
            '>': '&gt;',
            '"': '&quot;',
            "'": '&#x27;',
        };
        return str.replace(/[&<>"']/g, (ch) => map[ch] || ch);
    }

    private escapeAttr(str: string): string {
        return str.replace(/"/g, '&quot;').replace(/</g, '&lt;').replace(/>/g, '&gt;');
    }
}

customElements.define('ancf-search', ANCFSearch);
