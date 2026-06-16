package main

import (
	"context"
	"encoding/json"
	"log"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// ASRFactory：每个会话新建一条 ASR 流（mock 直接构造；volc 实际拨号）。
type ASRFactory func() (ASRStream, error)

// ===== 客户端 -> 服务端 控制消息 =====
type ctrlMsg struct {
	Type        string `json:"type"`
	Mode        string `json:"mode"`
	ResumeText  string `json:"resume_text"`
	CompanyText string `json:"company_text"`
}

// ===== 环形音频缓冲 =====
// 存最近 ~30s 的 int16，记录基准样本号，可按 start_time/end_time 毫秒切片。
type ringBuffer struct {
	mu   sync.Mutex
	data []int16
	cap  int
	base int64 // data[0] 对应的全局样本号
	sr   int
}

func newRingBuffer(sr, seconds int) *ringBuffer {
	return &ringBuffer{cap: sr * seconds, sr: sr}
}

func (r *ringBuffer) write(pcm []int16) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.data = append(r.data, pcm...)
	if len(r.data) > r.cap {
		drop := len(r.data) - r.cap
		r.data = r.data[drop:]
		r.base += int64(drop)
	}
}

// total：已写入的累计样本数（= 下一个待写样本的全局样本号）。
func (r *ringBuffer) total() int64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.base + int64(len(r.data))
}

// slice：按毫秒区间 [startMs,endMs] 取出该段 PCM。越界自动裁剪。
func (r *ringBuffer) slice(startMs, endMs int64) []int16 {
	r.mu.Lock()
	defer r.mu.Unlock()
	startSmp := startMs * int64(r.sr) / 1000
	endSmp := endMs * int64(r.sr) / 1000

	lo := startSmp - r.base
	hi := endSmp - r.base
	if lo < 0 {
		lo = 0
	}
	if hi > int64(len(r.data)) {
		hi = int64(len(r.data))
	}
	if lo >= hi {
		return nil
	}
	out := make([]int16, hi-lo)
	copy(out, r.data[lo:hi])
	return out
}

// ===== Session =====
type Session struct {
	cfg     Config
	sid     string
	conn    *websocket.Conn
	makeASR ASRFactory
	llm     LLM
	speaker Speaker
	ring    *ringBuffer

	// asr/asrDead/asrBackoff 受 mu 保护：ASR 流惰性建连（首帧音频才拨号）、断线自动重连。
	asr        ASRStream
	asrDead    bool      // 当前流已结束，下一帧音频触发重连
	asrBackoff time.Time // 重连退避截止（连接失败后短暂不再重试）

	mu                sync.Mutex // 保护 mode/enrolling/resume/company 及上面 asr 三件
	mode              string
	enrolling         bool
	enrollStartSample int64 // enroll_start 时的环形缓冲绝对样本号，停止时据此切出注册音频
	resumeText        string
	companyText       string

	writeMu sync.Mutex // 串行化 ws 写
	llmMu   sync.Mutex // 串行化 LLM（避免并发回答交错）

	lastQuestion string

	ctx    context.Context
	cancel context.CancelFunc
}

func NewSession(cfg Config, conn *websocket.Conn, makeASR ASRFactory, llm LLM) *Session {
	sid := uuid4()
	ctx, cancel := context.WithCancel(context.Background())
	return &Session{
		cfg:     cfg,
		sid:     sid,
		conn:    conn,
		makeASR: makeASR,
		llm:     llm,
		speaker: makeSpeaker(cfg, sid),
		ring:    newRingBuffer(cfg.SampleRate, 30),
		mode:    "conversation",
		ctx:     ctx,
		cancel:  cancel,
	}
}

func (s *Session) send(v any) {
	b, _ := json.Marshal(v)
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	_ = s.conn.WriteMessage(websocket.TextMessage, b)
}

func (s *Session) sendStatus(state string) {
	s.send(map[string]any{"type": "status", "state": state})
}

// Run：连接主循环。读 ws、惰性建 ASR、阻塞直到连接关闭。
func (s *Session) Run() {
	defer s.cancel()
	defer s.closeASR()

	// ASR 流不在此预建：火山大模型流式 ASR 对"建连后迟迟不发包"会判等包超时（45000081）
	// 而踢断会话。用户连上后往往要先传简历/取公司简报/做声纹注册，期间并不发音频，
	// 预建必被踢断。改为惰性：首帧音频到达才建连；连接因空闲被踢后，下一帧音频自动重连。
	s.sendStatus("ready")

	for {
		mt, data, err := s.conn.ReadMessage()
		if err != nil {
			s.cancel() // 客户端断开：标记正常收尾，避免误报 asr_error
			return
		}
		switch mt {
		case websocket.BinaryMessage:
			pcm := bytesToPCM(data)
			pos := s.ring.total() // 此帧在环形缓冲中的绝对起点（写入前）
			s.ring.write(pcm)
			s.feedAudio(pcm, pos)
		case websocket.TextMessage:
			s.handleControl(data)
		}
	}
}

