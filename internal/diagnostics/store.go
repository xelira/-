package diagnostics

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

type Store struct {
	mu    sync.Mutex
	path  string
	limit int
}

func NewStore(dataDir string, limit int) (*Store, error) {
	if limit < 20 {
		limit = 200
	}
	dir := filepath.Join(dataDir, "diagnostics")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	return &Store{path: filepath.Join(dir, "history.jsonl"), limit: limit}, nil
}

func (s *Store) SetLimit(limit int) {
	if limit < 20 {
		limit = 20
	}
	s.mu.Lock()
	s.limit = limit
	s.mu.Unlock()
}

func (s *Store) Append(records []Record) error {
	if len(records) == 0 {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	f, err := os.OpenFile(s.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	enc := json.NewEncoder(f)
	for _, r := range records {
		if err := enc.Encode(r); err != nil {
			_ = f.Close()
			return err
		}
	}
	if err := f.Close(); err != nil {
		return err
	}
	return s.compactLocked()
}

func (s *Store) compactLocked() error {
	records, err := s.readAllLocked()
	if err != nil {
		return err
	}
	if len(records) <= s.limit {
		return nil
	}
	records = records[len(records)-s.limit:]
	tmp := s.path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	enc := json.NewEncoder(f)
	for _, r := range records {
		if err := enc.Encode(r); err != nil {
			_ = f.Close()
			return err
		}
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

// History отдаёт последние записи в хронологическом порядке.
func (s *Store) History(limit int) ([]Record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	records, err := s.readAllLocked()
	if err != nil {
		return nil, err
	}
	if limit > 0 && len(records) > limit {
		records = records[len(records)-limit:]
	}
	return records, nil
}

func (s *Store) readAllLocked() ([]Record, error) {
	f, err := os.Open(s.path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var records []Record
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 4096), 1<<20)
	for sc.Scan() {
		var r Record
		if err := json.Unmarshal(sc.Bytes(), &r); err != nil {
			continue
		}
		records = append(records, r)
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return records, nil
}

func (s *Store) Export(report Report, dataDir string) (string, error) {
	dir := filepath.Join(dataDir, "exports")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	name := fmt.Sprintf("and-diagnostics-%s.json", report.GeneratedAt.Format("20060102-150405"))
	path := filepath.Join(dir, name)
	b, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return "", err
	}
	b = append(b, '\n')
	if err := os.WriteFile(path, b, 0o600); err != nil {
		return "", err
	}
	return path, nil
}
