// Real-browser reproduction of the winnerUsername bug: two tabs match, the
// loser refreshes (F5) mid-battle, then the battle ends and BOTH result pages
// must show the winner username (not tank_1).
const CDP_HTTP = 'http://127.0.0.1:9222';
const APP = 'http://127.0.0.1:8889/';
const BASE = 'http://127.0.0.1:8889';
const suffix = Date.now().toString(36);
const users = [
  { username: `bw_one_${suffix}`, password: 'password123' },
  { username: `bw_two_${suffix}`, password: 'password123' },
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

async function authUser(user) {
  let res = await fetch(`${BASE}/api/v1/auth/register`, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(user) });
  if (res.status === 409) {
    res = await fetch(`${BASE}/api/v1/auth/login`, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(user) });
  }
  if (!res.ok) fail(`auth ${user.username}: ${res.status}`);
  return res.json();
}

async function newTab() {
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
  return { ws, evaluate, call };
}

async function waitFor(tab, expression, label, timeoutMs = 10000) {
  const deadline = Date.now() + timeoutMs;
  for (;;) {
    const value = await tab.evaluate(expression);
    if (value) return;
    if (Date.now() > deadline) fail(`timed out waiting for ${label}`);
    await new Promise((resolve) => setTimeout(resolve, 200));
  }
}

const [one, two] = await Promise.all([authUser(users[0]), authUser(users[1])]);
const tabA = await newTab();
const tabB = await newTab();

async function prepareTab(tab, token) {
  await tab.call('Page.navigate', { url: APP });
  await waitFor(tab, "document.readyState === 'complete'", 'page load');
  await tab.evaluate(`sessionStorage.setItem('token', ${JSON.stringify(token)}); location.reload();`);
  await waitFor(tab, "!document.getElementById('lobby').classList.contains('hidden')", 'lobby');
}

await Promise.all([prepareTab(tabA, one.access_token), prepareTab(tabB, two.access_token)]);
console.log('PASS both tabs logged in to lobby');

await tabA.evaluate("document.querySelector('[data-count=\"2\"]').click()");
await tabB.evaluate("document.querySelector('[data-count=\"2\"]').click()");
await Promise.all([
  waitFor(tabA, "!document.getElementById('battle').classList.contains('hidden')", 'battle A'),
  waitFor(tabB, "!document.getElementById('battle').classList.contains('hidden')", 'battle B'),
]);
console.log('PASS both tabs entered battle');

// The loser (tab B) refreshes mid-battle and must recover via snapshot.
await tabB.call('Page.reload');
await waitFor(tabB, "!document.getElementById('battle').classList.contains('hidden')", 'battle B after F5', 15000);
await new Promise((resolve) => setTimeout(resolve, 2500));
console.log('PASS tab B re-entered battle after F5');

// Winner (tab A) leaves, ending the battle; both must show the winner username.
await tabA.evaluate("document.getElementById('leave-battle').click()");
await Promise.all([
  waitFor(tabA, "!document.getElementById('result').classList.contains('hidden')", 'result A'),
  waitFor(tabB, "!document.getElementById('result').classList.contains('hidden')", 'result B'),
]);
const titleA = await tabA.evaluate("document.getElementById('result-title').textContent");
const titleB = await tabB.evaluate("document.getElementById('result-title').textContent");
const expected = `Winner: ${two.user.username}`;
console.log(`PASS result titles: A="${titleA}" B="${titleB}"`);
if (titleA !== expected || titleB !== expected) {
  fail(`expected "${expected}" on both, got A="${titleA}" B="${titleB}"`);
}

tabA.ws.close();
tabB.ws.close();
console.log('BROWSER_WINNER_SMOKE_OK');
