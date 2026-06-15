# AI 面试助手 · 手机网页前端 (PWA)

Vite + TypeScript（vanilla TS，无框架）。移动优先，面试中操作少而大。复用 `p0/frontend` 的
AudioWorklet 16k Int16 PCM 采集 + WebSocket 逻辑。

## 功能

- 连接 Go 实时核心 WebSocket（`/ws`，地址可配置，默认同源）；连接状态指示灯。
- 开始/停止监听（控制是否上行音频）。
- 声纹注册（发 `enroll_start` 并采集一句，收 `enrolled` 提示）。
- 模式切换：口语化 / 公考结构化（发 `set_mode`）。
- 实时识别：`partial`（灰色小字）→ `transcript`（最终句，带 你/面试官 标签 + similarity）。
- 面试官 `question` 高亮；AI `answer` 大字号流式追加、可滚动；「重答」按钮发 `regenerate`。
- 延迟面板：解析 `answer_done.timing`，突出 `perceived_first_word_ms`，并列各环节。
- 本场 Q&A 历史列表，可回看。
- 上下文：简历上传(PDF/图片) → context `/resume` → `profile_text`；公司名 → `/company` → `brief_text`；
  发送 `set_context` 给 Go。
- 设置：WS 地址、context 地址（存 localStorage）。
- PWA：`manifest.json` + service worker，可添加到主屏、全屏 standalone。
- 隐蔽一键清屏：点「清屏」按钮，或双击页面标题。

## 开发

```bash
npm install
npm run dev      # http://localhost:5173 ，--host 已开，局域网可访问
```

dev 代理（见 `vite.config.ts`）：

- `/ws` → `ws://127.0.0.1:8000`（Go 实时核心）
- `/ctx` → `http://127.0.0.1:8102`（context 服务，去掉 `/ctx` 前缀）

dev 模式下设置里 WS/context 地址留空即走代理。

## 构建

```bash
npm run build    # 产物输出到 web/dist
```

Go 核心默认从 `../web/dist` 托管（`FRONTEND_DIR`）。`pcm-worklet.js`、`manifest.json`、`sw.js`、
图标在 `public/`，构建后位于 `dist` 根目录，路径为 `/pcm-worklet.js` 等，与 Go 文件服务器一致。

## 部署 / 生产路径约定

- 静态：Go 在 `:8000` 托管 `web/dist`，`/ws` 同源。
- context 服务（`:8102`）需通过反代暴露为同源 `/ctx`，或在设置里填完整地址（注意跨域 CORS 与 HTTPS 混合内容）。

## HTTPS 上手机要点

手机浏览器**必须 HTTPS** 才允许 `getUserMedia`（麦克风）与 service worker（`localhost` 例外，但手机不是 localhost）。

可选方案：

1. **内网穿透**：用 ngrok / cloudflared 暴露 Go 的 `:8000` 为 https，手机访问该域名。
   ```bash
   cloudflared tunnel --url http://127.0.0.1:8000
   ```
2. **本机自签证书 + Vite https**：`vite dev` 配 `server.https`，手机需信任自签证书（较麻烦）。
3. **正式域名 + 反向代理（Caddy/Nginx）** 终止 TLS，回源到 Go `:8000`，并把 `/ctx` 反代到 `:8102`。

进入页面后浏览器会请求麦克风权限，需手动允许。iOS Safari 需用户手势触发音频（已用按钮点击触发 `AudioContext.resume`）。

## 未验证项

- 未对真实 Go `/ws`、声纹、context 服务联调（仅本机构建自检）。
- service worker 仅在 HTTPS / localhost 下注册成功；HTTP 下静默失败，不影响主功能。
- PWA 图标为脚本生成的纯色占位（蓝点），建议替换为正式图标。
- iOS Safari AudioWorklet 行为未在真机验证。
