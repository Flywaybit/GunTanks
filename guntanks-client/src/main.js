import { request } from './api.js';
import { CONNECTION, PAGE, clearBattleState, clearInputState, setLeftBattle, setStoredToken, store } from './store.js';
import { goTo, transition } from './stateMachine.js';
import { createViewManager } from './viewManager.js';
import { createMatchController } from './matchController.js';
import { createInputController } from './inputController.js';
import { GameSocket } from './socket.js';
import { isTerrainReady, loadTerrainSnapshot, render, playShot } from './renderer.js';

const $ = (id) => document.getElementById(id);
const views = createViewManager();
let serverOffset = 0;
let syncing = false;
let lastBattleEvent = 0;
let leaveRequested = false;
let shotAnimating = false;
let animationGeneration = 0;
let localMode = false;
let localTimer = 0;

function status(text) { views.setText('page-status', text || ''); }
function page(page) { if (page !== PAGE.BATTLE && shotAnimating) animationGeneration += 1; goTo(page); views.show(page); clearInputState(); const fill = $('power-fill'); if (fill) fill.style.width = '0%'; views.setText('power-value', '0%'); }
function battleState(payload) { return payload?.state || payload; }
function newerState(candidate, current) {
  if (!candidate) return current;
  if (!current || candidate.revision > current.revision || (candidate.revision === current.revision && candidate.event_seq > current.event_seq)) return candidate;
  return current;
}
function deferBattleState(payload) { store.pendingBattle = newerState(battleState(payload), store.pendingBattle); }
function finalizeBattle(result) {
  store.result = result || store.battle || {};
  store.battle = null;
  store.pendingBattle = null;
  store.reconnect = null;
  syncing = false;
  leaveRequested = false;
  setLeftBattle(false);
  lastBattleEvent = 0;
  shotAnimating = false;
  animationGeneration += 1;
  clearInputState();
  page(PAGE.RESULT);
  views.setText('result-title', store.result.winner_tank_id ? `Winner: ${store.result.winner_tank_id}` : 'Battle complete');
}

const socket = new GameSocket(handleEvent, (connection) => {
  store.connection = connection;
  if (connection === CONNECTION.STOPPING) {
    clearInputState();
    if (store.page !== PAGE.AUTH && store.page !== PAGE.RESULT) page(PAGE.RECONNECTING);
    views.setText('reconnect-status', 'Server is shutting down. Please reconnect shortly.');
    status('Server is shutting down. Please reconnect shortly.');
  } else if (connection === CONNECTION.RECONNECTING && store.page !== PAGE.AUTH) {
    if (leaveRequested || store.page === PAGE.LEAVING_BATTLE || store.page === PAGE.RESULT) return;
    const battleActive = [PAGE.BATTLE, PAGE.BATTLE_LOADING, PAGE.MATCH_FOUND].includes(store.page) || !!store.battle;
    if (battleActive) {
      store.reconnectUntil = Date.now() + 60000;
      page(PAGE.RECONNECTING);
      status('Connection lost. Reconnecting...');
      matchController.onReconnect();
    } else {
      status('Connection lost. Reconnecting...');
    }
  } else if (connection === CONNECTION.OPEN && store.page === PAGE.RECONNECTING && !store.battle && !store.match) {
    page(PAGE.LOBBY);
    status('');
  }
});

