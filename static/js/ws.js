/* Minimal WebSocket client: connects, reconnects, and dispatches typed events. */
(() => {
  const state = {
    connected: false,
    ws: null,
    reconnectDelay: 1000,
    pushable: true,
  };
  const listeners = {};

  const on = (type, fn) => {
    (listeners[type] = listeners[type] || []).push(fn);
  };
  const emit = (type, data) => {
    (listeners[type] || []).forEach((fn) => fn(data));
  };

  function setStatus(cls) {
    const el = document.getElementById("ws-status");
    if (el) el.className = "ws-dot " + cls;
  }

  function connect() {
    if (!state.pushable) return;
    const proto = location.protocol === "https:" ? "wss" : "ws";
    const ws = new WebSocket(`${proto}://${location.host}/ws`);
    state.ws = ws;

    ws.onopen = () => {
      state.connected = true;
      state.reconnectDelay = 1000;
      setStatus("connected");
    };

    ws.onmessage = (e) => {
      let msg;
      try {
        msg = JSON.parse(e.data);
      } catch {
        return;
      }
      emit(msg.type, msg.data);
    };

    ws.onclose = () => {
      state.connected = false;
      setStatus("reconnecting");
      window.Raffle.emitFallbackDraw && window.Raffle.emitFallbackDraw();
      setTimeout(connect, state.reconnectDelay);
      state.reconnectDelay = Math.min(state.reconnectDelay * 2, 15000);
    };

    ws.onerror = () => ws.close();
  }

  window.Raffle = window.Raffle || {};
  window.Raffle.ws = state;
  window.Raffle.wsOn = on;
  window.Raffle.wsConnected = () => state.connected;

  connect();
})();