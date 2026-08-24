export const PAGE = Object.freeze({
  AUTH: 'AUTH',
  LOBBY: 'LOBBY',
  MATCHING: 'MATCHING',
  MATCH_FOUND: 'MATCH_FOUND',
  BATTLE_LOADING: 'BATTLE_LOADING',
  BATTLE: 'BATTLE',
  RESULT: 'RESULT',
  RECONNECTING: 'RECONNECTING',
  MATCH_CANCELING: 'MATCH_CANCELING',
  LEAVING_BATTLE: 'LEAVING_BATTLE',
});

export const CONNECTION = Object.freeze({
  DISCONNECTED: 'DISCONNECTED',
  CONNECTING: 'CONNECTING',
  OPEN: 'OPEN',
  RECONNECTING: 'RECONNECTING',
  CLOSED: 'CLOSED',
  STOPPING: 'STOPPING',
});

const readToken = () => {
  try {
    return sessionStorage.getItem('token');
  } catch (_) {
    return null;
  }
};
const readLeftBattle = () => {
  try { return sessionStorage.getItem('guntanks:left-battle') === '1'; } catch (_) { return false; }
};

export const store = {
  token: readToken(),
  user: null,
  page: PAGE.AUTH,
  connection: CONNECTION.DISCONNECTED,
  match: null,
  pendingBattle: null,
  battle: null,
  battleIdentity: null,
  result: null,
  reconnect: null,
  reconnectUntil: 0,
  lastEventSeq: new Map(),
  ui: {
    auth: '',
    lobby: '',
    matching: '',
    battle: '',
    result: '',
    reconnect: '',
    loading: '',
  },
  input: {
    heldKeys: new Set(),
    heldPointers: new Map(),
    moveDirection: null,
    aimDirection: null,
    charging: false,
    actionLocked: false,
    chargeValue: 0,
    firePointerId: null,
    fireFrame: 0,
    chargeStartedAt: 0,
  },
  authRequestSeq: 0,
  matchRequestSeq: 0,
  leftBattle: readLeftBattle(),
};

export function setLeftBattle(value) {
  store.leftBattle = !!value;
  try { if (store.leftBattle) sessionStorage.setItem('guntanks:left-battle', '1'); else sessionStorage.removeItem('guntanks:left-battle'); } catch (_) {}
}

export function setStoredToken(token) {
  store.token = token || null;
  try {
    if (store.token) {
      sessionStorage.setItem('token', store.token);
    } else {
      sessionStorage.removeItem('token');
    }
  } catch (_) {
    // sessionStorage can be unavailable in hardened browsers.
  }
}

export function clearMatchState() {
  store.match = null;
  store.pendingBattle = null;
  store.reconnect = null;
  store.result = null;
  store.ui.matching = '';
  store.ui.loading = '';
}

export function clearBattleState() {
  store.battle = null;
  store.pendingBattle = null;
  store.battleIdentity = null;
  store.input.actionLocked = false;
}

export function clearInputState() {
  store.input.heldKeys.clear();
  store.input.heldPointers.clear();
  store.input.moveDirection = null;
  store.input.aimDirection = null;
  store.input.charging = false;
  store.input.firePointerId = null;
  store.input.chargeStartedAt = 0;
  store.input.fireFrame = 0;
  store.input.actionLocked = false;
}
