package app

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net/http"
	"strings"
	"time"

	"planeduel/internal/game"
)

//go:embed web
var webFS embed.FS

// 通道：同一个 handler 挂在三个入口上，用 context 标记来自哪个入口。
//   gateway —— 飞牛统一网关（已通过 NAS 登录校验）
//   admin   —— 本机回环管理口（故障恢复用）
//   port    —— 独立端口（对外，游客可直达）
type channelKey struct{}

const (
	chGateway = "gateway"
	chAdmin   = "admin"
	chPort    = "port"
)

func withChannel(h http.Handler, name string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), channelKey{}, name)))
	})
}

func channelOf(r *http.Request) string {
	if v, ok := r.Context().Value(channelKey{}).(string); ok {
		return v
	}
	return ""
}

type Server struct {
	cfg      *ConfigStore
	sessions *SessionStore
	solo     *soloStore
	hub      *Hub
	ports    *PortManager
	gateway  string // 网关前缀，例如 /app/planeduel；端口模式下为空
}

func NewServer(cfg *ConfigStore, sessions *SessionStore, solo *soloStore, hub *Hub, ports *PortManager, gatewayPrefix string) *Server {
	return &Server{cfg: cfg, sessions: sessions, solo: solo, hub: hub, ports: ports, gateway: gatewayPrefix}
}

// Handler 返回带网关前缀兼容的处理器：同一份路由同时服务
//   - 端口直连： /api/...
//   - 飞牛统一网关： /app/planeduel/api/...
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /api/healthz", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]any{"ok": true, "time": time.Now().UTC()})
	})

	mux.HandleFunc("GET /api/session", s.handleGetSession)
	mux.HandleFunc("POST /api/session", s.handleCreateSession)

	mux.HandleFunc("GET /api/admin/settings", s.adminOnly(s.handleGetSettings))
	mux.HandleFunc("PUT /api/admin/settings", s.adminOnly(s.handlePutSettings))

	mux.HandleFunc("GET /api/players", s.handlePlayers)

	mux.HandleFunc("POST /api/solo", s.handleSoloNew)
	mux.HandleFunc("POST /api/solo/fire", s.handleSoloFire)

	mux.HandleFunc("GET /ws", s.hub.HandleWS)

	sub, err := fs.Sub(webFS, "web")
	if err != nil {
		log.Fatalf("内嵌前端资源缺失: %v", err)
	}
	static := http.FileServer(http.FS(sub))
	// 升级后不能让浏览器用旧的前端（无 Cache-Control 时浏览器会启发式缓存）
	mux.Handle("GET /", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-cache, must-revalidate")
		static.ServeHTTP(w, r)
	}))

	prefix := s.gateway
	base := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if prefix != "" && strings.HasPrefix(r.URL.Path, prefix) {
			r2 := r.Clone(r.Context())
			r2.URL.Path = strings.TrimPrefix(r.URL.Path, prefix)
			if r2.URL.Path == "" {
				r2.URL.Path = "/"
			}
			mux.ServeHTTP(w, r2)
			return
		}
		mux.ServeHTTP(w, r)
	})
	return base
}

// Channeled 把同一套路由绑定到某个入口通道上。
func (s *Server) Channeled(channel string) http.Handler {
	h := withChannel(s.Handler(), channel)
	if channel == chGateway {
		h = s.gatewaySession(h)
	}
	return h
}

// gatewaySession：从飞牛网关进来时，身份固定为「管理员」——不读 NAS 账号名，
// 也不依赖任何请求头。只在 /api/session 上自动建档，避免静态资源请求也创建会话。
func (s *Server) gatewaySession(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimSuffix(r.URL.Path, "/")
		if r.Method != http.MethodGet || !strings.HasSuffix(path, "/api/session") {
			next.ServeHTTP(w, r)
			return
		}
		if _, ok := s.sessionFrom(r); ok {
			next.ServeHTTP(w, r)
			return
		}
		if sess, err := s.sessions.CreateAdmin(s.hub.TokenOnline); err == nil {
			http.SetCookie(w, &http.Cookie{
				Name: cookieNameFor(chGateway), Value: sess.Token, Path: "/",
				HttpOnly: true, SameSite: http.SameSiteLaxMode,
				MaxAge: int((180 * 24 * time.Hour).Seconds()),
			})
			r.AddCookie(&http.Cookie{Name: cookieNameFor(chGateway), Value: sess.Token})
		}
		next.ServeHTTP(w, r)
	})
}

// PortHandler 是「独立端口」入口专用的处理器：热重开监听时也必须用它，
// 否则通道标记会丢失，端口访问就能拿到管理权限。
func (s *Server) PortHandler() http.Handler {
	return withChannel(s.Handler(), chPort)
}

// ---------------------------------------------------------------- 会话

const cookieName = "pd_sid" // 管理通道（127.0.0.1）继续沿用

