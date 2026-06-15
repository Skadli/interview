import "./style.css";
import type { Mode, QAItem, ServerMsg, Timing } from "./types";
import { AudioCapture } from "./audio";
import { WsClient, type WsState } from "./ws";
import {
  loadSettings,
  saveSettings,
  resolveWsUrl,
  resolveCtxBase,
  type Settings,
} from "./settings";
import { uploadResume, fetchCompany } from "./context-api";

// ---------- 状态 ----------
let settings: Settings = loadSettings();
let mode: Mode = "conversation";
let listening = false;
let enrolling = false;
let answerStreaming = false;
let currentAnswer = "";
const history: QAItem[] = [];
let resumeText = "";
let companyText = "";

let ws: WsClient;
const audio = new AudioCapture((buf) => {
  if (listening || enrolling) ws.sendPcm(buf);
});

// ---------- DOM ----------
const $ = (sel: string) => document.querySelector(sel) as HTMLElement;
const app = document.getElementById("app")!;

app.innerHTML = `
  <header>
    <h1>AI 面试助手</h1>
    <div style="display:flex;gap:8px;align-items:center">
      <span id="status" class="status"><span id="dot" class="dot"></span><span id="statusText">未连接</span></span>
      <button id="btnCtx" class="iconbtn" title="上下文">@</button>
      <button id="btnSet" class="iconbtn" title="设置">=</button>
    </div>
  </header>

  <div class="btns">
    <button id="btnConnect" class="wide">连接服务器</button>
    <button id="btnListen" class="btn-listen wide" disabled>开始监听</button>
    <button id="btnEnroll" disabled>声纹注册</button>
    <button id="btnClear" title="清屏">清屏</button>
  </div>

  <div class="modes">
    <button id="modeConv" class="sel">口语化</button>
    <button id="modeStruct">公考结构化</button>
  </div>

  <section class="card">
    <div class="label"><span>实时识别</span></div>
    <div id="partial" class="partial"></div>
    <div id="transcript" class="transcript">—</div>
  </section>

  <section class="card">
    <div class="label">面试官问题</div>
    <div id="question" class="question">—</div>
  </section>

  <section class="card answer-card">
    <div class="label"><span>AI 参考回答</span><button id="btnRegen" class="iconbtn" style="width:auto;padding:0 10px;height:26px;font-size:12px">重答</button></div>
    <div id="answer" class="answer">—</div>
  </section>

  <div id="latency" class="latency">延迟：—</div>

  <section class="card">
    <div class="label">本场 Q&amp;A 历史</div>
    <div id="history" class="history"><div class="empty">暂无</div></div>
  </section>

  <!-- 上下文抽屉 -->
  <div id="ctxBg" class="drawer-bg">
    <div class="drawer">
      <h2>面试上下文</h2>
      <div class="field">
        <label>简历（PDF / 图片）</label>
        <input id="resumeFile" type="file" accept=".pdf,.png,.jpg,.jpeg" />
        <div id="resumeNote" class="note"></div>
      </div>
      <div class="field">
        <label>目标公司</label>
        <div class="row">
          <input id="companyName" type="text" placeholder="如：字节跳动" />
          <button id="btnCompany" style="flex:0 0 84px">获取简报</button>
        </div>
        <div id="companyNote" class="note"></div>
      </div>
      <button id="btnSendCtx" class="wide" disabled>发送上下文给服务器</button>
      <div id="ctxNote" class="note"></div>
      <p class="hint">两者都获取后会发送 set_context；也可只发送已获取的部分。</p>
      <button id="ctxClose" class="wide" style="background:#2a2e37;margin-top:10px">关闭</button>
    </div>
  </div>

  <!-- 设置抽屉 -->
  <div id="setBg" class="drawer-bg">
    <div class="drawer">
      <h2>设置</h2>
      <div class="field">
        <label>WS 服务器地址（空 = 同源 /ws）</label>
        <input id="setWs" type="text" placeholder="ws://127.0.0.1:8000/ws 或留空" />
      </div>
      <div class="field">
        <label>Context 服务地址（空 = 同源 /ctx）</label>
        <input id="setCtx" type="text" placeholder="http://127.0.0.1:8102 或留空" />
      </div>
      <button id="btnSaveSet" class="wide">保存</button>
      <div id="setNote" class="note"></div>
      <p class="hint">保存后请重新点「连接服务器」。手机访问需 HTTPS 才能用麦克风。</p>
      <button id="setClose" class="wide" style="background:#2a2e37;margin-top:10px">关闭</button>
    </div>
  </div>
`;

// ---------- 元素引用 ----------
const dot = $("#dot");
const statusText = $("#statusText");
const btnConnect = $("#btnConnect") as HTMLButtonElement;
const btnListen = $("#btnListen") as HTMLButtonElement;
const btnEnroll = $("#btnEnroll") as HTMLButtonElement;
const partialEl = $("#partial");
const transcriptEl = $("#transcript");
const questionEl = $("#question");
const answerEl = $("#answer");
const latencyEl = $("#latency");
const historyEl = $("#history");