// feedAudio：把一帧音频喂给 ASR；按需惰性建连 / 断线重连。
// pos = 该帧在环形缓冲中的绝对起点样本号。每条 ASR 连接的识别时间戳都从 0 计，
// 用 pos 折算出的 baseMs 还原成会话全局时间戳，保证 handleFinal 按 [start,end]
// 切环形缓冲时取到正确音频。
func (s *Session) feedAudio(pcm []int16, pos int64) {
	s.mu.Lock()
	asr := s.asr
	need := asr == nil || s.asrDead
	backoff := s.asrBackoff
	s.mu.Unlock()

	if need {
		if time.Now().Before(backoff) {
			return // 退避期内丢弃此帧（pos 仍随环形缓冲推进，重连后基准依旧正确）
		}
		// volc 为同步拨号：期间本循环阻塞，WS 音频在 TCP 缓冲排队，连上后顺序补发。
		newASR, err := s.makeASR()
		if err != nil {
			log.Printf("[session %s] (re)connect ASR err: %v", s.sid[:8], err)
			s.mu.Lock()
			s.asrBackoff = time.Now().Add(1500 * time.Millisecond)
			s.mu.Unlock()
			s.sendStatus("asr_error")
			return
		}
		baseMs := msOf(pos, s.cfg.SampleRate)
		s.mu.Lock()
		s.asr = newASR
		s.asrDead = false
		s.mu.Unlock()
		go s.consumeASR(newASR, baseMs)
		asr = newASR
	}

	asr.SendAudio(pcm)
}

// closeASR：关闭当前 ASR 流（幂等；惰性建连下可能从未建过）。
func (s *Session) closeASR() {
	s.mu.Lock()
	asr := s.asr
	s.asr = nil
	s.mu.Unlock()
	if asr != nil {
		asr.Close()
	}
}

func (s *Session) handleControl(data []byte) {
	var m ctrlMsg
	if err := json.Unmarshal(data, &m); err != nil {
		return
	}
	switch m.Type {
	case "set_mode":
		if m.Mode == "structured" || m.Mode == "conversation" {
			s.mu.Lock()
			s.mode = m.Mode
			s.mu.Unlock()
			s.sendStatus("mode:" + m.Mode)
		}
	case "enroll_start":
		s.mu.Lock()
		s.enrolling = true
		s.enrollStartSample = s.ring.total() // 记录起点，停止时切 [起点,此刻] 作注册音频
		s.mu.Unlock()
		s.sendStatus("enrolling")
	case "enroll_stop":
		// 用户停止录音 → 用录到的音频完成声纹注册（不依赖 ASR 句尾端点）。
		s.finishEnroll()
	case "enroll_cancel":
		// 放弃注册（离开此步/麦克风失败）：解除武装，不注册，避免后续第一句真问题被当成声纹样本。
		s.mu.Lock()
		s.enrolling = false
		s.mu.Unlock()
		s.sendStatus("enroll_cancelled")
	case "set_context":
		s.mu.Lock()
		s.resumeText = m.ResumeText
		s.companyText = m.CompanyText
		s.mu.Unlock()
		s.sendStatus("context_set")
	case "regenerate":
		s.mu.Lock()
		q := s.lastQuestion
		s.mu.Unlock()
		if q != "" {
			now := time.Now()
			go s.answer(q, now, now, 0)
		}
	}
}

// consumeASR：消费一条 ASR 流的事件（baseMs 把连接内相对时间戳还原为会话全局）。
// partial 直接发；final 起 goroutine 处理。流结束（events 关闭）后标记需重连。
func (s *Session) consumeASR(asr ASRStream, baseMs int64) {
	for ev := range asr.Events() {
		if !ev.Final {
			s.send(map[string]any{"type": "partial", "text": ev.Text})
			continue
		}
		ev.StartMs += baseMs
		ev.EndMs += baseMs
		go s.handleFinal(ev)
	}
	// 该流结束：若仍是当前流，标记 asrDead，下一帧音频触发重连。
	s.mu.Lock()
	current := s.asr == asr
	if current {
		s.asrDead = true
	}
	s.mu.Unlock()
	if !current {
		return
	}
	// 正常收尾（ctx 取消）静默；否则只记日志——空闲被踢属预期，重连自动、不打扰前端。
	select {
	case <-s.ctx.Done():
	default:
		log.Printf("[session %s] ASR stream ended; 下一帧音频将自动重连", s.sid[:8])
	}
}

