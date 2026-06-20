<template>
  <div class="ancf-catalog" :class="{ 'is-loading': loading }">
    <!-- ========== 搜索栏 ========== -->
    <header class="catalog-header">
      <div class="header-top">
        <div class="shop-badge">
          <span class="shop-dot"></span>
          {{ shopDomain }}
        </div>
        <div class="header-actions">
          <button class="btn-wallet" @click="$emit('changeWallet')" title="Switch wallet">
            <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><rect x="2" y="4" width="20" height="16" rx="3"/><path d="M16 12h4"/><circle cx="16" cy="12" r="2"/></svg>
            <span class="wallet-addr">{{ walletDisplay }}</span>
          </button>
          <button class="btn-network" @click="$emit('changeNetwork')" title="Switch network">
            <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><circle cx="12" cy="12" r="10"/><path d="M2 12h20M12 2a15.3 15.3 0 014 10 15.3 15.3 0 01-4 10 15.3 15.3 0 01-4-10A15.3 15.3 0 0112 2z"/></svg>
            {{ network }}
          </button>
        </div>
      </div>

      <!-- 搜索框 -->
      <div class="search-wrap">
        <svg class="search-icon" width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5"><circle cx="11" cy="11" r="8"/><path d="M21 21l-4.3-4.3"/></svg>
        <input
          ref="searchInput"
          v-model="query"
          type="text"
          class="search-input"
          placeholder="Search GPUs, compute, storage..."
          autocomplete="off"
          @keydown.escape="clearSearch"
          @keydown.arrow-down="moveSelection(1)"
          @keydown.arrow-up="moveSelection(-1)"
          @keydown.enter="selectCurrent"
        />
        <button v-if="query" class="search-clear" @click="clearSearch">
          <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="3"><path d="M18 6L6 18M6 6l12 12"/></svg>
        </button>
      </div>

      <!-- 骨架屏/加载线 -->
      <div v-if="loading" class="search-loading-bar"></div>
    </header>

    <!-- ========== 分类胶囊 ========== -->
    <nav v-if="!loading && !error && !showResults" class="category-pills">
      <button
        v-for="cat in categories"
        :key="cat.key"
        class="pill"
        :class="{ active: activeCategory === cat.key }"
        @click="quickSearch(cat.label)"
      >
        <span class="pill-icon">{{ cat.icon }}</span>
        <span>{{ cat.label }}</span>
        <span class="pill-count">{{ cat.count }}</span>
      </button>
    </nav>

    <!-- ========== 错误状态 ========== -->
    <div v-if="error" class="state-error">
      <div class="error-card">
        <svg width="40" height="40" viewBox="0 0 24 24" fill="none" stroke="#FF4444" stroke-width="1.5"><circle cx="12" cy="12" r="10"/><path d="M12 8v4M12 16h.01"/></svg>
        <h3>Search Failed</h3>
        <p>{{ error }}</p>
        <button class="btn-retry" @click="doSearch(query)">
          <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5"><path d="M1 4v6h6M23 20v-6h-6"/><path d="M20.49 9A9 9 0 005.64 5.64L1 10m22 4l-4.64 4.36A9 9 0 013.51 15"/></svg>
          Retry
        </button>
      </div>
    </div>

    <!-- ========== 结果列表 ========== -->
    <div
      v-if="showResults && !loading && !error"
      class="results-container"
    >
      <div class="results-header">
        <span class="results-count">{{ items.length }} result{{ items.length !== 1 ? 's' : '' }}</span>
        <span class="results-query" v-if="query">for "{{ query }}"</span>
        <button v-if="query" class="btn-clear-results" @click="clearSearch">Clear</button>
      </div>

      <TransitionGroup name="card" tag="ul" class="results-grid">
        <li
          v-for="(item, idx) in items"
          :key="item.sku_id"
          class="product-card"
          :class="{ selected: idx === selectedIndex }"
          :style="{ '--card-delay': idx * 0.05 + 's' }"
          @click="selectItem(item)"
          @mouseenter="selectedIndex = idx"
        >
          <!-- GPU 缩略图 -->
          <div class="card-media">
            <img
              v-if="mediaThumbnail(item) && !failedImages[item.sku_id]"
              class="media-image"
              :src="mediaThumbnail(item)"
              :alt="item.title"
              loading="lazy"
              @error="markImageFailed(item.sku_id)"
            />
            <div v-else class="media-placeholder" :style="{ background: chipGradient(idx) }">
              <span class="media-chip">{{ item.specs?.GPU?.split(' ')[0] || item.sku_id.slice(-4).toUpperCase() }}</span>
              <div class="media-shimmer"></div>
            </div>
            <div class="stock-badge" :class="stockClass(item.stock_hint)">
              <span class="stock-dot"></span>
              {{ stockLabel(item.stock_hint) }}
            </div>
          </div>

          <!-- 信息区 -->
          <div class="card-body">
            <div class="card-sku">{{ item.sku_id }}</div>
            <h3 class="card-title">{{ item.title }}</h3>

            <!-- 规格条 -->
            <div class="spec-chips" v-if="item.specs">
              <span v-for="(v, k) in item.specs" :key="k" class="spec-chip">{{ k }}: {{ v }}</span>
            </div>
          </div>

          <!-- 价格区 -->
          <div class="card-footer">
            <div class="price-block">
              <span class="price-amount">{{ formatPrice(item.price) }}</span>
              <span class="price-unit">/hr</span>
            </div>
            <button class="btn-select" @click.stop="selectItem(item)">
              <span>Select</span>
              <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5"><path d="M5 12h14M12 5l7 7-7 7"/></svg>
            </button>
          </div>
        </li>
      </TransitionGroup>

      <!-- 空结果 -->
      <div v-if="!loading && !error && query && items.length === 0" class="state-empty">
        <svg width="48" height="48" viewBox="0 0 24 24" fill="none" stroke="#555" stroke-width="1.2"><circle cx="11" cy="11" r="8"/><path d="M21 21l-4.3-4.3"/><line x1="8" y1="11" x2="14" y2="11"/></svg>
        <h3>No products found</h3>
        <p>Try a different search term like "H100" or "A100"</p>
        <div class="suggestion-pills">
          <button v-for="s in suggestions" :key="s" class="pill" @click="quickSearch(s)">{{ s }}</button>
        </div>
      </div>
    </div>

    <!-- ========== 空态（未搜索） ========== -->
    <div v-if="!query && !loading && !error && !showResults" class="state-idle">
      <div class="idle-graphic">
        <div class="floating-orb orb-1"></div>
        <div class="floating-orb orb-2"></div>
        <div class="floating-orb orb-3"></div>
        <svg width="64" height="64" viewBox="0 0 24 24" fill="none" stroke="var(--ancf-primary)" stroke-width="1" opacity="0.4">
          <rect x="3" y="3" width="7" height="7" rx="1.5"/>
          <rect x="14" y="3" width="7" height="7" rx="1.5"/>
          <rect x="3" y="14" width="7" height="7" rx="1.5"/>
          <rect x="14" y="14" width="7" height="7" rx="1.5"/>
        </svg>
      </div>
      <h3>ANCF Commerce</h3>
      <p>Search for GPU compute, storage, or AI inference resources</p>
      <div class="suggestion-pills">
        <button v-for="s in suggestions" :key="s" class="pill" @click="quickSearch(s)">{{ s }}</button>
      </div>
    </div>
  </div>
