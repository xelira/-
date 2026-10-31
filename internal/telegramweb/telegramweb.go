// Package telegramweb проверяет и чинит доступ к веб-версии Telegram.
//
// Важно: это не MTProto-прокси и не DPI. Telegram Web — обычный сайт, которому
// нужен рабочий IP и WebSocket. Если провайдер блокирует сам IP, DPI-обход не
// поможет: hosts должен указать на доступный фронтенд Telegram.
package telegramweb

import (
	"bufio"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"

	"ampersand/internal/platform"
)

const (
	pageHost = "web.telegram.org"
	pagePath = "/a/"
	wsHost   = "kws2-1.web.telegram.org"
	wsPath   = "/apiws"
)

const FallbackIP = "149.154.167.220"

// CandidateIPs — известные фронтенды Telegram. Порядок важен: сначала IP,
// который обычно доступен при блокировке 149.154.167.99.
var CandidateIPs = []string{
	FallbackIP,
	"149.154.167.99",
	"149.154.167.51",
	"149.154.167.91",
	"149.154.175.50",
	"149.154.175.100",
}

// Domains — имена, которые нужны web.telegram.org/a и /k, включая WebSocket
// endpoints kws*/zws* и медиа-хосты.
func Domains() []string {
	out := []string{
		"web.telegram.org",
		"webk.telegram.org",
		"webz.telegram.org",
		"weba.telegram.org",
	}
	for dc := 1; dc <= 5; dc++ {
		out = append(out,
			fmt.Sprintf("kws%d.web.telegram.org", dc),
			fmt.Sprintf("kws%d-1.web.telegram.org", dc),
		)
	}
	out = append(out,
		"zws2.web.telegram.org",
		"zws2-1.web.telegram.org",
		"zws4.web.telegram.org",
		"zws4-1.web.telegram.org",
		"vesta.web.telegram.org",
		"vesta-1.web.telegram.org",
		"venus.web.telegram.org",
		"venus-1.web.telegram.org",
		"api.telegram.org",
		"telegram.org",
		"telegram.me",
		"t.me",
		"telegram.dog",
		"telesco.pe",
		"cdn.telesco.pe",
		"telegra.ph",
		"graph.org",
	)
	return uniqueStrings(out)
}

type Prober interface {
	Page(context.Context, string) error
	WebSocket(context.Context, string) error
}

type DefaultProber struct {
	Timeout time.Duration
}

func (p DefaultProber) timeout() time.Duration {
	if p.Timeout <= 0 {
		return 6 * time.Second
	}
	return p.Timeout
}

func (p DefaultProber) Page(ctx context.Context, ip string) error {
	timeout := p.timeout()
	dialer := &net.Dialer{Timeout: timeout}
	raw, err := dialer.DialContext(ctx, "tcp", net.JoinHostPort(ip, "443"))
	if err != nil {
		return err
	}
	defer raw.Close()
	_ = raw.SetDeadline(time.Now().Add(timeout))
	tlsConn := tls.Client(raw, &tls.Config{
		ServerName: pageHost,
		MinVersion: tls.VersionTLS12,
	})
	defer tlsConn.Close()
	if err := tlsConn.HandshakeContext(ctx); err != nil {
		return err
	}
	req := "GET " + pagePath + " HTTP/1.1\r\n" +
		"Host: " + pageHost + "\r\n" +
		"User-Agent: Mozilla/5.0 (ampersand telegram-web check)\r\n" +
		"Accept: text/html\r\n" +
		"Connection: close\r\n\r\n"
	if _, err := io.WriteString(tlsConn, req); err != nil {
		return err
	}
	resp, err := http.ReadResponse(bufio.NewReader(tlsConn), &http.Request{Method: http.MethodGet})
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if !strings.Contains(strings.ToLower(string(body)), "telegram") {
		return errors.New("страница не похожа на Telegram")
	}
	return nil
}

func (p DefaultProber) WebSocket(ctx context.Context, ip string) error {
	var last error
	for _, host := range []string{
		wsHost,
		"zws2-1.web.telegram.org",
		"vesta-1.web.telegram.org",
		"kws4-1.web.telegram.org",
	} {
		if err := p.webSocketHost(ctx, ip, host); err != nil {
			last = err
			continue
		}
		return nil
	}
	return last
}

