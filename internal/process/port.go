package process

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"time"
)

// PortAvailable проверяет, можно ли занять порт на loopback.
func PortAvailable(host string, port int) (bool, error) {
	if port < 1 || port > 65535 {
		return false, fmt.Errorf("invalid port %d", port)
	}
	ln, err := net.Listen("tcp", net.JoinHostPort(host, strconv.Itoa(port)))
	if err != nil {
		return false, nil
	}
	_ = ln.Close()
	return true, nil
}

// Listening отвечает, принимает ли TCP-порт соединения.
func Listening(host string, port int, timeout time.Duration) error {
	if timeout <= 0 {
		timeout = time.Second
	}
	conn, err := net.DialTimeout("tcp", net.JoinHostPort(host, strconv.Itoa(port)), timeout)
	if err != nil {
		return err
	}
	return conn.Close()
}

// WaitListening ждёт, пока порт начнёт принимать, или пока не выйдет контекст.
func WaitListening(ctx context.Context, host string, port int, interval time.Duration) error {
	if interval <= 0 {
		interval = 100 * time.Millisecond
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	var last error
	for {
		if err := Listening(host, port, 300*time.Millisecond); err == nil {
			return nil
		} else {
			last = err
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("waiting for %s:%d: %w (last error: %v)", host, port, ctx.Err(), last)
		case <-ticker.C:
		}
	}
}