function setBattle(payload) {
  const state = battleState(payload);
  if (!state?.battle_id) return;
  if (store.battle?.battle_id && store.battle.battle_id !== state.battle_id) animationGeneration += 1;
  const players = payload?.players || payload?.state?.players;
  if (players?.length) {
    const playersByTankId = Object.fromEntries(players.filter((p) => p.tank_id).map((p) => [p.tank_id, p]));
    const player = players.find((item) => item.user_id === store.user?.id);
    store.battleIdentity = { battleId: state.battle_id, myTankId: player?.tank_id || store.battleIdentity?.myTankId, playersByTankId };
  }
  if (store.battleIdentity?.battleId === state.battle_id && store.battleIdentity.myTankId) state.my_tank_id = store.battleIdentity.myTankId;
  store.battle = state;
  syncing = false;
  lastBattleEvent = Math.max(lastBattleEvent, state.event_seq || 0);
  if (store.page !== PAGE.BATTLE && store.page !== PAGE.RECONNECTING) page(PAGE.BATTLE);
  render($('stage'), state);
  if (store.page === PAGE.RECONNECTING && isTerrainReady()) { syncing = false; page(PAGE.BATTLE); }
  if (!localMode && state.battle_id && !isTerrainReady()) { syncing = true; loadTerrainSnapshot(state.battle_id, store.token).then((ok) => { if (ok) { syncing = false; render($('stage'), state); if (store.page === PAGE.RECONNECTING) page(PAGE.BATTLE); } else { status('Unable to load terrain snapshot. Battle input is locked.'); } }); }
  
  const currentTank = state.tanks?.find((tank) => tank.id === state.current_tank_id);
  if (currentTank) {
    views.setText('angle-display', `${Math.round(currentTank.angle)}°`);
    document.querySelectorAll('[data-weapon]').forEach((node) => {
      const selected = node.dataset.weapon === currentTank.weapon;
      const unavailable = node.dataset.weapon === 'ss' && currentTank.ss_cooldown > 0;
      node.classList.toggle('selected', selected); node.classList.toggle('cooldown', unavailable); node.setAttribute('aria-disabled', unavailable ? 'true' : 'false');
    });
  }
  views.setText('wind-text', `${state.wind?.speed ?? 0}`);
  if (state.turn_deadline_ms) views.setText('main-timer', `${Math.max(0, Math.min(30, Math.ceil((state.turn_deadline_ms - Date.now() - serverOffset) / 1000)))}`);
  const queue = $('turn-queue');
  if (queue) {
    queue.replaceChildren(...(state.tanks || []).filter((tank) => tank.alive).map((tank) => {
      const item = document.createElement('li');
      item.textContent = tank.id === state.current_tank_id ? `${tank.id} *` : tank.id;
      return item;
    }));
  }
}

function sendBattleCommand(type, payload = {}) {
  if (localMode) return handleLocalCommand(type, payload);
  if (store.page !== PAGE.BATTLE || syncing || store.input.actionLocked || !store.battle?.battle_id) return false;
  store.input.actionLocked = type === 'battle.fire';
  return socket.send(type, payload, { battle_id: store.battle.battle_id, revision: store.battle.revision });
}

