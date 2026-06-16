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

// ---------- 图标 ----------
const ICON = {
  link: `<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M10 13a5 5 0 0 0 7 0l3-3a5 5 0 0 0-7-7l-1.5 1.5"/><path d="M14 11a5 5 0 0 0-7 0l-3 3a5 5 0 0 0 7 7l1.5-1.5"/></svg>`,
  doc: `<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M14 3H7a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h10a2 2 0 0 0 2-2V8z"/><path d="M14 3v5h5"/><path d="M9 13h6M9 17h6"/></svg>`,
  mic: `<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><rect x="9" y="2" width="6" height="12" rx="3"/><path d="M5 11a7 7 0 0 0 14 0"/><path d="M12 18v3"/></svg>`,
  chat: `<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M21 12a8 8 0 0 1-8 8H7l-4 3V12a8 8 0 0 1 16 0Z"/></svg>`,
  check: `<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.4" stroke-linecap="round" stroke-linejoin="round"><path d="M20 6 9 17l-5-5"/></svg>`,
  upload: `<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M12 16V4m0 0 4 4m-4-4-4 4"/><path d="M4 16v2a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2v-2"/></svg>`,
};

const STEPS = [
  { id: 1, name: "连接服务器" },
  { id: 2, name: "上传资料" },
  { id: 3, name: "声纹注册" },
  { id: 4, name: "实时面试" },
];

// ---------- 状态 ----------
let settings: Settings = loadSettings();
let mode: Mode = "conversation";
let currentStep = 1;
const done = new Set<number>();
let connected = false;
let listening = false;
let enrolling = false;
let enrollTimer: number | undefined;
let answerStreaming = false;
let currentAnswer = "";
const history: QAItem[] = [];
let resumeText = "";
let companyText = "";

let ws: WsClient | null = null;
const audio = new AudioCapture((buf) => {
  if (listening || enrolling) ws?.sendPcm(buf);
});

// ---------- DOM ----------
const $ = <T extends HTMLElement = HTMLElement>(sel: string) =>
  document.querySelector(sel) as T;
const app = document.getElementById("app")!;

