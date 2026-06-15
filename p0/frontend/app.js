const $ = (id) => document.getElementById(id);
let ws, audioCtx, workletNode, mediaStream;
let listening = false, mode = "conversation", answerEl = null;

function log(s) { $("status").textContent = s; }

function connect() {
  const proto = location.protocol === "https:" ? "wss" : "ws";
  ws = new WebSocket(`${proto}://${location.host}/ws`);
  ws.binaryType = "arraybuffer";
  ws.onopen = () => {
    log("已连接");
    ["btnEnroll", "btnListen", "btnMode"].forEach((id) => ($(id).disabled = false));
  };
  ws.onclose = () => log("连接断开");
  ws.onerror = () => log("连接出错");
  ws.onmessage = (e) => handle(JSON.parse(e.data));
}

function handle(m) {
  switch (m.type) {
    case "status":
      log(m.state); break;
    case "transcript":
      $("transcript").textContent =
        `[${m.speaker === "user" ? "你" : "面试官"} · sim=${m.similarity}] ${m.text}`;
      break;
    case "question":
      $("question").textContent = m.text;
      answerEl = $("answer"); answerEl.textContent = "";
      break;
    case "answer_delta":
      if (answerEl) answerEl.textContent += m.text;
      break;
    case "answer_done": {
      const t = m.timing;
      $("latency").textContent =
        `感知首字≈${t.perceived_first_word_ms}ms ｜ 端点${t.endpoint_ms} + 说完后${t.to_first_word_ms} ` +
        `(声纹${t.speaker_ms} / ASR${t.asr_ms} / TTFT${t.llm_ttft_ms})`;
      break;
    }
    case "enrolled":
      log(m.ok ? "声纹注册成功" : "声纹模块不可用（所有声音都会当作面试官）");
      break;
  }
}

async function ensureAudio() {
  if (audioCtx) return;
  mediaStream = await navigator.mediaDevices.getUserMedia({
    audio: { echoCancellation: true, noiseSuppression: true, autoGainControl: true, channelCount: 1 },
  });
  audioCtx = new (window.AudioContext || window.webkitAudioContext)();
  if (audioCtx.state === "suspended") await audioCtx.resume();
  await audioCtx.audioWorklet.addModule("/static/pcm-worklet.js");
  const src = audioCtx.createMediaStreamSource(mediaStream);
  workletNode = new AudioWorkletNode(audioCtx, "pcm-worklet");
  workletNode.port.onmessage = (e) => {
    if (ws && ws.readyState === 1 && listening) ws.send(e.data);
  };
  src.connect(workletNode); // 不连 destination，避免回放啸叫
}

$("btnConnect").onclick = connect;

$("btnEnroll").onclick = async () => {
  try {
    await ensureAudio();
    listening = true;
    ws.send(JSON.stringify({ type: "enroll_start" }));
    log("声纹注册中：请正常说一句话…");
  } catch (e) { log("麦克风/音频初始化失败：" + e.message); }
};

$("btnListen").onclick = async () => {
  try {
    await ensureAudio();
    listening = !listening;
    $("btnListen").textContent = listening ? "停止监听" : "开始监听";
    $("btnListen").classList.toggle("active", listening);
    log(listening ? "监听中…" : "已暂停");
  } catch (e) { log("麦克风/音频初始化失败：" + e.message); }
};

$("btnMode").onclick = () => {
  mode = mode === "conversation" ? "structured" : "conversation";
  $("btnMode").textContent = mode === "conversation" ? "模式：口语化" : "模式：公考结构化";
  if (ws && ws.readyState === 1) ws.send(JSON.stringify({ type: "set_mode", mode }));
};
