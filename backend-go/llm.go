package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
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
	baseURL string
	apiKey  string
	cli     *http.Client
}

func NewArkLLM(baseURL, apiKey string) *ArkLLM {
	return &ArkLLM{
		baseURL: strings.TrimRight(baseURL, "/"),
		apiKey:  apiKey,
		cli:     &http.Client{Timeout: 60 * time.Second},
	}
}

type arkReq struct {
	Model    string       `json:"model"`
	Messages []arkMessage `json:"messages"`
	Stream   bool         `json:"stream"`
	Thinking arkThinking  `json:"thinking"`
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
	body := arkReq{
		Model: model,
		Messages: []arkMessage{
			{Role: "system", Content: system},
			{Role: "user", Content: user},
		},
		Stream:   true,
		Thinking: arkThinking{Type: "disabled"},
	}
	jb, _ := json.Marshal(body)

	req, err := http.NewRequestWithContext(ctx, "POST", a.baseURL+"/chat/completions", bytes.NewReader(jb))
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
				if data == "" {
					continue
				}
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
		if err != nil {
			// io.EOF 或其他：结束
			return nil
		}
	}
}
