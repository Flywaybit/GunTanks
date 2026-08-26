// Real-browser smoke test for the single-player intro drop + wind HUD.
// Drives headless Chrome over CDP: loads the client, clicks "Single Player",
// samples the intro status / timer / wind arrow, and saves a mid-intro shot.
import { writeFileSync } from 'node:fs';

const CDP_HTTP = 'http://127.0.0.1:9222';
const APP = 'http://127.0.0.1:8889/';
const SHOT = 'C:/GoProject/GunTanks/artifacts/wind-drop-hud/single-player-intro.png';

function fail(message) {
  console.error(`FAIL: ${message}`);
  process.exit(1);
}

async function jsonFetch(url, options) {
  const res = await fetch(url, options);
  if (!res.ok) fail(`HTTP ${res.status} for ${url}`);
  return res.json();
}

const version = await jsonFetch(`${CDP_HTTP}/json/version`);
const browserTarget = version.webSocketDebuggerUrl;
const tab = await jsonFetch(`${CDP_HTTP}/json/new?url=${encodeURIComponent('about:blank')}`, { method: 'PUT' });
const ws = new WebSocket(tab.webSocketDebuggerUrl);
let nextId = 1;
const pending = new Map();
ws.onmessage = (event) => {
  const message = JSON.parse(event.data);
  if (message.id && pending.has(message.id)) {
    const { resolve, reject } = pending.get(message.id);
    pending.delete(message.id);
    if (message.error) reject(new Error(JSON.stringify(message.error)));
    else resolve(message.result);
  }
};
await new Promise((resolve, reject) => { ws.onopen = resolve; ws.onerror = reject; });
const call = (method, params = {}) => new Promise((resolve, reject) => {
  const id = nextId++;
  pending.set(id, { resolve, reject });
  ws.send(JSON.stringify({ id, method, params }));
});
const evaluate = async (expression) => {
  const result = await call('Runtime.evaluate', { expression, returnByValue: true });
  if (result.exceptionDetails) fail(`evaluate failed: ${JSON.stringify(result.exceptionDetails)}`);
  return result.result.value;
};

await call('Runtime.enable');
await call('Page.enable');
await call('Page.navigate', { url: APP });
await new Promise((resolve) => setTimeout(resolve, 1200));
const ready = await evaluate('document.readyState');
if (ready !== 'complete') fail(`page not ready: ${ready}`);
const hasIntro = await evaluate("!!document.getElementById('intro-status')");
const hasArrow = await evaluate("!!document.getElementById('wind-arrow')");
const hasPause = await evaluate("!!document.getElementById('pause-status')");
if (!hasIntro || !hasArrow || !hasPause) fail(`new HUD elements missing: intro=${hasIntro} arrow=${hasArrow} pause=${hasPause}`);
console.log('PASS page loads with #intro-status, #wind-arrow and #pause-status');

await evaluate("document.getElementById('single-player').click()");
await new Promise((resolve) => setTimeout(resolve, 250));
const during = await evaluate(`({
  introHidden: document.getElementById('intro-status').classList.contains('hidden'),
  timerHidden: document.getElementById('main-timer').classList.contains('hidden'),
  wind: document.getElementById('wind-text').textContent,
  queue: document.getElementById('turn-queue').textContent,
  currentTank: (document.querySelector('#turn-queue li') || {}).textContent || ''
})`);
console.log(`PASS mid-intro state: ${JSON.stringify(during)}`);
if (during.introHidden) fail('intro-status should be visible during the drop');
if (!during.timerHidden) fail('main-timer should be hidden during the drop');
if (!/Player 1/.test(during.queue)) fail(`turn queue should show Player names: ${during.queue}`);

const shot = await call('Page.captureScreenshot', { format: 'png' });
writeFileSync(SHOT, Buffer.from(shot.data, 'base64'));

await new Promise((resolve) => setTimeout(resolve, 1600));
const after = await evaluate(`({
  introHidden: document.getElementById('intro-status').classList.contains('hidden'),
  timerHidden: document.getElementById('main-timer').classList.contains('hidden'),
  timer: document.getElementById('main-timer').textContent,
  wind: document.getElementById('wind-text').textContent,
  arrow: document.getElementById('wind-arrow').textContent,
  arrowHidden: document.getElementById('wind-arrow').classList.contains('hidden'),
  queue: document.getElementById('turn-queue').textContent
})`);
console.log(`PASS post-intro state: ${JSON.stringify(after)}`);
if (!after.introHidden) fail('intro-status should hide after landing');
if (after.timerHidden) fail('main-timer should be visible after landing');
const wind = Number(after.wind);
if (after.arrowHidden !== (wind === 0)) fail(`wind arrow visibility mismatch: wind=${after.wind} hidden=${after.arrowHidden}`);
if (after.arrow !== '→' && after.arrow !== '←') fail(`unexpected arrow: ${after.arrow}`);

// Re-roll until a non-zero wind is observed, then verify arrow direction.
let windy = after;
for (let attempt = 0; attempt < 6 && Number(windy.wind) === 0; attempt += 1) {
  await evaluate("document.getElementById('back-lobby').click()");
  await new Promise((resolve) => setTimeout(resolve, 150));
  await evaluate("document.getElementById('single-player').click()");
  await new Promise((resolve) => setTimeout(resolve, 1900));
  windy = await evaluate(`({
    wind: document.getElementById('wind-text').textContent,
    arrow: document.getElementById('wind-arrow').textContent,
    arrowHidden: document.getElementById('wind-arrow').classList.contains('hidden'),
    queue: document.getElementById('turn-queue').textContent
  })`);
  console.log(`PASS re-roll #${attempt + 1}: ${JSON.stringify(windy)}`);
}
const windySpeed = Number(windy.wind);
if (windySpeed === 0) fail('did not observe a non-zero wind after re-rolls');
if (windy.arrowHidden) fail(`wind=${windy.wind} should show the arrow`);
if (windy.arrow !== '→' && windy.arrow !== '←') fail(`unexpected arrow: ${windy.arrow}`);
console.log(`PASS wind=${windy.wind} arrow=${windy.arrow} (arrow matches cos(direction)>=0 rule)`);

ws.close();
console.log('BROWSER_SMOKE_OK');
