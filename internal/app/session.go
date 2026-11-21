package app

import (
	crand "crypto/rand"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"
)

var (
	ErrNickTaken   = errors.New("这个昵称已经有人用了，换一个吧")
	ErrNickInvalid = errors.New("昵称只能 2-12 个字，可用中英文、数字、下划线和短横线")
)

var reservedNicks = map[string]bool{
	"admin": true, "administrator": true, "root": true, "system": true, "sys": true,
	"管理员": true, "系统": true, "官方": true, "客服": true, "匿名": true, "游客": true,
}

// Session 是一个游客身份：昵称全局唯一，靠 Cookie 里的 token 认人。
type Session struct {
	Token   string
	Nick    string
	key     string // 归一化后的唯一键
	Created time.Time
	Seen    time.Time
}

// SessionStore 负责昵称占位与过期释放（防止昵称被永久占用）。
type SessionStore struct {
	mu     sync.Mutex
	ttl    time.Duration
	byTok  map[string]*Session
	byKey  map[string]*Session
	byFnos map[string]*Session // 飞牛用户名 → 会话（网关自动登录复用同一个身份）
}

func NewSessionStore(ttl time.Duration) *SessionStore {
	if ttl <= 0 {
		ttl = 30 * 24 * time.Hour
	}
	return &SessionStore{
		ttl:     ttl,
		byTok:   map[string]*Session{},
		byKey:   map[string]*Session{},
		byFnos:  map[string]*Session{},
	}
}

// NormalizeNick 归一化昵称，返回展示名与唯一键。
func NormalizeNick(raw string) (string, string, error) { return normalizeNick(raw, false) }

// normalizeNick 在 allowReserved=true 时允许使用保留词（网关固定昵称用）。
func normalizeNick(raw string, allowReserved bool) (string, string, error) {
	nick := strings.TrimSpace(raw)
	if n := utf8.RuneCountInString(nick); n < 2 || n > 12 {
		return "", "", ErrNickInvalid
	}
	for _, r := range nick {
		ok := unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == '-'
		if !ok {
			return "", "", ErrNickInvalid
		}
	}
	key := strings.ToLower(nick)
	if !allowReserved && reservedNicks[key] {
		return "", "", ErrNickInvalid
	}
	return nick, key, nil
}

func (s *SessionStore) gcLocked() {
	cut := time.Now().Add(-s.ttl)
	for tok, sess := range s.byTok {
		if sess.Seen.Before(cut) {
			delete(s.byTok, tok)
			if s.byKey[sess.key] == sess {
				delete(s.byKey, sess.key)
			}
			for fn, fs := range s.byFnos {
				if fs == sess {
					delete(s.byFnos, fn)
				}
			}
		}
	}
}

// createLocked 假定已持有锁。
func (s *SessionStore) createLocked(nick string) (*Session, error) {
	disp, key, err := NormalizeNick(nick)
	if err != nil {
		return nil, err
	}
	if _, taken := s.byKey[key]; taken {
		return nil, ErrNickTaken
	}
	now := time.Now()
	sess := &Session{Token: RandomToken(24), Nick: disp, key: key, Created: now, Seen: now}
	s.byTok[sess.Token] = sess
	s.byKey[key] = sess
	return sess, nil
}

func (s *SessionStore) Create(nick string) (*Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.gcLocked()
	return s.createLocked(nick)
}

func (s *SessionStore) Get(token string) (*Session, bool) {
	if token == "" {
		return nil, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.byTok[token]
	if !ok {
		return nil, false
	}
	sess.Seen = time.Now()
	cp := *sess
	return &cp, true
}

func (s *SessionStore) Rename(token, nick string) (*Session, error) {
	disp, key, err := NormalizeNick(nick)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.gcLocked()
	sess, ok := s.byTok[token]
	if !ok {
		return nil, errors.New("会话已过期，请重新设置昵称")
	}
	if owner, taken := s.byKey[key]; taken && owner != sess {
		return nil, ErrNickTaken
	}
	if s.byKey[sess.key] == sess {
		delete(s.byKey, sess.key)
	}
	sess.Nick, sess.key, sess.Seen = disp, key, time.Now()
	s.byKey[key] = sess
	cp := *sess
	return &cp, nil
}

func (s *SessionStore) Count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.gcLocked()
	return len(s.byTok)
}