function startSinglePlayer() {
  localMode = true;
  store.battleIdentity = { battleId: 'local', myTankId: 'tank_1', playersByTankId: Object.fromEntries([1, 2, 3, 4].map((i) => [`tank_${i}`, { user_id: `local-${i}`, tank_id: `tank_${i}`, username: `Player ${i}` }])) };
  const state = { battle_id: 'local', phase: 'playing', round: 1, turn_index: 0, current_tank_id: 'tank_1', turn_deadline_ms: Date.now() + 30000, wind: { speed: 0, direction: 0 }, tanks: [1, 2, 3, 4].map((i) => ({ id: `tank_${i}`, x: [100, 400, 700, 1070][i - 1], y: 300, angle: 0, health: 1000, alive: true, weapon: 'shell1', ss_cooldown: 0, moved: 0, facing: 'right' })), revision: 0, event_seq: 0 };
  store.battle = state;
  setBattle(state);
  clearInterval(localTimer);
  localTimer = setInterval(() => { if (localMode && store.battle?.phase === 'playing' && Date.now() >= store.battle.turn_deadline_ms) advanceLocalTurn(); }, 250);
}
function advanceLocalTurn() {
  const state = store.battle; if (!state) return;
  let next = state.turn_index;
  for (let i = 0; i < state.tanks.length; i++) { next = (next + 1) % state.tanks.length; if (state.tanks[next].alive) break; }
  state.turn_index = next; state.current_tank_id = state.tanks[next].id; state.round += next === 0 ? 1 : 0; state.turn_deadline_ms = Date.now() + 30000; state.revision++; state.event_seq++; state.tanks[next].moved = 0; setBattle(state);
}
function handleLocalCommand(type, payload) {
  const state = store.battle; const tank = state?.tanks?.find((item) => item.id === state.current_tank_id); if (!tank || state.phase !== 'playing') return false;
  if (type === 'battle.move_start' && tank.moved < 80) { tank.x = Math.max(0, Math.min(1175, tank.x + (payload.direction === 'left' ? -1.5 : 1.5))); tank.facing = payload.direction === 'left' ? 'left' : 'right'; tank.moved += 1; state.revision++; setBattle(state); return true; }
  if (type === 'battle.aim_start') { tank.angle = (tank.angle + (payload.direction === 'up' ? 2 : -2) + 360) % 360; state.revision++; setBattle(state); return true; }
  if (type === 'battle.select_weapon') { if (!(payload.weapon === 'ss' && tank.ss_cooldown > 0)) tank.weapon = payload.weapon; state.revision++; setBattle(state); return true; }
  if (type === 'battle.fire') { const target = state.tanks.find((item) => item.alive && item.id !== tank.id); if (target) { const damage = tank.weapon === 'ss' ? 350 : tank.weapon === 'shell2' ? 240 : 130; target.health = Math.max(0, target.health - damage); target.alive = target.health > 0; } if (tank.weapon === 'ss') { tank.ss_cooldown = 3; tank.weapon = 'shell1'; } advanceLocalTurn(); return true; }
  if (type === 'battle.leave') { localMode = false; clearInterval(localTimer); clearBattleState(); store.result = null; page(PAGE.LOBBY); return true; }
  return true;
}

