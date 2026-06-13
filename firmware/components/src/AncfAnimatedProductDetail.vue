<template>
  <div class="ancf-detail" :class="{ 'is-loading': loading }">
    <!-- ========== 返回导航 ========== -->
    <nav class="detail-nav">
      <button class="btn-back" @click="$emit('back')">
        <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5"><path d="M19 12H5M12 19l-7-7 7-7"/></svg>
        <span>Back to catalog</span>
      </button>
      <div class="nav-shop">
        <span class="shop-dot"></span>
        {{ shopDomain }}
      </div>
    </nav>

    <!-- ========== 加载态 ========== -->
    <div v-if="loading" class="loading-skeleton">
      <div class="skeleton-media"></div>
      <div class="skeleton-title"></div>
      <div class="skeleton-specs"></div>
      <div class="skeleton-price"></div>
    </div>

    <!-- ========== 商品详情 ========== -->
    <article v-if="product && !loading" class="product-detail">
      <!-- Hero 区 -->
      <header class="detail-hero">
        <div class="hero-media">
          <div class="gpu-visual" :style="{ background: heroGradient }">
            <div class="gpu-core">
              <span class="gpu-label">{{ chipLabel }}</span>
              <div class="gpu-rings">
                <div class="ring ring-1"></div>
                <div class="ring ring-2"></div>
                <div class="ring ring-3"></div>
              </div>
            </div>
            <div class="particles">
              <span v-for="i in 12" :key="i" class="particle" :style="particleStyle(i)"></span>
            </div>
          </div>
          <div class="stock-float" :class="stockClass(product.stock_hint)">
            <span class="stock-dot"></span>
            {{ stockLabel(product.stock_hint) }}
          </div>
        </div>
        <div class="hero-info">
          <span class="hero-sku">{{ product.sku_id }}</span>
          <h1 class="hero-title">{{ product.title }}</h1>
          <p class="hero-desc" v-if="product.description">{{ product.description }}</p>

          <!-- 规格网格 -->
          <div class="spec-grid" v-if="product.specs">
            <div v-for="(v, k) in product.specs" :key="k" class="spec-item">
              <span class="spec-key">{{ k }}</span>
              <span class="spec-val">{{ v }}</span>
            </div>
          </div>
        </div>
      </header>

      <!-- ========== 报价区 ========== -->
      <section class="quote-section">
        <div class="quote-header-bar">
          <h2>Quote Configuration</h2>
          <span class="quote-id" v-if="quoteResult">Quote: {{ quoteResult.quote_id?.slice(0, 16) }}...</span>
        </div>

        <!-- 数量选择 -->
        <div class="qty-row">
          <span class="qty-label">Quantity</span>
          <div class="qty-control">
            <button class="qty-btn" @click="qty = Math.max(1, qty - 1)" :disabled="qty <= 1">−</button>
            <span class="qty-value" :class="{ changed: qtyChanged }">{{ qty }}</span>
            <button class="qty-btn" @click="qty = Math.min(99, qty + 1)" :disabled="qty >= 99">+</button>
          </div>
          <span class="qty-unit">units</span>
        </div>

        <!-- 价格预览 -->
        <div class="price-preview" :class="{ 'is-updated': qtyChanged }">
          <div class="preview-unit">
            <span class="preview-label">Unit Price</span>
            <span class="preview-value unit-price">{{ formatPrice(product.price) }}<span class="per">/hr</span></span>
          </div>
          <div class="preview-divider">
            <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" opacity="0.3"><path d="M5 12h14"/><path d="M12 5l7 7-7 7"/></svg>
          </div>
          <div class="preview-total">
            <span class="preview-label">Estimated Total</span>
            <span class="preview-value total-price" ref="totalPriceEl">{{ formatPrice(totalPrice) }}<span class="per">/hr</span></span>
          </div>
        </div>

        <!-- 获取报价按钮 -->
        <button
          class="btn-get-quote"
          :class="{ loading: quoting, done: !!quoteResult, expired: quoteExpired }"
          :disabled="quoting || quoteExpired"
          @click="requestQuote"
        >
          <span v-if="!quoting && !quoteResult" class="btn-content">
            <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><rect x="3" y="3" width="18" height="18" rx="3"/><path d="M9 12h6M12 9v6"/></svg>
            Get Quote
          </span>
          <span v-else-if="quoting" class="btn-content">
            <span class="spinner-sm"></span>
            Requesting...
          </span>
          <span v-else-if="quoteExpired" class="btn-content">
            <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><circle cx="12" cy="12" r="10"/><path d="M12 8v4M12 16h.01"/></svg>
            Quote Expired
          </span>
          <span v-else class="btn-content">
            <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5"><path d="M20 6L9 17l-5-5"/></svg>
            Quote Ready — Confirm
          </span>
        </button>

        <!-- 报价结果详情 -->
        <Transition name="slide">
          <div v-if="quoteResult && !quoteExpired" class="quote-result">
            <div class="quote-lines">
              <div class="quote-line" v-for="(line, i) in quoteResult.lines" :key="i">
                <span class="ql-sku">{{ line.sku_id }}</span>
                <span class="ql-qty">×{{ line.quantity }}</span>
                <span class="ql-unit">{{ formatMinor(line.unit_price_minor, quoteResult.scale) }}</span>
                <span class="ql-eq">=</span>
                <span class="ql-total">{{ formatMinor(line.line_total_minor, quoteResult.scale) }} {{ quoteResult.currency }}</span>
              </div>
            </div>
            <div class="quote-total-bar">
              <div class="quote-timer" :class="{ urgent: quoteUrgent }">
                <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5"><circle cx="12" cy="12" r="10"/><path d="M12 6v6l4 2"/></svg>
                {{ timeRemaining }}
              </div>
              <span class="quote-grand">Total: <strong>{{ formatMinor(quoteResult.total_minor, quoteResult.scale) }} {{ quoteResult.currency }}</strong></span>
            </div>
          </div>
        </Transition>
      </section>

      <!-- ========== 错误 ========== -->
      <Transition name="fade">
        <div v-if="error" class="error-toast">
          <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="#FF4444" stroke-width="2"><circle cx="12" cy="12" r="10"/><path d="M12 8v4M12 16h.01"/></svg>
          {{ error }}
          <button @click="error = ''">&times;</button>
        </div>
      </Transition>
    </article>
  </div>