// sanitizeNick 把飞牛用户名清洗成合法昵称（去掉非法字符、限长、兜底）。
func sanitizeNick(name string) string {
	var b []rune
	for _, r := range strings.TrimSpace(name) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == '-' {
			b = append(b, r)
		}
	}
	if len(b) > 10 {
		b = b[:10]
	}
	if len(b) < 2 {
		return "飞牛用户"
	}
	return string(b)
}

// Ensure 为飞牛网关用户准备身份：昵称默认取 NAS 用户名，被占用时自动加后缀。
// 同一个 NAS 用户重复访问会复用同一个会话（不依赖浏览器 Cookie 也能识别）。
func (s *SessionStore) Ensure(fnosName string) (*Session, error) {
	base := sanitizeNick(fnosName)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.gcLocked()

	if sess, ok := s.byFnos[base]; ok {
		sess.Seen = time.Now()
		cp := *sess
		return &cp, nil
	}
	for i := 0; i < 20; i++ {
		cand := base
		if i > 0 {
			cand = fmt.Sprintf("%s-%d", base, i+1)
		}
		sess, err := s.createLocked(cand)
		if err == nil {
			s.byFnos[base] = sess
			return sess, nil
		}
		if !errors.Is(err, ErrNickTaken) {
			return nil, err
		}
	}
	return nil, ErrNickTaken
}

// AdminNick 是飞牛网关进来的固定昵称（用户仍可自行改名）。
const AdminNick = "管理员"

// CreateAdmin 给网关用户固定的「管理员」昵称（不关心是哪个 NAS 账号）。
// 同一浏览器靠 Cookie 复用会话；另一个浏览器进来会得到「管理员2」。
func (s *SessionStore) CreateAdmin(online func(token string) bool) (*Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.gcLocked()

	// 只有一个网关管理员：旧的管理员会话如果不在线，就直接复用它，
	// 避免每开一个新浏览器就变成「管理员2」「管理员3」。
	if existing, ok := s.byKey[strings.ToLower(AdminNick)]; ok {
		if online == nil || !online(existing.Token) {
			existing.Seen = time.Now()
			cp := *existing
			return &cp, nil
		}
	}

	for i := 0; i < 20; i++ {
		cand := AdminNick
		if i > 0 {
			cand = fmt.Sprintf("%s%d", AdminNick, i+1)
		}
		disp, key, err := normalizeNick(cand, true) // 允许保留词
		if err != nil {
			return nil, err
		}
		if _, taken := s.byKey[key]; taken {
			continue
		}
		now := time.Now()
		sess := &Session{Token: RandomToken(24), Nick: disp, key: key, Created: now, Seen: now}
		s.byTok[sess.Token] = sess
		s.byKey[key] = sess
		return sess, nil
	}
	return nil, ErrNickTaken
}

// CreateGuest 给外网访客自动分配「打飞机 + 0~3 位随机字母数字」的昵称。
func (s *SessionStore) CreateGuest() (*Session, error) {
	const alphabet = "abcdefghjkmnpqrstuvwxyz23456789"
	for attempt := 0; attempt < 60; attempt++ {
		n := attempt % 4 // 0、1、2、3 位后缀
		var sb strings.Builder
		sb.WriteString("打飞机")
		if n > 0 {
			b := make([]byte, n)
			if _, err := crand.Read(b); err != nil {
				return nil, err
			}
			for _, v := range b {
				sb.WriteByte(alphabet[int(v)%len(alphabet)])
			}
		}
		sess, err := s.Create(sb.String())
		if err == nil {
			return sess, nil
		}
		if !errors.Is(err, ErrNickTaken) {
			return nil, err
		}
	}
	return nil, ErrNickTaken
}

// PlayerView 用于「在线玩家」列表。
type PlayerView struct {
	Nick    string `json:"nick"`
	Online  bool   `json:"online"`
	IdleSec int    `json:"idle_sec"`
	Me      bool   `json:"me"`
}

// List 返回所有昵称（含离线），在线按活跃时间排序。
func (s *SessionStore) List(onlineWithin time.Duration, self string) []PlayerView {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.gcLocked()
	now := time.Now()
	out := make([]PlayerView, 0, len(s.byTok))
	for _, sess := range s.byTok {
		idle := now.Sub(sess.Seen)
		out = append(out, PlayerView{
			Nick:    sess.Nick,
			Online:  idle <= onlineWithin,
			IdleSec: int(idle.Seconds()),
			Me:      sess.Nick == self,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Online != out[j].Online {
			return out[i].Online
		}
		if out[i].IdleSec != out[j].IdleSec {
			return out[i].IdleSec < out[j].IdleSec
		}
		return out[i].Nick < out[j].Nick
	})
	return out
}
