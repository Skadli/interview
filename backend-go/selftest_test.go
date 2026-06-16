package main

import (
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// 合成一段 PCM：先 toneMs 毫秒正弦波（有声），再 silMs 毫秒静音。
func synthPCM(sr, toneMs, silMs int, amp float64) []int16 {
	tn := sr * toneMs / 1000
	sn := sr * silMs / 1000
	out := make([]int16, tn+sn)
	freq := 220.0
	for i := 0; i < tn; i++ {
		out[i] = int16(amp * 32767 * math.Sin(2*math.Pi*freq*float64(i)/float64(sr)))
	}
	// 后半段保持 0（静音）
	return out
}

// 起一个内置 ws 服务，用 mock ASR + mock LLM 跑通编排，验证事件序列。
func TestPipelineMock(t *testing.T) {
	cfg := loadConfig()
	cfg.ASRProvider = "mock"
	cfg.LLMProvider = "mock"
	cfg.SpeakerSidecarURL = "" // Noop：一律面试官

	asrFactory := makeASRFactory(cfg)
	llm := makeLLM(cfg)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade: %v", err)
			return
		}
		defer conn.Close()
		NewSession(cfg, conn, asrFactory, llm).Run()
	}))
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws"
	c, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer c.Close()

	// 设为口语化模式
	_ = c.WriteJSON(map[string]any{"type": "set_mode", "mode": "conversation"})

	// 喂入：1.2s 有声（应被识别为一句）+ 1.0s 静音（触发句尾端点）
	pcm := synthPCM(cfg.SampleRate, 1200, 1000, 0.3)
	// 分块发送，模拟流式（每 200ms 一块 = 3200 样本）
	chunk := cfg.SampleRate / 5
	for i := 0; i < len(pcm); i += chunk {
		end := i + chunk
		if end > len(pcm) {
			end = len(pcm)
		}
		if err := c.WriteMessage(websocket.BinaryMessage, pcmToBytes(pcm[i:end])); err != nil {
			t.Fatalf("write audio: %v", err)
		}
	}

	// 读事件，收集到 answer_done 为止
	c.SetReadDeadline(time.Now().Add(15 * time.Second))
	var (
		gotTranscript bool
		gotQuestion   bool
		gotDelta      bool
		gotDone       bool
		deltaBuf      strings.Builder
		doneTiming    map[string]any
	)

	for !gotDone {
		_, data, err := c.ReadMessage()
		if err != nil {
			t.Fatalf("read: %v (transcript=%v question=%v delta=%v)", err, gotTranscript, gotQuestion, gotDelta)
		}
		var m map[string]any
		if err := json.Unmarshal(data, &m); err != nil {
			continue
		}
		switch m["type"] {
		case "status":
			t.Logf("status: %v", m["state"])
		case "partial":
			t.Logf("partial: %v", m["text"])
		case "transcript":
			gotTranscript = true
			t.Logf("transcript: speaker=%v text=%v", m["speaker"], m["text"])
			if m["speaker"] != "interviewer" {
				t.Errorf("expected interviewer (Noop speaker), got %v", m["speaker"])
			}
		case "question":
			gotQuestion = true
			t.Logf("question: %v", m["text"])
		case "answer_delta":
			gotDelta = true
			if s, ok := m["text"].(string); ok {
				deltaBuf.WriteString(s)
			}
		case "answer_done":
			gotDone = true
			doneTiming, _ = m["timing"].(map[string]any)
		}
	}

	// 顺序断言
	if !gotTranscript {
		t.Fatal("missing transcript event")
	}
	if !gotQuestion {
		t.Fatal("missing question event")
	}
	if !gotDelta {
		t.Fatal("missing answer_delta event")
	}
	if !gotDone {
		t.Fatal("missing answer_done event")
	}
	if deltaBuf.Len() == 0 {
		t.Fatal("answer text is empty")
	}
	if doneTiming == nil {
		t.Fatal("answer_done missing timing")
	}
	for _, k := range []string{"endpoint_ms", "speaker_ms", "asr_ms", "llm_ttft_ms",
		"to_first_word_ms", "perceived_first_word_ms", "llm_total_ms"} {
		if _, ok := doneTiming[k]; !ok {
			t.Errorf("timing missing key %s", k)
		}
	}
	t.Logf("PASS: transcript -> question -> answer_delta(%d chars) -> answer_done; timing=%v",
		deltaBuf.Len(), doneTiming)
}

// 验证 buildPrompt 注入简历/公司文本
func TestBuildPromptContext(t *testing.T) {
	sys, usr := buildPrompt("conversation", "请自我介绍", "我做过推荐系统", "字节跳动")
	// user 应包含原始问题，并明确标注为"面试官的提问"、要求以求职者身份作答
	if !strings.Contains(usr, "请自我介绍") {
		t.Errorf("user missing question: %q", usr)
	}
	if !strings.Contains(usr, "面试官") || !strings.Contains(usr, "求职者") {
		t.Errorf("user missing interviewer/candidate framing: %q", usr)
	}
	if !strings.Contains(sys, "我做过推荐系统") || !strings.Contains(sys, "字节跳动") {
		t.Errorf("context not injected: %q", sys)
	}
	if !strings.Contains(sys, "参考资料") {
		t.Errorf("missing 参考资料 marker")
	}

	sys2, _ := buildPrompt("structured", "组织一次调研", "", "")
	if !strings.Contains(sys2, "结构化") {
		t.Errorf("structured system prompt missing")
	}
	if strings.Contains(sys2, "参考资料") {
		t.Errorf("should not have 参考资料 when no context")
	}
}

// 验证环形缓冲按毫秒切片
func TestRingBufferSlice(t *testing.T) {
	rb := newRingBuffer(16000, 1) // cap=16000
	// 写 16000 个样本（1s），值 = 索引
	pcm := make([]int16, 16000)
	for i := range pcm {
		pcm[i] = int16(i % 100)
	}
	rb.write(pcm)
	seg := rb.slice(100, 200) // 100ms-200ms -> 样本 1600-3200
	if len(seg) != 1600 {
		t.Errorf("expected 1600 samples, got %d", len(seg))
	}
}
