// 极简 service worker：缓存应用外壳，支持添加到主屏 / 离线打开页面。
// 注意：不缓存 /ws（WebSocket）与 context 服务请求（/ctx、跨域 API），这些始终走网络。
const CACHE = "interview-shell-v1";
const SHELL = ["/", "/index.html", "/manifest.json", "/pcm-worklet.js"];

self.addEventListener("install", (e) => {
  e.waitUntil(
    caches.open(CACHE).then((c) => c.addAll(SHELL)).catch(() => {}),
  );
  self.skipWaiting();
});

self.addEventListener("activate", (e) => {
  e.waitUntil(
    caches.keys().then((keys) =>
      Promise.all(keys.filter((k) => k !== CACHE).map((k) => caches.delete(k))),
    ),
  );
  self.clients.claim();
});

self.addEventListener("fetch", (e) => {
  const req = e.request;
  const url = new URL(req.url);

  // 仅处理 GET 同源静态请求；其余（POST/上下文 API/WS）直接走网络。
  if (req.method !== "GET" || url.origin !== self.location.origin) return;
  if (url.pathname.startsWith("/ctx") || url.pathname.startsWith("/ws")) return;

  // 导航请求：网络优先，失败回退缓存的 index。
  if (req.mode === "navigate") {
    e.respondWith(fetch(req).catch(() => caches.match("/index.html")));
    return;
  }

  // 静态资源：缓存优先，回填缓存。
  e.respondWith(
    caches.match(req).then(
      (hit) =>
        hit ||
        fetch(req).then((res) => {
          const copy = res.clone();
          caches.open(CACHE).then((c) => c.put(req, copy)).catch(() => {});
          return res;
        }),
    ),
  );
});
