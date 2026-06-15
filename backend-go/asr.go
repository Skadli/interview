package main

import (
	"encoding/binary"
	"encoding/json"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// ASREvent：ASR 产出的事件。
//   - Final=false：进行中（partial），仅 Text 有效。
//   - Final=true ：一句结束（definite），带 StartMs/EndMs（毫秒，相对会话起点）。
type ASREvent struct {
	Text    string
	Final   bool
	StartMs int64
	EndMs   int64
}

// ASRStream：一条贯穿整个会话的流式 ASR 连接。
type ASRStream interface {
	SendAudio(pcm []int16)   // 持续灌入音频
	Events() <-chan ASREvent // 消费识别事件
	Close()                  // 结束会话（发最后一包并关闭）
}

// ====================== Mock ASR（能量 VAD 切分） ======================

var mockQuestions = []string{
	"请你做个自我介绍。",
	"谈谈你最有成就感的项目。",
	"你为什么想加入我们公司？",
	"如果同事不配合你怎么办？",
	"对'年轻人躺平'谈谈看法。",
	"一周内组织200人宣讲会你怎么安排？",
}

type MockASR struct {
	sr     int
	events chan ASREvent

	mu       sync.Mutex
	buf      []int16 // 累积的待处理样本（窗口对齐）
	totalSmp int64   // 已消费的样本数（用于时间戳）

	speaking   bool
	segStart   int64 // 当前段起点样本号
	segSamples []int16
	silWins    int // 连续静音窗口数
	voiceWins  int // 连续有声窗口数
	qIdx       int
	closed     bool
}

const (
	mockWin       = 512   // 每窗样本数
	mockRMSThresh = 0.015 // 能量阈值
	mockStartWins = 2     // 连续 N 窗判开始说话
	mockMinSegMs  = 350   // 短于此丢弃
)

func NewMockASR(sr int) *MockASR {
	m := &MockASR{sr: sr, events: make(chan ASREvent, 32)}
	// 静音 ~650ms 判句尾
	m.silWinsNeeded()
	return m
}

func (m *MockASR) silWinsNeeded() int {
	// 650ms 内有多少个 512 样本窗口
	winMs := float64(mockWin) / float64(m.sr) * 1000.0
	return int(650.0/winMs + 0.5)
}

func (m *MockASR) Events() <-chan ASREvent { return m.events }

func (m *MockASR) SendAudio(pcm []int16) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return
	}
	m.buf = append(m.buf, pcm...)
	silNeeded := m.silWinsNeeded()

	for len(m.buf) >= mockWin {
		win := m.buf[:mockWin]
		m.buf = m.buf[mockWin:]
		winStart := m.totalSmp
		m.totalSmp += mockWin

		energy := rmsI16(win)
		voiced := energy >= mockRMSThresh

		if !m.speaking {
			if voiced {
				m.voiceWins++
				if m.voiceWins >= mockStartWins {
					m.speaking = true
					m.segStart = winStart - int64(mockStartWins-1)*mockWin
					if m.segStart < 0 {
						m.segStart = 0
					}
					m.segSamples = m.segSamples[:0]
					m.silWins = 0
				}
			} else {
				m.voiceWins = 0
			}
			if m.speaking {
				m.segSamples = append(m.segSamples, win...)
			}
			continue
		}

		// speaking
		m.segSamples = append(m.segSamples, win...)
		if voiced {
			m.silWins = 0
		} else {
			m.silWins++
			if m.silWins >= silNeeded {
				m.endSegment()
			}
		}
	}
}

// endSegment：必须在持锁状态调用。
func (m *MockASR) endSegment() {
	segEnd := m.totalSmp
	durMs := msOf(int64(len(m.segSamples)), m.sr)
	m.speaking = false
	m.voiceWins = 0
	m.silWins = 0
	if durMs >= mockMinSegMs {
		text := mockQuestions[m.qIdx%len(mockQuestions)]
		m.qIdx++
		ev := ASREvent{
			Text:    text,
			Final:   true,
			StartMs: msOf(m.segStart, m.sr),
			EndMs:   msOf(segEnd, m.sr),
		}
		select {
		case m.events <- ev:
		default:
		}
	}
	m.segSamples = nil
}

func (m *MockASR) Close() {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return
	}
	m.closed = true
	if m.speaking {
		m.endSegment()
	}
	m.mu.Unlock()
	close(m.events)
}

// ====================== 火山大模型流式 ASR ======================

