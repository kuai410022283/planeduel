package app

import (
	"bufio"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// 极简 WebSocket（RFC 6455 服务端）：只做文本帧 + ping/pong/close，
// 不做分片与压缩。够用且没有第三方依赖（NAS 上拿不到 Go module）。

const wsGUID = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"

var (
	errNotWebSocket = errors.New("不是 WebSocket 请求")
	errFrameTooBig  = errors.New("帧太大")
)

type wsConn struct {
	conn    net.Conn
	rw      *bufio.ReadWriter
	writeMu sync.Mutex
}

func upgradeWS(w http.ResponseWriter, r *http.Request) (*wsConn, error) {
	if !strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
		return nil, errNotWebSocket
	}
	if !headerHasToken(r.Header.Get("Connection"), "upgrade") {
		return nil, errNotWebSocket
	}
	key := r.Header.Get("Sec-WebSocket-Key")
	if key == "" {
		return nil, errNotWebSocket
	}
	if v := r.Header.Get("Sec-WebSocket-Version"); v != "13" {
		return nil, fmt.Errorf("只支持 WebSocket 13，收到 %q", v)
	}
	hj, ok := w.(http.Hijacker)
	if !ok {
		return nil, errors.New("当前连接不支持升级")
	}
	conn, rw, err := hj.Hijack()
	if err != nil {
		return nil, err
	}

	sum := sha1.Sum([]byte(key + wsGUID))
	accept := base64.StdEncoding.EncodeToString(sum[:])
	resp := "HTTP/1.1 101 Switching Protocols\r\n" +
		"Upgrade: websocket\r\n" +
		"Connection: Upgrade\r\n" +
		"Sec-WebSocket-Accept: " + accept + "\r\n\r\n"
	if _, err := rw.WriteString(resp); err != nil {
		_ = conn.Close()
		return nil, err
	}
	if err := rw.Flush(); err != nil {
		_ = conn.Close()
		return nil, err
	}
	return &wsConn{conn: conn, rw: rw}, nil
}

func headerHasToken(h, token string) bool {
	for _, part := range strings.Split(h, ",") {
		if strings.EqualFold(strings.TrimSpace(part), token) {
			return true
		}
	}
	return false
}

// ReadMessage 返回 opcode 与载荷；文本帧 opcode=1。
func (c *wsConn) ReadMessage() (byte, []byte, error) {
	var head [2]byte
	if _, err := io.ReadFull(c.rw, head[:]); err != nil {
		return 0, nil, err
	}
	opcode := head[0] & 0x0f
	masked := head[1]&0x80 != 0
	length := int(head[1] & 0x7f)

	switch length {
	case 126:
		var ext [2]byte
		if _, err := io.ReadFull(c.rw, ext[:]); err != nil {
			return 0, nil, err
		}
		length = int(binary.BigEndian.Uint16(ext[:]))
	case 127:
		var ext [8]byte
		if _, err := io.ReadFull(c.rw, ext[:]); err != nil {
			return 0, nil, err
		}
		length = int(binary.BigEndian.Uint64(ext[:]))
	}
	if length < 0 || length > 1<<20 {
		return 0, nil, errFrameTooBig
	}

	var mask [4]byte
	if masked {
		if _, err := io.ReadFull(c.rw, mask[:]); err != nil {
			return 0, nil, err
		}
	}
	payload := make([]byte, length)
	if _, err := io.ReadFull(c.rw, payload); err != nil {
		return 0, nil, err
	}
	if masked {
		for i := range payload {
			payload[i] ^= mask[i%4]
		}
	}
	return opcode, payload, nil
}

func (c *wsConn) write(opcode byte, data []byte) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()

	var head []byte
	head = append(head, 0x80|opcode) // FIN + opcode
	n := len(data)
	switch {
	case n < 126:
		head = append(head, byte(n))
	case n < 1<<16:
		head = append(head, 126, byte(n>>8), byte(n))
	default:
		head = append(head, 127)
		var ext [8]byte
		binary.BigEndian.PutUint64(ext[:], uint64(n))
		head = append(head, ext[:]...)
	}
	_ = c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	if _, err := c.rw.Write(head); err != nil {
		return err
	}
	if _, err := c.rw.Write(data); err != nil {
		return err
	}
	return c.rw.Flush()
}

func (c *wsConn) WriteText(data []byte) error { return c.write(0x1, data) }
func (c *wsConn) WritePing() error            { return c.write(0x9, nil) }
func (c *wsConn) WriteClose() error           { return c.write(0x8, nil) }

func (c *wsConn) Close() error { return c.conn.Close() }

func (c *wsConn) SetReadDeadline(t time.Time) error { return c.conn.SetReadDeadline(t) }
