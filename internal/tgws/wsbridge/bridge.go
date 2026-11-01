package wsbridge

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/sha1"
	"crypto/tls"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"ampersand/internal/tgws/config"
	"ampersand/internal/tgws/mtproto"
)

const (
	opText   = 0x1
	opBinary = 0x2
	opClose  = 0x8
	opPing   = 0x9
	opPong   = 0xA

	closeWriteTimeout = 250 * time.Millisecond
)

type HandshakeError struct {
	StatusCode int
	StatusLine string
	Headers    map[string]string
	Location   string
}

func (e *HandshakeError) Error() string {
	return fmt.Sprintf("websocket handshake failed: %s", e.StatusLine)
}

func (e *HandshakeError) IsRedirect() bool {
	switch e.StatusCode {
	case 301, 302, 303, 307, 308:
		return true
	default:
		return false
	}
}

type Client struct {
	conn   net.Conn
	reader *bufio.Reader
}

type WSEndpoint struct {
	DialHost string
	TLSHost  string
	HTTPHost string
	// Plain dials port 80 without TLS (Cloudflare "Flexible" mode): no SNI is
	// ever sent, so SNI-based filters cannot match.
	Plain bool
}

func NewClient(conn net.Conn) *Client {
	return &Client{
		conn:   conn,
		reader: bufio.NewReader(conn),
	}
}

func DialEndpoint(ctx context.Context, cfg config.Config, ep WSEndpoint) (*Client, error) {
	port := "443"
	if ep.Plain {
		port = "80"
	}
	addr := net.JoinHostPort(ep.DialHost, port)
	dialer := &net.Dialer{Timeout: cfg.DialTimeout}
	rawConn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, err
	}
	if tc, ok := rawConn.(*net.TCPConn); ok {
		_ = tc.SetNoDelay(true)
	}

	var conn net.Conn = rawConn
	if !ep.Plain {
		conn = tls.Client(rawConn, &tls.Config{
			ServerName:         ep.TLSHost,
			InsecureSkipVerify: true,
			MinVersion:         tls.VersionTLS12,
			NextProtos:         []string{"http/1.1"},
			// Upstream v1.11.3: the post-quantum X25519MLKEM768 key share makes the
			// ClientHello large enough to be dropped by some DPI, so offer only
			// classical groups.
			CurvePreferences: []tls.CurveID{tls.X25519, tls.CurveP256, tls.CurveP384},
		})
	}
	releaseDeadline := bindConnToContext(ctx, conn, cfg.DialTimeout)
	defer releaseDeadline()

	if tc, ok := conn.(*tls.Conn); ok {
		if err := tc.Handshake(); err != nil {
			_ = tc.Close()
			return nil, err
		}
	}

	client := NewClient(conn)

	if err := client.handshake(ep.HTTPHost, cfg.ConnectWSPath); err != nil {
		_ = client.Close()
		return nil, err
	}
	return client, nil
}

func Dial(ctx context.Context, cfg config.Config, targetIP string, domain string) (*Client, error) {
	return DialEndpoint(ctx, cfg, WSEndpoint{
		DialHost: targetIP,
		TLSHost:  domain,
		HTTPHost: domain,
		// --no-secure only affects hostname (Cloudflare) targets, never raw
		// Telegram IPs.
		Plain: cfg.CFNoSecure && net.ParseIP(targetIP) == nil,
	})
}

func bindConnToContext(ctx context.Context, conn net.Conn, timeout time.Duration) func() {
	deadline, hasDeadline := effectiveConnDeadline(ctx, timeout)
	if hasDeadline {
		_ = conn.SetDeadline(deadline)
	}

	cancelDone := make(chan struct{})
	stopCancel := context.AfterFunc(ctx, func() {
		_ = conn.SetDeadline(time.Now())
		close(cancelDone)
	})

	return func() {
		if !stopCancel() {
			<-cancelDone
		}
		if hasDeadline || ctx.Err() != nil {
			_ = conn.SetDeadline(time.Time{})
		}
	}
}