// cookieNameFor 按通道区分 Cookie 名。
// 关键：Cookie 不区分端口，如果网关和端口共用 pd_sid，同一个浏览器打开两边
// 就会拿到同一个身份（互相顶号），端口侧也就永远看不到「管理员」这个独立玩家。
func cookieNameFor(ch string) string {
	switch ch {
	case chGateway:
		return "pd_sid_gw"
	case chPort:
		return "pd_sid_port"
	default:
		return cookieName
	}
}

// sessionTokenFrom 依次从「本标签页 token」→ 查询参数 → 按通道隔离的 Cookie 里取身份。
// 支持 X-Session 是为了让同一台电脑的不同浏览器标签各自拥有独立身份。
func sessionTokenFrom(r *http.Request) string {
	if t := r.Header.Get("X-Session"); t != "" {
		return t
	}
	if t := r.URL.Query().Get("sid"); t != "" {
		return t
	}
	if c, err := r.Cookie(cookieNameFor(channelOf(r))); err == nil {
		return c.Value
	}
	return ""
}

func (s *Server) sessionFrom(r *http.Request) (*Session, bool) {
	tok := sessionTokenFrom(r)
	if tok == "" {
		return nil, false
	}
	return s.sessions.Get(tok)
}

func (s *Server) handleGetSession(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.sessionFrom(r)
	resp := map[string]any{
		"channel":   channelOf(r),
		"can_admin": s.isAdmin(r),
	}
	if !ok {
		resp["nick"] = ""
		resp["authenticated"] = false
		writeJSON(w, 200, resp)
		return
	}
	resp["nick"] = sess.Nick
	resp["authenticated"] = true
	resp["token"] = sess.Token // 前端存到 sessionStorage，实现「一个标签一个身份」
	writeJSON(w, 200, resp)
}

func (s *Server) handleCreateSession(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Nick string `json:"nick"`
	}
	if err := readJSON(r, &req); err != nil {
		fail(w, 400, err.Error())
		return
	}
	var sess *Session
	var err error
	switch {
	case strings.TrimSpace(req.Nick) == "":
		// 没填昵称（外网访客）：自动分配「打飞机 + 随机后缀」
		sess, err = s.sessions.CreateGuest()
	default:
		// 已经有身份就改名（保留 token，避免留下占位的僵尸昵称）
		if cur, ok := s.sessionFrom(r); ok {
			sess, err = s.sessions.Rename(cur.Token, req.Nick)
		} else {
			sess, err = s.sessions.Create(req.Nick)
		}
	}
	if errors.Is(err, ErrNickTaken) {
		fail(w, 409, err.Error())
		return
	}
	if err != nil {
		fail(w, 400, err.Error())
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name: cookieNameFor(channelOf(r)), Value: sess.Token, Path: "/",
		HttpOnly: true, SameSite: http.SameSiteLaxMode,
		MaxAge: int((180 * 24 * time.Hour).Seconds()),
	})
	writeJSON(w, 200, map[string]any{"nick": sess.Nick, "authenticated": true, "token": sess.Token})
}

// ---------------------------------------------------------------- 管理（端口开关）

func (s *Server) adminOnly(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if channelOf(r) == chPort {
			// 独立端口是对外通道，永远不给管理能力（前端也不展示面板）
			fail(w, 403, "设置面板只能在飞牛网关或本机管理入口使用")
			return
		}
		if !s.isAdmin(r) {
			fail(w, 403, "需要管理员身份（飞牛网关管理员，或有效的管理口令）")
			return
		}
		next(w, r)
	}
}

// isAdmin 管理权限只由「入口」决定，不再有口令：
//   - 飞牛网关：登录态即管理员
//   - 本机回环 127.0.0.1：相当于已在本机（只有 NAS 自身能访问）
//   - 独立端口（对外）：永远不是管理员
func (s *Server) isAdmin(r *http.Request) bool {
	return channelOf(r) != chPort
}

type settingsResp struct {
	Enabled    bool       `json:"public_port_enabled"`
	Port       int        `json:"public_port"`
	Status     PortStatus `json:"status"`
	Gateway    bool       `json:"gateway_mode"`
	SwitchedFrom int      `json:"switched_from,omitempty"` // 因占用而换端口时，原端口
	Notice       string   `json:"notice,omitempty"`        // 给面板看的提示文案

	Sessions   int        `json:"sessions"`
	Rules      game.Rules `json:"rules"`
}

func (s *Server) handleGetSettings(w http.ResponseWriter, r *http.Request) {
	cfg := s.cfg.Get()
	writeJSON(w, 200, settingsResp{
		Enabled: cfg.PublicPortEnabled, Port: cfg.PublicPort,
		Status: s.ports.Status(), Gateway: s.gateway != "",
		Sessions: s.sessions.Count(),
		Rules: game.DefaultRules(),
	})
}