app.innerHTML = `
  <header class="topbar">
    <div class="brand"><span class="logo">AI</span><span class="name">面试助手</span></div>
    <span class="status"><span id="dot" class="dot"></span><span id="statusText">未连接</span></span>
  </header>

  <nav class="stepper" id="stepper"></nav>

  <!-- 第一步：连接服务器 -->
  <section class="step-panel active" data-step="1">
    <div class="step-hero">
      <div class="step-icon">${ICON.link}</div>
      <h2 class="step-title">连接服务器</h2>
      <p class="step-sub">输入实时核心服务器地址并连接，开启面试辅助。</p>
    </div>
    <div class="card">
      <div class="field">
        <label>WS 服务器地址</label>
        <input id="setWs" type="text" placeholder="ws://127.0.0.1:8000/ws（留空 = 同源）" />
        <div class="note dim">留空则连接当前网页同源的 /ws。</div>
      </div>
      <button id="btnConnect" class="btn-primary">连接服务器</button>
      <div id="connNote" class="note"></div>
    </div>
    <p class="hint">手机访问需通过 HTTPS 才能使用麦克风。</p>
  </section>

  <!-- 第二步：上传简历和公司 -->
  <section class="step-panel" data-step="2">
    <div class="step-hero">
      <div class="step-icon">${ICON.doc}</div>
      <h2 class="step-title">上传简历和公司</h2>
      <p class="step-sub">提供简历与目标公司，让 AI 的回答更贴合你的背景。</p>
    </div>
    <div class="card">
      <div class="field">
        <label>简历（PDF / 图片）</label>
        <label class="upload-zone" for="resumeFile" id="resumeZone">
          ${ICON.upload}<span class="uz-text" id="resumeZoneText">点击选择文件</span>
        </label>
        <input id="resumeFile" type="file" accept=".pdf,.png,.jpg,.jpeg" hidden />
        <div id="resumeNote" class="note"></div>
      </div>
      <div class="field">
        <label>目标公司</label>
        <div class="row">
          <input id="companyName" type="text" placeholder="如：字节跳动" />
          <button id="btnCompany" class="btn-secondary" style="flex:0 0 96px">获取简报</button>
        </div>
        <div id="companyNote" class="note"></div>
      </div>
      <button id="btnSendCtx" class="btn-primary mt" disabled>发送到服务器</button>
      <div id="ctxNote" class="note"></div>
    </div>
    <details class="adv">
      <summary>高级：Context 服务地址</summary>
      <div class="card">
        <div class="field">
          <label>Context 服务地址（空 = 同源 /ctx）</label>
          <input id="setCtx" type="text" placeholder="http://127.0.0.1:8102 或留空" />
        </div>
        <button id="btnSaveCtx" class="btn-secondary">保存地址</button>
        <div id="ctxAddrNote" class="note"></div>
      </div>
    </details>
    <p class="hint">两者可只填其一；本步可跳过。发送后即可进入下一步。</p>
  </section>

  <!-- 第三步：声纹注册 -->
  <section class="step-panel" data-step="3">
    <div class="step-hero">
      <div class="step-icon">${ICON.mic}</div>
      <h2 class="step-title">声纹注册</h2>
      <p class="step-sub">念一句话，让系统记住你的声音，用于区分「你」和「面试官」。</p>
    </div>
    <div class="enroll-area">
      <button id="btnEnroll" class="mic-btn">${ICON.mic}</button>
      <div id="enrollNote" class="enroll-note">点击麦克风，自然地说一句话（约 3 秒）。</div>
    </div>
    <p class="hint">本步可跳过。跳过后系统会把所有声音都当作面试官提问。</p>
  </section>

  <!-- 第四步：实时面试 -->
  <section class="step-panel" data-step="4">
    <div class="step-hero" style="padding-bottom:0">
      <h2 class="step-title" style="font-size:19px">实时面试</h2>
    </div>
    <div class="live-bar">
      <div class="modes">
        <button id="modeConv" class="sel">口语化</button>
        <button id="modeStruct">公考结构化</button>
      </div>
      <button id="btnListen" class="btn-listen" disabled><span class="lz"></span><span id="listenText">开始监听</span></button>
    </div>

    <div class="hear">
      <div id="partial" class="partial"></div>
      <div id="transcript" class="transcript">—</div>
    </div>

    <section class="card q-card">
      <div class="label">面试官问题</div>
      <div id="question" class="question">—</div>
    </section>

    <section class="card answer-card">
      <div class="label">
        <span>AI 参考回答</span>
        <div class="label-actions">
          <button id="btnRegen" class="tinybtn">重答</button>
          <button id="btnClear" class="tinybtn">清屏</button>
        </div>
      </div>
      <div id="answer" class="answer">—</div>
    </section>

    <div id="latency" class="latency">延迟：—</div>

    <details class="history-wrap">
      <summary>本场 Q&amp;A 历史</summary>
      <div id="history" class="history"><div class="empty">暂无</div></div>
    </details>
  </section>

  <!-- 步骤底部导航（第四步隐藏） -->
  <nav class="step-nav" id="stepNav">
    <button id="btnBack" class="btn-back">← 上一步</button>
    <button id="btnNext" class="btn-next">下一步 →</button>
  </nav>
`;

// ---------- 元素引用 ----------
const dot = $("#dot");
const statusText = $("#statusText");
const stepperEl = $("#stepper");
const stepNav = $("#stepNav");
const btnBack = $<HTMLButtonElement>("#btnBack");
const btnNext = $<HTMLButtonElement>("#btnNext");
const panels = Array.from(
  document.querySelectorAll<HTMLElement>(".step-panel"),
);

