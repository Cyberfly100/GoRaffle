// Minimal WebSocket client: connects, reconnects, and dispatches typed events.
const state = {
  connected: false,
  reconnectDelay: 1000,
};
const listeners = {};

// Subscribes to one event type pushed by the server.
export function on(type, fn) {
  (listeners[type] = listeners[type] || []).push(fn);
}

export function connected() {
  return state.connected;
}

function emit(type, data) {
  (listeners[type] || []).forEach((fn) => fn(data));
}

function setStatus(cls) {
  const el = document.getElementById("ws-status");
  if (!el) return;
  el.className = "ws-dot " + cls;
  el.title = cls === "connected" ? "backend connected" : "backend reconnecting";
}

function connect() {
  const proto = location.protocol === "https:" ? "wss" : "ws";
  const ws = new WebSocket(`${proto}://${location.host}/ws`);

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
    setTimeout(connect, state.reconnectDelay);
    state.reconnectDelay = Math.min(state.reconnectDelay * 2, 15000);
  };

  ws.onerror = () => ws.close();
}

connect();
