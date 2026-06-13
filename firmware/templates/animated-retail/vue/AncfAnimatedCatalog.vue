<template>
  <main class="ancf-catalog" :style="cssVars">
    <header class="topbar">
      <button class="icon-btn" type="button" aria-label="Back" @click="$emit('back')">
        <span aria-hidden="true">&lt;</span>
      </button>
      <div>
        <p class="eyebrow">{{ shopLabel }}</p>
        <h1>{{ title }}</h1>
      </div>
      <div class="status">
        <span class="dot"></span>
        <span>{{ manifestStatus }}</span>
      </div>
    </header>

    <section class="intro">
      <p class="kicker">{{ categoryLabel }}</p>
      <h2>{{ heading }}</h2>
      <p>{{ description }}</p>
      <div class="search-row">
        <label class="search" aria-label="Search products">
          <span aria-hidden="true">Search</span>
          <input
            v-model="query"
            :placeholder="searchPlaceholder"
            type="search"
            autocomplete="off"
          />
        </label>
        <div class="chips" aria-label="Catalog filters">
          <button
            v-for="filter in filters"
            :key="filter"
            class="chip"
            :class="{ active: activeFilter === filter }"
            type="button"
            @click="activeFilter = filter"
          >
            {{ filter }}
          </button>
        </div>
      </div>
    </section>

    <section class="product-list" aria-label="Products">
      <article
        v-for="(item, index) in visibleProducts"
        :key="item.sku_id"
        class="product-row"
        :style="{ '--delay': `${index * 42}ms` }"
      >
        <button class="media" type="button" @click="select(item)">
          <img v-if="item.media?.thumbnail" :src="item.media.thumbnail" :alt="item.title" loading="lazy" />
          <span v-else aria-hidden="true">{{ mediaFallback(item) }}</span>
        </button>
        <div class="summary">
          <div class="summary-head">
            <p class="sku">{{ item.sku_id }}</p>
            <span class="stock" :class="{ low: stockLevel(item) === 'low' }">
              {{ formatStock(item.stock_hint) }}
            </span>
          </div>
          <h3>{{ item.title }}</h3>
          <dl class="specs">
            <div v-for="(value, key) in topSpecs(item)" :key="key">
              <dt>{{ key }}</dt>
              <dd>{{ value }}</dd>
            </div>
          </dl>
        </div>
        <div class="commerce">
          <p class="price">{{ formatPrice(item.price) }}</p>
          <button class="quote-btn" type="button" @click="quote(item)">Quote</button>
        </div>
      </article>
    </section>

    <aside class="protocol-panel" aria-label="Agent protocol panel">
      <div>
        <p class="panel-label">Agent Intake</p>
        <strong>{{ visibleProducts.length }}</strong>
        <span>items rendered</span>
      </div>
      <div>
        <p class="panel-label">Bridge</p>
        <code>ancf:search</code>
      </div>
      <div>
        <p class="panel-label">Payload</p>
        <code>{{ payloadSize }}</code>
      </div>
    </aside>

    <footer class="bottom-nav" aria-label="Catalog navigation">
      <button type="button" class="nav-item active">Catalog</button>
      <button type="button" class="nav-item">Quote</button>
      <button type="button" class="nav-item">Checkout</button>
    </footer>
  </main>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue';

type Price = {
  currency: string;
  amount_minor: string;
  scale: number;
};

export type AncfCatalogProduct = {
  sku_id: string;
  title: string;
  price: Price;
  stock_hint?: number;
  specs?: Record<string, string>;
  media?: { thumbnail?: string; gallery?: string[] };
  category?: string;
};

const props = withDefaults(defineProps<{
  products: AncfCatalogProduct[];
  title?: string;
  heading?: string;
  description?: string;
  shopLabel?: string;
  categoryLabel?: string;
  manifestStatus?: string;
  searchPlaceholder?: string;
  filters?: string[];
  theme?: Record<string, string>;
  maxItems?: number;
}>(), {
  title: 'ANCF Shop',
  heading: 'Compute Infrastructure',
  description: 'Backend-authoritative product data rendered locally by an Agent.',
  shopLabel: 'zero_shop_sol_01',
  categoryLabel: 'Display-only catalog',
  manifestStatus: 'Manifest valid',
  searchPlaceholder: 'Search SKU, GPU, capability',
  filters: () => ['All', 'GPU', 'API', 'Storage'],
  theme: () => ({}),
  maxItems: 24,
});

