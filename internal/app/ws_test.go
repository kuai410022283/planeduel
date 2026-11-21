package app

import (
	"bufio"
	"crypto/rand"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// ---------------------------------------------------------------- 最小 WS 客户端
// 只用于测试：验证握手、掩码帧编解码、以及 hub 的真实链路。

type wsTestClient struct {
	conn net.Conn
	rw   *bufio.Reader
}

func (c *wsTestClient) sendText(t *testing.T, payload []byte) {
	t.Helper()
	var frame []byte
	frame = append(frame, 0x81) // FIN + text

	n := len(payload)
	switch {
	case n < 126:
		frame = append(frame, byte(n)|0x80)
	case n < 1<<16:
		frame = append(frame, 126|0x80, byte(n>>8), byte(n))
	default:
		frame = append(frame, 127|0x80)
		var ext [8]byte
		binary.BigEndian.PutUint64(ext[:], uint64(n))
		frame = append(frame, ext[:]...)
	}
	var mask [4]byte
	if _, err := rand.Read(mask[:]); err != nil {
		t.Fatal(err)
	}
	frame = append(frame, mask[:]...)
	for i, b := range payload {
		frame = append(frame, b^mask[i%4])
	}
	if _, err := c.conn.Write(frame); err != nil {
		t.Fatalf("写帧失败: %v", err)
	}
}

func (c *wsTestClient) readText(t *testing.T) map[string]any {
	t.Helper()
	_ = c.conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	var head [2]byte
	if _, err := io.ReadFull(c.rw, head[:]); err != nil {
		t.Fatalf("读帧头失败: %v", err)
	}
	if head[0]&0x0f != 0x1 {
		t.Fatalf("期望文本帧，收到 opcode %d", head[0]&0x0f)
	}
	length := int(head[1] & 0x7f)
	switch length {
	case 126:
		var ext [2]byte
		io.ReadFull(c.rw, ext[:])
		length = int(binary.BigEndian.Uint16(ext[:]))
	case 127:
		var ext [8]byte
		io.ReadFull(c.rw, ext[:])
		length = int(binary.BigEndian.Uint64(ext[:]))
	}
	buf := make([]byte, length)
	if _, err := io.ReadFull(c.rw, buf); err != nil {
		t.Fatalf("读载荷失败: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(buf, &m); err != nil {
		t.Fatalf("载荷不是 JSON: %q", string(buf))
	}
	return m
}


// waitFor 跳过中间消息，等指定类型（连接建立时会先后推 players / welcome）。
func (c *wsTestClient) waitFor(t *testing.T, typ string, max int) map[string]any {
	t.Helper()
	for i := 0; i < max; i++ {
		m := c.readText(t)
		if m["t"] == typ {
			return m
		}
	}
	t.Fatalf("等不到消息类型 %q", typ)
	return nil
}
func dialWS(t *testing.T, baseURL, token string) *wsTestClient {
	t.Helper()
	addr := strings.TrimPrefix(baseURL, "http://")
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("连接失败: %v", err)
	}
	keyBytes := make([]byte, 16)
	_, _ = rand.Read(keyBytes)
	key := base64.StdEncoding.EncodeToString(keyBytes)

	req := fmt.Sprintf(
		"GET /ws HTTP/1.1\r\nHost: %s\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n"+
			"Sec-WebSocket-Key: %s\r\nSec-WebSocket-Version: 13\r\nCookie: %s=%s\r\n\r\n",
		addr, key, cookieName, token)
	if _, err := conn.Write([]byte(req)); err != nil {
		t.Fatalf("发送握手失败: %v", err)
	}

	rw := bufio.NewReader(conn)
	resp, err := rw.ReadString('\n')
	if err != nil {
		t.Fatalf("读响应失败: %v", err)
	}
	if !strings.Contains(resp, "101") {
		t.Fatalf("握手失败: %q", strings.TrimSpace(resp))
	}
	headers := map[string]string{}
	for {
		line, err := rw.ReadString('\n')
		if err != nil {
			t.Fatalf("读响应头失败: %v", err)
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			break
		}
		parts := strings.SplitN(line, ":", 2)
		if len(parts) == 2 {
			headers[strings.ToLower(strings.TrimSpace(parts[0]))] = strings.TrimSpace(parts[1])
		}
	}
	sum := sha1.Sum([]byte(key + wsGUID))
	want := base64.StdEncoding.EncodeToString(sum[:])
	if got := headers["sec-websocket-accept"]; got != want {
		t.Fatalf("Sec-WebSocket-Accept 不匹配：%q != %q", got, want)
	}
	return &wsTestClient{conn: conn, rw: rw}
}

// ---------------------------------------------------------------- 用例

// 真实 socket：握手 + 收发消息
func TestWebSocketRoundTrip(t *testing.T) {
	s := newTestServer(t)
	sess, err := s.sessions.Create("ws甲")
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(s.Channeled("admin"))
	defer ts.Close()

	c := dialWS(t, ts.URL, sess.Token)
	defer c.conn.Close()

	// 连上后服务端会先推 players 列表
	c.waitFor(t, "players", 4)

	c.sendText(t, []byte(`{"t":"ping"}`))
	c.waitFor(t, "pong", 4)
}

// 没有 Cookie 时必须拒绝
func TestWebSocketRequiresSession(t *testing.T) {
	s := newTestServer(t)
	ts := httptest.NewServer(s.Channeled("admin"))
	defer ts.Close()

	addr := strings.TrimPrefix(ts.URL, "http://")
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	req := fmt.Sprintf("GET /ws HTTP/1.1\r\nHost: %s\r\nUpgrade: websocket\r\n"+
		"Connection: Upgrade\r\nSec-WebSocket-Key: AAAAAAAAAAAAAAAAAAAAAA==\r\nSec-WebSocket-Version: 13\r\n\r\n", addr)
	conn.Write([]byte(req))
	rw := bufio.NewReader(conn)
	line, _ := rw.ReadString('\n')
	if !strings.Contains(line, "401") {
		t.Fatalf("无会话应返回 401，实际 %q", strings.TrimSpace(line))
	}
}

// 两个人通过真实 WebSocket 互相邀请并开局
func TestWebSocketInviteFlow(t *testing.T) {
	s := newTestServer(t)
	sa, _ := s.sessions.Create("玩家甲")
	sb, _ := s.sessions.Create("玩家乙")
	ts := httptest.NewServer(s.Channeled("admin"))
	defer ts.Close()

	ca := dialWS(t, ts.URL, sa.Token)
	defer ca.conn.Close()
	cb := dialWS(t, ts.URL, sb.Token)
	defer cb.conn.Close()

	ca.waitFor(t, "players", 4)
	cb.waitFor(t, "players", 4)

	ca.sendText(t, []byte(`{"t":"invite","to":"玩家乙"}`))

	// 乙收到邀请
	inv := cb.waitFor(t, "invite", 4)
	invID, _ := inv["id"].(string)
	if invID == "" {
		t.Fatal("乙没有收到邀请")
	}

	cb.sendText(t, []byte(fmt.Sprintf(`{"t":"invite.reply","id":%q,"accept":true}`, invID)))

	// 双方都应收到 playing 状态
	ra := ca.waitFor(t, "room", 8)
	rb := cb.waitFor(t, "room", 8)
	if asRoom(t, rb).State != "playing" {
		t.Fatal("乙那边也应看到已开局")
	}
	roomA := asRoom(t, ra)
	if roomA.State != "playing" {
		t.Fatalf("应已开局，实际 %s", roomA.State)
	}
}
