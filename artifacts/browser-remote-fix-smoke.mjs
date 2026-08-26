// Real-browser smoke test for the remote-access fixes:
//  A) single-player with a ~30s client clock skew must finish the 1.5s intro
//     and let player 1 act immediately (unified nowMs clock base);
//  B) a match opened over the LAN IP (insecure context, crypto.subtle
//     undefined) must load terrain, accept input and finish normally.
const CDP_HTTP = 'http://127.0.0.1:9222';
const APP = 'http://127.0.0.1:8889/';
const LAN = 'http://192.168.96.89:8889';
const suffix = Date.now().toString(36);
const users = [
  { username: `rm_one_${suffix}`, password: 'password123' },
  { username: `rm_two_${suffix}`, password: 'password123' },
];

function fail(message) {
  console.error(`FAIL: ${message}`);
  process.exit(1);
}

async function jsonFetch(url, options) {
  const res = await fetch(url, options);
  if (!res.ok) fail(`HTTP ${res.status} for ${url}: ${await res.text()}`);
  return res.json();
}

async function newTab(injection) {
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
  if (injection) await call('Page.addScriptToEvaluateOnNewDocument', { source: injection });
  return { ws, call, evaluate };
}

async function waitFor(tab, expression, label, timeoutMs = 10000) {
  const deadline = Date.now() + timeoutMs;
  for (;;) {
    if (await tab.evaluate(expression)) return;
    if (Date.now() > deadline) fail(`timed out waiting for ${label}`);
    await new Promise((resolve) => setTimeout(resolve, 200));
  }
}

const key = async (tab, type, code, keyName, vk) => {
  await tab.call('Input.dispatchKeyEvent', { type, code, key: keyName, windowsVirtualKeyCode: vk, nativeVirtualKeyCode: vk });
};

async function tankBandShot(tab) {
  const rect = await tab.evaluate(`(() => { const r = document.getElementById('stage').getBoundingClientRect(); return { left: r.left, top: r.top, width: r.width, height: r.height }; })()`);
  const clip = {
    x: rect.left + (60 / 1200) * rect.width,
    y: rect.top + (240 / 650) * rect.height,
    width: (180 / 1200) * rect.width,
    height: (120 / 650) * rect.height,
    scale: 1,
  };
  const shot = await tab.call('Page.captureScreenshot', { format: 'png', clip });
  return shot.data;
}

async function authUser(user) {
  let res = await fetch(`${LAN}/api/v1/auth/register`, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(user) });
  if (res.status === 409) {
    res = await fetch(`${LAN}/api/v1/auth/login`, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(user) });
  }
  if (!res.ok) fail(`auth ${user.username}: ${res.status}`);
  return res.json();
}

// ---- Part A: clock skew must not stretch the single-player intro ----
const tabA = await newTab(`(() => { const real = Date.now.bind(Date); Date.now = () => real() + 30000; })();`);
await tabA.call('Page.navigate', { url: APP });
await waitFor(tabA, "document.readyState === 'complete'", 'page load');
await tabA.evaluate("document.getElementById('single-player').click()");
const startedAt = Date.now();
await waitFor(tabA, "document.getElementById('intro-status').classList.contains('hidden')", 'intro complete', 6000);
const introMs = Date.now() - startedAt;
console.log(`PASS remote single-player intro completed in ${introMs}ms (clock +30s)`);
if (introMs > 4500) fail(`intro took too long with clock skew: ${introMs}ms`);
const before = await tankBandShot(tabA);
await key(tabA, 'keyDown', 'KeyD', 'd', 68);
await new Promise((resolve) => setTimeout(resolve, 600));
await key(tabA, 'keyUp', 'KeyD', 'd', 68);
const after = await tankBandShot(tabA);
if (before === after) fail('player 1 could not move right after landing');
console.log('PASS player 1 operable immediately after landing');

// ---- Part B: LAN IP (insecure context) online match ----
const [one, two] = await Promise.all([authUser(users[0]), authUser(users[1])]);
const tabB1 = await newTab();
const tabB2 = await newTab();
async function prepareOnline(tab, token) {
  await tab.call('Page.navigate', { url: LAN });
  await waitFor(tab, "document.readyState === 'complete'", 'lan page load');
  const secure = await tab.evaluate('({ secure: window.isSecureContext, subtle: !!window.crypto?.subtle })');
  console.log(`PASS LAN context probe: ${JSON.stringify(secure)}`);
  if (secure.secure || secure.subtle) fail('LAN origin unexpectedly secure');
  await tab.evaluate(`sessionStorage.setItem('token', ${JSON.stringify(token)}); location.reload();`);
  await waitFor(tab, "!document.getElementById('lobby').classList.contains('hidden')", 'lan lobby');
}
await Promise.all([prepareOnline(tabB1, one.access_token), prepareOnline(tabB2, two.access_token)]);
await tabB1.evaluate("document.querySelector('[data-count=\"2\"]').click()");
await tabB2.evaluate("document.querySelector('[data-count=\"2\"]').click()");
await Promise.all([
  waitFor(tabB1, "!document.getElementById('battle').classList.contains('hidden')", 'lan battle B1'),
  waitFor(tabB2, "!document.getElementById('battle').classList.contains('hidden')", 'lan battle B2'),
]);
await new Promise((resolve) => setTimeout(resolve, 2500));
const failedStatus = await tabB1.evaluate("document.getElementById('page-status').textContent");
if (failedStatus.includes('Terrain failed')) fail(`terrain failed to load on LAN: ${failedStatus}`);
console.log('PASS LAN terrain snapshot loaded (crypto.subtle skip path)');

const b1Before = await tankBandShot(tabB1);
await key(tabB1, 'keyDown', 'KeyD', 'd', 68);
await new Promise((resolve) => setTimeout(resolve, 600));
await key(tabB1, 'keyUp', 'KeyD', 'd', 68);
const b1After = await tankBandShot(tabB1);
if (b1Before === b1After) fail('LAN player input did not respond');
console.log('PASS LAN player input responds (not locked by syncing)');

await tabB1.evaluate("document.getElementById('leave-battle').click()");
await Promise.all([
  waitFor(tabB1, "!document.getElementById('result').classList.contains('hidden')", 'lan result B1'),
  waitFor(tabB2, "!document.getElementById('result').classList.contains('hidden')", 'lan result B2'),
]);
const titleA = await tabB1.evaluate("document.getElementById('result-title').textContent");
const titleB = await tabB2.evaluate("document.getElementById('result-title').textContent");
const expected = `Winner: ${two.user.username}`;
console.log(`PASS LAN result titles: A="${titleA}" B="${titleB}"`);
if (titleA !== expected || titleB !== expected) fail(`expected "${expected}" on both LAN tabs`);

tabA.ws.close();
tabB1.ws.close();
tabB2.ws.close();
console.log('BROWSER_REMOTE_FIX_OK');
