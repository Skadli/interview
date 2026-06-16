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

	asr ASRStream

	mu          sync.Mutex // 保护 mode/enrolling/resume/company
	mode        string
	enrolling   bool
	resumeText  string
	companyText string

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

// Run：连接主循环。读 ws、起 ASR 消费 goroutine。阻塞直到连接关闭。
func (s *Session) Run() {
	defer s.cancel()

	asr, err := s.makeASR()
	if err != nil {
		log.Printf("[session %s] create ASR err: %v", s.sid[:8], err)
		s.sendStatus("asr_error")
		return
	}
	s.asr = asr
	defer s.asr.Close()

	go s.consumeASR()

	s.sendStatus("ready")

	for {
		mt, data, err := s.conn.ReadMessage()
		if err != nil {
			s.cancel() // 客户端断开：先标记正常结束，再由 defer 关 ASR（避免误报 asr_error）
			return
		}
		switch mt {
		case websocket.BinaryMessage:
			pcm := bytesToPCM(data)
			s.ring.write(pcm)
			s.asr.SendAudio(pcm)
		case websocket.TextMessage:
			s.handleControl(data)
		}
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
		s.mu.Unlock()
		s.sendStatus("enrolling")
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

// consumeASR：消费 ASR 事件。partial 直接发；final 起 goroutine 处理。
func (s *Session) consumeASR() {
	for ev := range s.asr.Events() {
		if !ev.Final {
			s.send(map[string]any{"type": "partial", "text": ev.Text})
			continue
		}
		go s.handleFinal(ev)
	}
	// events 关闭即 ASR 流结束。若会话尚未正常收尾（ctx 未取消），说明是上游 ASR
	// 出错导致流中断（如握手/鉴权/参数被拒），通知前端而非静默卡死。
	select {
	case <-s.ctx.Done():
	default:
		log.Printf("[session %s] ASR stream ended unexpectedly", s.sid[:8])
		s.sendStatus("asr_error")
	}
}

func (s *Session) handleFinal(ev ASREvent) {
	tEnd := time.Now()

	// 按 [start,end] 从环形缓冲切出该句音频
	seg := s.ring.slice(ev.StartMs, ev.EndMs)

	// enrolling：注册声纹后返回
	s.mu.Lock()
	enrolling := s.enrolling
	s.mu.Unlock()
	if enrolling {
		ok := false
		if s.speaker.Enabled() {
			ok = s.speaker.Enroll(seg)
		}
		s.mu.Lock()
		s.enrolling = false
		s.mu.Unlock()
		s.send(map[string]any{"type": "enrolled", "ok": ok})
		return
	}

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