// ---------- 状态显示 ----------
function setStatus(text: string, kind: "on" | "err" | "off" = "off") {
  statusText.textContent = text;
  dot.className = "dot" + (kind === "on" ? " on" : kind === "err" ? " err" : "");
}

function setControlsEnabled(on: boolean) {
  btnListen.disabled = !on;
  btnEnroll.disabled = !on;
}

// ---------- WS 连接 ----------
function connect() {
  const url = resolveWsUrl(settings.wsUrl);
  setStatus("连接中…");
  ws = new WsClient(url);
  ws.onState = onWsState;
  ws.onMessage = handleMsg;
  ws.connect();
}

function onWsState(s: WsState) {
  if (s === "open") {
    setStatus("已连接", "on");
    setControlsEnabled(true);
    // 同步当前模式
    ws.send({ type: "set_mode", mode });
  } else if (s === "connecting") {
    setStatus("连接中…");
  } else if (s === "error") {
    setStatus("连接出错", "err");
    setControlsEnabled(false);
  } else {
    setStatus("连接断开", "err");
    setControlsEnabled(false);
    listening = false;
    enrolling = false;
    syncListenBtn();
  }
}

// ---------- 消息处理 ----------
function handleMsg(m: ServerMsg) {
  switch (m.type) {
    case "status":
      setStatus(m.state, "on");
      break;
    case "partial":
      partialEl.textContent = m.text;
      break;
    case "transcript": {
      partialEl.textContent = "";
      const isUser = m.speaker === "user";
      const tag = isUser ? "你" : "面试官";
      const sim =
        typeof m.similarity === "number" ? ` ${m.similarity.toFixed(2)}` : "";
      transcriptEl.innerHTML = `<span class="spk ${m.speaker}">${tag}${sim}</span>${escapeHtml(m.text)}`;
      break;
    }
    case "question":
      questionEl.textContent = m.text;
      answerEl.textContent = "";
      answerEl.classList.add("streaming");
      answerStreaming = true;
      currentAnswer = "";
      break;
    case "answer_delta":
      if (!answerStreaming) {
        answerEl.textContent = "";
        answerEl.classList.add("streaming");
        answerStreaming = true;
        currentAnswer = "";
      }
      currentAnswer += m.text;
      answerEl.textContent = currentAnswer;
      answerEl.scrollTop = answerEl.scrollHeight;
      break;
    case "answer_done":
      answerStreaming = false;
      answerEl.classList.remove("streaming");
      renderTiming(m.timing);
      pushHistory(questionEl.textContent || "", currentAnswer);
      break;
    case "enrolled":
      enrolling = false;
      setStatus(
        m.ok ? "声纹注册成功" : "声纹模块不可用（所有声音都判为面试官）",
        m.ok ? "on" : "err",
      );
      break;
  }
}

function renderTiming(t: Timing) {
  latencyEl.innerHTML =
    `感知首字 <b>${t.perceived_first_word_ms}ms</b> ｜ ` +
    `端点 ${t.endpoint_ms} + 说完后 ${t.to_first_word_ms} ` +
    `(声纹 ${t.speaker_ms} / ASR ${t.asr_ms} / TTFT ${t.llm_ttft_ms} / LLM总 ${t.llm_total_ms})`;
}

function pushHistory(q: string, a: string) {
  if (!q && !a) return;
  history.unshift({ question: q, answer: a });
  renderHistory();
}

function renderHistory() {
  if (history.length === 0) {
    historyEl.innerHTML = `<div class="empty">暂无</div>`;
    return;
  }
  historyEl.innerHTML = history
    .map(
      (it) =>
        `<div class="qa"><div class="q">${escapeHtml(it.question || "（无问题）")}</div><div class="a">${escapeHtml(it.answer)}</div></div>`,
    )
    .join("");
}

function escapeHtml(s: string): string {
  return s
    .replace(/&/g, "&amp;")
    .replace(/</g, "&lt;")
    .replace(/>/g, "&gt;");
}

// ---------- 按钮事件 ----------
btnConnect.onclick = () => connect();

function syncListenBtn() {
  btnListen.textContent = listening ? "停止监听" : "开始监听";
  btnListen.classList.toggle("active", listening);
}

btnListen.onclick = async () => {
  try {
    await audio.ensure();
    await audio.resume();
    listening = !listening;
    syncListenBtn();
    setStatus(listening ? "监听中…" : "已暂停", listening ? "on" : "off");
  } catch (e) {
    setStatus("麦克风初始化失败：" + (e as Error).message, "err");
  }
};