</template>

<script setup>
import { ref, computed, watch, onMounted } from 'vue'

const props = defineProps({
  apiBase: { type: String, default: 'http://127.0.0.1:8080' },
  wallet: { type: String, default: 'USER_WALLET' },
  network: { type: String, default: 'solana-mainnet' },
  shopDomain: { type: String, default: 'yourshop.com' },
  themeTokens: { type: Object, default: () => ({}) }
})

const emit = defineEmits(['select', 'changeWallet', 'changeNetwork'])

// ---- State ----
const query = ref('')
const loading = ref(false)
const error = ref('')
const items = ref([])
const selectedIndex = ref(-1)
const activeCategory = ref('')
const showResults = ref(false)
const searchInput = ref(null)
const failedImages = ref({})

const categories = ref([
  { key: 'gpu', icon: '⚡', label: 'GPU Compute', count: 3 },
  { key: 'storage', icon: '💾', label: 'Storage', count: 0 },
  { key: 'ai', icon: '🧠', label: 'AI Inference', count: 2 },
  { key: 'network', icon: '🌐', label: 'Bandwidth', count: 0 }
])

const suggestions = ['H100', 'A100', 'L40S', 'GPU']

// ---- Computed ----
const walletDisplay = computed(() => {
  const w = props.wallet
  if (!w || w === 'USER_WALLET') return 'Connect Wallet'
  return w.length > 12 ? w.slice(0, 6) + '...' + w.slice(-4) : w
})

// ---- Watch ----
let debounceTimer = null
watch(query, (val) => {
  clearTimeout(debounceTimer)
  if (!val.trim()) {
    showResults.value = false
    items.value = []
    error.value = ''
    return
  }
  debounceTimer = setTimeout(() => doSearch(val), 280)
})

