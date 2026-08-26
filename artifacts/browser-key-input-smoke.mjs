// Real-browser smoke test for KeyInput.md: holding a key must keep moving the
// tank / rotating the aim in single-player (like the online 60Hz loop), with
// the 80-unit move cap enforced.
const CDP_HTTP = 'http://127.0.0.1:9222';
const APP = 'http://127.0.0.1:8889/';

function fail(message) {
  console.error(`FAIL: ${message}`);
  process.exit(1);
}

async function jsonFetch(url, options) {
  const res = await fetch(url, options);
  if (!res.ok) fail(`HTTP ${res.status} for ${url}`);
  return res.json();
}

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
await call('Page.setWebLifecycleState', { state: 'active' });

const key = async (type, code, keyName, vk) => {
  await call('Input.dispatchKeyEvent', { type, code, key: keyName, windowsVirtualKeyCode: vk, nativeVirtualKeyCode: vk });
};

async function waitFor(expression, label, timeoutMs = 10000) {
  const deadline = Date.now() + timeoutMs;
  for (;;) {
    if (await evaluate(expression)) return;
    if (Date.now() > deadline) fail(`timed out waiting for ${label}`);
    await new Promise((resolve) => setTimeout(resolve, 200));
  }
}

async function tankBandShot() {
  const rect = await evaluate(`(() => { const r = document.getElementById('stage').getBoundingClientRect(); return { left: r.left, top: r.top, width: r.width, height: r.height }; })()`);
  const clip = {
    x: rect.left + (60 / 1200) * rect.width,
    y: rect.top + (240 / 650) * rect.height,
    width: (180 / 1200) * rect.width,
    height: (120 / 650) * rect.height,
    scale: 1,
  };
  const shot = await call('Page.captureScreenshot', { format: 'png', clip });
  return shot.data;
}

await call('Page.navigate', { url: APP });
await waitFor("document.readyState === 'complete'", 'page load');
await evaluate("document.getElementById('single-player').click()");
await waitFor("!document.getElementById('battle').classList.contains('hidden')", 'battle visible');
await new Promise((resolve) => setTimeout(resolve, 2200));
console.log('PASS single-player battle ready');

// Continuous aim: hold 'w' and the angle display must keep climbing.
const angle0 = await evaluate("parseInt(document.getElementById('angle-display').textContent, 10) || 0");
await key('keyDown', 'KeyW', 'w', 87);
await new Promise((resolve) => setTimeout(resolve, 600));
await key('keyUp', 'KeyW', 'w', 87);
const angleAfter = await evaluate("parseInt(document.getElementById('angle-display').textContent, 10) || 0");
console.log(`PASS aim held 600ms: angle ${angle0} -> ${angleAfter}`);
if (angleAfter - angle0 < 40) fail(`aim did not keep rotating while held: ${angle0} -> ${angleAfter}`);

// Continuous movement: the tank band on the canvas must change while 'd' is held.
const before = await tankBandShot();
await key('keyDown', 'KeyD', 'd', 68);
await new Promise((resolve) => setTimeout(resolve, 800));
await key('keyUp', 'KeyD', 'd', 68);
const after = await tankBandShot();
if (before === after) fail('tank did not move while d was held');
console.log('PASS tank moved continuously while d was held (canvas band changed)');

// Move cap: keep holding until moved reaches 80, then holding again must not move.
await key('keyDown', 'KeyD', 'd', 68);
await new Promise((resolve) => setTimeout(resolve, 3000));
await key('keyUp', 'KeyD', 'd', 68);
const capped = await tankBandShot();
await key('keyDown', 'KeyD', 'd', 68);
await new Promise((resolve) => setTimeout(resolve, 600));
await key('keyUp', 'KeyD', 'd', 68);
const afterCap = await tankBandShot();
if (capped !== afterCap) fail('tank kept moving after the 80-unit cap');
console.log('PASS move cap at 80 units enforced');

ws.close();
console.log('BROWSER_KEY_INPUT_OK');