func effectiveConnDeadline(ctx context.Context, timeout time.Duration) (time.Time, bool) {
	var deadline time.Time
	hasDeadline := false

	if timeout > 0 {
		deadline = time.Now().Add(timeout)
		hasDeadline = true
	}

	if ctxDeadline, ok := ctx.Deadline(); ok && (!hasDeadline || ctxDeadline.Before(deadline)) {
		deadline = ctxDeadline
		hasDeadline = true
	}

	return deadline, hasDeadline
}

func (c *Client) Send(data []byte) error {
	frame, err := buildFrame(opBinary, data, true)
	if err != nil {
		return err
	}
	_, err = c.conn.Write(frame)
	return err
}

func (c *Client) SendBatch(parts [][]byte) error {
	for _, part := range parts {
		if err := c.Send(part); err != nil {
			return err
		}
	}
	return nil
}

func (c *Client) Recv() ([]byte, error) {
	for {
		opcode, payload, err := c.readFrame()
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil, nil
			}
			return nil, err
		}
		switch opcode {
		case opClose:
			_ = c.writeControl(opClose, payload[:min(2, len(payload))])
			return nil, nil
		case opPing:
			if err := c.writeControl(opPong, payload); err != nil {
				return nil, err
			}
		case opPong:
			continue
		case opText, opBinary:
			return payload, nil
		default:
			continue
		}
	}
}

func (c *Client) IsReusable() bool {
	if c == nil || c.conn == nil || c.reader == nil {
		return false
	}
	if c.reader.Buffered() > 0 {
		return false
	}

	if err := c.conn.SetReadDeadline(time.Now()); err != nil {
		return false
	}
	defer c.conn.SetReadDeadline(time.Time{})

	_, err := c.reader.Peek(1)
	if err == nil {
		return false
	}

	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return true
	}

	return false
}

func (c *Client) Close() error {
	if c == nil || c.conn == nil {
		return nil
	}

	if closeWriteTimeout > 0 {
		_ = c.conn.SetWriteDeadline(time.Now().Add(closeWriteTimeout))
	}
	_ = c.writeControl(opClose, nil)
	if closeWriteTimeout > 0 {
		_ = c.conn.SetWriteDeadline(time.Time{})
	}
	return c.conn.Close()
}

func Bridge(ctx context.Context, clientConn net.Conn, ws *Client, init []byte, splitter *mtproto.Splitter) error {
	if err := ws.Send(init); err != nil {
		return err
	}

	errCh := make(chan error, 2)
	go func() {
		errCh <- pumpTCPToWS(clientConn, ws, splitter)
	}()
	go func() {
		errCh <- pumpWSToTCP(clientConn, ws)
	}()

	select {
	case <-ctx.Done():
		_ = ws.Close()
		return ctx.Err()
	case err := <-errCh:
		_ = ws.Close()
		return err
	}
}

func (c *Client) handshake(domain string, path string) error {
	keyRaw := make([]byte, 16)
	if _, err := rand.Read(keyRaw); err != nil {
		return err
	}
	key := base64.StdEncoding.EncodeToString(keyRaw)

	req := strings.Builder{}
	req.WriteString("GET " + path + " HTTP/1.1\r\n")
	req.WriteString("Host: " + domain + "\r\n")
	req.WriteString("Upgrade: websocket\r\n")
	req.WriteString("Connection: Upgrade\r\n")
	req.WriteString("Sec-WebSocket-Key: " + key + "\r\n")
	req.WriteString("Sec-WebSocket-Version: 13\r\n")
	req.WriteString("Sec-WebSocket-Protocol: binary\r\n")
	req.WriteString("Origin: https://web.telegram.org\r\n")
	req.WriteString("User-Agent: Mozilla/5.0\r\n\r\n")

	if _, err := io.WriteString(c.conn, req.String()); err != nil {
		return err
	}

	statusLine, err := c.reader.ReadString('\n')
	if err != nil {
		return err
	}
	statusLine = strings.TrimSpace(statusLine)
	parts := strings.SplitN(statusLine, " ", 3)
	if len(parts) < 2 {
		return &HandshakeError{StatusCode: 0, StatusLine: statusLine}
	}

	statusCode, err := strconv.Atoi(parts[1])
	if err != nil {
		return &HandshakeError{StatusCode: 0, StatusLine: statusLine}
	}

	headers := map[string]string{}
	for {
		line, err := c.reader.ReadString('\n')
		if err != nil {
			return err
		}
		line = strings.TrimSpace(line)
		if line == "" {
			break
		}
		if idx := strings.IndexByte(line, ':'); idx > 0 {
			headers[strings.ToLower(strings.TrimSpace(line[:idx]))] = strings.TrimSpace(line[idx+1:])
		}
	}

	if statusCode != 101 {
		return &HandshakeError{
			StatusCode: statusCode,
			StatusLine: statusLine,
			Headers:    headers,
			Location:   headers["location"],
		}
	}

	expectedAccept := wsAcceptKey(key)
	if headers["sec-websocket-accept"] != expectedAccept {
		return errors.New("unexpected Sec-WebSocket-Accept header")
	}
	return nil
}

