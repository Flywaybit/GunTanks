// WebSocket smoke test for the winnerUsername demand: battle.snapshot and
// battle.finished must both carry a players ARRAY (username/tank_id) so a
// refreshed/reconnected loser still renders the winner username.
const BASE = 'http://127.0.0.1:8889';
const WS = 'ws://127.0.0.1:8889/ws';
const suffix = Date.now().toString(36);
const users = [
  { username: `winner_one_${suffix}`, password: 'password123' },
  { username: `winner_two_${suffix}`, password: 'password123' },
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
  return new Promise((resolve, reject) => {
    const ws = new WebSocket(`${WS}?token=${encodeURIComponent(token)}`);
    const queue = [];
    const waiters = [];
    ws.onopen = () => resolve(ws);
    ws.onerror = () => reject(new Error('ws error'));
    ws.onmessage = (event) => {
      const parsed = JSON.parse(event.data);
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

function playersOf(payload) {
  return payload.players ?? payload?.state?.players;
}

function assertPlayersArray(players, label) {
  if (!Array.isArray(players)) fail(`${label} players is not an array: ${JSON.stringify(players)}`);
  if (players.length !== 2) fail(`${label} players length=${players.length}`);
  for (const p of players) {
    if (!p.username || !p.tank_id) fail(`${label} player missing username/tank_id: ${JSON.stringify(p)}`);
  }
}

const [one, two] = await Promise.all([authUser(users[0]), authUser(users[1])]);
const ws1 = await connect(one.access_token);
const ws2 = await connect(two.access_token);
await ws1.next('hello');
await ws2.next('hello');

ws1.sendJson('match.join', { player_count: 2, match_request_id: 'win-req-1' }, { request_id: 'win-req-1' });
ws2.sendJson('match.join', { player_count: 2, match_request_id: 'win-req-2' }, { request_id: 'win-req-2' });
const started = await ws1.next('battle.started', 5000);
await ws2.next('battle.started', 5000);
const battleId = started.battle_id;
assertPlayersArray(started.payload.players, 'battle.started');
console.log('PASS battle.started players is an array');

// Simulate the loser refreshing the page: request a snapshot mid-battle.
ws2.sendJson('battle.resync', { last_event_seq: 0 }, { battle_id: battleId, request_id: 'win-resync-1' });
const snapshot = await ws2.next('battle.snapshot', 5000);
assertPlayersArray(snapshot.payload.players, 'battle.snapshot(resync)');
const snapshotState = snapshot.payload.state;
if (!snapshotState || !snapshotState.battle_id) fail('battle.snapshot missing state');
ws2.sendJson('battle.resync_ack', {}, { battle_id: battleId, request_id: 'win-resync-ack-1' });
console.log('PASS battle.snapshot players is an array with username/tank_id');

// Play to the end (opponent leaves), then check battle.finished carries players.
await ws1.next('battle.intro_complete', 5000);
await ws2.next('battle.intro_complete', 5000);
ws1.sendJson('battle.leave', {}, { battle_id: battleId, revision: snapshotState.revision, request_id: 'win-leave-1' });
const finished = await ws1.next('battle.finished', 5000);
await ws2.next('battle.finished', 5000);
if (finished.payload.state) {
  assertPlayersArray(finished.payload.players, 'battle.finished');
  if (finished.payload.state.phase !== 'finished' || !finished.payload.state.winner_tank_id) {
    fail(`battle.finished state wrong: ${JSON.stringify(finished.payload.state)}`);
  }
  console.log(`PASS battle.finished wrapper: winner_tank_id=${finished.payload.state.winner_tank_id} players=[${finished.payload.players.map((p) => p.username).join(', ')}]`);
} else {
  fail(`battle.finished payload is not a wrapper: ${JSON.stringify(finished.payload)}`);
}

ws1.close();
ws2.close();
console.log('WINNER_SMOKE_OK');
