// Real-browser smoke for 解决方案.md: when battle.intro_complete is missed or
// delayed (simulated by swallowing the event), the client must self-heal and
// unlock the first turn ~1.5s after the intro window instead of waiting for
// the 30s timeout / next state event.
const CDP_HTTP = 'http://127.0.0.1:9222';
const APP = 'http://127.0.0.1:8889/';
const BASE = 'http://127.0.0.1:8889';
const suffix = Date.now().toString(36);
const users = [
  { username: `heal_one_${suffix}`, password: 'password123' },
  { username: `heal_two_${suffix}`, password: 'password123' },
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

async function authUser(user) {
  let res = await fetch(`${BASE}/api/v1/auth/register`, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(user) });
  if (res.status === 409) {
    res = await fetch(`${BASE}/api/v1/auth/login`, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(user) });
  }
  if (!res.ok) fail(`auth ${user.username}: ${res.status}`);
  return res.json();
}

async function prepareOnline(tab, token) {
  await tab.call('Page.navigate', { url: APP });
  await waitFor(tab, "document.readyState === 'complete'", 'page load');
  await tab.evaluate(`sessionStorage.setItem('token', ${JSON.stringify(token)}); location.reload();`);
  await waitFor(tab, "!document.getElementById('lobby').classList.contains('hidden')", 'lobby');
}

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

// Swallow battle.intro_complete so the client never applies the intro-end event.
const swallowIntro = `
  (() => {
    const NativeWS = window.WebSocket;
    window.WebSocket = class extends NativeWS {
      constructor(url, protocols) {
        super(url, protocols);
        this.addEventListener('message', (event) => {
          try {
            const parsed = JSON.parse(event.data);
            if (parsed.type === 'battle.intro_complete') event.stopImmediatePropagation();
          } catch (_) {}
        }, true);
      }
    };
  })();
`;

const [one, two] = await Promise.all([authUser(users[0]), authUser(users[1])]);
const healTab = await newTab(swallowIntro); // joins first -> tank_1, first turn
const normalTab = await newTab();
await Promise.all([prepareOnline(healTab, one.access_token), prepareOnline(normalTab, two.access_token)]);

await healTab.evaluate("document.querySelector('[data-count=\"2\"]').click()");
await new Promise((resolve) => setTimeout(resolve, 300));
await normalTab.evaluate("document.querySelector('[data-count=\"2\"]').click()");
await Promise.all([
  waitFor(healTab, "!document.getElementById('battle').classList.contains('hidden')", 'heal tab battle'),
  waitFor(normalTab, "!document.getElementById('battle').classList.contains('hidden')", 'normal tab battle'),
]);
console.log('PASS both tabs entered battle (intro_complete swallowed on tank_1 tab)');

// The first turn must unlock shortly after the 1.5s intro window, without the
// intro_complete event: hold 'd' and the tank must move.
await new Promise((resolve) => setTimeout(resolve, 2200));
const before = await tankBandShot(healTab);
await healTab.call('Input.dispatchKeyEvent', { type: 'keyDown', code: 'KeyD', key: 'd', windowsVirtualKeyCode: 68, nativeVirtualKeyCode: 68 });
await new Promise((resolve) => setTimeout(resolve, 600));
await healTab.call('Input.dispatchKeyEvent', { type: 'keyUp', code: 'KeyD', key: 'd', windowsVirtualKeyCode: 68, nativeVirtualKeyCode: 68 });
const after = await tankBandShot(healTab);
if (before === after) fail('tank_1 first turn stayed locked without intro_complete');
console.log('PASS tank_1 first turn unlocked via self-heal (no intro_complete event)');

healTab.ws.close();
normalTab.ws.close();
console.log('BROWSER_INTRO_SELFHEAL_OK');