function handleEvent(event) {
  if (event.type === 'hello') {
    if (event.payload?.user) store.user = event.payload.user;
    serverOffset = (event.payload?.server_time_ms || Date.now()) - Date.now();
    if (store.page === PAGE.RECONNECTING) socket.send('battle.resync', { last_event_seq: lastBattleEvent }, { battle_id: store.battle?.battle_id });
    return;
  }
  if (event.type === 'reconnect.accepted') {
    if (store.leftBattle && !store.battle) return;
    syncing = true;
    status('Synchronizing battle...');
    socket.send('battle.resync', { last_event_seq: lastBattleEvent }, { battle_id: event.battle_id });
    return;
  }
  if (event.type === 'match.waiting' || event.type === 'match.cancelled' || event.type === 'match.failed' || event.type === 'battle.started') {
    matchController.handleEvent(event);
    return;
  }
  if (event.type === 'battle.snapshot' || event.type === 'battle.tank_state' || event.type === 'battle.turn_changed' || event.type === 'battle.player_eliminated') {
    if (shotAnimating) {
      deferBattleState(event.payload);
      if (event.event_seq) lastBattleEvent = Math.max(lastBattleEvent, event.event_seq);
      if (event.type === 'battle.snapshot' && event.battle_id) socket.send('battle.resync_ack', {}, { battle_id: event.battle_id });
      return;
    }
    if (event.type === 'battle.turn_changed') { clearInputState(); $('power-fill').style.width = '0%'; views.setText('power-value', '0%'); }
    setBattle(event.payload);
    store.input.actionLocked = false;
    if (event.event_seq) lastBattleEvent = Math.max(lastBattleEvent, event.event_seq);
    if (event.type === 'battle.snapshot' && event.battle_id) socket.send('battle.resync_ack', {}, { battle_id: event.battle_id });
    if (event.payload?.phase === 'finished') {
      finalizeBattle(event.payload);
    }
    return;
  }
  if (event.type === 'battle.shot_resolved') {
    if (event.event_seq) lastBattleEvent = Math.max(lastBattleEvent, event.event_seq);
    const shot = event.payload?.shot;
    const state = event.payload?.state;
    if (!shot || !state) return;
    views.setText('page-status', `${shot.impact?.kind || 'miss'} / ${shot.damage || 0} damage`);
    shotAnimating = true;
    const generation = ++animationGeneration;
    const battleID = state.battle_id;
    deferBattleState(state);
    playShot($('stage'), shot, () => {
      if (generation !== animationGeneration || store.page === PAGE.RESULT || store.battle?.battle_id !== battleID) return;
      shotAnimating = false;
      const authoritative = newerState(store.pendingBattle, state);
      store.pendingBattle = null;
      setBattle(authoritative);
      if (authoritative.phase === 'finished') finalizeBattle(authoritative);
      else store.input.actionLocked = false;
    });
    return;
  }
  if (event.type === 'battle.finished' || event.type === 'battle.result') {
    if (shotAnimating) { deferBattleState(event.payload); return; }
    finalizeBattle(battleState(event.payload));
    return;
  }
  if (event.type === 'error') {
    store.input.actionLocked = false;
    const code = event.payload?.code;
    if (code === 'SESSION_EXPIRED') {
      setStoredToken(null); socket.close(); clearBattleState(); page(PAGE.AUTH); views.setText('auth-status', 'Session expired. Please log in again.'); return;
    }
    if (code === 'BATTLE_ALREADY_STARTED' || code === 'MATCH_REQUEST_MISMATCH') {
      matchController.handleEvent(event);
      return;
    }
    if (code === 'STALE_REVISION' && store.battle?.battle_id) {
      syncing = true;
      store.input.actionLocked = true;
      socket.send('battle.resync', { last_event_seq: lastBattleEvent }, { battle_id: store.battle.battle_id });
      return;
    }
    if (store.page === PAGE.RECONNECTING && code === 'INVALID_STATE') {
      clearBattleState();
      page(store.result ? PAGE.RESULT : PAGE.LOBBY);
      status('Battle is no longer active.');
      return;
    }
    if ([PAGE.MATCHING, PAGE.MATCH_CANCELING].includes(store.page) || code === 'BATTLE_CREATE_FAILED' || code === 'MATCH_JOIN_FAILED') {
      matchController.handleEvent({ type: 'match.failed', payload: event.payload });
      return;
    }
    if ([PAGE.BATTLE, PAGE.BATTLE_LOADING].includes(store.page)) {
      status(event.payload?.message || 'Battle request failed.');
      return;
    }
    if (store.page === PAGE.LEAVING_BATTLE) {
      leaveRequested = false;
      setLeftBattle(false);
      page(PAGE.BATTLE);
      status(event.payload?.message || 'Leave failed.');
      return;
    }
    if (store.page === PAGE.AUTH) {
      views.setText('auth-status', event.payload?.message || 'Request failed');
      return;
    }
    status(event.payload?.message || 'Request failed');
  }
}

const matchController = createMatchController({
  socket,
  getPage: () => store.page,
  onPageChange: (p) => page(p),
  onStatus: status,
  onMatchWaiting: (payload) => views.setText('matching-count', payload?.player_count || store.match?.playerCount || ''),
  onMatchCleared: () => views.setText('matching-count', ''),
  onCancelRetry: () => { $('cancel-match').disabled = false; },
  onBattleStart: (payload) => {
    page(PAGE.MATCH_FOUND);
    views.setText('page-status', 'Match found.');
    setTimeout(() => {
      if (store.page !== PAGE.MATCH_FOUND) return;
      page(PAGE.BATTLE_LOADING);
      views.setText('loading-status', 'Battle found. Loading arena...');
      if (payload?.state || payload?.tanks) {
        setTimeout(() => { if (store.page === PAGE.BATTLE_LOADING) setBattle(payload); }, 100);
      } else if (payload?.battle_id) {
        socket.send('battle.resync', { last_event_seq: 0 }, { battle_id: payload.battle_id });
      } else {
        matchController.handleEvent({ type: 'match.failed', payload: { code: 'BATTLE_CREATE_FAILED', message: 'Battle state unavailable.' } });
      }
    }, 250);
  },
});

