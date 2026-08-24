import { PAGE, store } from './store.js';
import { canReceiveBattleInput } from './stateMachine.js';

const DIRECTION_KEYS = new Map([
  ['a', { kind: 'move', direction: 'left' }],
  ['d', { kind: 'move', direction: 'right' }],
  ['w', { kind: 'aim', direction: 'up' }],
  ['s', { kind: 'aim', direction: 'down' }],
]);

const FIRE_DURATION_MS = 900;
const WEAPON_KEYS = new Map([['1', 'shell1'], ['2', 'shell2'], ['3', 'ss']]);

export function createInputController({ getPage, getBattle, isSyncing, sendBattleCommand, setPower, getPower, onStatus }) {
  let fireTick = 0;

  const active = () => canReceiveBattleInput(getPage()) && !isSyncing() && !store.input.actionLocked && !!getBattle()?.battle_id;

  function updatePower(value) {
    const normalized = Math.max(0, Math.min(100, Math.round(value)));
    store.input.chargeValue = normalized;
    if (setPower) {
      setPower(normalized);
    }
  }

  function stopAxis(kind) {
    const current = store.input[`${kind}Direction`];
    if (!current) {
      return;
    }
    store.input[`${kind}Direction`] = null;
    sendBattleCommand(`battle.${kind}_stop`);
  }

  function startAxis(kind, direction) {
    if (!active()) {
      return false;
    }
    const field = `${kind}Direction`;
    if (store.input[field] === direction) {
      return true;
    }
    if (store.input[field] && store.input[field] !== direction) {
      sendBattleCommand(`battle.${kind}_stop`);
    }
    store.input[field] = direction;
    sendBattleCommand(`battle.${kind}_start`, { direction });
    return true;
  }

  function syncFirePower(now = performance.now()) {
    if (!store.input.charging) {
      return;
    }
    const elapsed = Math.min(FIRE_DURATION_MS, now - store.input.chargeStartedAt);
    updatePower((elapsed / FIRE_DURATION_MS) * 100);
    fireTick = requestAnimationFrame(syncFirePower);
  }

  function startCharging() {
    if (!active() || store.input.charging) {
      return false;
    }
    store.input.charging = true;
    store.input.chargeStartedAt = performance.now();
    updatePower(0);
    fireTick = requestAnimationFrame(syncFirePower);
    return true;
  }

  function commitFire() {
    if (!store.input.charging) {
      return false;
    }
    cancelAnimationFrame(fireTick);
    store.input.charging = false;
    const power = Math.max(0, Math.min(100, Number(getPower?.() ?? store.input.chargeValue)));
    updatePower(power);
    const ok = sendBattleCommand('battle.fire', { power });
    updatePower(0);
    if (!ok) {
      onStatus?.('Fire was blocked by current battle state.');
    }
    return ok;
  }

  function cancelFire() {
    if (!store.input.charging) {
      return false;
    }
    cancelAnimationFrame(fireTick);
    store.input.charging = false;
    updatePower(0);
    return true;
  }

  function releaseAllInputs(reason = 'blur') {
    if (store.input.moveDirection) {
      sendBattleCommand('battle.move_stop');
      store.input.moveDirection = null;
    }
    if (store.input.aimDirection) {
      sendBattleCommand('battle.aim_stop');
      store.input.aimDirection = null;
    }
    if (store.input.charging) {
      cancelFire();
    }
    store.input.heldKeys.clear();
    store.input.heldPointers.forEach((_, pointerId) => {
      const target = store.input.heldPointers.get(pointerId);
      try {
        target?.releasePointerCapture(pointerId);
      } catch (_) {
        // ignore
      }
    });
    store.input.heldPointers.clear();
    if (reason) {
      onStatus?.('');
    }
  }

  function handleKeyDown(event) {
    if (getPage() !== PAGE.BATTLE || !active()) {
      return;
    }
    if (event.target instanceof HTMLInputElement || event.target instanceof HTMLTextAreaElement || event.target instanceof HTMLSelectElement || event.target?.isContentEditable) {
      return;
    }
    if (event.code === 'Space') {
      event.preventDefault();
      startCharging();
      return;
    }
    const weapon = WEAPON_KEYS.get(event.key);
    if (weapon) {
      event.preventDefault();
      sendBattleCommand('battle.select_weapon', { weapon });
      return;
    }
    const mapped = DIRECTION_KEYS.get(event.key.toLowerCase());
    if (!mapped) {
      return;
    }
    event.preventDefault();
    const key = event.key.toLowerCase();
    if (store.input.heldKeys.has(key)) {
      return;
    }
    store.input.heldKeys.add(key);
    startAxis(mapped.kind, mapped.direction);
  }

  function handleKeyUp(event) {
    if (getPage() !== PAGE.BATTLE) {
      return;
    }
    if (event.code === 'Space') {
      event.preventDefault();
      commitFire();
      return;
    }
    const mapped = DIRECTION_KEYS.get(event.key.toLowerCase());
    if (!mapped) {
      return;
    }
    event.preventDefault();
    store.input.heldKeys.delete(event.key.toLowerCase());
    stopAxis(mapped.kind);
  }

  function handlePointerDown(event) {
    if (!active()) {
      return;
    }
    const target = event.target.closest('[data-hold], [data-fire]');
    if (!target) {
      return;
    }
    event.preventDefault();
    target.setPointerCapture?.(event.pointerId);
    store.input.heldPointers.set(event.pointerId, target);
    if (target.dataset.hold) {
      const [kind, direction] = target.dataset.hold.split('-');
      startAxis(kind, direction);
    } else if (target.dataset.fire !== undefined) {
      store.input.firePointerId = event.pointerId;
      startCharging();
    }
  }

  function handlePointerUp(event) {
    const target = store.input.heldPointers.get(event.pointerId) || event.target.closest('[data-hold], [data-fire]');
    if (!target) {
      return;
    }
    event.preventDefault();
    if (target.dataset.hold) {
      const [kind] = target.dataset.hold.split('-');
      stopAxis(kind);
    } else if (target.dataset.fire !== undefined && store.input.firePointerId === event.pointerId) {
      commitFire();
      store.input.firePointerId = null;
    }
    try {
      target.releasePointerCapture?.(event.pointerId);
    } catch (_) {
      // ignore
    }
    store.input.heldPointers.delete(event.pointerId);
  }

  function handlePointerCancel(event) {
    const target = store.input.heldPointers.get(event.pointerId);
    if (!target) {
      return;
    }
    if (target.dataset.hold) {
      const [kind] = target.dataset.hold.split('-');
      stopAxis(kind);
    } else if (target.dataset.fire !== undefined) {
      cancelFire();
    }
    store.input.heldPointers.delete(event.pointerId);
  }

  function handleBlur() {
    releaseAllInputs('blur');
  }

  function handleVisibility() {
    if (document.visibilityState === 'hidden') {
      releaseAllInputs('hidden');
    }
  }

  document.addEventListener('keydown', handleKeyDown, true);
  document.addEventListener('keyup', handleKeyUp, true);
  document.addEventListener('pointerdown', handlePointerDown, true);
  document.addEventListener('pointerup', handlePointerUp, true);
  document.addEventListener('pointercancel', handlePointerCancel, true);
  window.addEventListener('blur', handleBlur);
  document.addEventListener('visibilitychange', handleVisibility);
  window.addEventListener('pagehide', handleBlur);

  return { releaseAllInputs };
}
