package platform

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// HostsFile управляет небольшим управляемым блоком в системном hosts.
//
// Приложение меняет hosts только по явному действию пользователя. Перед
// изменением сохраняется полная копия файла и список удалённых строк, чтобы
// «Вернуть как было» не зависело от того, помнит ли приложение прошлый запуск.
type HostsFile struct {
	Path         string
	BackupDir    string
	DisableFlush bool
	mu           sync.Mutex
}

type HostEntry struct {
	IP   string `json:"ip"`
	Host string `json:"host"`
}

type HostsStatus struct {
	Path      string
	Applied   bool
	Entries   []HostEntry
	Conflicts []string
}

type hostsState struct {
	Applied      bool          `json:"applied"`
	Entries      []HostEntry   `json:"entries"`
	Removed      []removedLine `json:"removed,omitempty"`
	OriginalHash string        `json:"originalHash,omitempty"`
	AppliedHash  string        `json:"appliedHash,omitempty"`
	AppliedAt    time.Time     `json:"appliedAt"`
}

type removedLine struct {
	Index int    `json:"index"`
	Text  string `json:"text"`
}

const (
	hostsBlockBegin = "# >>> ampersand telegram web >>>"
	hostsBlockEnd   = "# <<< ampersand telegram web <<<"
)

func NewHostsFile(path, backupDir string) *HostsFile {
	return &HostsFile{Path: path, BackupDir: backupDir}
}

func (h *HostsFile) statePath() string  { return filepath.Join(h.BackupDir, "hosts-state.json") }
func (h *HostsFile) backupPath() string { return filepath.Join(h.BackupDir, "hosts.bak") }

// Status читает hosts и возвращает состояние управляемого блока. Ошибка
// чтения не фатальна для интерфейса: просто покажем её в Problems.
func (h *HostsFile) Status() (HostsStatus, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.statusLocked()
}

func (h *HostsFile) statusLocked() (HostsStatus, error) {
	st := HostsStatus{Path: h.Path}
	b, err := os.ReadFile(h.Path)
	if err != nil {
		return st, err
	}
	lines, _ := splitHostsLines(string(b))
	entries, start, end, ok := parseManagedBlock(lines)
	if ok {
		st.Applied = true
		st.Entries = entries
	}
	if ok {
		known := map[string]bool{}
		for _, e := range entries {
			known[strings.ToLower(e.Host)] = true
		}
		for i, line := range lines {
			if i >= start && i <= end {
				continue
			}
			fields := hostFields(line)
			if len(fields) < 2 {
				continue
			}
			for _, host := range fields[1:] {
				if known[strings.ToLower(host)] {
					st.Conflicts = append(st.Conflicts, strings.TrimSpace(line))
					break
				}
			}
		}
	}
	return st, nil
}