</template>

<script setup>
import { ref, computed, watch, onUnmounted } from 'vue'

const props = defineProps({
  product: { type: Object, default: null },
  apiBase: { type: String, default: 'http://127.0.0.1:8080' },
  wallet: { type: String, default: 'USER_WALLET' },
  network: { type: String, default: 'solana-mainnet' },
  shopDomain: { type: String, default: 'yourshop.com' },
  shopId: { type: String, default: 'zero_shop_sol_01' },
  agentSessionId: { type: String, default: 'agent_session_local_001' }
})

const emit = defineEmits(['back', 'quoteReady', 'confirmCheckout'])

// ---- State ----
const qty = ref(1)
const quoting = ref(false)
const quoteResult = ref(null)
const quoteExpired = ref(false)
const timeRemaining = ref('')
const error = ref('')
const loading = ref(false)
let expiryTimer = null

// ---- Computed ----
const qtyChanged = computed(() => qty.value !== 1)

const totalPrice = computed(() => {
  if (!props.product?.price) return { currency: 'vUSDC', amount_minor: '0', scale: 6 }
  const p = props.product.price
  return { ...p, amount_minor: String(BigInt(p.amount_minor) * BigInt(qty.value)) }
})

const quoteUrgent = computed(() => {
  if (!quoteResult.value) return false
  const exp = new Date(quoteResult.value.expires_at).getTime()
  return (exp - Date.now()) < 60000
})

