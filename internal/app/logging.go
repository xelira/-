package app

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

type rotatingWriter struct {
	mu       sync.Mutex
	path     string
	maxBytes int64
	backups  int
	file     *os.File
	size     int64
}

func newRotatingWriter(path string, maxBytes int64, backups int) (*rotatingWriter, error) {
	if maxBytes <= 0 {
		maxBytes = 5 << 20
	}
	if backups < 1 {
		backups = 2
	}
	w := &rotatingWriter{path: path, maxBytes: maxBytes, backups: backups}
	if err := w.open(); err != nil {
		return nil, err
	}
	return w, nil
}

func (w *rotatingWriter) open() error {
	if err := os.MkdirAll(filepath.Dir(w.path), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(w.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	st, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return err
	}
	w.file, w.size = f, st.Size()
	return nil
}

func (w *rotatingWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.file == nil {
		if err := w.open(); err != nil {
			return 0, err
		}
	}
	if w.size+int64(len(p)) > w.maxBytes {
		if err := w.rotateLocked(); err != nil {
			return 0, err
		}
	}
	n, err := w.file.Write(p)
	w.size += int64(n)
	return n, err
}

func (w *rotatingWriter) rotateLocked() error {
	_ = w.file.Close()
	for i := w.backups - 1; i >= 1; i-- {
		from := fmt.Sprintf("%s.%d", w.path, i)
		to := fmt.Sprintf("%s.%d", w.path, i+1)
		_ = os.Rename(from, to)
	}
	_ = os.Rename(w.path, w.path+".1")
	return w.open()
}

func (w *rotatingWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.file == nil {
		return nil
	}
	err := w.file.Close()
	w.file = nil
	return err
}

type redactingHandler struct {
	inner  slog.Handler
	secret string
}

func (h redactingHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.inner.Enabled(ctx, level)
}

func (h redactingHandler) Handle(ctx context.Context, r slog.Record) error {
	if h.secret == "" {
		return h.inner.Handle(ctx, r)
	}
	c := slog.NewRecord(r.Time, r.Level, strings.ReplaceAll(r.Message, h.secret, "[redacted]"), r.PC)
	r.Attrs(func(a slog.Attr) bool {
		if a.Value.Kind() == slog.KindString {
			a.Value = slog.StringValue(strings.ReplaceAll(a.Value.String(), h.secret, "[redacted]"))
		}
		c.AddAttrs(a)
		return true
	})
	return h.inner.Handle(ctx, c)
}

func (h redactingHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return redactingHandler{inner: h.inner.WithAttrs(redactAttrs(attrs, h.secret)), secret: h.secret}
}

func (h redactingHandler) WithGroup(name string) slog.Handler {
	return redactingHandler{inner: h.inner.WithGroup(name), secret: h.secret}
}

func redactAttrs(attrs []slog.Attr, secret string) []slog.Attr {
	if secret == "" {
		return attrs
	}
	out := make([]slog.Attr, len(attrs))
	for i, a := range attrs {
		if a.Value.Kind() == slog.KindString {
			a.Value = slog.StringValue(strings.ReplaceAll(a.Value.String(), secret, "[redacted]"))
		}
		out[i] = a
	}
	return out
}

// newLogger делает ограниченные логи с ротацией и вырезает секрет прокси.
func newLogger(dataDir, secret string, debug bool) (*slog.Logger, io.Closer, error) {
	writer, err := newRotatingWriter(filepath.Join(dataDir, "logs", "app.log"), 5<<20, 3)
	if err != nil {
		return nil, nil, fmt.Errorf("open log: %w", err)
	}
	level := slog.LevelInfo
	if debug {
		level = slog.LevelDebug
	}
	inner := slog.NewTextHandler(writer, &slog.HandlerOptions{Level: level})
	logger := slog.New(redactingHandler{inner: inner, secret: secret})
	return logger, writer, nil
}
