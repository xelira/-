package process

import "sync"

// Ring хранит последние строки вывода процесса. Без роста памяти.
type Ring struct {
	mu    sync.Mutex
	lines []string
	max   int
	start int
	count int
}

func NewRing(max int) *Ring {
	if max < 1 {
		max = 1
	}
	return &Ring{max: max, lines: make([]string, max)}
}

func (r *Ring) Add(line string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.count < r.max {
		r.lines[(r.start+r.count)%r.max] = line
		r.count++
		return
	}
	r.lines[r.start] = line
	r.start = (r.start + 1) % r.max
}

func (r *Ring) Clear() {
	r.mu.Lock()
	r.start, r.count = 0, 0
	r.mu.Unlock()
}

func (r *Ring) Tail(n int) []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if n <= 0 || n > r.count {
		n = r.count
	}
	out := make([]string, 0, n)
	for i := r.count - n; i < r.count; i++ {
		out = append(out, r.lines[(r.start+i)%r.max])
	}
	return out
}