func (p DefaultProber) webSocketHost(ctx context.Context, ip, host string) error {
	timeout := p.timeout()
	transport := &http.Transport{
		Proxy:               nil,
		ForceAttemptHTTP2:   false,
		DisableKeepAlives:   true,
		TLSHandshakeTimeout: timeout,
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			_, port, err := net.SplitHostPort(addr)
			if err != nil {
				port = "443"
			}
			d := &net.Dialer{Timeout: timeout}
			return d.DialContext(ctx, network, net.JoinHostPort(ip, port))
		},
		TLSClientConfig: &tls.Config{
			ServerName: host,
			MinVersion: tls.VersionTLS12,
		},
	}
	client := &http.Client{Transport: transport, Timeout: timeout}
	conn, _, err := websocket.Dial(ctx, "wss://"+host+wsPath, &websocket.DialOptions{
		HTTPClient:   client,
		HTTPHeader:   http.Header{"Origin": []string{"https://web.telegram.org"}},
		Subprotocols: []string{"binary"},
	})
	if err != nil {
		return err
	}
	_ = conn.CloseNow()
	return nil
}

type Status struct {
	Supported   bool      `json:"supported"`
	Applied     bool      `json:"applied"`
	IP          string    `json:"ip,omitempty"`
	DNS         []string  `json:"dns,omitempty"`
	PageOK      bool      `json:"pageOk"`
	WebSocketOK bool      `json:"webSocketOk"`
	HostsPath   string    `json:"hostsPath,omitempty"`
	Detail      string    `json:"detail"`
	Problems    []string  `json:"problems,omitempty"`
	CheckedAt   time.Time `json:"checkedAt,omitempty"`
}

type Fix struct {
	hosts      *platform.HostsFile
	prober     Prober
	candidates []string
	domains    []string
	resolve    func(context.Context, string) ([]string, error)

	mu   sync.RWMutex
	last Status
}

func New(hosts *platform.HostsFile) *Fix {
	return &Fix{
		hosts:      hosts,
		prober:     DefaultProber{Timeout: 3 * time.Second},
		candidates: append([]string(nil), CandidateIPs...),
		domains:    Domains(),
		resolve: func(ctx context.Context, host string) ([]string, error) {
			return net.DefaultResolver.LookupHost(ctx, host)
		},
	}
}

func (f *Fix) Status() Status {
	f.mu.RLock()
	st := f.last
	f.mu.RUnlock()

	st.Supported = supported()
	st.HostsPath = f.hosts.Path
	if hs, err := f.hosts.Status(); err == nil {
		st.Applied = hs.Applied
		if len(hs.Entries) > 0 {
			st.IP = hs.Entries[0].IP
		}
		st.Problems = appendUnique(st.Problems, hs.Conflicts...)
	} else {
		st.Problems = appendUnique(st.Problems, err.Error())
	}
	if st.Detail == "" {
		switch {
		case st.Applied:
			st.Detail = "hosts исправлен: web.telegram.org → " + st.IP
		case st.CheckedAt.IsZero():
			st.Detail = "Не проверялось"
		default:
			st.Detail = "Рабочий IP не найден"
		}
	}
	return st
}

