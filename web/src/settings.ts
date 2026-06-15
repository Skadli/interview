// 设置项：WS 服务器地址、context 服务地址，存 localStorage。

export interface Settings {
  wsUrl: string; // 留空 = 同源 /ws
  ctxUrl: string; // 留空 = 同源 /ctx
}

const KEY = "interview.settings.v1";

export function loadSettings(): Settings {
  try {
    const raw = localStorage.getItem(KEY);
    if (raw) {
      const s = JSON.parse(raw);
      return { wsUrl: s.wsUrl ?? "", ctxUrl: s.ctxUrl ?? "" };
    }
  } catch {
    /* ignore */
  }
  return { wsUrl: "", ctxUrl: "" };
}

export function saveSettings(s: Settings): void {
  localStorage.setItem(KEY, JSON.stringify(s));
}

// 解析最终 WebSocket URL。
// 配置为空 -> 同源 /ws（含 https->wss）。
// 配置为 ws://host:port 或 host:port -> 规范化。
export function resolveWsUrl(cfg: string): string {
  const v = cfg.trim();
  if (!v) {
    const proto = location.protocol === "https:" ? "wss" : "ws";
    return `${proto}://${location.host}/ws`;
  }
  if (/^wss?:\/\//i.test(v)) return v;
  if (/^https?:\/\//i.test(v)) {
    return v.replace(/^http/i, "ws");
  }
  // 裸 host[:port][/path]
  const proto = location.protocol === "https:" ? "wss" : "ws";
  const path = v.includes("/ws") ? "" : "/ws";
  return `${proto}://${v}${path}`;
}

// 解析 context 服务 base（无尾斜杠）。空 -> 同源 /ctx。
export function resolveCtxBase(cfg: string): string {
  const v = cfg.trim().replace(/\/+$/, "");
  if (!v) return "/ctx";
  return v;
}