// 协议常量
const (
	msgClientFull  = 0b0001
	msgClientAudio = 0b0010
	msgServerFull  = 0b1001
	msgServerError = 0b1111

	flagPosSeq     = 0b0001 // 带序列号
	flagNegWithSeq = 0b0011 // 最后一包，序列号取负

	serJSON = 0b0001
	serRAW  = 0b0000

	compNone = 0b0000
	compGZIP = 0b0001
)

type VolcASR struct {
	cfg    Config
	conn   *websocket.Conn
	events chan ASREvent

	mu     sync.Mutex
	seq    int32
	closed bool

	lastEndMs int64 // 已发过 final 的最大 end_time，避免重复
}

func NewVolcASR(cfg Config) (*VolcASR, error) {
	header := http.Header{}
	if cfg.VolcAPIKey != "" {
		// 新版统一鉴权：单个 X-Api-Key
		header.Set("X-Api-Key", cfg.VolcAPIKey)
	} else {
		// 旧版鉴权：App Key + Access Token
		header.Set("X-Api-App-Key", cfg.VolcAppKey)
		header.Set("X-Api-Access-Key", cfg.VolcAccessKey)
	}
	header.Set("X-Api-Resource-Id", cfg.VolcResourceID)
	header.Set("X-Api-Request-Id", uuid4())

	dialer := websocket.Dialer{HandshakeTimeout: 10 * time.Second}
	conn, _, err := dialer.Dial(cfg.VolcASRURL, header)
	if err != nil {
		return nil, err
	}

	v := &VolcASR{
		cfg:    cfg,
		conn:   conn,
		events: make(chan ASREvent, 64),
		seq:    0,
	}

	if err := v.sendFullRequest(); err != nil {
		conn.Close()
		return nil, err
	}

	go v.readLoop()
	return v, nil
}

func (v *VolcASR) Events() <-chan ASREvent { return v.events }

// 构造 4 字节头
func header4(msgType, flags, ser, comp byte) []byte {
	return []byte{
		(0b0001 << 4) | 0b0001, // 协议版本1 + 头长1
		(msgType << 4) | flags,
		(ser << 4) | comp,
		0x00,
	}
}

func (v *VolcASR) nextSeq() int32 {
	v.seq++
	return v.seq
}

// 首包：full client request（JSON + gzip，带正序号）
func (v *VolcASR) sendFullRequest() error {
	reqJSON := map[string]any{
		"user": map[string]any{"uid": "interview"},
		"audio": map[string]any{
			"format": "pcm", "codec": "raw", "rate": 16000, "bits": 16, "channel": 1,
		},
		"request": map[string]any{
			"model_name":      v.cfg.VolcASRModel,
			"enable_punc":     true,
			"enable_itn":      true,
			"enable_ddc":      true,
			"show_utterances": true,
		},
	}
	jb, _ := json.Marshal(reqJSON)
	payload := gz(jb)

	var msg []byte
	msg = append(msg, header4(msgClientFull, flagPosSeq, serJSON, compGZIP)...)
	msg = append(msg, i32(v.nextSeq())...) // 序列号
	msg = append(msg, u32be(uint32(len(payload)))...)
	msg = append(msg, payload...)

	v.mu.Lock()
	defer v.mu.Unlock()
	return v.conn.WriteMessage(websocket.BinaryMessage, msg)
}

// 音频包（audio only，RAW + gzip，带正序号）
func (v *VolcASR) SendAudio(pcm []int16) {
	v.mu.Lock()
	if v.closed {
		v.mu.Unlock()
		return
	}
	seq := v.nextSeq()
	v.mu.Unlock()

	payload := gz(pcmToBytes(pcm))
	var msg []byte
	msg = append(msg, header4(msgClientAudio, flagPosSeq, serRAW, compGZIP)...)
	msg = append(msg, i32(seq)...)
	msg = append(msg, u32be(uint32(len(payload)))...)
	msg = append(msg, payload...)

	v.mu.Lock()
	defer v.mu.Unlock()
	if v.closed {
		return
	}
	if err := v.conn.WriteMessage(websocket.BinaryMessage, msg); err != nil {
		log.Printf("[volc-asr] send audio err: %v", err)
	}
}