const chipLabel = computed(() => {
  if (!props.product) return ''
  return props.product.specs?.GPU?.split(' ')[0] || props.product.sku_id?.slice(-4).toUpperCase() || 'GPU'
})

const heroGradient = computed(() => {
  const gradients = [
    'radial-gradient(ellipse at 30% 20%, rgba(0,255,163,0.06) 0%, transparent 60%), linear-gradient(160deg, #0d2818 0%, #0D0E12 60%)',
    'radial-gradient(ellipse at 30% 20%, rgba(68,136,255,0.06) 0%, transparent 60%), linear-gradient(160deg, #0d1a28 0%, #0D0E12 60%)',
    'radial-gradient(ellipse at 30% 20%, rgba(255,68,170,0.06) 0%, transparent 60%), linear-gradient(160deg, #280d1a 0%, #0D0E12 60%)'
  ]
  const idx = (props.product?.sku_id?.length || 0) % gradients.length
  return gradients[idx]
})

// ---- Methods ----
function formatPrice(price) {
  if (!price) return '—'
  const major = parseInt(price.amount_minor) / Math.pow(10, price.scale || 6)
  return `${major.toFixed(price.scale || 6)} ${price.currency || 'vUSDC'}`
}

function formatMinor(minor, scale = 6) {
  const major = parseInt(minor) / Math.pow(10, scale)
  return major.toFixed(Math.min(scale, 4))
}

function stockClass(stock) {
  if (!stock || stock === 0) return 'none'
  if (stock < 50) return 'low'
  return 'ok'
}

function stockLabel(stock) {
  if (!stock || stock === 0) return 'Out of stock'
  if (stock >= 100) return `${stock}+ available`
  return `${stock} available`
}

function particleStyle(i) {
  const angle = (i / 12) * 360
  const delay = (i * 0.3) % 3
  return {
    '--angle': `${angle}deg`,
    '--delay': `${delay}s`
  }
}

function startExpiryTimer() {
  clearInterval(expiryTimer)
  if (!quoteResult.value) return
  const tick = () => {
    const diff = new Date(quoteResult.value.expires_at).getTime() - Date.now()
    if (diff <= 0) {
      quoteExpired.value = true
      clearInterval(expiryTimer)
      return
    }
    const m = Math.floor(diff / 60000)
    const s = Math.floor((diff % 60000) / 1000)
    timeRemaining.value = `Expires in ${m}:${String(s).padStart(2,'0')}`
  }
  tick()
  expiryTimer = setInterval(tick, 1000)
}

// ---- Quote Request ----
async function requestQuote() {
  if (quoteExpired.value) return
  if (quoteResult.value) {
    // 已有报价 → 确认 → 触发 checkout
    emit('confirmCheckout', {
      quote_id: quoteResult.value.quote_id,
      product: props.product,
      qty: qty.value,
      wallet: props.wallet
    })
    return
  }

  quoting.value = true
  error.value = ''

  try {
    const bridge = window.__ancfBridge
    let result
    const params = {
      wallet: props.wallet,
      network: props.network,
      lines: [{ sku_id: props.product.sku_id, quantity: qty.value }]
    }

    if (bridge) {
      result = await bridge.handleCommand({
        command: 'ancf:quote',
        params,
        requestId: crypto.randomUUID?.() || Date.now().toString(36)
      })
    } else {
      const resp = await fetch(`${props.apiBase}/api/v1/cli/quote`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(params)
      })
      if (!resp.ok) throw new Error(`Quote API HTTP ${resp.status}`)
      result = await resp.json()
    }

    quoteResult.value = result.result || result
    startExpiryTimer()
    emit('quoteReady', quoteResult.value)
  } catch (e) {
    error.value = e.message || 'Quote request failed'
  } finally {
    quoting.value = false
  }
}

watch(() => props.product, () => {
  qty.value = 1
  quoteResult.value = null
  quoteExpired.value = false
  error.value = ''
  clearInterval(expiryTimer)
})

