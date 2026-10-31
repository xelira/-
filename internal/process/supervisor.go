// Пакет process держит дочерние процессы в узде: запуск, логи, остановка.
package process

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

var (
	ErrAlreadyRunning = errors.New("process is already running")
	ErrNotRunning     = errors.New("process is not running")
)

type State string

const (
	StateStopped State = "stopped"
	StateRunning State = "running"
	StateFailed  State = "failed"
)

// Spec описывает запуск бинарника. Shell специально не поддерживаем:
// только exec с массивом аргументов, никаких сюрпризов с кавычками.
type Spec struct {
	Name string
	Path string
	Args []string
	Dir  string
	Env  []string
}

// Status — снимок состояния на текущий момент.
type Status struct {
	State     State
	PID       int
	StartedAt time.Time
	StoppedAt time.Time
	ExitError string
}

type Supervisor struct {
	mu        sync.RWMutex
	name      string
	logger    *slog.Logger
	ring      *Ring
	cmd       *exec.Cmd
	state     State
	startedAt time.Time
	stoppedAt time.Time
	exitErr   error
	stopping  bool
	done      chan struct{}
	onExit    func(Status)
}

func New(name string, logger *slog.Logger, ringLines int) *Supervisor {
	if logger == nil {
		logger = slog.Default()
	}
	return &Supervisor{name: name, logger: logger, ring: NewRing(ringLines), state: StateStopped}
}

// OnExit вешает неблокирующий колбэк на завершение процесса.
func (s *Supervisor) OnExit(fn func(Status)) {
	s.mu.Lock()
	s.onExit = fn
	s.mu.Unlock()
}

func (s *Supervisor) Start(spec Spec) error {
	if strings.TrimSpace(spec.Path) == "" {
		return errors.New("process path is empty")
	}
	if info, err := os.Stat(spec.Path); err != nil {
		return fmt.Errorf("engine executable: %w", err)
	} else if info.IsDir() {
		return fmt.Errorf("engine executable is a directory: %s", spec.Path)
	}

	s.mu.Lock()
	if s.state == StateRunning && s.cmd != nil {
		s.mu.Unlock()
		return ErrAlreadyRunning
	}
	s.ring.Clear()
	s.state = StateRunning
	s.startedAt = time.Now()
	s.stoppedAt = time.Time{}
	s.exitErr = nil
	s.stopping = false
	s.done = make(chan struct{})
	done := s.done
	name := s.name
	s.mu.Unlock()

	cmd := exec.Command(spec.Path, spec.Args...)
	cmd.Dir = spec.Dir
	cmd.Env = append(os.Environ(), spec.Env...)
	configureProcess(cmd)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		s.fail(err)
		return fmt.Errorf("capture stdout: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		s.fail(err)
		return fmt.Errorf("capture stderr: %w", err)
	}
	if err := cmd.Start(); err != nil {
		s.fail(err)
		return fmt.Errorf("start %s: %w", name, err)
	}

	s.mu.Lock()
	s.cmd = cmd
	pid := cmd.Process.Pid
	s.mu.Unlock()
	s.logger.Info("process started", "module", name, "pid", pid, "path", spec.Path)

	var wg sync.WaitGroup
	wg.Add(2)
	go s.copyOutput(&wg, stdout, "stdout")
	go s.copyOutput(&wg, stderr, "stderr")
	go func() {
		wg.Wait()
		err := cmd.Wait()
		s.finish(err)
		close(done)
	}()
	return nil
}

func (s *Supervisor) copyOutput(wg *sync.WaitGroup, r io.Reader, stream string) {
	defer wg.Done()
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 4096), 256*1024)
	for scanner.Scan() {
		line := scanner.Text()
		s.ring.Add(line)
		s.logger.Debug("engine output", "module", s.name, "stream", stream, "line", line)
	}
	if err := scanner.Err(); err != nil {
		s.logger.Debug("engine output closed", "module", s.name, "stream", stream, "error", err)
	}
}

func (s *Supervisor) finish(err error) {
	s.mu.Lock()
	stopping := s.stopping
	s.cmd = nil
	s.stoppedAt = time.Now()
	if stopping {
		s.state = StateStopped
		s.exitErr = nil
	} else if err != nil {
		s.state = StateFailed
		s.exitErr = err
	} else {
		s.state = StateStopped
		s.exitErr = nil
	}
	status := Status{State: s.state, StartedAt: s.startedAt, StoppedAt: s.stoppedAt}
	if s.exitErr != nil {
		status.ExitError = s.exitErr.Error()
	}
	cb := s.onExit
	s.mu.Unlock()

	if status.State == StateFailed {
		s.logger.Error("process exited unexpectedly", "module", s.name, "error", err)
	} else {
		s.logger.Info("process stopped", "module", s.name)
	}
	if cb != nil {
		go cb(status)
	}
}

func (s *Supervisor) fail(err error) {
	s.mu.Lock()
	s.state = StateFailed
	s.exitErr = err
	s.stoppedAt = time.Now()
	s.mu.Unlock()
}

func (s *Supervisor) Stop(grace time.Duration) error {
	s.mu.Lock()
	cmd := s.cmd
	if cmd == nil || s.state != StateRunning {
		s.mu.Unlock()
		return ErrNotRunning
	}
	s.stopping = true
	done := s.done
	s.mu.Unlock()

	terminateProcess(cmd)
	select {
	case <-done:
		return nil
	case <-time.After(grace):
		killProcess(cmd)
		select {
		case <-done:
			return nil
		case <-time.After(3 * time.Second):
			return errors.New("process did not exit after kill")
		}
	}
}

func (s *Supervisor) Status() Status {
	s.mu.RLock()
	defer s.mu.RUnlock()
	st := Status{
		State:     s.state,
		StartedAt: s.startedAt,
		StoppedAt: s.stoppedAt,
	}
	if s.cmd != nil && s.cmd.Process != nil {
		st.PID = s.cmd.Process.Pid
	}
	if s.exitErr != nil {
		st.ExitError = s.exitErr.Error()
	}
	return st
}

func (s *Supervisor) Running() bool {
	return s.Status().State == StateRunning
}

func (s *Supervisor) Output(lines int) []string { return s.ring.Tail(lines) }

// WaitListening ждёт готовности: успешный check, смерть процесса или таймаут.
func (s *Supervisor) WaitListening(ctx context.Context, check func() error, interval time.Duration) error {
	if interval <= 0 {
		interval = 100 * time.Millisecond
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		if !s.Running() {
			return fmt.Errorf("process exited before readiness check: %s", s.Status().ExitError)
		}
		if err := check(); err == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("readiness timeout: %w", ctx.Err())
		case <-ticker.C:
		}
	}
}