func (s *Server) handlePutSettings(w http.ResponseWriter, r *http.Request) {
	var req struct {
		PublicPortEnabled *bool `json:"public_port_enabled"`
		PublicPort        *int  `json:"public_port"`
	}
	if err := readJSON(r, &req); err != nil {
		fail(w, 400, err.Error())
		return
	}
	if req.PublicPort != nil && (*req.PublicPort < 1 || *req.PublicPort > 65535) {
		fail(w, 400, "端口号必须在 1-65535 之间")
		return
	}
	cfg, err := s.cfg.Update(func(c *Config) error {
		if req.PublicPortEnabled != nil {
			c.PublicPortEnabled = *req.PublicPortEnabled
		}
		if req.PublicPort != nil {
			c.PublicPort = *req.PublicPort
		}
		return nil
	})
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	status, _, switched, notice := s.applyPortConfig(cfg.PublicPortEnabled, cfg.PublicPort)
	cfg = s.cfg.Get() // 端口可能被顺延，开关可能被回滚
	writeJSON(w, 200, settingsResp{
		Enabled: cfg.PublicPortEnabled, Port: cfg.PublicPort,
		Status: status, Gateway: s.gateway != "",
		Sessions: s.sessions.Count(), Rules: game.DefaultRules(),
		SwitchedFrom: switched, Notice: notice,
	})
}

// applyPortConfig 按配置开启端口：被占用时自动顺延，实在找不到就回滚为关闭。
// 返回监听状态、实际端口、原端口（0 表示没换）、以及给面板的提示文案。
func (s *Server) applyPortConfig(enabled bool, port int) (PortStatus, int, int, string) {
	if !enabled {
		s.ports.Apply(false, port, nil)
		return s.ports.Status(), port, 0, ""
	}

	actual, switched, err := s.ports.ApplyWithFallback(true, port, s.PortHandler(), fallbackAttempts)
	if err == nil {
		if switched > 0 {
			_, _ = s.cfg.Update(func(c *Config) error {
				c.PublicPort = actual
				return nil
			})
			return s.ports.Status(), actual, switched,
				fmt.Sprintf("端口 %d 已被占用，已自动改用 %d", switched, actual)
		}
		return s.ports.Status(), actual, 0, ""
	}

	// 彻底失败：把开关回滚成关闭，避免面板显示「已开启」却没在监听
	s.ports.Apply(false, port, nil)
	_, _ = s.cfg.Update(func(c *Config) error {
		c.PublicPortEnabled = false
		return nil
	})
	return s.ports.Status(), port, 0, err.Error() + "；对外访问已自动关闭"
}

// ApplyPortConfig 供启动时调用：按当前配置开启端口（同样带顺延与回滚）。
func (s *Server) ApplyPortConfig() (PortStatus, int, int, string) {
	cfg := s.cfg.Get()
	return s.applyPortConfig(cfg.PublicPortEnabled, cfg.PublicPort)
}

// ---------------------------------------------------------------- 在线玩家

func (s *Server) handlePlayers(w http.ResponseWriter, r *http.Request) {
	me := ""
	if sess, ok := s.sessionFrom(r); ok {
		me = sess.Nick
	}
	writeJSON(w, 200, map[string]any{
		"players": s.sessions.List(5*time.Minute, me),
		"total":   s.sessions.Count(),
	})
}

// ---------------------------------------------------------------- 单人视角对局

func (s *Server) handleSoloNew(w http.ResponseWriter, r *http.Request) {
	me := "玩家"
	if sess, ok := s.sessionFrom(r); ok {
		me = sess.Nick
	}
	solo, err := s.solo.New(game.DefaultRules(), me)
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, solo.view())
}

func (s *Server) handleSoloFire(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID   string `json:"id"`
		Cell string `json:"cell"`
	}
	if err := readJSON(r, &req); err != nil {
		fail(w, 400, err.Error())
		return
	}
	solo, ok := s.solo.Get(req.ID)
	if !ok {
		fail(w, 404, errMatchNotFound.Error())
		return
	}
	cell, err := game.ParseCell(strings.TrimSpace(req.Cell))
	if err != nil {
		fail(w, 400, err.Error())
		return
	}
	view, err := solo.Fire(cell)
	if errors.Is(err, errFinished) {
		fail(w, 409, err.Error())
		return
	}
	if err != nil {
		code := 400
		if errors.Is(err, game.ErrNotYourTurn) || errors.Is(err, game.ErrRepeated) {
			code = 409
		}
		fail(w, code, err.Error())
		return
	}
	writeJSON(w, 200, view)
}

// ---------------------------------------------------------------- 工具

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
}

func fail(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

func readJSON(r *http.Request, v any) error {
	defer r.Body.Close()
	b, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		return err
	}
	if len(strings.TrimSpace(string(b))) == 0 {
		return nil
	}
	return json.Unmarshal(b, v)
}

// ReapIdleSessions 定期清理「离线超过 grace 且不在对局中」的会话，
// 管理员与游客昵称都会释放；正在对局/观战的不动。
func (s *Server) ReapIdleSessions(grace time.Duration) int {
	return s.sessions.ReapIdleSessions(grace, s.hub.TokenOnline, s.hub.TokenBusy)
}