func (c *Client) readFrame() (byte, []byte, error) {
	var hdr [2]byte
	if _, err := io.ReadFull(c.reader, hdr[:]); err != nil {
		return 0, nil, err
	}

	opcode := hdr[0] & 0x0F
	length := uint64(hdr[1] & 0x7F)
	masked := hdr[1]&0x80 != 0

	switch length {
	case 126:
		var ext [2]byte
		if _, err := io.ReadFull(c.reader, ext[:]); err != nil {
			return 0, nil, err
		}
		length = uint64(binary.BigEndian.Uint16(ext[:]))
	case 127:
		var ext [8]byte
		if _, err := io.ReadFull(c.reader, ext[:]); err != nil {
			return 0, nil, err
		}
		length = binary.BigEndian.Uint64(ext[:])
	}

	var mask [4]byte
	if masked {
		if _, err := io.ReadFull(c.reader, mask[:]); err != nil {
			return 0, nil, err
		}
	}

	payload := make([]byte, int(length))
	if _, err := io.ReadFull(c.reader, payload); err != nil {
		return 0, nil, err
	}
	if masked {
		applyMask(payload, mask[:])
	}
	return opcode, payload, nil
}

func (c *Client) writeControl(opcode byte, payload []byte) error {
	frame, err := buildFrame(opcode, payload, true)
	if err != nil {
		return err
	}
	_, err = c.conn.Write(frame)
	return err
}

func buildFrame(opcode byte, payload []byte, masked bool) ([]byte, error) {
	length := len(payload)
	header := make([]byte, 0, 14+length)
	header = append(header, 0x80|opcode)

	maskBit := byte(0)
	if masked {
		maskBit = 0x80
	}

	switch {
	case length < 126:
		header = append(header, maskBit|byte(length))
	case length < 65536:
		header = append(header, maskBit|126)
		var ext [2]byte
		binary.BigEndian.PutUint16(ext[:], uint16(length))
		header = append(header, ext[:]...)
	default:
		header = append(header, maskBit|127)
		var ext [8]byte
		binary.BigEndian.PutUint64(ext[:], uint64(length))
		header = append(header, ext[:]...)
	}

	out := append(header, payload...)
	if !masked {
		return out, nil
	}

	mask := make([]byte, 4)
	if _, err := rand.Read(mask); err != nil {
		return nil, err
	}

	out = append(header, mask...)
	maskedPayload := append([]byte(nil), payload...)
	applyMask(maskedPayload, mask)
	out = append(out, maskedPayload...)
	return out, nil
}

func pumpTCPToWS(clientConn net.Conn, ws *Client, splitter *mtproto.Splitter) error {
	buf := make([]byte, 64*1024)
	for {
		n, err := clientConn.Read(buf)
		if n > 0 {
			chunk := append([]byte(nil), buf[:n]...)
			if splitter == nil {
				if err := ws.Send(chunk); err != nil {
					return err
				}
			} else {
				parts := splitter.Split(chunk)
				if len(parts) == 0 {
					if err != nil {
						return normalizeEOF(err)
					}
					continue
				}
				if err := ws.SendBatch(parts); err != nil {
					return err
				}
			}
		}
		if err != nil {
			if splitter != nil {
				if tail := splitter.Flush(); len(tail) > 0 {
					if sendErr := ws.SendBatch(tail); sendErr != nil {
						return sendErr
					}
				}
			}
			return normalizeEOF(err)
		}
	}
}