func (f *Fix) Check(ctx context.Context) Status {
	st := Status{
		Supported: supported(),
		HostsPath: f.hosts.Path,
		CheckedAt: time.Now(),
	}
	if !st.Supported {
		st.Detail = "Telegram Web hosts недоступен на этой ОС"
		return st
	}

	resolve := f.resolve
	if resolve == nil {
		resolve = func(ctx context.Context, host string) ([]string, error) {
			return net.DefaultResolver.LookupHost(ctx, host)
		}
	}
	dnsCtx, cancel := context.WithTimeout(ctx, 4*time.Second)
	addrs, dnsErr := resolve(dnsCtx, pageHost)
	cancel()
	if dnsErr == nil {
		st.DNS = uniqueStrings(addrs)
	} else {
		st.Problems = append(st.Problems, "DNS: "+dnsErr.Error())
	}

	candidates := uniqueStrings(append(append([]string{}, f.candidates...), st.DNS...))
	var failures []string
	for _, ip := range candidates {
		var lastErr error
		// .220 sometimes accepts a connection only on the second attempt.
		// Не сдаёмся после одного таймаута.
		for attempt := 0; attempt < 2; attempt++ {
			if err := f.prober.Page(ctx, ip); err != nil {
				lastErr = fmt.Errorf("%s page: %w", ip, err)
				continue
			}
			if err := f.prober.WebSocket(ctx, ip); err != nil {
				lastErr = fmt.Errorf("%s websocket: %w", ip, err)
				continue
			}
			lastErr = nil
			break
		}
		if lastErr != nil {
			failures = append(failures, lastErr.Error())
			continue
		}
		st.IP = ip
		st.PageOK = true
		st.WebSocketOK = true
		st.Detail = "Рабочий IP: " + ip + " (страница и WebSocket отвечают)"
		break
	}
	if st.IP == "" {
		st.Detail = "Не найден рабочий IP Telegram Web"
		if len(failures) > 0 {
			st.Problems = append(st.Problems, failures...)
		}
	}
	if hs, err := f.hosts.Status(); err == nil {
		st.Applied = hs.Applied
		st.Problems = appendUnique(st.Problems, hs.Conflicts...)
	}
	f.mu.Lock()
	f.last = st
	f.mu.Unlock()
	return st
}

func (f *Fix) Apply(ctx context.Context) (Status, error) {
	st := f.Check(ctx)
	verified := st.IP != ""
	ip := st.IP
	if ip == "" {
		// Если проверка не прошла из-за временной блокировки, всё равно
		// записываем известный фронтенд .220: при включённом DPI он обычно
		// работает, а 149.154.167.99 в такой сети недоступен полностью.
		ip = FallbackIP
		for _, candidate := range f.candidates {
			if tcpReachable(ctx, candidate, 2*time.Second) {
				ip = candidate
				break
			}
		}
		if tcpReachable(ctx, ip, 3*time.Second) {
			st.Detail = "TCP " + ip + " доступен; TLS/WebSocket не подтверждены, записываю hosts для работы через DPI"
		} else {
			st.Detail = "IP " + ip + " не подтверждён; записываю hosts, но может понадобиться VPN"
		}
	}
	entries := make([]platform.HostEntry, 0, len(f.domains))
	for _, host := range f.domains {
		entries = append(entries, platform.HostEntry{IP: ip, Host: host})
	}
	if err := f.hosts.Apply(entries); err != nil {
		st.Problems = append(st.Problems, err.Error())
		return st, err
	}
	st.Applied = true
	st.IP = ip
	st.Detail = "hosts обновлён: web.telegram.org → " + ip + ". Перезапустите браузер."
	if !verified {
		st.Detail += " Проверка TLS/WebSocket не прошла; включите DPI-профиль «Telegram Web» или VPN."
	}
	f.mu.Lock()
	f.last = st
	f.mu.Unlock()
	return st, nil
}

func tcpReachable(ctx context.Context, ip string, timeout time.Duration) bool {
	d := net.Dialer{Timeout: timeout}
	conn, err := d.DialContext(ctx, "tcp", net.JoinHostPort(ip, "443"))
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

func (f *Fix) Revert() error {
	if err := f.hosts.Revert(); err != nil {
		return err
	}
	f.mu.Lock()
	f.last = Status{
		Supported: supported(),
		HostsPath: f.hosts.Path,
		Detail:    "Записи Telegram Web удалены из hosts",
	}
	f.mu.Unlock()
	return nil
}

func supported() bool {
	return runtime.GOOS == "linux" || runtime.GOOS == "windows"
}

func uniqueStrings(values []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}

func appendUnique(base []string, values ...string) []string {
	seen := map[string]bool{}
	for _, v := range base {
		seen[v] = true
	}
	for _, v := range values {
		v = strings.TrimSpace(v)
		if v == "" || seen[v] {
			continue
		}
		seen[v] = true
		base = append(base, v)
	}
	return base
}