const emit = defineEmits<{
  select: [item: AncfCatalogProduct];
  quote: [item: AncfCatalogProduct];
  back: [];
}>();

const query = ref('');
const activeFilter = ref('All');

const cssVars = computed(() => ({
  '--ancf-primary': props.theme.primary || '#00FFA3',
  '--ancf-background': props.theme.background || '#F8FAFC',
  '--ancf-text': props.theme.text || '#111827',
  '--ancf-surface': props.theme.surface || '#FFFFFF',
  '--ancf-border': props.theme.border || '#E5E7EB',
  '--ancf-muted': props.theme.muted || '#6B7280',
}));

const visibleProducts = computed(() => {
  const q = query.value.trim().toLowerCase();
  const category = activeFilter.value;
  return props.products
    .filter((item) => {
      const matchesCategory = category === 'All' || item.category === category;
      const text = `${item.sku_id} ${item.title} ${Object.values(item.specs || {}).join(' ')}`.toLowerCase();
      return matchesCategory && (!q || text.includes(q));
    })
    .slice(0, props.maxItems);
});

const payloadSize = computed(() => {
  const bytes = new TextEncoder().encode(JSON.stringify(visibleProducts.value)).length;
  return bytes < 1024 ? `${bytes}B` : `${(bytes / 1024).toFixed(1)}KB`;
});

function formatPrice(price: Price): string {
  const value = Number(BigInt(price.amount_minor)) / 10 ** price.scale;
  return `${value.toFixed(Math.min(price.scale, 6))} ${price.currency}`;
}

function formatStock(stock?: number): string {
  if (stock === undefined) return 'Stock hidden';
  if (stock === 0) return 'Out';
  if (stock < 10) return `${stock} left`;
  return `${stock} available`;
}

function stockLevel(item: AncfCatalogProduct): 'low' | 'normal' {
  return item.stock_hint !== undefined && item.stock_hint < 10 ? 'low' : 'normal';
}

function topSpecs(item: AncfCatalogProduct): Record<string, string> {
  return Object.fromEntries(Object.entries(item.specs || {}).slice(0, 3));
}

function mediaFallback(item: AncfCatalogProduct): string {
  return item.title.slice(0, 2).toUpperCase();
}

function select(item: AncfCatalogProduct): void {
  emit('select', item);
}

function quote(item: AncfCatalogProduct): void {
  emit('quote', item);
}
</script>

<style scoped>
.ancf-catalog {
  min-height: 100vh;
  background: var(--ancf-background);
  color: var(--ancf-text);
  font-family: Inter, ui-sans-serif, system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif;
  padding: 12px 12px 76px;
}

.topbar,
.intro,
.product-row,
.protocol-panel,
.bottom-nav {
  background: var(--ancf-surface);
  border: 1px solid var(--ancf-border);
  border-radius: 8px;
}

.topbar {
  display: flex;
  align-items: center;
  gap: 12px;
  min-height: 56px;
  padding: 8px 12px;
}

.icon-btn,
.quote-btn,
.chip,
.nav-item {
  border: 1px solid var(--ancf-border);
  background: transparent;
  color: inherit;
  border-radius: 8px;
  min-height: 36px;
  cursor: pointer;
}

.icon-btn {
  width: 36px;
}

.eyebrow,
.kicker,
.panel-label,
.sku,
.stock {
  margin: 0;
  color: var(--ancf-muted);
  font-size: 11px;
  line-height: 1.2;
}

h1,
h2,
h3,
p {
  margin: 0;
}

h1 {
  font-size: 13px;
  font-weight: 700;
}

.status {
  margin-left: auto;
  display: flex;
  align-items: center;
  gap: 6px;
  color: var(--ancf-muted);
  font-size: 11px;
}

.dot {
  width: 6px;
  height: 6px;
  border-radius: 999px;
  background: var(--ancf-primary);
}