// ---- Methods ----
async function doSearch(q) {
  if (!q?.trim()) return
  loading.value = true
  error.value = ''
  selectedIndex.value = -1
  showResults.value = true

  try {
    // Agent Bridge 优先
    const bridge = window.__ancfBridge
    let result
    if (bridge) {
      result = await bridge.handleCommand({
        command: 'ancf:search',
        params: { query: q.trim(), limit: 20 },
        requestId: crypto.randomUUID?.() || Date.now().toString(36)
      })
    } else {
      const resp = await fetch(`${props.apiBase}/api/v1/cli/search?q=${encodeURIComponent(q.trim())}&limit=20`)
      if (!resp.ok) throw new Error(`HTTP ${resp.status}`)
      result = await resp.json()
    }

    items.value = (result.items || result.result?.items || [])
    if (items.value.length === 0 && result.result?.items === undefined && result.items === undefined) {
      items.value = Array.isArray(result) ? result : []
    }
  } catch (e) {
    error.value = e.message || 'Search failed'
    items.value = []
  } finally {
    loading.value = false
  }
}

function selectItem(item) {
  emit('select', item)
}

function clearSearch() {
  query.value = ''
  items.value = []
  error.value = ''
  showResults.value = false
  selectedIndex.value = -1
  searchInput.value?.focus()
}

function quickSearch(term) {
  query.value = term
  activeCategory.value = ''
  searchInput.value?.focus()
}

function moveSelection(dir) {
  if (!items.value.length) return
  selectedIndex.value = Math.max(0, Math.min(items.value.length - 1, selectedIndex.value + dir))
}

function selectCurrent() {
  if (selectedIndex.value >= 0 && selectedIndex.value < items.value.length) {
    selectItem(items.value[selectedIndex.value])
  }
}

// ---- Helpers ----
function formatPrice(price) {
  if (!price) return '—'
  const major = parseInt(price.amount_minor) / Math.pow(10, price.scale || 6)
  return `${major.toFixed(price.scale || 6)} ${price.currency || 'vUSDC'}`
}

function stockClass(stock) {
  if (!stock || stock === 0) return 'none'
  if (stock < 50) return 'low'
  return 'ok'
}

function stockLabel(stock) {
  if (!stock || stock === 0) return 'Out of stock'
  if (stock >= 100) return `${stock}+ avail`
  return `${stock} left`
}

function mediaThumbnail(item) {
  const media = item?.media || {}
  if (typeof media.thumbnail === 'string' && media.thumbnail) return media.thumbnail
  if (Array.isArray(media.gallery)) return media.gallery.find(Boolean) || ''
  return ''
}

function markImageFailed(skuID) {
  failedImages.value = { ...failedImages.value, [skuID]: true }
}

function chipGradient(idx) {
  const gradients = [
    'linear-gradient(135deg, #1a3a2a 0%, #0d2818 100%)',
    'linear-gradient(135deg, #1a2a3a 0%, #0d1a28 100%)',
    'linear-gradient(135deg, #2a1a3a 0%, #1a0d28 100%)',
    'linear-gradient(135deg, #3a2a1a 0%, #281a0d 100%)'
  ]
  return gradients[idx % gradients.length]
}

onMounted(() => {
  searchInput.value?.focus()
})
</script>

<style scoped>
/* ================================================================
   ANCF Animated Catalog — Vue 3 SFC
   Dark theme, GPU compute marketplace
   ================================================================ */

.ancf-catalog {
  --ancf-primary: v-bind('themeTokens.primary || "#00FFA3"');
  --ancf-bg: v-bind('themeTokens.background || "#0D0E12"');
  --ancf-text: v-bind('themeTokens.text || "#FFFFFF"');
  --ancf-surface: #13141F;
  --ancf-border: #1E2030;
  --ancf-radius: 12px;
  font-family: 'Inter', system-ui, -apple-system, sans-serif;
  color: var(--ancf-text);
  max-width: 960px;
  margin: 0 auto;
}