createInputController({
  getPage: () => store.page,
  getBattle: () => store.battle,
  isSyncing: () => syncing,
  sendBattleCommand,
  setPower: (value) => { $('power-fill').style.width = `${value}%`; views.setText('power-value', `${value}%`); },
  getPower: () => store.input.chargeValue,
  onStatus: status,
});

let authBusy = false;
async function auth(path) {
  if (authBusy) return;
  const username = $('username').value.trim();
  const password = $('password').value;
  if (!username) return views.setText('auth-status', 'Username is required.');
  if (!password.trim()) return views.setText('auth-status', 'Password is required.');
  authBusy = true;
  $('login').disabled = true;
  $('register').disabled = true;
  try {
    const data = await request(path, { method: 'POST', body: JSON.stringify({ username, password }) });
    setStoredToken(data.access_token); store.user = data.user; page(PAGE.LOBBY); socket.connect(store.token);
  } catch (error) { views.setText('auth-status', error.message); }
  finally {
    authBusy = false;
    $('login').disabled = false;
    $('register').disabled = false;
  }
}

$('login').onclick = () => auth('/auth/login');
$('single-player').onclick = () => startSinglePlayer();
$('register').onclick = () => auth('/auth/register');
$('logout').onclick = async () => {
  try { if (store.token) await request('/auth/logout', { method: 'POST' }); } catch (_) { /* socket close still releases the lock */ }
  setStoredToken(null); socket.close(); store.user = null; page(PAGE.AUTH);
};
document.querySelectorAll('[data-count]').forEach((button) => {
  button.onclick = () => {
    $('cancel-match').disabled = false;
    matchController.setMatch(Number(button.dataset.count));
  };
});
$('cancel-match').onclick = () => {
  if (matchController.cancelMatch()) $('cancel-match').disabled = true;
};
document.querySelectorAll('[data-weapon]').forEach((node) => {
  const select = () => sendBattleCommand('battle.select_weapon', { weapon: node.dataset.weapon });
  node.addEventListener('click', select);
  node.addEventListener('keydown', (event) => { if (event.key === 'Enter' || event.key === ' ') { event.preventDefault(); select(); } });
});
$('leave-battle').onclick = () => {
  if (leaveRequested || store.page !== PAGE.BATTLE) return;
  leaveRequested = sendBattleCommand('battle.leave');
  if (leaveRequested) { setLeftBattle(true); page(PAGE.LEAVING_BATTLE); views.setText('page-status', 'Leaving battle...'); }
};
$('back-lobby').onclick = () => { clearBattleState(); store.result = null; setLeftBattle(false); page(PAGE.LOBBY); };

const battleShell = $('battle');
new ResizeObserver(([entry]) => {
  battleShell.style.setProperty('--battle-scale', `${entry.contentRect.width / 1200}`);
}).observe(battleShell);

setInterval(() => {
  const deadline = store.battle?.turn_deadline_ms;
  if (store.page === PAGE.BATTLE && deadline) views.setText('main-timer', `${Math.max(0, Math.min(30, Math.ceil((deadline - Date.now() - serverOffset) / 1000)))}`);
}, 250);

setInterval(() => {
  if (store.page === PAGE.MATCHING || store.page === PAGE.MATCH_CANCELING) {
    const elapsed = store.match ? Math.max(0, Math.floor((Date.now() - store.match.startedAt) / 1000)) : 0;
    views.setText('matching-elapsed', `${elapsed}s`);
  }
}, 500);

setInterval(() => {
  if (store.page === PAGE.RECONNECTING && store.reconnectUntil) {
    const seconds = Math.max(0, Math.ceil((store.reconnectUntil - Date.now()) / 1000));
    views.setText('reconnect-status', `Waiting for opponent reconnect (${seconds}s)`);
    if (seconds === 0 && store.battle) { clearBattleState(); page(PAGE.RESULT); views.setText('result-title', 'Battle ended'); }
  }
}, 250);

if (store.token) {
  request('/me').then((me) => { store.user = me.user; page(PAGE.LOBBY); socket.connect(store.token); }).catch(() => { setStoredToken(null); page(PAGE.AUTH); });
} else page(PAGE.AUTH);
