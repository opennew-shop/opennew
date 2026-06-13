const { chromium } = require('playwright');

const TARGET = 'http://127.0.0.1:3000';

(async () => {
  const browser = await chromium.launch({ headless: true });
  const page = await browser.newPage();
  const logs = [];

  page.on('console', msg => logs.push({ t: msg.type(), m: msg.text() }));
  page.on('pageerror', err => logs.push({ t: 'error', m: err.message }));
  page.on('requestfailed', req => {
    console.log('  REQUEST FAILED:', req.url(), req.failure()?.errorText || 'unknown');
  });

  await page.goto(TARGET, { waitUntil: 'networkidle', timeout: 20000 });
  await page.waitForTimeout(800);

  const errors = logs.filter(l => l.t === 'error');
  console.log('1. JS Errors:', errors.length === 0 ? 'NONE OK' : errors.map(e => e.m).join('|'));

  const vueOk = await page.evaluate(() => !!document.querySelector('#app')?.__vue_app__);
  console.log('2. Vue runtime mounted:', vueOk ? 'YES (unexpected)' : 'NO OK');

  const payloadOk = await page.evaluate(() => !!document.querySelector('script#ancf-payload[type="application/json"]'));
  console.log('3. Payload slot exists:', payloadOk ? 'YES OK' : 'NO FAIL');

  const rowCount = await page.evaluate(() => document.querySelectorAll('.product-row').length);
  console.log('4. Stitch product rows:', rowCount, rowCount > 0 ? 'OK' : 'FAIL');

  const searchOk = await page.isVisible('#search');
  console.log('5. Template search input:', searchOk ? 'YES OK' : 'NO FAIL');

  if (searchOk) {
    await page.fill('#search', 'H100');
    await page.waitForTimeout(500);
    const filteredCount = await page.evaluate(() => document.querySelectorAll('.product-row').length);
    const firstTitle = await page.evaluate(() => document.querySelector('.product-row h3')?.textContent?.trim() || 'N/A');
    console.log('6. Rows after local filter:', filteredCount, '| first:', firstTitle);
  }

  await page.evaluate(() => {
    document.querySelector('.media')?.click();
  });
  await page.waitForURL(/\/detail\?sku=/, { timeout: 8000 });
  await page.waitForTimeout(800);

  const detailPath = page.url();
  const safetyOk = await page.isVisible('.safety');
  const detailTitle = await page.evaluate(() => document.querySelector('.summary h1')?.textContent?.trim() || 'N/A');
  console.log('7. Detail URL:', detailPath);
  console.log('8. Detail safety notice:', safetyOk ? 'YES OK' : 'NO FAIL');
  console.log('9. Detail title:', detailTitle);

  let quoteEventSku = '';
  await page.exposeFunction('captureQuoteEvent', sku => {
    quoteEventSku = sku;
  });
  await page.evaluate(() => {
    document.addEventListener('ANCF_TEMPLATE_QUOTE', event => {
      window.captureQuoteEvent(event.detail?.sku_id || '');
    }, { once: true });
  });

  const dialogMessages = [];
  page.on('dialog', async dialog => {
    dialogMessages.push(dialog.message());
    if (dialog.type() === 'prompt') {
      await dialog.accept('DEMO_WALLET_ABC123');
      return;
    }
    if (dialog.type() === 'confirm') {
      await dialog.dismiss();
      return;
    }
    await dialog.dismiss();
  });
  await page.click('[data-action="quote"]');
  await page.waitForTimeout(1200);
  console.log('10. Dialogs:', dialogMessages.map(m => m.split('\n')[0]).join(' | ') || 'NONE');
  console.log('11. Quote intent event SKU:', quoteEventSku || 'NO EVENT');

  await page.screenshot({ path: 'd:/开发者测试/sol web demo/screenshot.png', fullPage: true });
  console.log('\nScreenshot saved to screenshot.png');
  console.log('\nBrowser console (last 10):');
  logs.slice(-10).forEach(l => console.log('  [' + l.t + ']', l.m));

  await browser.close();
  console.log('Test complete.');
})();
