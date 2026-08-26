// Real-browser smoke test for the single-player fixes: clean start, intro drop,
// fire + turn advance (no stuck), Leave returns to lobby immediately, and a
// second match starts fresh.
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

async function waitFor(expression, label, timeoutMs = 10000) {
  const deadline = Date.now() + timeoutMs;
  for (;;) {
    if (await evaluate(expression)) return;
    if (Date.now() > deadline) fail(`timed out waiting for ${label}`);
    await new Promise((resolve) => setTimeout(resolve, 200));
  }
}

async function startGame() {
  await evaluate("document.getElementById('single-player').click()");
  await waitFor("!document.getElementById('battle').classList.contains('hidden')", 'battle visible');
  await new Promise((resolve) => setTimeout(resolve, 2200));
}

await call('Page.navigate', { url: APP });
await waitFor("document.readyState === 'complete'", 'page load');

// First match: intro drop completes, timer shows.
await startGame();
const firstState = await evaluate(`({
  introHidden: document.getElementById('intro-status').classList.contains('hidden'),
  timer: document.getElementById('main-timer').textContent,
  queue: document.getElementById('turn-queue').textContent
})`);
console.log(`PASS first match state: ${JSON.stringify(firstState)}`);
if (!firstState.introHidden) fail('intro-status still visible after drop');
if (!/Player 1 \*/.test(firstState.queue)) fail(`current turn should be Player 1: ${firstState.queue}`);

// Fire with ~50% power: hold space, release, wait for the turn to advance.
await call('Input.dispatchKeyEvent', { type: 'keyDown', code: 'Space', key: ' ', windowsVirtualKeyCode: 32, nativeVirtualKeyCode: 32 });
await new Promise((resolve) => setTimeout(resolve, 450));
await call('Input.dispatchKeyEvent', { type: 'keyUp', code: 'Space', key: ' ', windowsVirtualKeyCode: 32, nativeVirtualKeyCode: 32 });
await waitFor("document.getElementById('turn-queue').textContent.includes('Player 2 *')", 'turn advanced to Player 2', 10000);
console.log('PASS fired and turn advanced to Player 2 (no stuck)');

// Leave returns to the lobby immediately (no LEAVING_BATTLE wait).
await evaluate("document.getElementById('leave-battle').click()");
await waitFor("!document.getElementById('lobby').classList.contains('hidden')", 'lobby after leave', 5000);
const leavingHidden = await evaluate("document.getElementById('battle').classList.contains('hidden')");
if (!leavingHidden) fail('battle still visible after leave');
console.log('PASS Leave returned to lobby immediately');

// Second match starts fresh: intro again, current turn Player 1.
await startGame();
const secondQueue = await evaluate("document.getElementById('turn-queue').textContent");
if (!/Player 1 \*/.test(secondQueue)) fail(`second match current turn wrong: ${secondQueue}`);
console.log(`PASS second match started fresh: ${JSON.stringify(secondQueue)}`);

ws.close();
console.log('BROWSER_SINGLE_PLAYER_FIX_OK');
