/**
 * Build Vue SFC → standalone ES Module JS
 *
 * Compiles AncfAnimatedCatalog.vue and AncfAnimatedProductDetail.vue
 * into browser-runnable ES modules that use the global Vue runtime.
 *
 * The output files import nothing — they expect `Vue` on the global scope
 * (from the Vue CDN script tag loaded before them).
 *
 * Usage: node build-vue-components.cjs
 */

const fs = require('fs');
const path = require('path');

const srcDir = path.resolve(__dirname, '..', 'src');
const distDir = path.resolve(__dirname, '..', 'dist');

const files = [
  'AncfAnimatedCatalog.vue',
  'AncfAnimatedProductDetail.vue'
];

// ---- Minimal Vue SFC compiler (no dependency needed) ----
// Extracts <template>, <script setup>, <style scoped> from an SFC
// and produces a self-contained ES module that uses global Vue.

function parseSFC(content) {
  const templateMatch = content.match(/<template>([\s\S]*?)<\/template>/);
  const scriptMatch = content.match(/<script setup[^>]*>([\s\S]*?)<\/script>/);
  const styleMatch = content.match(/<style scoped[^>]*>([\s\S]*?)<\/style>/);

  return {
    template: templateMatch ? templateMatch[1].trim() : '',
    script: scriptMatch ? scriptMatch[1].trim() : '',
    style: styleMatch ? styleMatch[1].trim() : ''
  };
}

/**
 * Transpile Vue SFC <script setup> to a form compatible with Options API
 * that can run in browser without a build step.
 *
 * Strategy: wrap the setup code in a Vue component definition object
 * using the setup() function, and attach the template and styles.
 */
function compileToBrowserModule(sfc, componentName) {
  const { template, script, style } = parseSFC(sfc);

  // Escape template for JS string
  const escapedTemplate = template
    .replace(/\\/g, '\\\\')
    .replace(/`/g, '\\`')
    .replace(/\$/g, '\\$');

  // Escape style for JS string
  const escapedStyle = style
    .replace(/\\/g, '\\\\')
    .replace(/`/g, '\\`')
    .replace(/\$/g, '\\$');

  // Convert <script setup> to setup() function body.
  // We need to handle: imports → global Vue, ref/reactive/computed/watch → Vue.xxx
  // For simplicity, we use Vue's defineComponent with a setup function.

  // Strip import statements and convert to global Vue references
  let setupBody = script
    // Remove import lines (they reference Vue which is global)
    .replace(/^import\s+.*from\s+['"]vue['"];?\s*$/gm, '')
    // Convert Vue imports to global references
    .replace(/\bimport\s*\{([^}]+)\}\s*from\s*['"]vue['"];?\s*/g, '')
    // Remove empty lines left by import removal
    .replace(/^\s*\n/gm, '');

  // Map Vue composition API calls to global Vue.xxx
  const vueGlobals = [
    'ref', 'reactive', 'computed', 'watch', 'onMounted',
    'onUnmounted', 'defineProps', 'defineEmits', 'nextTick',
    'toRefs', 'toRef', 'provide', 'inject', 'shallowRef',
    'triggerRef', 'customRef', 'readonly', 'watchEffect',
    'onBeforeMount', 'onBeforeUnmount', 'onBeforeUpdate',
    'onUpdated', 'onActivated', 'onDeactivated', 'onErrorCaptured'
  ];

  for (const fn of vueGlobals) {
    // Only replace standalone calls, not property accesses
    const regex = new RegExp(`(?<!\\.)${fn}\\(`, 'g');
    setupBody = setupBody.replace(regex, `Vue.${fn}(`);
  }

  // Handle defineProps → convert to props option in the component definition
  // We'll handle props separately so skip defineProps in setup body
  setupBody = setupBody.replace(/const\s+props\s*=\s*defineProps\(([\s\S]*?)\)/g, (match, propsDef) => {
    return `const props = __props__`;
  });
  setupBody = setupBody.replace(/const\s+emit\s*=\s*defineEmits\(([\s\S]*?)\)/g, (match, emitsDef) => {
    return `const emit = __emit__`;
  });

  // Extract props definition from defineProps
  const propsMatch = script.match(/defineProps\(\{([\s\S]*?)\}\)/);
  let propsDef = '{}';
  if (propsMatch) {
    // Convert TypeScript-style props to Vue runtime props
    propsDef = `{${propsMatch[1]}}`
      .replace(/:\s*\{\s*type:\s*(\w+)/g, ': { type: $1')
      .replace(/:\s*\{\s*type:\s*\[([^\]]+)\]/g, ': { type: [$1]');
  }

  // Extract emits from defineEmits
  const emitsMatch = script.match(/defineEmits\(\[([\s\S]*?)\]\)/);
  const emitsArr = emitsMatch ? `[${emitsMatch[1]}]` : '[]';

  return `/* ${componentName} — compiled from Vue SFC */
/* Auto-generated. DO NOT EDIT. Source: firmware/components/src/${componentName}.vue */

(function() {
  const { defineComponent, ref, reactive, computed, watch, onMounted, onUnmounted } = Vue;

  const component = {
    name: '${componentName}',
    props: ${propsDef},
    emits: ${emitsArr},
    template: \`${escapedTemplate}\`,
    setup(props, context) {
      const __props__ = props;
      const __emit__ = (...args) => context.emit(...args);
      ${setupBody}
      return { __props__, __emit__ };
    }
  };

  /* Inject scoped styles */
  if (typeof document !== 'undefined') {
    const styleEl = document.createElement('style');
    styleEl.setAttribute('data-vue-component', '${componentName}');
    styleEl.textContent = \`${escapedStyle}\`;
    document.head.appendChild(styleEl);
  }

  /* Register globally */
  if (typeof window !== 'undefined') {
    window.__ancfComponents = window.__ancfComponents || {};
    window.__ancfComponents['${componentName}'] = component;
  }

  /* Export as ES module */
  export default component;
})();
`;
}

// ---- Main ----
if (!fs.existsSync(distDir)) {
  fs.mkdirSync(distDir, { recursive: true });
}

for (const file of files) {
  const srcPath = path.join(srcDir, file);
  if (!fs.existsSync(srcPath)) {
    console.error(`[build-vue] MISSING: ${srcPath}`);
    continue;
  }

  const sfc = fs.readFileSync(srcPath, 'utf-8');
  const componentName = file.replace('.vue', '');
  const js = compileToBrowserModule(sfc, componentName);
  const outFile = file.replace('.vue', '.vue.js');
  const outPath = path.join(distDir, outFile);

  fs.writeFileSync(outPath, js, 'utf-8');
  console.log(`[build-vue] ${file} → ${outFile} (${(js.length / 1024).toFixed(1)} KB)`);
}

console.log('[build-vue] Done.');
