# Animated Retail Templates

Fixed ANCF frontend templates derived from Stitch screens in project `9907418127151602795`.

## Files

```text
vue/AncfAnimatedCatalog.vue
vue/AncfAnimatedProductDetail.vue
html/ancf-animated-catalog.template.html
html/ancf-animated-product-detail.template.html
template-manifest.json
AGENT_EMBEDDING.md
```

## Intended Use

- Vue components: use inside a Vue app or a generated Vue shell.
- HTML templates: use for one-shot local Agent rendering without a build step.
- Raw Stitch files: keep as visual references under `.stitch/designs/stitch-animated-retail/`.

## Event Contract

```text
ANCF_TEMPLATE_SELECT
ANCF_TEMPLATE_QUOTE
ANCF_TEMPLATE_BACK
```

These events are UI intent only. Checkout still requires backend quote and checkout APIs.