onUnmounted(() => clearInterval(expiryTimer))
</script>

<style scoped>
/* ================================================================
   ANCF Animated Product Detail — Vue 3 SFC
   ================================================================ */

.ancf-detail {
  --ancf-primary: #00FFA3;
  --ancf-bg: #0D0E12;
  --ancf-text: #FFFFFF;
  --ancf-surface: #13141F;
  --ancf-border: #1E2030;
  --ancf-radius: 14px;
  font-family: 'Inter', system-ui, -apple-system, sans-serif;
  color: var(--ancf-text);
  max-width: 720px;
  margin: 0 auto;
}

/* ---- Nav ---- */
.detail-nav { display: flex; justify-content: space-between; align-items: center; padding: 12px 0; margin-bottom: 4px; }
.btn-back { display: flex; align-items: center; gap: 6px; background: none; border: none; color: #999; cursor: pointer; font-size: 13px; padding: 6px 0; transition: color .2s; }
.btn-back:hover { color: #fff; }
.nav-shop { display: flex; align-items: center; gap: 6px; font-size: 11px; color: #555; }
.shop-dot { width: 6px; height: 6px; border-radius: 50%; background: #00FFA3; }

/* ---- Skeleton ---- */
.loading-skeleton { padding: 32px 0; display: flex; flex-direction: column; gap: 16px; }
.skeleton-media { height: 160px; border-radius: var(--ancf-radius); background: linear-gradient(90deg, #13141F 0%, #1a1b2e 50%, #13141F 100%); background-size: 200% 100%; animation: skeleton-shine 1.8s infinite; }
.skeleton-title { height: 28px; width: 70%; border-radius: 6px; background: linear-gradient(90deg, #13141F 0%, #1a1b2e 50%, #13141F 100%); background-size: 200% 100%; animation: skeleton-shine 1.8s .1s infinite; }
.skeleton-specs { height: 60px; width: 100%; border-radius: 6px; background: linear-gradient(90deg, #13141F 0%, #1a1b2e 50%, #13141F 100%); background-size: 200% 100%; animation: skeleton-shine 1.8s .2s infinite; }
.skeleton-price { height: 44px; width: 50%; border-radius: 6px; background: linear-gradient(90deg, #13141F 0%, #1a1b2e 50%, #13141F 100%); background-size: 200% 100%; animation: skeleton-shine 1.8s .3s infinite; }
@keyframes skeleton-shine { 0%{background-position:200% 0} 100%{background-position:-200% 0} }

/* ---- Hero ---- */
.detail-hero {
  display: grid; grid-template-columns: 1fr 1fr; gap: 24px;
  padding: 24px; background: var(--ancf-surface); border: 1px solid var(--ancf-border);
  border-radius: var(--ancf-radius); margin-bottom: 20px;
}
.hero-media { position: relative; }
.gpu-visual {
  width: 100%; aspect-ratio: 1; border-radius: 12px;
  display: flex; align-items: center; justify-content: center; overflow: hidden; position: relative;
}
.gpu-core { position: relative; z-index: 1; display: flex; flex-direction: column; align-items: center; }
.gpu-label { font-size: 36px; font-weight: 900; letter-spacing: 2px; color: rgba(255,255,255,0.08); }
.gpu-rings { position: absolute; }
.ring { position: absolute; top: 50%; left: 50%; border-radius: 50%; border: 1px solid rgba(255,255,255,0.04); transform: translate(-50%, -50%); }
.ring-1 { width: 80px; height: 80px; animation: ring-spin 8s linear infinite; }
.ring-2 { width: 120px; height: 120px; animation: ring-spin 12s linear infinite reverse; }
.ring-3 { width: 160px; height: 160px; animation: ring-spin 16s linear infinite; }
@keyframes ring-spin { to { transform: translate(-50%, -50%) rotate(360deg); } }

.particles { position: absolute; inset: 0; }
.particle { position: absolute; top: 50%; left: 50%; width: 2px; height: 2px; background: var(--ancf-primary); border-radius: 50%; opacity: 0.3;
  animation: particle-drift 4s var(--delay, 0s) infinite;
  transform: rotate(var(--angle, 0deg)) translateY(-40px);
}
@keyframes particle-drift { 0%,100%{opacity:.1;transform:rotate(var(--angle)) translateY(-30px)} 50%{opacity:.4;transform:rotate(var(--angle)) translateY(-50px)} }

.stock-float {
  position: absolute; bottom: 8px; left: 50%; transform: translateX(-50%);
  display: flex; align-items: center; gap: 5px; padding: 4px 14px; border-radius: 12px; font-size: 12px; font-weight: 500;
  backdrop-filter: blur(10px);
}
.stock-float.ok { background: rgba(0,255,163,0.1); color: #00FFA3; border: 1px solid rgba(0,255,163,0.15); }
.stock-float.low { background: rgba(255,170,0,0.1); color: #FFAA00; border: 1px solid rgba(255,170,0,0.15); }
.stock-float.none { background: rgba(255,68,68,0.1); color: #FF4444; border: 1px solid rgba(255,68,68,0.15); }
.stock-dot { width: 5px; height: 5px; border-radius: 50%; background: currentColor; }

.hero-info { display: flex; flex-direction: column; justify-content: center; }
.hero-sku { font-size: 11px; color: #555; font-family: monospace; text-transform: uppercase; letter-spacing: .5px; margin-bottom: 6px; }
.hero-title { font-size: 20px; font-weight: 700; margin: 0 0 8px; line-height: 1.3; }
.hero-desc { color: #777; font-size: 13px; line-height: 1.5; margin: 0 0 12px; }

.spec-grid { display: grid; grid-template-columns: 1fr 1fr; gap: 6px; }
.spec-item { display: flex; justify-content: space-between; padding: 6px 10px; background: rgba(255,255,255,0.025); border-radius: 6px; }
.spec-key { font-size: 11px; color: #666; text-transform: uppercase; letter-spacing: .5px; }
.spec-val { font-size: 12px; color: #aaa; font-weight: 500; }

/* ---- Quote Section ---- */
.quote-section { background: var(--ancf-surface); border: 1px solid var(--ancf-border); border-radius: var(--ancf-radius); padding: 20px 24px; }
.quote-header-bar { display: flex; justify-content: space-between; align-items: center; margin-bottom: 16px; }
.quote-header-bar h2 { font-size: 16px; font-weight: 600; margin: 0; }
.quote-id { font-size: 11px; color: #555; font-family: monospace; }

.qty-row { display: flex; align-items: center; gap: 14px; margin-bottom: 16px; }
.qty-label { font-size: 13px; color: #888; flex: 1; }
.qty-control { display: flex; align-items: center; gap: 0; border: 1px solid var(--ancf-border); border-radius: 10px; overflow: hidden; }
.qty-btn {
  width: 38px; height: 38px; border: none; background: rgba(255,255,255,0.03); color: #aaa; font-size: 18px;
  cursor: pointer; transition: all .2s; display: flex; align-items: center; justify-content: center;
}
.qty-btn:hover:not(:disabled) { background: rgba(255,255,255,0.08); color: #fff; }
.qty-btn:disabled { opacity: .3; cursor: not-allowed; }
.qty-value { width: 48px; text-align: center; font-size: 20px; font-weight: 700; transition: all .3s; }
.qty-value.changed { color: var(--ancf-primary); }
.qty-unit { font-size: 12px; color: #666; }

/* Price Preview */
.price-preview { display: flex; align-items: center; gap: 16px; padding: 16px; background: rgba(255,255,255,0.015); border-radius: 10px; margin-bottom: 16px; transition: background .3s; }
.price-preview.is-updated { background: rgba(0,255,163,0.03); }
.preview-unit, .preview-total { flex: 1; }
.preview-label { display: block; font-size: 10px; text-transform: uppercase; letter-spacing: .8px; color: #555; margin-bottom: 4px; }
.preview-value { font-size: 20px; font-weight: 700; transition: all .3s; }
.unit-price { color: #aaa; }
.total-price { color: var(--ancf-primary); font-size: 24px; }
.per { font-size: 12px; color: #555; font-weight: 400; }
.preview-divider { color: #333; flex-shrink: 0; }

/* Get Quote Button */
.btn-get-quote {
  width: 100%; padding: 14px; border-radius: 12px; border: none;
  font-size: 15px; font-weight: 600; cursor: pointer; transition: all .3s;
}
.btn-get-quote:not(.loading):not(.done):not(.expired) {
  background: var(--ancf-primary); color: #0D0E12;
}
.btn-get-quote:not(.loading):not(.done):not(.expired):hover {
  filter: brightness(1.1); transform: translateY(-1px); box-shadow: 0 4px 20px rgba(0,255,163,0.2);
}
.btn-get-quote.loading { background: rgba(255,255,255,0.05); color: #888; cursor: wait; }
.btn-get-quote.done { background: rgba(0,255,163,0.08); color: var(--ancf-primary); border: 1px solid rgba(0,255,163,0.2); }
.btn-get-quote.done:hover { background: rgba(0,255,163,0.15); }
.btn-get-quote.expired { background: rgba(255,68,68,0.06); color: #FF4444; cursor: not-allowed; border: 1px solid rgba(255,68,68,0.15); }
.btn-content { display: flex; align-items: center; justify-content: center; gap: 8px; }

.spinner-sm { width: 16px; height: 16px; border: 2px solid rgba(255,255,255,0.1); border-top-color: #888; border-radius: 50%; animation: spin .6s linear infinite; }
@keyframes spin { to { transform: rotate(360deg); } }

/* Quote Result */
.quote-result { margin-top: 16px; border-top: 1px solid var(--ancf-border); padding-top: 16px; }
.quote-lines { margin-bottom: 12px; }
.quote-line { display: flex; align-items: center; gap: 10px; padding: 6px 0; font-size: 13px; }
.ql-sku { font-family: monospace; font-size: 11px; color: #666; flex: 1; }
.ql-qty { color: #888; }
.ql-unit { color: #aaa; }
.ql-eq { color: #555; }
.ql-total { color: var(--ancf-primary); font-weight: 600; }

.quote-total-bar { display: flex; align-items: center; justify-content: space-between; }
.quote-timer { display: flex; align-items: center; gap: 5px; font-size: 12px; color: #FFAA00; font-family: monospace; }
.quote-timer.urgent { color: #FF4444; animation: urgent-pulse 1s infinite; }
@keyframes urgent-pulse { 0%,100%{opacity:1} 50%{opacity:.5} }
.quote-grand { font-size: 14px; color: #aaa; }
.quote-grand strong { color: #fff; }

/* Error Toast */
.error-toast {
  display: flex; align-items: center; gap: 8px; margin-top: 12px; padding: 10px 14px;
  background: rgba(255,68,68,0.08); border: 1px solid rgba(255,68,68,0.2);
  border-radius: 10px; color: #FF6666; font-size: 13px;
}
.error-toast button { margin-left: auto; background: none; border: none; color: #FF6666; cursor: pointer; font-size: 16px; }

/* Transitions */
.slide-enter-active, .slide-leave-active { transition: all .35s cubic-bezier(.4,0,.2,1); }
.slide-enter-from, .slide-leave-to { opacity: 0; transform: translateY(-8px); }
.fade-enter-active, .fade-leave-active { transition: opacity .25s; }
.fade-enter-from, .fade-leave-to { opacity: 0; }

@media (max-width: 600px) {
  .detail-hero { grid-template-columns: 1fr; gap: 16px; }
  .price-preview { flex-direction: column; text-align: center; }
  .preview-divider { transform: rotate(90deg); }
}
</style>
