// WebSocket smoke test for the intro drop / wind / username demand.
// Simulates two clients: match, reject commands during intro, receive
// battle.intro_complete, verify snapshot during intro, then finish the battle.
const BASE = 'http://127.0.0.1:8889';
const WS = 'ws://127.0.0.1:8889/ws';
const suffix = Date.now().toString(36);
const users = [
  { username: `ws_one_${suffix}`, password: 'password123' },
  { username: `ws_two_${suffix}`, password: 'password123' },
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
  const data = await res.json();
  return data;
}

function connect(token) {
  const debugName = token.slice(-6);
  return new Promise((resolve, reject) => {
    const ws = new WebSocket(`${WS}?token=${encodeURIComponent(token)}`);
    ws.debugName = debugName;
    const queue = [];
    const waiters = [];
    ws.onopen = () => resolve(ws);
    ws.onerror = () => reject(new Error('ws error'));
    ws.onmessage = (event) => {
      const parsed = JSON.parse(event.data);
      if (process.env.WS_DEBUG) console.log(`[${ws.debugName}] ${parsed.type}`);
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

const [one, two] = await Promise.all([authUser(users[0]), authUser(users[1])]);
const ws1 = await connect(one.access_token);
const ws2 = await connect(two.access_token);
await ws1.next('hello');
await ws2.next('hello');

ws1.sendJson('match.join', { player_count: 2, match_request_id: 'smoke-req-1' }, { request_id: 'smoke-req-1' });
ws2.sendJson('match.join', { player_count: 2, match_request_id: 'smoke-req-2' }, { request_id: 'smoke-req-2' });

const started1 = await ws1.next('battle.started', 5000);
const started2 = await ws2.next('battle.started', 5000);
const battleId = started1.battle_id;
if (started2.battle_id !== battleId) fail('clients received different battles');
const payload = started1.payload;
const state = payload.state;
if (!state.intro_end_ms || state.intro_end_ms <= Date.now()) fail(`intro_end_ms missing/past: ${state.intro_end_ms}`);
if (state.turn_deadline_ms !== state.intro_end_ms + 30000) fail(`turn_deadline_ms=${state.turn_deadline_ms} intro_end_ms=${state.intro_end_ms}`);
if (state.phase !== 'playing') fail(`phase=${state.phase}`);
for (const tank of state.tanks) {
  if (tank.y !== -200) fail(`tank ${tank.id} spawn y=${tank.y}, want -200`);
  if (!tank.land_y) fail(`tank ${tank.id} missing land_y`);
}
if (state.wind.speed < 0 || state.wind.speed > 25 || state.wind.direction < 0 || state.wind.direction > 359) fail(`bad wind: ${JSON.stringify(state.wind)}`);
if (!payload.players?.every((p) => p.username && p.tank_id)) fail('battle.started players missing username/tank_id');
console.log(`PASS battle.started: intro_end_ms=${state.intro_end_ms} deadline_delta=${state.turn_deadline_ms - state.intro_end_ms}ms tanks_y=${state.tanks.map((t) => t.y).join(',')} land_y=${state.tanks.map((t) => t.land_y).join(',')} wind=${state.wind.speed}/${state.wind.direction}`);

// Request a snapshot during intro and verify it carries the intro fields.
ws1.sendJson('battle.resync', { last_event_seq: 0 }, { battle_id: battleId, request_id: 'smoke-resync-1' });
const snapshot = await ws1.next('battle.snapshot', 5000);
if (snapshot.payload.state.intro_end_ms !== state.intro_end_ms) fail('snapshot lost intro_end_ms');
if (snapshot.payload.state.tanks[0].land_y !== state.tanks[0].land_y) fail('snapshot lost land_y');
ws1.sendJson('battle.resync_ack', {}, { battle_id: battleId, request_id: 'smoke-resync-ack-1' });
console.log('PASS battle.snapshot during intro carries intro fields');

// Commands must be rejected while the intro is in progress.
ws1.sendJson('battle.fire', { power: 50 }, { battle_id: battleId, revision: state.revision, request_id: 'smoke-fire-1' });
let rejected = false;
for (let i = 0; i < 8; i += 1) {
  const event = await ws1.next('error', 3000).catch(() => null);
  if (event && event.payload?.code === 'INTRO_IN_PROGRESS') { rejected = true; break; }
}
if (!rejected) fail('battle.fire was not rejected during intro');
console.log('PASS intro rejects battle.fire with INTRO_IN_PROGRESS');

// The intro completes with tanks landed and the 30s timer armed.
const done1 = await ws1.next('battle.intro_complete', 5000);
await ws2.next('battle.intro_complete', 5000);
const landed = done1.payload;
for (const tank of landed.tanks) {
  if (tank.y !== tank.land_y) fail(`tank ${tank.id} y=${tank.y} != land_y=${tank.land_y}`);
}
if (landed.phase !== 'playing') fail(`post-intro phase=${landed.phase}`);
console.log(`PASS battle.intro_complete: tanks landed at ${landed.tanks.map((t) => t.y).join(',')}`);

// After landing, normal commands are accepted again.
ws1.sendJson('battle.aim_start', { direction: 'up' }, { battle_id: battleId, revision: landed.revision, request_id: 'smoke-aim-1' });
const stateEvent = await ws1.next('battle.tank_state', 3000);
if (stateEvent.payload.current_tank_id !== landed.current_tank_id) fail('aim_start changed current tank');
console.log('PASS aim_start accepted after landing');

// Leave: battle finishes for both clients.
ws1.sendJson('battle.leave', {}, { battle_id: battleId, revision: stateEvent.revision, request_id: 'smoke-leave-1' });
const finished1 = await ws1.next('battle.finished', 5000);
const finished2 = await ws2.next('battle.finished', 5000);
const finishedState1 = finished1.payload.state || finished1.payload;
const finishedState2 = finished2.payload.state || finished2.payload;
if (finishedState1.phase !== 'finished' || finishedState2.phase !== 'finished') fail('battle did not finish');
console.log(`PASS battle.finished winner_tank_id=${finishedState1.winner_tank_id}`);

ws1.close();
ws2.close();
console.log('SMOKE_OK');