/* ---- Header ---- */
.catalog-header { position: sticky; top: 0; z-index: 10; background: var(--ancf-bg); padding: 16px 0 8px; }
.header-top { display: flex; justify-content: space-between; align-items: center; margin-bottom: 12px; }
.shop-badge { display: flex; align-items: center; gap: 8px; font-size: 12px; color: #999; }
.shop-dot { width: 7px; height: 7px; border-radius: 50%; background: #00FFA3; box-shadow: 0 0 8px #00FFA3; animation: dot-pulse 2s infinite; }
@keyframes dot-pulse { 0%,100%{box-shadow:0 0 4px #00FFA3} 50%{box-shadow:0 0 14px #00FFA3} }

.header-actions { display: flex; gap: 8px; }
.btn-wallet, .btn-network {
  display: flex; align-items: center; gap: 6px;
  padding: 6px 12px; border-radius: 20px; border: 1px solid var(--ancf-border);
  background: var(--ancf-surface); color: #aaa; font-size: 12px;
  cursor: pointer; transition: all .2s;
}
.btn-wallet:hover, .btn-network:hover { border-color: #555; color: #fff; }
.wallet-addr { font-family: monospace; font-size: 11px; }

/* ---- Search ---- */
.search-wrap {
  display: flex; align-items: center; gap: 10px; padding: 12px 16px;
  background: var(--ancf-surface); border: 1px solid var(--ancf-border);
  border-radius: var(--ancf-radius); transition: border-color .25s;
}
.search-wrap:focus-within { border-color: var(--ancf-primary); box-shadow: 0 0 0 3px rgba(0,255,163,0.08); }
.search-icon { color: #555; flex-shrink: 0; }
.search-input { flex: 1; background: none; border: none; outline: none; color: var(--ancf-text); font-size: 16px; }
.search-input::placeholder { color: #444; }
.search-clear { background: none; border: none; color: #555; cursor: pointer; padding: 4px; border-radius: 4px; }
.search-clear:hover { color: #fff; background: #222; }

.search-loading-bar {
  height: 2px; background: linear-gradient(90deg, transparent, var(--ancf-primary), transparent);
  background-size: 200% 100%; animation: loading-slide 1.2s infinite; margin-top: 8px; border-radius: 1px;
}
@keyframes loading-slide { 0%{background-position:200% 0} 100%{background-position:-200% 0} }

/* ---- Category Pills ---- */
.category-pills { display: flex; gap: 8px; padding: 16px 0; overflow-x: auto; }
.pill {
  display: flex; align-items: center; gap: 6px; padding: 8px 16px;
  border-radius: 20px; border: 1px solid var(--ancf-border);
  background: var(--ancf-surface); color: #aaa; font-size: 13px;
  white-space: nowrap; cursor: pointer; transition: all .2s;
}
.pill:hover, .pill.active { border-color: var(--ancf-primary); color: #fff; background: rgba(0,255,163,0.06); }
.pill-icon { font-size: 15px; }
.pill-count { font-size: 10px; color: #555; background: rgba(255,255,255,0.05); padding: 2px 6px; border-radius: 10px; }

/* ---- Results ---- */
.results-container { padding-top: 8px; }
.results-header { display: flex; align-items: baseline; gap: 6px; margin-bottom: 12px; font-size: 13px; }
.results-count { color: var(--ancf-primary); font-weight: 600; }
.results-query { color: #666; }
.btn-clear-results { margin-left: auto; background: none; border: none; color: #555; cursor: pointer; font-size: 12px; }
.btn-clear-results:hover { color: #fff; }

.results-grid { display: grid; grid-template-columns: repeat(auto-fill, minmax(280px, 1fr)); gap: 14px; list-style: none; padding: 0; }

/* ---- Product Card ---- */
.product-card {
  background: var(--ancf-surface); border: 1px solid var(--ancf-border);
  border-radius: var(--ancf-radius); overflow: hidden;
  cursor: pointer; transition: all .3s cubic-bezier(.4,0,.2,1);
  animation: card-in .45s cubic-bezier(.4,0,.2,1) var(--card-delay, 0s) both;
}
.product-card:hover, .product-card.selected {
  transform: translateY(-2px);
  border-color: var(--ancf-primary);
  box-shadow: 0 8px 24px rgba(0,255,163,0.08);
}
@keyframes card-in { from { opacity:0; transform:translateY(16px) } to { opacity:1; transform:translateY(0) } }

/* Card media */
.card-media { position: relative; height: 100px; overflow: hidden; }
.media-image {
  width: 100%; height: 100%; object-fit: cover; display: block;
  background: #111827;
}
.media-placeholder {
  width: 100%; height: 100%; display: flex; align-items: center; justify-content: center;
  position: relative;
}
.media-chip { font-size: 28px; font-weight: 800; letter-spacing: 1px; color: rgba(255,255,255,0.15); }
.media-shimmer {
  position: absolute; inset: 0;
  background: linear-gradient(105deg, transparent 40%, rgba(255,255,255,0.03) 50%, transparent 60%);
  background-size: 200% 100%; animation: shimmer 2.5s infinite;
}
@keyframes shimmer { 0%{background-position:200% 0} 100%{background-position:-200% 0} }

.stock-badge {
  position: absolute; top: 10px; right: 10px; display: flex; align-items: center; gap: 5px;
  padding: 3px 10px; border-radius: 10px; font-size: 11px; font-weight: 500;
  backdrop-filter: blur(8px);
}
.stock-badge.ok { background: rgba(0,255,163,0.12); color: #00FFA3; border: 1px solid rgba(0,255,163,0.2); }
.stock-badge.low { background: rgba(255,170,0,0.12); color: #FFAA00; border: 1px solid rgba(255,170,0,0.2); }
.stock-badge.none { background: rgba(255,68,68,0.12); color: #FF4444; border: 1px solid rgba(255,68,68,0.2); }
.stock-dot { width: 5px; height: 5px; border-radius: 50%; background: currentColor; }

/* Card body */
.card-body { padding: 12px 14px 0; }
.card-sku { font-size: 10px; color: #555; font-family: monospace; margin-bottom: 4px; text-transform: uppercase; letter-spacing: .5px; }
.card-title { font-size: 15px; font-weight: 600; margin: 0 0 8px; line-height: 1.3; }
.spec-chips { display: flex; flex-wrap: wrap; gap: 4px; margin-bottom: 4px; }
.spec-chip { font-size: 10px; padding: 2px 7px; border-radius: 4px; background: rgba(255,255,255,0.04); color: #777; }

/* Card footer */
.card-footer { display: flex; align-items: center; justify-content: space-between; padding: 10px 14px 14px; }
.price-block { display: flex; align-items: baseline; gap: 2px; }
.price-amount { font-size: 18px; font-weight: 700; color: var(--ancf-primary); }
.price-unit { font-size: 12px; color: #666; }
.btn-select {
  display: flex; align-items: center; gap: 4px; padding: 7px 14px;
  border-radius: 8px; border: 1px solid rgba(0,255,163,0.3);
  background: rgba(0,255,163,0.06); color: var(--ancf-primary);
  font-size: 13px; font-weight: 600; cursor: pointer; transition: all .2s;
}
.btn-select:hover { background: rgba(0,255,163,0.15); }
.btn-select:hover svg { transform: translateX(2px); }
.btn-select svg { transition: transform .2s; }

/* ---- States ---- */
.state-error, .state-empty, .state-idle { text-align: center; padding: 48px 20px; }
.error-card {
  background: rgba(255,68,68,0.06); border: 1px solid rgba(255,68,68,0.2);
  border-radius: var(--ancf-radius); padding: 32px; max-width: 360px; margin: 0 auto;
}
.error-card h3 { margin: 12px 0 8px; color: #FF4444; }
.error-card p { color: #888; font-size: 13px; margin-bottom: 16px; }
.btn-retry {
  display: inline-flex; align-items: center; gap: 6px; padding: 8px 20px;
  border-radius: 8px; border: 1px solid #FF4444; background: transparent; color: #FF4444;
  cursor: pointer; font-size: 13px; transition: all .2s;
}
.btn-retry:hover { background: rgba(255,68,68,0.1); }

.state-empty h3, .state-idle h3 { margin: 16px 0 8px; font-size: 18px; }
.state-empty p, .state-idle p { color: #666; margin-bottom: 20px; max-width: 360px; margin-inline: auto; }

.suggestion-pills { display: flex; gap: 8px; justify-content: center; flex-wrap: wrap; }
.suggestion-pills .pill { cursor: pointer; }

/* Idle animation */
.idle-graphic { position: relative; width: 80px; height: 80px; margin: 0 auto 8px; display: flex; align-items: center; justify-content: center; }
.floating-orb { position: absolute; border-radius: 50%; opacity: 0.12; }
.orb-1 { width: 30px; height: 30px; background: var(--ancf-primary); animation: float-orb 3s infinite; }
.orb-2 { width: 18px; height: 18px; background: #4488FF; animation: float-orb 3s .5s infinite; }
.orb-3 { width: 22px; height: 22px; background: #FF44AA; animation: float-orb 3s 1s infinite; }
@keyframes float-orb { 0%,100%{transform:translate(0,0) scale(1)} 33%{transform:translate(6px,-12px) scale(1.2)} 66%{transform:translate(-8px,4px) scale(.8)} }

/* Transitions */
.card-enter-active, .card-leave-active { transition: all .35s cubic-bezier(.4,0,.2,1); }
.card-enter-from { opacity: 0; transform: translateY(20px) scale(.96); }
.card-leave-to { opacity: 0; transform: translateY(-8px) scale(.96); }
</style>
