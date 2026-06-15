package main

import (
	"os"
	"strconv"
)

type Config struct {
	Addr        string
	FrontendDir string

	ASRProvider string // mock | volc
	LLMProvider string // mock | ark

	// 火山大模型流式 ASR
	VolcAPIKey     string // 新版统一鉴权 X-Api-Key（优先）
	VolcAppKey     string // 旧版鉴权 X-Api-App-Key
	VolcAccessKey  string // 旧版鉴权 X-Api-Access-Key
	VolcResourceID string
	VolcASRURL     string

	// 火山方舟（豆包 1.6）
	ArkAPIKey   string
	ArkBaseURL  string
	ModelFast   string // 口语化：极速
	ModelStrong string // 结构化：重质量

	// 声纹 sidecar（Python）；为空则不分离（全部视为面试官）
	SpeakerSidecarURL string
	SpeakerThresh     float64

	// 上下文服务（简历/公司）；非空则把 /ctx/* 反代到这里（让手机单一同源，免 CORS/混合内容）
	ContextURL string

	SampleRate int
	EndpointMs int // 仅 mock 能量VAD用；火山模式端点由服务端决定
}

func env(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}
func envF(k string, d float64) float64 {
	if v := os.Getenv(k); v != "" {
		if f, e := strconv.ParseFloat(v, 64); e == nil {
			return f
		}
	}
	return d
}
func envI(k string, d int) int {
	if v := os.Getenv(k); v != "" {
		if n, e := strconv.Atoi(v); e == nil {
			return n
		}
	}
	return d
}

func loadConfig() Config {
	return Config{
		Addr:        env("ADDR", ":8000"),
		FrontendDir: env("FRONTEND_DIR", "../web/dist"),
		ASRProvider: env("ASR_PROVIDER", "mock"),
		LLMProvider: env("LLM_PROVIDER", "mock"),

		VolcAPIKey:     os.Getenv("VOLC_API_KEY"),
		VolcAppKey:     os.Getenv("VOLC_APP_KEY"),
		VolcAccessKey:  os.Getenv("VOLC_ACCESS_KEY"),
		VolcResourceID: env("VOLC_RESOURCE_ID", "volc.bigasr.sauc.duration"),
		VolcASRURL:     env("VOLC_ASR_URL", "wss://openspeech.bytedance.com/api/v3/sauc/bigmodel"),

		ArkAPIKey:   os.Getenv("ARK_API_KEY"),
		ArkBaseURL:  env("ARK_BASE_URL", "https://ark.cn-beijing.volces.com/api/v3"),
		ModelFast:   env("MODEL_FAST", "doubao-seed-2-0-mini-260428"),
		ModelStrong: env("MODEL_STRONG", "doubao-seed-2-0-mini-260428"),

		SpeakerSidecarURL: os.Getenv("SPEAKER_SIDECAR_URL"),
		SpeakerThresh:     envF("SPEAKER_THRESH", 0.75),

		ContextURL: os.Getenv("CONTEXT_URL"),

		SampleRate: 16000,
		EndpointMs: envI("ENDPOINT_MS", 650),
	}
}
