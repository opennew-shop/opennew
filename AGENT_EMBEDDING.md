# ANCF Agent Embedding Specification

Version: 2.1  
Default renderer: Stitch fixed one-shot templates  
Updated: 2026-06-07

## Current Default

The Node Agent now renders the Stitch-converted lightweight templates in:

```text
firmware/templates/animated-retail/
  html/
    ancf-animated-catalog.template.html
    ancf-animated-product-detail.template.html
  template-manifest.json
  AGENT_EMBEDDING.md
```

The runtime path is:

```text
GET /                    -> generateStitchCatalogHTML()
GET /?lang=zh-CN         -> generateStitchCatalogHTML() with Chinese labels
GET /detail?sku={sku_id} -> generateStitchProductDetailHTML()
POST /bridge             -> whitelisted backend command proxy
```

The default page must not load Vue runtime, Tailwind browser runtime, remote component JavaScript, or the old `AncfAnimated*.vue.js` bundles.

## Data Injection Contract

The Agent injects untrusted display data into the fixed payload slot:

```html
<script id="ancf-payload" type="application/json">
  ...
</script>
```

The catalog template receives:

```json
{
  "locale": "en-US",
  "labels": {
    "searchLabel": "Search",
    "quote": "Quote",
    "requestQuote": "Request Quote"
  },
  "shopLabel": "zero_shop_sol_01",
  "title": "ANCF Shop",
  "heading": "Compute Infrastructure",
  "description": "Backend-authoritative product data rendered locally by an Agent.",
  "categoryLabel": "Display-only catalog",
  "manifestStatus": "Manifest valid",
  "searchPlaceholder": "Search SKU, GPU, capability",
  "filters": ["All", "GPU", "API", "Storage"],
  "maxItems": 24,
  "products": []
}
```

The detail template receives:

```json
{
  "locale": "en-US",
  "labels": {
    "backendQuoteRequired": "backend quote required",
    "securityNotice": "Security notice"
  },
  "shopLabel": "zero_shop_sol_01",
  "manifestStatus": "manifest active",
  "protocolVersion": "ANCF-1.0",
  "product": {
    "sku_id": "sku_gpu_h100_v1",
    "title": "H100 Compute Rental, Hourly",
    "price": { "currency": "AGP", "amount_minor": "2450000", "scale": 6 },
    "stock_hint": 42,
    "specs": { "GPU": "80GB SXM5", "CUDA": "12.4" },
    "media": { "thumbnail": "", "gallery": [] }
  }
}
```

JSON is escaped before injection so `</script>`, HTML tags, and control separators cannot break out of the payload script.

## Native Localization

Localization is native to the Agent payload and does not require an i18n runtime.

Locale resolution order:

```text
1. Query string: ?lang=zh-CN or ?locale=zh-CN
2. Accept-Language request header
3. Default: en-US
```

Supported locales:

```text
en-US
zh-CN
```

The Agent injects:

```json
{
  "locale": "zh-CN",
  "labels": {
    "searchLabel": "搜索",
    "quote": "报价",
    "securityNotice": "安全提示",
    "filters": {
      "All": "全部",
      "GPU": "GPU",
      "API": "API",
      "Storage": "存储"
    }
  }
}
```

Templates must use `payload.labels` for UI text and keep product data as display data. Filter IDs remain stable protocol keys (`All`, `GPU`, `API`, `Storage`); only their rendered labels are localized.

## Template Events

Templates only emit intent events:

```text
ANCF_TEMPLATE_SELECT
ANCF_TEMPLATE_QUOTE
ANCF_TEMPLATE_BACK
```

The Agent orchestration script maps them as follows:

```text
ANCF_TEMPLATE_SELECT -> local navigation to /detail?sku={sku_id}
ANCF_TEMPLATE_QUOTE  -> /bridge command ancf:quote
ANCF_TEMPLATE_BACK   -> local navigation to /
```

Checkout still requires backend quote, checkout prepare, wallet signature prompt, and checkout commit through `/bridge`.

## Security Rules

- The local template is not authoritative for price, stock, payment, fulfillment, or minting.
- Search result prices are display-only. Checkout must use `ancf:quote`.
- Browser code cannot call arbitrary backend endpoints; only `/bridge` whitelisted commands are allowed.
- CSP allows local inline template scripts but removes `unsafe-eval`; Vue template compiler is not part of the default path.
- Product titles, descriptions, specs, and media URLs are treated as untrusted display data.
- Media should be proxied or host-whitelisted in production.

## Legacy Vue Implementation

The DeepSeek/Claude Vue 3 animated components remain in:

```text
firmware/components/src/AncfAnimatedCatalog.vue
firmware/components/src/AncfAnimatedProductDetail.vue
firmware/components/dist/AncfAnimatedCatalog.vue.js
firmware/components/dist/AncfAnimatedProductDetail.vue.js
```

They are no longer the default Agent renderer. Keep them as an experimental `legacy-vue-spa` renderer only, because they require Vue runtime and integrate quote UI inside the product detail component, which does not match the current one-shot Stitch template contract.
