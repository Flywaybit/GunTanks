import { CONNECTION, store } from './store.js';

export class GameSocket {
  constructor(onEvent, onState = () => {}) {
    this.onEvent = onEvent;
    this.onState = onState;
    this.ws = null;
    this.heartbeat = 0;
    this.manualClose = false;
    this.retryTimer = 0;
    this.retry = 0;
    this.connectionId = 0;
    this.serverStopping = false;
    this.closeWait = 0;
  }

  connect(token) {
    if (this.serverStopping) return false;
    this.close(false);
    const connectionId = ++this.connectionId;
    this.manualClose = false;
    window.clearTimeout(this.retryTimer);
    if (!token) return false;
    const scheme = location.protocol === 'https:' ? 'wss' : 'ws';
    const url = `${scheme}://${location.host}/ws?token=${encodeURIComponent(token)}`;
    store.connection = CONNECTION.CONNECTING;
    this.onState(store.connection);
    this.ws = new WebSocket(url);
    this.ws.onopen = () => {
      if (connectionId !== this.connectionId) return;
      this.retry = 0;
      store.connection = CONNECTION.OPEN;
      this.onState(store.connection);
      this.heartbeat = window.setInterval(() => this.send('ping'), 10000);
    };
    this.ws.onmessage = (event) => {
      if (connectionId !== this.connectionId) return;
      try { this.onEvent(JSON.parse(event.data)); } catch (_) { /* malformed event */ }
    };
    this.ws.onerror = () => {};
    this.ws.onclose = (event) => {
      if (connectionId !== this.connectionId) return;
      window.clearInterval(this.heartbeat);
      this.heartbeat = 0;
      window.clearTimeout(this.closeWait);
      const reason = String(event?.reason || '');
      const stopping = this.serverStopping || event?.code === 1001 || /server shutting down|service stopped/i.test(reason);
      if (stopping) this.serverStopping = true;
      store.connection = stopping ? CONNECTION.STOPPING : (this.manualClose ? CONNECTION.CLOSED : CONNECTION.RECONNECTING);
      this.onState(store.connection);
      if (!stopping && !this.manualClose && store.token) {
        const delay = Math.min(10000, 500 * (2 ** this.retry++));
        this.retryTimer = window.setTimeout(() => this.connect(store.token), delay);
      }
    };
    return true;
  }

  send(type, payload = {}, extra = {}) {
    if (this.serverStopping) return false;
    if (!this.ws || this.ws.readyState !== WebSocket.OPEN) return false;
    const message = { type, payload, ...extra };
    if (type.startsWith('battle.') && !message.requestId) {
      message.request_id = crypto.randomUUID ? crypto.randomUUID() : `${Date.now()}-${Math.random()}`;
      delete message.requestId;
    } else if (message.requestId) {
      message.request_id = message.requestId;
      delete message.requestId;
    }
    this.ws.send(JSON.stringify(message));
    return true;
  }

  close(manual = true) {
    this.connectionId += 1;
    this.manualClose = manual;
    const current = this.ws;
    if (current) {
      if (manual && current.readyState === WebSocket.OPEN) {
        try { current.close(1000, 'client closing'); } catch (_) { current.close(); }
        this.closeWait = window.setTimeout(() => { try { current.close(); } catch (_) {} }, 1000);
      } else current.close();
    }
    this.ws = null;
    window.clearInterval(this.heartbeat);
    if (manual) window.clearTimeout(this.retryTimer);
  }

  markServerStopping() {
    this.serverStopping = true;
    window.clearTimeout(this.retryTimer);
    window.clearTimeout(this.closeWait);
    window.clearInterval(this.heartbeat);
    if (this.ws) {
      try { this.ws.close(1001, 'server shutting down'); } catch (_) { this.ws.close(); }
    }
  }
}