// Apply записывает управляемый блок. Строки, которые уже содержали эти же
// имена, временно убираются (и восстанавливаются при Revert), иначе старый
// DNS-ответ с заблокированным IP мог бы остаться первым в hosts.
func (h *HostsFile) Apply(entries []HostEntry) error {
	entries = normalizeEntries(entries)
	if len(entries) == 0 {
		return errors.New("hosts: empty entry list")
	}
	for _, e := range entries {
		if err := validateHostEntry(e); err != nil {
			return err
		}
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	original, mode, err := readHosts(h.Path)
	if err != nil {
		return err
	}
	lines, eol := splitHostsLines(original)
	lines = removeManagedBlock(lines)

	managed := map[string]bool{}
	for _, e := range entries {
		managed[strings.ToLower(e.Host)] = true
	}

	kept := make([]string, 0, len(lines)+len(entries)+2)
	removed := make([]removedLine, 0)
	for _, line := range lines {
		fields := hostFields(line)
		drop := false
		if len(fields) >= 2 {
			for _, host := range fields[1:] {
				if managed[strings.ToLower(host)] {
					drop = true
					break
				}
			}
		}
		if drop {
			removed = append(removed, removedLine{Index: len(kept), Text: line})
			continue
		}
		kept = append(kept, line)
	}

	block := make([]string, 0, len(entries)+2)
	block = append(block, hostsBlockBegin)
	for _, e := range entries {
		block = append(block, e.IP+" "+e.Host)
	}
	block = append(block, hostsBlockEnd)

	next := make([]string, 0, len(block)+len(kept)+1)
	next = append(next, block...)
	next = append(next, kept...)
	// Убираем случайные пустые строки в начале, но не трогаем содержимое.
	for len(next) > 0 && strings.TrimSpace(next[0]) == "" {
		next = next[1:]
	}
	next = append(next, "")
	data := strings.Join(next, eol)

	if err := os.MkdirAll(h.BackupDir, 0o700); err != nil {
		return fmt.Errorf("hosts backup dir: %w", err)
	}
	var previous hostsState
	_ = readJSON(h.statePath(), &previous)
	preserveBackup := previous.Applied
	if _, err := os.Stat(h.backupPath()); err != nil {
		preserveBackup = false
	}
	if preserveBackup {
		// Повторное применение не должно затирать исходную резервную копию
		// версией, в которой уже есть наш блок.
		removed = append(previous.Removed, removed...)
	} else {
		if err := os.WriteFile(h.backupPath(), []byte(original), 0o600); err != nil {
			return fmt.Errorf("hosts backup: %w", err)
		}
	}
	originalHash := hashText(original)
	if preserveBackup && previous.OriginalHash != "" {
		originalHash = previous.OriginalHash
	}
	state := hostsState{
		Applied:      true,
		Entries:      entries,
		Removed:      removed,
		OriginalHash: originalHash,
		AppliedHash:  hashText(data),
		AppliedAt:    time.Now(),
	}
	if err := writeJSON(h.statePath(), state); err != nil {
		return fmt.Errorf("hosts state: %w", err)
	}
	if err := writeHosts(h.Path, []byte(data), mode); err != nil {
		_ = os.Remove(h.statePath())
		return err
	}
	if !h.DisableFlush {
		_ = FlushDNSCache()
	}
	return nil
}

// Revert удаляет управляемый блок и возвращает строки, которые были убраны
// при Apply. Если файл после Apply никто не менял, восстанавливается полная
// резервная копия — это самый предсказуемый вариант.
func (h *HostsFile) Revert() error {
	h.mu.Lock()
	defer h.mu.Unlock()

	original, mode, err := readHosts(h.Path)
	if err != nil {
		return err
	}
	var state hostsState
	if err := readJSON(h.statePath(), &state); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("hosts state: %w", err)
	}

	if state.Applied && state.AppliedHash != "" && hashText(original) == state.AppliedHash {
		if b, err := os.ReadFile(h.backupPath()); err == nil {
			if err := writeHosts(h.Path, b, mode); err != nil {
				return err
			}
			_ = os.Remove(h.statePath())
			if !h.DisableFlush {
				_ = FlushDNSCache()
			}
			return nil
		}
	}

	lines, eol := splitHostsLines(original)
	lines = removeManagedBlock(lines)
	// Возвращаем удалённые строки в обратном порядке, чтобы индексы,
	// сохранённые относительно очищенного списка, не съехали.
	for i := len(state.Removed) - 1; i >= 0; i-- {
		r := state.Removed[i]
		if r.Index < 0 {
			r.Index = 0
		}
		if r.Index > len(lines) {
			r.Index = len(lines)
		}
		lines = append(lines, "")
		copy(lines[r.Index+1:], lines[r.Index:])
		lines[r.Index] = r.Text
	}
	next := strings.Join(lines, eol)
	if next != "" && !strings.HasSuffix(next, eol) {
		next += eol
	}
	if err := writeHosts(h.Path, []byte(next), mode); err != nil {
		return err
	}
	_ = os.Remove(h.statePath())
	if !h.DisableFlush {
		_ = FlushDNSCache()
	}
	return nil
}

func readHosts(path string) (string, os.FileMode, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", 0, fmt.Errorf("read hosts: %w", err)
	}
	mode := os.FileMode(0o644)
	if info, err := os.Stat(path); err == nil {
		mode = info.Mode().Perm()
		if mode == 0 {
			mode = 0o644
		}
	}
	return string(b), mode, nil
}