// 第一步
const inpWs = $<HTMLInputElement>("#setWs");
const btnConnect = $<HTMLButtonElement>("#btnConnect");
const connNote = $("#connNote");
// 第二步
const resumeFile = $<HTMLInputElement>("#resumeFile");
const resumeZone = $("#resumeZone");
const resumeZoneText = $("#resumeZoneText");
const resumeNote = $("#resumeNote");
const companyName = $<HTMLInputElement>("#companyName");
const btnCompany = $<HTMLButtonElement>("#btnCompany");
const companyNote = $("#companyNote");
const btnSendCtx = $<HTMLButtonElement>("#btnSendCtx");
const ctxNote = $("#ctxNote");
const inpCtx = $<HTMLInputElement>("#setCtx");
const btnSaveCtx = $<HTMLButtonElement>("#btnSaveCtx");
const ctxAddrNote = $("#ctxAddrNote");
// 第三步
const btnEnroll = $<HTMLButtonElement>("#btnEnroll");
const enrollNote = $("#enrollNote");
// 第四步
const modeConv = $<HTMLButtonElement>("#modeConv");
const modeStruct = $<HTMLButtonElement>("#modeStruct");
const btnListen = $<HTMLButtonElement>("#btnListen");
const listenText = $("#listenText");
const partialEl = $("#partial");
const transcriptEl = $("#transcript");
const questionEl = $("#question");
const answerEl = $("#answer");
const latencyEl = $("#latency");
const historyEl = $("#history");

// 预填设置
inpWs.value = settings.wsUrl;
inpCtx.value = settings.ctxUrl;

// ---------- 顶部状态 ----------
function setStatus(text: string, kind: "on" | "err" | "off" = "off") {
  statusText.textContent = text;
  dot.className = "dot" + (kind === "on" ? " on" : kind === "err" ? " err" : "");
}

// ---------- 向导导航 ----------
function isUnlocked(step: number): boolean {
  return step === 1 || connected;
}

function renderStepper() {
  stepperEl.innerHTML = STEPS.map((s) => {
    const isDone = done.has(s.id);
    const isActive = currentStep === s.id;
    const prevDone = s.id > 1 && done.has(s.id - 1);
    const cls = [
      "step-item",
      isActive ? "active" : "",
      isDone ? "done" : "",
      prevDone ? "prev-done" : "",
    ]
      .filter(Boolean)
      .join(" ");
    const bullet = isDone ? ICON.check : String(s.id);
    const locked = !isUnlocked(s.id);
    return `<button class="${cls}" data-go="${s.id}" ${locked ? "disabled" : ""}>
      <span class="step-bullet">${bullet}</span>
      <span class="step-name">${s.name}</span>
    </button>`;
  }).join("");
  stepperEl.querySelectorAll<HTMLButtonElement>("[data-go]").forEach((b) => {
    b.onclick = () => goToStep(Number(b.dataset.go));
  });
}

function markDone(step: number, value: boolean) {
  if (value) done.add(step);
  else done.delete(step);
  renderStepper();
  updateNav();
}

function updateNav() {
  if (currentStep === 4) {
    stepNav.style.display = "none";
    return;
  }
  stepNav.style.display = "flex";
  btnBack.style.display = currentStep === 1 ? "none" : "block";

  let ready: boolean;
  if (currentStep === 1) {
    ready = connected;
    btnNext.disabled = !connected;
    btnNext.textContent = "下一步 →";
  } else {
    ready = done.has(currentStep);
    btnNext.disabled = false;
    btnNext.textContent = ready ? "下一步 →" : "跳过此步 →";
  }
  btnNext.classList.toggle("pulse", ready);
}

function goToStep(step: number) {
  if (step < 1 || step > 4 || !isUnlocked(step)) return;
  // 录音中离开声纹步骤：自动取消，避免带着 enrolling 进入面试。
  if (enrolling && currentStep === 3 && step !== 3) {
    cancelEnroll("已退出声纹注册。");
  }
  currentStep = step;
  panels.forEach((p) =>
    p.classList.toggle("active", Number(p.dataset.step) === step),
  );
  renderStepper();
  updateNav();
  window.scrollTo({ top: 0, behavior: "smooth" });
}

