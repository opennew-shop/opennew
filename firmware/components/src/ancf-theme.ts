/**
 * <ancf-theme> - ANCF Theme Injection Component
 *
 * Injects CSS custom properties (design tokens) into the document root.
 * Renders no visible content; operates purely via adoptedStyleSheets
 * or a <style> element appended to <head>.
 *
 * Usage:
 *   <ancf-theme tokens='{"primary":"#00FFA3","background":"#0D0E12","text":"#FFFFFF"}'></ancf-theme>
 *
 * Tokens key          | CSS Variable              | Default
 * ------------------- | ------------------------- | ---------
 * primary             | --ancf-primary            | #00FFA3
 * background          | --ancf-background         | #0D0E12
 * text                | --ancf-text               | #FFFFFF
 * surface             | --ancf-surface            | #1A1A2E
 * border              | --ancf-border             | #2A2A4A
 * radius              | --ancf-radius             | 8px
 * font                | --ancf-font               | 'Inter', system-ui, sans-serif
 * danger              | --ancf-danger             | #FF4444
 * warning             | --ancf-warning            | #FFAA00
 * success             | --ancf-success            | #00FFA3
 *
 * Security: only sets CSS custom properties. Does not evaluate JavaScript
 * or load external resources. Token values are escaped before injection.
 */
class ANCFTheme extends HTMLElement {
    static observedAttributes = ['tokens'];

    /** Default theme tokens applied when no tokens attribute is present. */
    private static readonly DEFAULTS: Record<string, string> = {
        primary: '#00FFA3',
        background: '#0D0E12',
        text: '#FFFFFF',
        surface: '#1A1A2E',
        border: '#2A2A4A',
        radius: '8px',
        font: "'Inter', system-ui, sans-serif",
        danger: '#FF4444',
        warning: '#FFAA00',
        success: '#00FFA3',
    };

    /** Reference to the injected style, so we can remove/replace it. */
    private injectedIndex: number = -1;

    connectedCallback(): void {
        this.applyTheme();
    }

    attributeChangedCallback(name: string, _oldValue: string | null, _newValue: string | null): void {
        if (name === 'tokens') {
            this.applyTheme();
        }
    }

    disconnectedCallback(): void {
        this.removeTheme();
    }

    /**
     * Parse the `tokens` attribute (JSON string) and merge with defaults.
     * Returns a flat Record of token-name -> CSS value pairs.
     */
    private parseTokens(): Record<string, string> {
        const merged: Record<string, string> = { ...ANCFTheme.DEFAULTS };
        const raw = this.getAttribute('tokens');
        if (!raw) return merged;
        try {
            const overrides: unknown = JSON.parse(raw);
            if (overrides && typeof overrides === 'object' && !Array.isArray(overrides)) {
                for (const [key, value] of Object.entries(overrides as Record<string, unknown>)) {
                    if (typeof value === 'string') {
                        // Sanitize: strip any characters that could break CSS
                        const safe = value.replace(/[<>"'&;{}()]/g, '');
                        merged[key] = safe;
                    }
                }
            }
        } catch {
            // JSON parse failure -> silently use defaults
        }
        return merged;
    }

    /**
     * Build a `:root { ... }` CSS string from the merged token map.
     */
    private buildCSS(tokens: Record<string, string>): string {
        const vars: string[] = [];
        for (const [key, value] of Object.entries(tokens)) {
            vars.push(`  --ancf-${key}: ${value};`);
        }
        return `:root {\n${vars.join('\n')}\n}`;
    }

    /**
     * Inject theme tokens into the document. Prefers adoptedStyleSheets
     * for performance; falls back to a <style> element in <head>.
     */
    private applyTheme(): void {
        this.removeTheme();

        const tokens = this.parseTokens();
        const css = this.buildCSS(tokens);

        if (typeof document !== 'undefined' && document.adoptedStyleSheets) {
            try {
                const sheet = new CSSStyleSheet();
                sheet.replaceSync(css);
                const sheets = [...document.adoptedStyleSheets, sheet];
                document.adoptedStyleSheets = sheets;
                this.injectedIndex = sheets.length - 1;
                return;
            } catch {
                // adoptedStyleSheets not available or CSS is invalid — fall through
            }
        }

        // Fallback: inject <style> element
        const style = document.createElement('style');
        style.setAttribute('data-ancf-theme', '');
        style.textContent = css;
        document.head.appendChild(style);
        this.injectedIndex = -2; // marker for fallback mode
    }

    /**
     * Remove previously injected theme from the document.
     */
    private removeTheme(): void {
        if (this.injectedIndex >= 0 && document.adoptedStyleSheets) {
            try {
                const sheets = [...document.adoptedStyleSheets];
                if (this.injectedIndex < sheets.length) {
                    sheets.splice(this.injectedIndex, 1);
                    document.adoptedStyleSheets = sheets;
                }
            } catch {
                // Ignore removal errors
            }
        }
        if (this.injectedIndex === -2) {
            const el = document.head.querySelector('style[data-ancf-theme]');
            if (el) el.remove();
        }
        this.injectedIndex = -1;
    }
}

customElements.define('ancf-theme', ANCFTheme);