// finishEnroll：用 enroll_start 以来录到的音频做声纹注册（由 enroll_stop / 超时触发，
// 不依赖 ASR 句尾端点——声纹只需原始人声，不需要转写）。校验时长与能量，失败回带原因。
func (s *Session) finishEnroll() {
	s.mu.Lock()
	if !s.enrolling {
		s.mu.Unlock()
		return // 已结束/已取消，忽略重复 stop
	}
	s.enrolling = false
	startSmp := s.enrollStartSample
	s.mu.Unlock()

	sr := s.cfg.SampleRate
	seg := s.ring.slice(msOf(startSmp, sr), msOf(s.ring.total(), sr))
	durMs := msOf(int64(len(seg)), sr)

	ok, reason := false, ""
	switch {
	case !s.speaker.Enabled():
		reason = "no_sidecar"
	case durMs < 800:
		reason = "too_short"
	case rmsI16(seg) < 0.01:
		reason = "no_speech"
	case s.speaker.Enroll(seg):
		ok = true
	default:
		reason = "enroll_failed"
	}
	s.send(map[string]any{"type": "enrolled", "ok": ok, "reason": reason})
}

func (s *Session) handleFinal(ev ASREvent) {
	tEnd := time.Now()

	// 注册阶段：忽略 ASR 句子（注册由 enroll_stop 显式驱动，见 finishEnroll），
	// 既不当作提问，也不在此注册声纹。
	s.mu.Lock()
	enrolling := s.enrolling
	s.mu.Unlock()
	if enrolling {
		return
	}

	// 按 [start,end] 从环形缓冲切出该句音频（用于声纹验证）
	seg := s.ring.slice(ev.StartMs, ev.EndMs)

	// 说话人判定
	var (
		isUser bool
		sim    float64
		tSpk   = tEnd
	)
	speaker := "interviewer"
	if s.speaker.Enabled() {
		isUser, sim = s.speaker.Verify(seg)
		tSpk = time.Now()
		if isUser {
			speaker = "user"
		}
	}

	s.send(map[string]any{
		"type":       "transcript",
		"text":       ev.Text,
		"speaker":    speaker,
		"similarity": round3(sim),
	})

	// 本人在回答 → 不触发
	if isUser {
		s.sendStatus("你在回答")
		return
	}

	// 非提问 → 不触发
	if !isQuestion(ev.Text) {
		s.sendStatus("非提问")
		return
	}

	s.mu.Lock()
	s.lastQuestion = ev.Text
	s.mu.Unlock()

	s.send(map[string]any{"type": "question", "text": ev.Text})
	s.answer(ev.Text, tEnd, tSpk, sim)
}

// answer：按 mode 选模型，流式生成参考回答并打点。
func (s *Session) answer(question string, tEnd, tSpk time.Time, _ float64) {
	// 串行化，避免多句并发回答交错
	s.llmMu.Lock()
	defer s.llmMu.Unlock()

	if tSpk.IsZero() {
		tSpk = tEnd
	}

	s.mu.Lock()
	mode := s.mode
	resume := s.resumeText
	company := s.companyText
	s.mu.Unlock()

	model := s.cfg.ModelFast
	if mode == "structured" {
		model = s.cfg.ModelStrong
	}
	system, user := buildPrompt(mode, question, resume, company)

	var (
		tFirst time.Time
		first  = true
	)

	err := s.llm.Stream(s.ctx, model, system, user, func(delta string) {
		if first {
			tFirst = time.Now()
			first = false
		}
		s.send(map[string]any{"type": "answer_delta", "text": delta})
	})
	if err != nil {
		log.Printf("[session %s] llm err: %v", s.sid[:8], err)
	}
	tDone := time.Now()
	if tFirst.IsZero() {
		tFirst = tDone
	}

	speakerMs := round3(float64(tSpk.Sub(tEnd).Microseconds()) / 1000.0)
	llmTTFTMs := round3(float64(tFirst.Sub(tSpk).Microseconds()) / 1000.0)
	toFirstWordMs := round3(float64(tFirst.Sub(tEnd).Microseconds()) / 1000.0)
	llmTotalMs := round3(float64(tDone.Sub(tFirst).Microseconds()) / 1000.0)

	s.send(map[string]any{
		"type": "answer_done",
		"timing": map[string]any{
			"endpoint_ms":             s.cfg.EndpointMs, // 信息性：上游端点等待
			"speaker_ms":              speakerMs,
			"asr_ms":                  0, // 火山流式无单独调用
			"llm_ttft_ms":             llmTTFTMs,
			"to_first_word_ms":        toFirstWordMs,
			"perceived_first_word_ms": toFirstWordMs, // 火山模式端点已在上游，等于 to_first_word
			"llm_total_ms":            llmTotalMs,
		},
	})
}