btnBack.onclick = () => goToStep(currentStep - 1);
btnNext.onclick = () => goToStep(currentStep + 1);

// ---------- 第一步：连接 ----------
btnConnect.onclick = () => connect();

function connect() {
  settings = { ...settings, wsUrl: inpWs.value.trim() };
  saveSettings(settings);
  const url = resolveWsUrl(settings.wsUrl);
  setStatus("连接中…");
  connNote.className = "note dim";
  connNote.textContent = "正在连接 " + url;
  btnConnect.disabled = true;
  ws = new WsClient(url);
  ws.onState = onWsState;
  ws.onMessage = handleMsg;
  ws.connect();
}

function onWsState(s: WsState) {
  if (s === "open") {
    connected = true;
    setStatus("已连接", "on");
    setControlsEnabled(true);
    ws?.send({ type: "set_mode", mode });
    btnConnect.disabled = false;
    btnConnect.textContent = "已连接 ✓ 重新连接";
    btnConnect.classList.add("ok");
    connNote.className = "note";
    connNote.textContent = "连接成功，点击「下一步」继续。";
    markDone(1, true);
  } else if (s === "connecting") {
    setStatus("连接中…");
  } else {
    connected = false;
    setControlsEnabled(false);
    listening = false;
    enrolling = false;
    syncListenBtn();
    btnConnect.disabled = false;
    btnConnect.textContent = "重新连接";
    btnConnect.classList.remove("ok");
    if (s === "error") {
      setStatus("连接出错", "err");
      connNote.className = "note err";
      connNote.textContent = "连接出错，请检查地址后重试。";
    } else {
      setStatus("连接断开", "err");
      connNote.className = "note err";
      connNote.textContent = "连接已断开，请重新连接。";
    }
    markDone(1, false);
  }
}

function setControlsEnabled(on: boolean) {
  btnListen.disabled = !on;
}

// ---------- 第二步：上下文 ----------
function updateCtxSendState() {
  btnSendCtx.disabled = !(resumeText || companyText);
}

resumeFile.onchange = async (e) => {
  const file = (e.target as HTMLInputElement).files?.[0];
  if (!file) return;
  resumeZoneText.textContent = file.name;
  resumeNote.className = "note dim";
  resumeNote.textContent = "解析中…";
  try {
    resumeText = await uploadResume(resolveCtxBase(settings.ctxUrl), file);
    resumeZone.classList.add("filled");
    resumeNote.className = "note";
    resumeNote.textContent = `已获取简历画像（${resumeText.length} 字）`;
  } catch (err) {
    resumeZone.classList.remove("filled");
    resumeNote.className = "note err";
    resumeNote.textContent = (err as Error).message;
    resumeText = "";
  }
  updateCtxSendState();
};

btnCompany.onclick = () => fetchCompanyBrief();
companyName.addEventListener("keydown", (e) => {
  if (e.key === "Enter") fetchCompanyBrief();
});

async function fetchCompanyBrief() {
  const name = companyName.value.trim();
  if (!name) {
    companyNote.className = "note err";
    companyNote.textContent = "请输入公司名";
    return;
  }
  companyNote.className = "note dim";
  companyNote.textContent = "获取中…";
  try {
    companyText = await fetchCompany(resolveCtxBase(settings.ctxUrl), name);
    companyNote.className = "note";
    companyNote.textContent = `已获取公司简报（${companyText.length} 字）`;
  } catch (err) {
    companyNote.className = "note err";
    companyNote.textContent = (err as Error).message;
    companyText = "";
  }
  updateCtxSendState();
}

btnSendCtx.onclick = () => {
  if (!ws || !ws.connected) {
    ctxNote.className = "note err";
    ctxNote.textContent = "未连接服务器，请先返回第一步连接。";
    return;
  }
  ws.send({
    type: "set_context",
    resume_text: resumeText,
    company_text: companyText,
  });
  ctxNote.className = "note";
  ctxNote.textContent = "已发送上下文，点击「下一步」继续。";
  markDone(2, true);
};

