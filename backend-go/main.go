package main

import (
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1 << 16,
	WriteBufferSize: 1 << 16,
	CheckOrigin:     func(r *http.Request) bool { return true }, // 全放行
}

// 构造一条 ASR 流：mock 直接构造；volc 拨号建连。
func makeASRFactory(cfg Config) ASRFactory {
	if cfg.ASRProvider == "volc" {
		return func() (ASRStream, error) {
			return NewVolcASR(cfg)
		}
	}
	return func() (ASRStream, error) {
		return NewMockASR(cfg.SampleRate), nil
	}
}

func makeLLM(cfg Config) LLM {
	if cfg.LLMProvider == "ark" {
		return NewArkLLM(cfg.ArkBaseURL, cfg.ArkAPIKey)
	}
	return MockLLM{}
}

func main() {
	log.SetFlags(log.LstdFlags | log.Lmicroseconds)
	cfg := loadConfig()
	asrFactory := makeASRFactory(cfg)
	llm := makeLLM(cfg)

	mux := http.NewServeMux()

	// /ws：升级为 WebSocket，每连接一个 Session
	mux.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			log.Printf("upgrade err: %v", err)
			return
		}
		defer conn.Close()
		sess := NewSession(cfg, conn, asrFactory, llm)
		log.Printf("[ws] new session %s", sess.sid[:8])
		sess.Run()
		log.Printf("[ws] session %s closed", sess.sid[:8])
	})

	// 健康检查
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	// /ctx/* 反代到上下文服务（简历/公司），让手机在单一同源(HTTPS)下访问，避免 CORS/混合内容
	if cfg.ContextURL != "" {
		if target, err := url.Parse(cfg.ContextURL); err == nil {
			rp := httputil.NewSingleHostReverseProxy(target)
			mux.HandleFunc("/ctx/", func(w http.ResponseWriter, r *http.Request) {
				r.URL.Path = strings.TrimPrefix(r.URL.Path, "/ctx")
				if r.URL.Path == "" {
					r.URL.Path = "/"
				}
				rp.ServeHTTP(w, r)
			})
			log.Printf("context proxy : /ctx/* -> %s", cfg.ContextURL)
		} else {
			log.Printf("context proxy : 无效 CONTEXT_URL=%s: %v", cfg.ContextURL, err)
		}
	}

	// 静态前端
	indexPath := filepath.Join(cfg.FrontendDir, "index.html")
	fs := http.FileServer(http.Dir(cfg.FrontendDir))
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			if _, err := os.Stat(indexPath); err == nil {
				http.ServeFile(w, r, indexPath)
				return
			}
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			_, _ = w.Write([]byte("interview-backend running. 前端未找到（FRONTEND_DIR=" +
				cfg.FrontendDir + "）。WebSocket: /ws"))
			return
		}
		fs.ServeHTTP(w, r)
	})

	log.Printf("==== interview-backend ====")
	log.Printf("listen        : %s", cfg.Addr)
	log.Printf("asr provider  : %s (url=%s)", cfg.ASRProvider, cfg.VolcASRURL)
	log.Printf("llm provider  : %s (fast=%s strong=%s)", cfg.LLMProvider, cfg.ModelFast, cfg.ModelStrong)
	if cfg.SpeakerSidecarURL != "" {
		log.Printf("speaker       : sidecar %s (thresh=%.2f)", cfg.SpeakerSidecarURL, cfg.SpeakerThresh)
	} else {
		log.Printf("speaker       : noop (未配置声纹，全部判面试官)")
	}
	log.Printf("frontend dir  : %s", cfg.FrontendDir)
	log.Printf("endpoint_ms   : %d", cfg.EndpointMs)

	if err := http.ListenAndServe(cfg.Addr, mux); err != nil {
		log.Fatalf("listen err: %v", err)
	}
}