btnEnroll.onclick = async () => {
  try {
    await audio.ensure();
    await audio.resume();
    enrolling = true;
    ws.send({ type: "enroll_start" });
    setStatus("声纹注册中：请正常说一句话…", "on");
  } catch (e) {
    setStatus("麦克风初始化失败：" + (e as Error).message, "err");
  }
};

($("#btnRegen") as HTMLButtonElement).onclick = () => {
  if (ws && ws.connected) ws.send({ type: "regenerate" });
};

// 隐蔽一键清屏：长按或点击「清屏」
($("#btnClear") as HTMLButtonElement).onclick = () => clearScreen();
function clearScreen() {
  partialEl.textContent = "";
  transcriptEl.textContent = "—";
  questionEl.textContent = "—";
  answerEl.textContent = "—";
  answerEl.classList.remove("streaming");
  latencyEl.textContent = "延迟：—";
  history.length = 0;
  renderHistory();
}
// 双击页面标题也清屏（隐蔽）
$("h1").addEventListener("dblclick", clearScreen);

// 模式切换
const modeConv = $("#modeConv") as HTMLButtonElement;
const modeStruct = $("#modeStruct") as HTMLButtonElement;
function setMode(m: Mode) {
  mode = m;
  modeConv.classList.toggle("sel", m === "conversation");
  modeStruct.classList.toggle("sel", m === "structured");
  if (ws && ws.connected) ws.send({ type: "set_mode", mode });
}
modeConv.onclick = () => setMode("conversation");
modeStruct.onclick = () => setMode("structured");

// ---------- 上下文抽屉 ----------
const ctxBg = $("#ctxBg");
const resumeNote = $("#resumeNote");
const companyNote = $("#companyNote");
const ctxNote = $("#ctxNote");
const btnSendCtx = $("#btnSendCtx") as HTMLButtonElement;

$("#btnCtx").onclick = () => ctxBg.classList.add("open");
$("#ctxClose").onclick = () => ctxBg.classList.remove("open");
ctxBg.addEventListener("click", (e) => {
  if (e.target === ctxBg) ctxBg.classList.remove("open");
});

function updateCtxSendState() {
  btnSendCtx.disabled = !(resumeText || companyText);
}

($("#resumeFile") as HTMLInputElement).onchange = async (e) => {
  const file = (e.target as HTMLInputElement).files?.[0];
  if (!file) return;
  resumeNote.className = "note";
  resumeNote.textContent = "解析中…";
  try {
    resumeText = await uploadResume(resolveCtxBase(settings.ctxUrl), file);
    resumeNote.textContent = `已获取简历画像（${resumeText.length} 字）`;
  } catch (err) {
    resumeNote.className = "note err";
    resumeNote.textContent = (err as Error).message;
    resumeText = "";
  }
  updateCtxSendState();
};

($("#btnCompany") as HTMLButtonElement).onclick = async () => {
  const name = ($("#companyName") as HTMLInputElement).value.trim();
  if (!name) {
    companyNote.className = "note err";
    companyNote.textContent = "请输入公司名";
    return;
  }
  companyNote.className = "note";
  companyNote.textContent = "获取中…";
  try {
    companyText = await fetchCompany(resolveCtxBase(settings.ctxUrl), name);
    companyNote.textContent = `已获取公司简报（${companyText.length} 字）`;
  } catch (err) {
    companyNote.className = "note err";
    companyNote.textContent = (err as Error).message;
    companyText = "";
  }
  updateCtxSendState();
};

btnSendCtx.onclick = () => {
  if (!ws || !ws.connected) {
    ctxNote.className = "note err";
    ctxNote.textContent = "未连接服务器";
    return;
  }
  ws.send({
    type: "set_context",
    resume_text: resumeText,
    company_text: companyText,
  });
  ctxNote.className = "note";
  ctxNote.textContent = "已发送上下文";
};

// ---------- 设置抽屉 ----------
const setBg = $("#setBg");
const setWs = $("#setWs") as HTMLInputElement;
const setCtx = $("#setCtx") as HTMLInputElement;
const setNote = $("#setNote");

$("#btnSet").onclick = () => {
  setWs.value = settings.wsUrl;
  setCtx.value = settings.ctxUrl;
  setBg.classList.add("open");
};
$("#setClose").onclick = () => setBg.classList.remove("open");
setBg.addEventListener("click", (e) => {
  if (e.target === setBg) setBg.classList.remove("open");
});

($("#btnSaveSet") as HTMLButtonElement).onclick = () => {
  settings = { wsUrl: setWs.value.trim(), ctxUrl: setCtx.value.trim() };
  saveSettings(settings);
  setNote.className = "note";
  setNote.textContent = "已保存。请重新点「连接服务器」。";
};

// ---------- PWA service worker ----------
if ("serviceWorker" in navigator) {
  window.addEventListener("load", () => {
    navigator.serviceWorker.register("/sw.js").catch(() => {
      /* 忽略注册失败（如非 HTTPS） */
    });
  });
}