func pumpWSToTCP(clientConn net.Conn, ws *Client) error {
	for {
		data, err := ws.Recv()
		if err != nil {
			return err
		}
		if data == nil {
			return nil
		}
		if _, err := clientConn.Write(data); err != nil {
			return err
		}
	}
}

func wsAcceptKey(key string) string {
	sum := sha1.Sum([]byte(key + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"))
	return base64.StdEncoding.EncodeToString(sum[:])
}

func applyMask(payload []byte, mask []byte) {
	for i := range payload {
		payload[i] ^= mask[i%4]
	}
}

func normalizeEOF(err error) error {
	if errors.Is(err, io.EOF) {
		return nil
	}
	return err
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// DialFronted opens a websocket to targetIP with a TLS SNI that differs from
// the HTTP Host header (domain fronting). Telegram's own endpoints ignore the
// SNI, so a neutral name can slip past SNI-based filters. It is only
// meaningful for raw IP targets, never for Cloudflare hosts.
func DialFronted(ctx context.Context, cfg config.Config, targetIP string, domain string) (*Client, error) {
	return DialEndpoint(ctx, cfg, WSEndpoint{
		DialHost: targetIP,
		TLSHost:  cfg.FrontingSNI,
		HTTPHost: domain,
	})
}

// DialWithFronting dials normally and, for raw IP targets with a configured
// FrontingSNI, falls back to a fronted dial (or tries it first when the last
// successful dial was fronted). The preference is remembered in *frontFirst,
// mirroring upstream's try_fronting_first.
func DialWithFronting(ctx context.Context, cfg config.Config, targetIP, domain string, dial, dialFront DialFunc, frontFirst *atomic.Bool) (*Client, error) {
	if dial == nil {
		dial = Dial
	}
	if dialFront == nil {
		dialFront = DialFronted
	}
	if cfg.FrontingSNI == "" || net.ParseIP(targetIP) == nil {
		return dial(ctx, cfg, targetIP, domain)
	}

	modes := [2]bool{false, true}
	if frontFirst != nil && frontFirst.Load() {
		modes = [2]bool{true, false}
	}
	var lastErr error
	for _, fronted := range modes {
		var (
			client *Client
			err    error
		)
		if fronted {
			client, err = dialFront(ctx, cfg, targetIP, domain)
		} else {
			client, err = dial(ctx, cfg, targetIP, domain)
		}
		if err == nil {
			if frontFirst != nil {
				frontFirst.Store(fronted)
			}
			return client, nil
		}
		lastErr = err
		if ctx.Err() != nil {
			break
		}
	}
	return nil, lastErr
}

var autoFrontFirst atomic.Bool

// DialAuto is the default dialer: a plain dial, falling back to (and then
// preferring) a fronted dial for raw Telegram IPs when cfg.FrontingSNI is set.
func DialAuto(ctx context.Context, cfg config.Config, targetIP string, domain string) (*Client, error) {
	return DialWithFronting(ctx, cfg, targetIP, domain, Dial, DialFronted, &autoFrontFirst)
}

// WorkerPath returns the websocket path understood by the Cloudflare Worker
// relay script from upstream docs/CfWorker.md.
func WorkerPath(dstIP string, dc int) string {
	q := url.Values{}
	q.Set("dst", dstIP)
	q.Set("dc", strconv.Itoa(dc))
	return "/apiws?" + q.Encode()
}

// DialWorker opens a websocket to a Cloudflare Worker that relays raw TCP to
// dstIP:443. It honours cfg.CFNoSecure like any other Cloudflare host.
func DialWorker(ctx context.Context, cfg config.Config, workerDomain, dstIP string, dc int) (*Client, error) {
	cfg.ConnectWSPath = WorkerPath(dstIP, dc)
	return DialEndpoint(ctx, cfg, WSEndpoint{
		DialHost: workerDomain,
		TLSHost:  workerDomain,
		HTTPHost: workerDomain,
		Plain:    cfg.CFNoSecure,
	})
}
