// WebSocket smoke test for the pause demand: disconnect pauses the battle,
// commands are rejected with BATTLE_PAUSED, reconnect resumes with an extended
// deadline, and the battle then finishes normally.
const BASE = 'http://127.0.0.1:8889';
const WS = 'ws://127.0.0.1:8889/ws';
const suffix = Date.now().toString(36);
const users = [
  { username: `pause_one_${suffix}`, password: 'password123' },
  { username: `pause_two_${suffix}`, password: 'password123' },
];

function fail(message) {
  console.error(`FAIL: ${message}`);
  process.exit(1);
}

async function authUser(user) {
  const body = JSON.stringify(user);
  let res = await fetch(`${BASE}/api/v1/auth/register`, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body });
  if (res.status === 409) {
    res = await fetch(`${BASE}/api/v1/auth/login`, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body });
  }
  if (!res.ok) fail(`auth ${user.username}: ${res.status} ${await res.text()}`);
  return res.json();
}

function connect(token) {
  const debugName = token.slice(-6);
  return new Promise((resolve, reject) => {
    const ws = new WebSocket(`${WS}?token=${encodeURIComponent(token)}`);
    const queue = [];
    const waiters = [];
    ws.debugName = debugName;
    ws.onopen = () => resolve(ws);
    ws.onerror = () => reject(new Error('ws error'));
    ws.onmessage = (event) => {
      const parsed = JSON.parse(event.data);
      if (process.env.WS_DEBUG) console.log(`[${ws.debugName}] ${parsed.type} rev=${parsed.revision} paused=${parsed.payload?.paused ?? parsed.payload?.state?.paused}`);
      if (parsed.type === 'pong') return;
      const waiterIndex = waiters.findIndex((item) => item.type === parsed.type);
      if (waiterIndex >= 0) {
        waiters.splice(waiterIndex, 1)[0].resolve(parsed);
      } else {
        queue.push(parsed);
      }
    };
    ws.next = (type, timeoutMs = 5000) => {
      const index = queue.findIndex((item) => item.type === type);
      if (index >= 0) return Promise.resolve(queue.splice(index, 1)[0]);
      return new Promise((resolve, reject) => {
        const timer = setTimeout(() => reject(new Error(`timeout waiting for ${type}`)), timeoutMs);
        waiters.push({ type, resolve: (ev) => { clearTimeout(timer); resolve(ev); } });
      });
    };
    ws.sendJson = (type, payload = {}, extra = {}) => ws.send(JSON.stringify({ type, payload, ...extra }));
  });
}

async function waitForStateFlag(ws, flag, value, timeoutMs = 5000) {
  const deadline = Date.now() + timeoutMs;
  for (;;) {
    const remaining = deadline - Date.now();
    if (remaining <= 0) fail(`timed out waiting for state.${flag}=${value}`);
    const ev = await ws.next('battle.tank_state', remaining);
    const state = ev.payload?.state || ev.payload;
    if (!!state?.[flag] === !!value) return ev;
  }
}

async function waitForRevisionGreater(ws, base, timeoutMs = 5000) {
  const deadline = Date.now() + timeoutMs;
  for (;;) {
    const remaining = deadline - Date.now();
    if (remaining <= 0) fail(`timed out waiting for revision > ${base}`);
    const ev = await ws.next('battle.tank_state', remaining);
    const state = ev.payload?.state || ev.payload;
    if (state.revision > base) return ev;
  }
}

const [one, two] = await Promise.all([authUser(users[0]), authUser(users[1])]);
const ws1 = await connect(one.access_token);
const ws2 = await connect(two.access_token);
await ws1.next('hello');
await ws2.next('hello');

ws1.sendJson('match.join', { player_count: 2, match_request_id: 'pause-req-1' }, { request_id: 'pause-req-1' });
ws2.sendJson('match.join', { player_count: 2, match_request_id: 'pause-req-2' }, { request_id: 'pause-req-2' });
const started = await ws1.next('battle.started', 5000);
await ws2.next('battle.started', 5000);
const battleId = started.battle_id;
await ws1.next('battle.intro_complete', 5000);
await ws2.next('battle.intro_complete', 5000);
console.log('PASS battle started and intro completed');

// u2 disconnects; u1 must observe paused=true.
ws2.close();
const pausedEvent = await waitForStateFlag(ws1, 'paused', true);
const pausedState = pausedEvent.payload?.state || pausedEvent.payload;
console.log(`PASS paused broadcast: paused=true deadline=${pausedState.turn_deadline_ms}`);

// Commands during pause are rejected with BATTLE_PAUSED.
ws1.sendJson('battle.fire', { power: 50 }, { battle_id: battleId, revision: pausedState.revision, request_id: 'pause-fire-1' });
let rejected = false;
for (let i = 0; i < 8; i += 1) {
  const event = await ws1.next('error', 3000).catch(() => null);
  if (event && event.payload?.code === 'BATTLE_PAUSED') { rejected = true; break; }
}
if (!rejected) fail('battle.fire was not rejected during pause');
console.log('PASS pause rejects battle.fire with BATTLE_PAUSED');

// u2 reconnects; u1 must observe paused=false with an extended deadline.
const ws2b = await connect(two.access_token);
await ws2b.next('hello');
const resumedEvent = await waitForStateFlag(ws1, 'paused', false);
const resumedState = resumedEvent.payload?.state || resumedEvent.payload;
if (resumedState.paused) fail('state still paused after reconnect');
if (!(resumedState.turn_deadline_ms > pausedState.turn_deadline_ms)) {
  fail(`deadline not extended: paused=${pausedState.turn_deadline_ms} resumed=${resumedState.turn_deadline_ms}`);
}
console.log(`PASS resume broadcast: paused=false deadline extended ${pausedState.turn_deadline_ms} -> ${resumedState.turn_deadline_ms}`);

// Normal play resumes: an aim command is accepted.
ws1.sendJson('battle.aim_start', { direction: 'up' }, { battle_id: battleId, revision: resumedState.revision, request_id: 'pause-aim-1' });
const aimEvent = await waitForRevisionGreater(ws1, resumedState.revision, 3000);
const aimState = aimEvent.payload?.state || aimEvent.payload;
if (aimState.revision <= resumedState.revision) fail('aim_start did not advance state after resume');
console.log('PASS aim_start accepted after resume');

// Finish via leave.
ws1.sendJson('battle.leave', {}, { battle_id: battleId, revision: aimState.revision, request_id: 'pause-leave-1' });
const finished1 = await ws1.next('battle.finished', 5000);
const finished2 = await ws2b.next('battle.finished', 5000);
const finishedState1 = finished1.payload.state || finished1.payload;
const finishedState2 = finished2.payload.state || finished2.payload;
if (finishedState1.phase !== 'finished' || finishedState2.phase !== 'finished') fail('battle did not finish');
console.log(`PASS battle.finished winner_tank_id=${finishedState1.winner_tank_id}`);

ws1.close();
ws2b.close();
console.log('PAUSE_SMOKE_OK');
