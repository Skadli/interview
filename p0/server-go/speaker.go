package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"time"
)

// Speaker：说话人验证。Verify 返回 (是否本人, 相似度)。
type Speaker interface {
	Enabled() bool
	Enroll(pcm []int16) bool
	Verify(pcm []int16) (bool, float64)
}

// NoopSpeaker：未配置声纹时使用——一律判为面试官（不分离）。
type NoopSpeaker struct{}

func (NoopSpeaker) Enabled() bool              { return false }
func (NoopSpeaker) Enroll([]int16) bool        { return false }
func (NoopSpeaker) Verify([]int16) (bool, float64) { return false, 0 }

// SidecarSpeaker：调用 Python 声纹服务（本地 HTTP）。
type SidecarSpeaker struct {
	url string
	cli *http.Client
}

func NewSidecarSpeaker(url string) *SidecarSpeaker {
	return &SidecarSpeaker{url: url, cli: &http.Client{Timeout: 3 * time.Second}}
}

func (s *SidecarSpeaker) Enabled() bool { return true }

func (s *SidecarSpeaker) call(path string, pcm []int16) (map[string]any, error) {
	req, err := http.NewRequest("POST", s.url+path, bytes.NewReader(pcmToBytes(pcm)))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	resp, err := s.cli.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var m map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&m)
	return m, nil
}

func (s *SidecarSpeaker) Enroll(pcm []int16) bool {
	m, err := s.call("/enroll", pcm)
	if err != nil {
		return false
	}
	ok, _ := m["ok"].(bool)
	return ok
}

func (s *SidecarSpeaker) Verify(pcm []int16) (bool, float64) {
	m, err := s.call("/verify", pcm)
	if err != nil {
		return false, 0
	}
	isUser, _ := m["is_user"].(bool)
	sim, _ := m["similarity"].(float64)
	return isUser, sim
}

func makeSpeaker(cfg Config) Speaker {
	if cfg.SpeakerSidecarURL != "" {
		return NewSidecarSpeaker(cfg.SpeakerSidecarURL)
	}
	return NoopSpeaker{}
}
