import { PAGE, clearMatchState, setLeftBattle, store } from './store.js';
import { goTo } from './stateMachine.js';

export function createMatchController({ socket, onStatus, onPageChange, onMatchWaiting, onMatchCleared, onBattleStart, onCancelRetry, getPage }) {
  let replayTimer = 0;
  let cancelTimer = 0;
  let cancelRequestId = '';
  let settled = false;
  let cancelledRequestId = '';

  function clearReplayTimer() {
    if (replayTimer) {
      clearTimeout(replayTimer);
      replayTimer = 0;
    }
  }

  function setMatch(count) {
    clearMatchState();
    setLeftBattle(false);
    const requestId = crypto.randomUUID ? crypto.randomUUID() : `${Date.now()}-${Math.random()}`;
    store.match = {
      requestId,
      playerCount: count,
      startedAt: Date.now(),
      phase: 'joining',
      replayed: false,
    };
    settled = false;
    cancelledRequestId = '';
    store.matchRequestSeq += 1;
    goTo(PAGE.MATCHING);
    onPageChange?.(PAGE.MATCHING);
    onStatus?.(`Matching for ${count} players...`);
    socket.send('match.join', { player_count: count, match_request_id: requestId }, { requestId });
    return store.match;
  }

  function cancelMatch() {
    if (!store.match) {
      return false;
    }
    if (store.match.phase === 'canceling') {
      return true;
    }
    store.match.phase = 'canceling';
    cancelRequestId = store.match.requestId;
    cancelledRequestId = cancelRequestId;
    goTo(PAGE.MATCH_CANCELING);
    onPageChange?.(PAGE.MATCH_CANCELING);
    onStatus?.('Canceling match...');
    socket.send('match.cancel', { match_request_id: cancelRequestId }, { requestId: cancelRequestId });
    clearTimeout(cancelTimer);
    cancelTimer = setTimeout(() => {
      if (store.match?.phase !== 'canceling') return;
      store.match.phase = 'matching';
      onCancelRetry?.();
      onStatus?.('Cancel failed. Please try again.');
      onPageChange?.(PAGE.MATCHING);
    }, 3000);
    return true;
  }

  function handleEvent(event) {
    if (event.type === 'match.waiting') {
      if (!store.match || store.match.phase === 'canceling' || event.payload?.match_request_id === cancelledRequestId || (event.payload?.match_request_id && event.payload.match_request_id !== store.match.requestId)) {
        return;
      }
      store.match.phase = 'matching';
      onMatchWaiting?.(event.payload);
      goTo(PAGE.MATCHING);
      onPageChange?.(PAGE.MATCHING);
      onStatus?.(`Matching for ${event.payload?.player_count || store.match.playerCount} players...`);
      return;
    }

    if (event.type === 'match.cancelled') {
      if (store.match && store.match.phase !== 'canceling') {
        return;
      }
      if (event.payload?.match_request_id && event.payload.match_request_id !== store.match.requestId) return;
      clearReplayTimer(); clearTimeout(cancelTimer); cancelRequestId = '';
      clearMatchState();
      goTo(PAGE.LOBBY);
      onMatchCleared?.();
      onPageChange?.(PAGE.LOBBY);
      onStatus?.('');
      return;
    }

    if (event.type === 'match.failed') {
      if (!store.match || (event.payload?.match_request_id && event.payload.match_request_id !== store.match.requestId)) return;
      clearReplayTimer(); clearTimeout(cancelTimer); settled = true;
      clearMatchState();
      goTo(PAGE.LOBBY); onMatchCleared?.(); onPageChange?.(PAGE.LOBBY);
      onStatus?.(event.payload?.message || 'Match creation failed. Please try again.');
      return;
    }

    if (event.type === 'error' && event.payload?.code === 'BATTLE_ALREADY_STARTED') {
      if (!store.match || settled) return;
      clearTimeout(cancelTimer); settled = true; store.match.phase = 'matched';
      onStatus?.('Match already started. Loading battle...');
      onBattleStart?.(event.payload);
      return;
    }
    if (event.type === 'error' && event.payload?.code === 'MATCH_REQUEST_MISMATCH') {
      if (store.match?.phase === 'canceling') {
        clearTimeout(cancelTimer); store.match.phase = 'matching'; onCancelRetry?.(); onStatus?.('Cancel failed. Please try again.'); onPageChange?.(PAGE.MATCHING);
      }
      return;
    }

    if (event.type === 'battle.started') {
      if (settled || !store.match || store.match.phase === 'canceling') return;
      if (store.match && store.match.phase === 'canceling') {
        // battle already won the race; cancel response will be ignored later.
      }
      clearReplayTimer();
      clearTimeout(cancelTimer); settled = true;
      store.match && (store.match.phase = 'matched');
      onBattleStart?.(event.payload);
    }
  }

  function onReconnect() {
    if (!store.match || store.match.phase === 'canceling' || getPage() !== PAGE.RECONNECTING) {
      return;
    }
    clearReplayTimer();
    replayTimer = setTimeout(() => {
      if (!store.match || store.match.phase === 'canceling' || getPage() !== PAGE.RECONNECTING) {
        return;
      }
      if (!store.match.replayed) {
        store.match.replayed = true;
        socket.send('match.join', { player_count: store.match.playerCount, match_request_id: store.match.requestId }, { requestId: store.match.requestId });
        onStatus?.(`Restoring match for ${store.match.playerCount} players...`);
      }
    }, 800);
  }

  function clear() {
    clearReplayTimer();
    clearMatchState();
    clearTimeout(cancelTimer);
  }

  return { setMatch, cancelMatch, handleEvent, onReconnect, clear };
}