btnSaveCtx.onclick = () => {
  settings = { ...settings, ctxUrl: inpCtx.value.trim() };
  saveSettings(settings);
  ctxAddrNote.className = "note";
  ctxAddrNote.textContent = "已保存。";
};

// ---------- 第三步：声纹注册 ----------
function clearEnrollTimeout() {
  if (enrollTimer !== undefined) {
    clearTimeout(enrollTimer);
    enrollTimer = undefined;
  }
}

// 停止/取消注册：解除本地录音态并通知服务端解除武装，
// 避免服务端一直 enrolling、把后续第一句真问题当成声纹样本吃掉。
function cancelEnroll(msg: string, isErr = false) {
  clearEnrollTimeout();
  enrolling = false;
  btnEnroll.classList.remove("recording");
  ws?.send({ type: "enroll_cancel" });
  enrollNote.className = isErr ? "enroll-note err" : "enroll-note";
  enrollNote.textContent = msg;
  setStatus("已停止", "off");
}

btnEnroll.onclick = async () => {
  // 录音中再次点击 = 停止（解决“录音不可停止”）。
  if (enrolling) {
    cancelEnroll("已停止录音，可重新点击注册。");
    return;
  }
  if (!ws || !ws.connected) {
    enrollNote.className = "enroll-note err";
    enrollNote.textContent = "未连接服务器，请先返回第一步连接。";
    return;
  }
  try {
    await audio.ensure();
    await audio.resume();
    enrolling = true;
    ws.send({ type: "enroll_start" });
    btnEnroll.classList.remove("ok");
    btnEnroll.classList.add("recording");
    enrollNote.className = "enroll-note";
    enrollNote.textContent = "录音中：请正常说一句话…（再次点击可停止）";
    setStatus("声纹注册中…", "on");
    // 安全超时：ASR 迟迟不出句尾时自动停止，避免永远卡在录音态。
    clearEnrollTimeout();
    enrollTimer = window.setTimeout(() => {
      if (enrolling)
        cancelEnroll("未检测到完整语音，请在安静环境下说一句话后重试。", true);
    }, 12000);
  } catch (e) {
    enrolling = false;
    enrollNote.className = "enroll-note err";
    enrollNote.textContent = "麦克风初始化失败：" + (e as Error).message;
  }
};

// ---------- 第四步：实时面试 ----------
function syncListenBtn() {
  listenText.textContent = listening ? "停止监听" : "开始监听";
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

function setMode(m: Mode) {
  mode = m;
  modeConv.classList.toggle("sel", m === "conversation");
  modeStruct.classList.toggle("sel", m === "structured");
  if (ws && ws.connected) ws.send({ type: "set_mode", mode });
}
modeConv.onclick = () => setMode("conversation");
modeStruct.onclick = () => setMode("structured");

$<HTMLButtonElement>("#btnRegen").onclick = () => {
  if (ws && ws.connected) ws.send({ type: "regenerate" });
};

$<HTMLButtonElement>("#btnClear").onclick = () => clearScreen();
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
      clearEnrollTimeout();
      enrolling = false;
      btnEnroll.classList.remove("recording");
      if (m.ok) {
        btnEnroll.classList.add("ok");
        enrollNote.className = "enroll-note ok";
        enrollNote.textContent = "声纹注册成功 ✓ 点击「下一步」开始面试。";
        setStatus("声纹注册成功", "on");
        markDone(3, true);
      } else {
        enrollNote.className = "enroll-note err";
        enrollNote.textContent =
          "声纹模块不可用（所有声音都会判为面试官）。可重试或跳过。";
        setStatus("声纹模块不可用", "err");
      }
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

// ---------- 初始化 ----------
renderStepper();
updateNav();

// ---------- PWA service worker ----------
if ("serviceWorker" in navigator) {
  window.addEventListener("load", () => {
    navigator.serviceWorker.register("/sw.js").catch(() => {
      /* 忽略注册失败（如非 HTTPS） */
    });
  });
}
