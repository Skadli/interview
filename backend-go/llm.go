package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"
)

// LLM：流式生成。Stream 把增量文本逐块投给 onDelta，结束返回 nil。
type LLM interface {
	Stream(ctx context.Context, model, system, user string, onDelta func(string)) error
}

// ====================== Mock LLM ======================

type MockLLM struct{}

func (MockLLM) Stream(ctx context.Context, model, system, user string, onDelta func(string)) error {
	// 模拟首字延迟
	select {
	case <-time.After(200 * time.Millisecond):
	case <-ctx.Done():
		return ctx.Err()
	}

	var answer string
	if strings.Contains(system, "结构化") {
		answer = "【思路】本题为综合分析题，按表态—分析—对策展开。" +
			"一是要正确看待这一现象，既看到其客观成因，也认清其潜在影响。" +
			"二是深入剖析原因，从个人、社会、制度多层面分析。" +
			"三是提出切实可行的对策，多方协同、标本兼治。"
	} else {
		answer = "我的核心思路是先给结论再补理由。结合我过往的项目经历，我会主动沟通、快速对齐目标，" +
			"用结果说话。这样既能解决眼前问题，也能积累长期信任。"
	}

	for _, r := range answer {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(12 * time.Millisecond):
		}
		onDelta(string(r))
	}
	return nil
}

// ====================== Ark（豆包，OpenAI 兼容） ======================

type ArkLLM struct {
	baseURL    string
	apiKey     string
	cli        *http.Client
	useContext bool // 启用上下文缓存（需用推理接入点 ep-xxx，否则自动回退普通模式）

	mu    sync.Mutex
	ctxID map[string]ctxEntry // key = model + "\x00" + system  ->  context_id
}

type ctxEntry struct {
	id string
	at time.Time
}

// 本地缓存有效期（略小于服务端 ttl=3600s，过期后重建）
const ctxLocalTTL = 50 * time.Minute

func NewArkLLM(baseURL, apiKey string, useContext bool) *ArkLLM {
	return &ArkLLM{
		baseURL:    strings.TrimRight(baseURL, "/"),
		apiKey:     apiKey,
		cli:        &http.Client{Timeout: 60 * time.Second},
		useContext: useContext,
		ctxID:      map[string]ctxEntry{},
	}
}

type arkMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}
type arkThinking struct {
	Type string `json:"type"`
}

type arkStreamChunk struct {
	Choices []struct {
		Delta struct {
			Content string `json:"content"`
		} `json:"delta"`
	} `json:"choices"`
}

func (a *ArkLLM) Stream(ctx context.Context, model, system, user string, onDelta func(string)) error {
	// 上下文缓存：把固定前缀(system，含简历/公司)缓存为 context，每个问题只发增量，压低首字延迟。
	// 需要用推理接入点(ep-xxx)；任何失败都自动回退普通模式，保证可用。
	if a.useContext {
		if id, err := a.ensureContext(ctx, model, system); err == nil && id != "" {
			body := map[string]any{
				"model":      model,
				"context_id": id,
				"messages":   []arkMessage{{Role: "user", Content: user}},
				"stream":     true,
				"thinking":   arkThinking{Type: "disabled"},
			}
			if e := a.streamReq(ctx, a.baseURL+"/context/chat/completions", body, onDelta); e == nil {
				return nil
			} else {
				log.Printf("[ark] 上下文 chat 失败，回退普通: %v", e)
			}
		} else if err != nil {
			log.Printf("[ark] 上下文缓存不可用(需 ep 接入点)，回退普通: %v", err)
		}
	}
	body := map[string]any{
		"model": model,
		"messages": []arkMessage{
			{Role: "system", Content: system},
			{Role: "user", Content: user},
		},
		"stream":   true,
		"thinking": arkThinking{Type: "disabled"},
	}
	return a.streamReq(ctx, a.baseURL+"/chat/completions", body, onDelta)
}

// ensureContext：按 (model+system) 复用 context_id；不存在或过期则创建。
func (a *ArkLLM) ensureContext(ctx context.Context, model, system string) (string, error) {
	key := model + "\x00" + system
	a.mu.Lock()
	if e, ok := a.ctxID[key]; ok && time.Since(e.at) < ctxLocalTTL {
		id := e.id
		a.mu.Unlock()
		return id, nil
	}
	a.mu.Unlock()

	id, err := a.createContext(ctx, model, system)
	if err != nil {
		return "", err
	}
	a.mu.Lock()
	a.ctxID[key] = ctxEntry{id: id, at: time.Now()}
	a.mu.Unlock()
	return id, nil
}

func (a *ArkLLM) createContext(ctx context.Context, model, system string) (string, error) {
	body := map[string]any{
		"model":    model,
		"mode":     "common_prefix",
		"messages": []arkMessage{{Role: "system", Content: system}},
		"ttl":      3600,
	}
	jb, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, "POST", a.baseURL+"/context/create", bytes.NewReader(jb))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+a.apiKey)

	resp, err := a.cli.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	buf := new(bytes.Buffer)
	_, _ = buf.ReadFrom(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("context create http %d: %s", resp.StatusCode, buf.String())
	}
	var r struct {
		ID        string `json:"id"`
		ContextID string `json:"context_id"`
	}
	if e := json.Unmarshal(buf.Bytes(), &r); e != nil {
		return "", e
	}
	if r.ID != "" {
		return r.ID, nil
	}
	return r.ContextID, nil
}

// streamReq：发起一次 SSE 流式请求，逐块投给 onDelta。
func (a *ArkLLM) streamReq(ctx context.Context, url string, body any, onDelta func(string)) error {
	jb, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(jb))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+a.apiKey)
	req.Header.Set("Accept", "text/event-stream")

	resp, err := a.cli.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		buf := new(bytes.Buffer)
		_, _ = buf.ReadFrom(resp.Body)
		return fmt.Errorf("ark http %d: %s", resp.StatusCode, buf.String())
	}

	reader := bufio.NewReader(resp.Body)
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		line, err := reader.ReadString('\n')
		if len(line) > 0 {
			line = strings.TrimRight(line, "\r\n")
			if strings.HasPrefix(line, "data:") {
				data := strings.TrimSpace(line[len("data:"):])
				if data == "[DONE]" {
					return nil
				}
				if data != "" {
					var chunk arkStreamChunk
					if e := json.Unmarshal([]byte(data), &chunk); e == nil {
						for _, c := range chunk.Choices {
							if c.Delta.Content != "" {
								onDelta(c.Delta.Content)
							}
						}
					}
				}
			}
		}
		if err != nil {
			return nil
		}
	}
}