func writeHosts(path string, data []byte, mode os.FileMode) error {
	target := path
	if info, err := os.Lstat(path); err == nil && info.Mode()&os.ModeSymlink != 0 {
		if resolved, err := filepath.EvalSymlinks(path); err == nil {
			target = resolved
		}
	}
	dir := filepath.Dir(target)
	tmp, err := os.CreateTemp(dir, ".hosts-and-*")
	if err != nil {
		return fmt.Errorf("hosts temp file: %w", err)
	}
	name := tmp.Name()
	defer os.Remove(name)
	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(name, target); err != nil {
		return fmt.Errorf("write hosts: %w", err)
	}
	return nil
}

func splitHostsLines(s string) ([]string, string) {
	eol := "\n"
	if strings.Contains(s, "\r\n") {
		eol = "\r\n"
	}
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.TrimSuffix(s, "\n")
	if s == "" {
		return nil, eol
	}
	return strings.Split(s, "\n"), eol
}

func hostFields(line string) []string {
	line = strings.TrimSpace(line)
	if line == "" || strings.HasPrefix(line, "#") {
		return nil
	}
	if i := strings.IndexByte(line, '#'); i >= 0 {
		line = strings.TrimSpace(line[:i])
	}
	return strings.Fields(line)
}

func parseManagedBlock(lines []string) ([]HostEntry, int, int, bool) {
	start, end := -1, -1
	for i, line := range lines {
		switch strings.TrimSpace(line) {
		case hostsBlockBegin:
			if start < 0 {
				start = i
			}
		case hostsBlockEnd:
			if start >= 0 {
				end = i
			}
		}
		if start >= 0 && end >= 0 {
			break
		}
	}
	if start < 0 || end < start {
		return nil, -1, -1, false
	}
	var entries []HostEntry
	for _, line := range lines[start+1 : end] {
		fields := hostFields(line)
		if len(fields) < 2 {
			continue
		}
		for _, host := range fields[1:] {
			entries = append(entries, HostEntry{IP: fields[0], Host: host})
		}
	}
	return entries, start, end, true
}

func removeManagedBlock(lines []string) []string {
	out := make([]string, 0, len(lines))
	for i := 0; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) != hostsBlockBegin {
			out = append(out, lines[i])
			continue
		}
		end := -1
		for j := i + 1; j < len(lines); j++ {
			if strings.TrimSpace(lines[j]) == hostsBlockEnd {
				end = j
				break
			}
		}
		if end < 0 {
			// Повреждённый блок без конца: не удаляем чужие строки.
			out = append(out, lines[i])
			continue
		}
		i = end
	}
	return out
}

func normalizeEntries(entries []HostEntry) []HostEntry {
	seen := map[string]bool{}
	out := make([]HostEntry, 0, len(entries))
	for _, e := range entries {
		e.IP = strings.TrimSpace(e.IP)
		e.Host = strings.TrimSpace(e.Host)
		key := strings.ToLower(e.Host)
		if e.Host == "" || seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, e)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Host < out[j].Host })
	return out
}

func validateHostEntry(e HostEntry) error {
	ip := net.ParseIP(e.IP)
	if ip == nil {
		return fmt.Errorf("hosts: invalid IP %q", e.IP)
	}
	if !validHostName(e.Host) {
		return fmt.Errorf("hosts: invalid host %q", e.Host)
	}
	return nil
}

func validHostName(host string) bool {
	if host == "" || len(host) > 253 {
		return false
	}
	if strings.ContainsAny(host, " \t\r\n#;\"'\\/") {
		return false
	}
	labels := strings.Split(host, ".")
	if len(labels) < 2 {
		return false
	}
	for _, label := range labels {
		if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, r := range label {
			if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-') {
				return false
			}
		}
	}
	return true
}

func hashText(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

func writeJSON(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	return os.WriteFile(path, b, 0o600)
}

func readJSON(path string, v any) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}