// 末包：audio only，负序号（标志位 NEG_WITH_SEQUENCE），空 payload。
func (v *VolcASR) sendLast() {
	v.mu.Lock()
	if v.closed {
		v.mu.Unlock()
		return
	}
	seq := v.nextSeq()
	v.mu.Unlock()

	payload := gz([]byte{})
	var msg []byte
	msg = append(msg, header4(msgClientAudio, flagNegWithSeq, serRAW, compGZIP)...)
	msg = append(msg, i32(-seq)...) // 序列号取负
	msg = append(msg, u32be(uint32(len(payload)))...)
	msg = append(msg, payload...)

	v.mu.Lock()
	defer v.mu.Unlock()
	_ = v.conn.WriteMessage(websocket.BinaryMessage, msg)
}

func (v *VolcASR) Close() {
	v.mu.Lock()
	if v.closed {
		v.mu.Unlock()
		return
	}
	v.mu.Unlock()

	v.sendLast()

	v.mu.Lock()
	v.closed = true
	_ = v.conn.Close()
	v.mu.Unlock()
}

// 火山响应 JSON
type volcResp struct {
	Result struct {
		Text       string `json:"text"`
		Utterances []struct {
			Text      string `json:"text"`
			StartTime int64  `json:"start_time"`
			EndTime   int64  `json:"end_time"`
			Definite  bool   `json:"definite"`
		} `json:"utterances"`
	} `json:"result"`
}

func (v *VolcASR) readLoop() {
	defer func() {
		v.mu.Lock()
		closed := v.closed
		v.mu.Unlock()
		if !closed {
			close(v.events)
		}
	}()

	for {
		_, data, err := v.conn.ReadMessage()
		if err != nil {
			v.mu.Lock()
			closed := v.closed
			v.mu.Unlock()
			if !closed {
				log.Printf("[volc-asr] read err: %v", err)
				close(v.events)
				v.mu.Lock()
				v.closed = true
				v.mu.Unlock()
			}
			return
		}
		v.handleFrame(data)
	}
}

func (v *VolcASR) handleFrame(data []byte) {
	if len(data) < 4 {
		return
	}
	msgType := data[0] >> 4 // 头第二个 nibble 是头长；msgType 在 byte1
	_ = msgType
	b1 := data[1]
	mt := b1 >> 4
	flags := b1 & 0x0f
	b2 := data[2]
	comp := b2 & 0x0f

	off := 4
	// 标志位 & 0x01：带序列号
	if flags&0x01 != 0 {
		if len(data) < off+4 {
			return
		}
		off += 4
	}
	// 标志位 & 0x04：带 event（防御性跳过）
	if flags&0x04 != 0 {
		if len(data) < off+4 {
			return
		}
		off += 4
	}

	switch mt {
	case msgServerError:
		// 读错误码 + payload，记录日志
		log.Printf("[volc-asr] server error frame (flags=%#x)", flags)
		return
	case msgServerFull:
		if len(data) < off+4 {
			return
		}
		size := binary.BigEndian.Uint32(data[off : off+4])
		off += 4
		if len(data) < off+int(size) {
			return
		}
		payload := data[off : off+int(size)]
		if comp == compGZIP {
			payload = gunzip(payload)
		}
		v.parseResult(payload)
	default:
		// 其他类型忽略
	}
}

func (v *VolcASR) parseResult(payload []byte) {
	var r volcResp
	if err := json.Unmarshal(payload, &r); err != nil {
		return
	}
	if len(r.Result.Utterances) == 0 {
		// 没有 utterances：把整段 text 作为 partial
		if r.Result.Text != "" {
			v.emit(ASREvent{Text: r.Result.Text, Final: false})
		}
		return
	}
	for _, u := range r.Result.Utterances {
		if !u.Definite {
			v.emit(ASREvent{Text: u.Text, Final: false})
			continue
		}
		// definite=true：仅当 end_time 大于已处理过的才发 final
		v.mu.Lock()
		newer := u.EndTime > v.lastEndMs
		if newer {
			v.lastEndMs = u.EndTime
		}
		v.mu.Unlock()
		if newer {
			v.emit(ASREvent{
				Text:    u.Text,
				Final:   true,
				StartMs: u.StartTime,
				EndMs:   u.EndTime,
			})
		}
	}
}

func (v *VolcASR) emit(ev ASREvent) {
	defer func() { _ = recover() }() // 防止向已关闭 channel 发送
	v.mu.Lock()
	closed := v.closed
	v.mu.Unlock()
	if closed {
		return
	}
	select {
	case v.events <- ev:
	default:
	}
}