.intro {
  margin-top: 12px;
  padding: 16px;
}

.intro h2 {
  margin-top: 4px;
  font-size: clamp(32px, 10vw, 56px);
  line-height: 0.94;
  letter-spacing: 0;
}

.intro p:not(.kicker) {
  margin-top: 10px;
  color: var(--ancf-muted);
  max-width: 560px;
}

.search-row {
  margin-top: 18px;
  display: grid;
  gap: 10px;
}

.search {
  display: flex;
  align-items: center;
  gap: 8px;
  min-height: 42px;
  border: 1px solid var(--ancf-border);
  border-radius: 8px;
  padding: 0 12px;
}

.search input {
  width: 100%;
  border: 0;
  outline: 0;
  background: transparent;
  color: inherit;
}

.chips {
  display: flex;
  gap: 8px;
  overflow-x: auto;
}

.chip {
  padding: 0 12px;
  white-space: nowrap;
}

.chip.active,
.quote-btn {
  border-color: var(--ancf-primary);
  color: #003920;
  background: var(--ancf-primary);
}

.product-list {
  margin-top: 12px;
  display: grid;
  gap: 10px;
}

.product-row {
  display: grid;
  grid-template-columns: 96px minmax(0, 1fr);
  gap: 12px;
  padding: 10px;
  animation: rowIn 420ms ease both;
  animation-delay: var(--delay);
}

.media {
  border: 1px solid var(--ancf-border);
  border-radius: 8px;
  background: #111827;
  color: var(--ancf-primary);
  aspect-ratio: 1;
  overflow: hidden;
}

.media img {
  width: 100%;
  height: 100%;
  object-fit: cover;
  display: block;
  filter: grayscale(0.15);
}

.summary {
  min-width: 0;
}

.summary-head {
  display: flex;
  align-items: center;
  gap: 8px;
}

.sku {
  font-family: "SFMono-Regular", Consolas, monospace;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.stock {
  margin-left: auto;
  color: #007146;
}

.stock.low {
  color: #B45309;
}

h3 {
  margin-top: 4px;
  font-size: 15px;
  line-height: 1.25;
}

.specs {
  display: flex;
  gap: 6px;
  flex-wrap: wrap;
  margin: 8px 0 0;
}

.specs div {
  display: inline-flex;
  gap: 4px;
  border: 1px solid var(--ancf-border);
  border-radius: 999px;
  padding: 3px 7px;
  font-size: 11px;
}

.specs dt {
  color: var(--ancf-muted);
}

.specs dd {
  margin: 0;
}

.commerce {
  grid-column: 1 / -1;
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
}

.price {
  font-weight: 800;
}

.quote-btn {
  min-width: 92px;
}

.protocol-panel {
  margin-top: 12px;
  display: grid;
  grid-template-columns: repeat(3, 1fr);
  gap: 1px;
  overflow: hidden;
}

.protocol-panel > div {
  padding: 10px;
}

.protocol-panel strong,
.protocol-panel code {
  display: block;
  margin-top: 4px;
  font-size: 13px;
}

.bottom-nav {
  position: fixed;
  left: 12px;
  right: 12px;
  bottom: 12px;
  display: grid;
  grid-template-columns: repeat(3, 1fr);
  gap: 8px;
  padding: 8px;
}

.nav-item {
  font-size: 12px;
}

.nav-item.active {
  color: #003920;
  border-color: var(--ancf-primary);
  background: var(--ancf-primary);
}

@keyframes rowIn {
  from {
    opacity: 0;
    transform: translateY(8px);
  }
  to {
    opacity: 1;
    transform: translateY(0);
  }
}

@media (min-width: 900px) {
  .ancf-catalog {
    max-width: 1180px;
    margin: 0 auto;
    padding-bottom: 92px;
  }

  .search-row {
    grid-template-columns: minmax(280px, 1fr) auto;
    align-items: center;
  }

  .product-row {
    grid-template-columns: 116px minmax(0, 1fr) 160px;
    align-items: center;
  }

  .commerce {
    grid-column: auto;
    display: grid;
    justify-items: end;
  }
}
</style>
