package main

import (
	"embed"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"

	"github.com/gorilla/websocket"
)

//go:embed web/index.html
var staticFS embed.FS

var upgrader = websocket.Upgrader{
	// 仅供本机浏览器访问，放开 Origin 校验。
	CheckOrigin: func(r *http.Request) bool { return true },
}

// server 持有配置并对外提供 HTTP/WebSocket 接口。
// 配置（含 SSH/MySQL/Redis 凭据）固定来自 config.yaml，界面不可见、不可改。
type server struct {
	cfg      *Config
	deployMu sync.Mutex // 同一时刻只允许一次发布（共享全局 SSH 隧道）
}

func newServer(cfg *Config) *server {
	return &server{cfg: cfg}
}

func (s *server) routes() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handleIndex)
	mux.HandleFunc("/api/meta", s.handleMeta)
	mux.HandleFunc("/ws", s.handleWS)
	mux.HandleFunc("/logs", s.handleLogs)
	return mux
}

func (s *server) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	data, err := staticFS.ReadFile("web/index.html")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(data)
}

// handleMeta 只返回界面渲染所需的非敏感信息（服列表），绝不暴露任何凭据。
func (s *server) handleMeta(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"always":     s.cfg.Servers.Always,
		"selectable": s.cfg.Servers.Selectable,
	})
}

// wsMsg 是服务端推给前端的消息。
type wsMsg struct {
	Type string `json:"type"` // log | done | error
	Msg  string `json:"msg,omitempty"`
}

// clientMsg 是前端发起发布时的消息：界面只提供 tag 与勾选清档的服。
type clientMsg struct {
	Action   string `json:"action"`
	Tag      string `json:"tag"`
	Selected []int  `json:"selected"`
}

func (s *server) handleWS(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()

	var in clientMsg
	if err := conn.ReadJSON(&in); err != nil {
		return
	}
	if in.Action != "start" {
		conn.WriteJSON(wsMsg{Type: "error", Msg: "未知请求"})
		return
	}

	// 串行化写 WebSocket（runDeploy 内部多 goroutine 回传日志）。
	var writeMu sync.Mutex
	send := func(m wsMsg) {
		writeMu.Lock()
		conn.WriteJSON(m)
		writeMu.Unlock()
	}
	logFn := func(line string) { send(wsMsg{Type: "log", Msg: line}) }

	// 防止并发发布。
	if !s.deployMu.TryLock() {
		send(wsMsg{Type: "error", Msg: "已有发布任务正在执行，请稍候"})
		return
	}
	defer s.deployMu.Unlock()

	if err := runDeploy(s.cfg, in.Tag, in.Selected, logFn); err != nil {
		send(wsMsg{Type: "error", Msg: err.Error()})
		return
	}
	send(wsMsg{Type: "done"})
}

// logReq 是前端发起查看日志时的首条消息。
type logReq struct {
	Server int `json:"server"`
}

// handleLogs 通过专用 SSH 连接实时跟踪某游戏服的 kubectl 日志。
// 浏览器关闭或切换服时 WebSocket 断开 → 关闭 SSH 连接 → 远端 kubectl 终止，不留残留。
func (s *server) handleLogs(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()

	var in logReq
	if err := conn.ReadJSON(&in); err != nil {
		return
	}

	var writeMu sync.Mutex
	send := func(m wsMsg) {
		writeMu.Lock()
		conn.WriteJSON(m)
		writeMu.Unlock()
	}

	if !containsInt(s.cfg.Servers.Always, in.Server) {
		send(wsMsg{Type: "error", Msg: fmt.Sprintf("不允许查看服 %d 的日志", in.Server)})
		return
	}
	logFn := func(line string) { send(wsMsg{Type: "log", Msg: line}) }

	send(wsMsg{Type: "log", Msg: fmt.Sprintf("📜 正在连接并跟踪 %d 的日志……", in.Server)})

	client, err := dialSSH(s.cfg)
	if err != nil {
		send(wsMsg{Type: "error", Msg: err.Error()})
		return
	}
	defer client.Close()

	// 持续读取以探测 WebSocket 关闭；一旦关闭就切断 SSH 连接，终止远端 kubectl logs -f。
	go func() {
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				client.Close()
				return
			}
		}
	}()

	if err := streamServerLogs(client, s.cfg, in.Server, logFn); err != nil {
		send(wsMsg{Type: "error", Msg: err.Error()})
		return
	}
	send(wsMsg{Type: "done"})
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}
