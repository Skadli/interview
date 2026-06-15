import { defineConfig } from "vite";

// 构建产物输出到 web/dist（Go 核心默认从 ../web/dist 托管）。
// dev 代理：/ws -> Go 实时核心(8000)，/ctx -> context 服务(8102)。
export default defineConfig({
  build: {
    outDir: "dist",
    emptyOutDir: true,
    target: "es2020",
  },
  server: {
    host: true, // 局域网可访问，方便手机调试
    port: 5173,
    proxy: {
      "/ws": {
        target: "ws://127.0.0.1:8000",
        ws: true,
        changeOrigin: true,
      },
      "/ctx": {
        target: "http://127.0.0.1:8102",
        changeOrigin: true,
        rewrite: (p) => p.replace(/^\/ctx/, ""),
      },
    },
  },
});
