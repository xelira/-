package wsbridge

import (
	"context"
	"strconv"
	"sync"
	"time"

	"ampersand/internal/tgws/config"
)

const (
	defaultPoolMaxAge      = 55 * time.Second
	defaultPoolRefillDelay = 250 * time.Millisecond

	// Failed refills are retried with exponential backoff (mirrors
	// Flowseal/tg-ws-proxy REFILL_BACKOFF_*), but capped lower than upstream's
	// one hour so a transient outage does not leave the pool cold for long.
	defaultRefillBackoffInitial = time.Second
	defaultRefillBackoffMax     = 5 * time.Minute

	// The rotation timer re-checks idle connections at least this often.
	defaultRotateInterval = 5 * time.Second
	// A bucket nobody asked for within this window stops being kept warm.
	defaultRotateIdleStop = 10 * time.Minute
)

type DialFunc func(ctx context.Context, cfg config.Config, targetIP string, domain string) (*Client, error)

type poolKey struct {
	dc       int
	isMedia  bool
	targetIP string
}

type pooledClient struct {
	client  *Client
	created time.Time
}

type Pool struct {
	cfg         config.Config
	maxIdle     int
	maxAge      time.Duration
	refillDelay time.Duration
	dial        DialFunc
	now         func() time.Time
	sleep       func(time.Duration)
	mu          sync.Mutex
	idle        map[poolKey][]pooledClient
	refilling   map[poolKey]bool
	closed      bool

	backoffInitial time.Duration
	backoffMax     time.Duration
	rotateInterval time.Duration
	rotateIdleStop time.Duration
	failures       map[poolKey]int
	refillAfter    map[poolKey]time.Time
	rotating       map[poolKey]*time.Timer
	lastGet        map[poolKey]time.Time
}

func NewPool(cfg config.Config) *Pool {
	if cfg.PoolSize <= 0 {
		return nil
	}

	return &Pool{
		cfg:         cfg,
		maxIdle:     cfg.PoolSize,
		maxAge:      durationOrDefault(cfg.PoolMaxAge, defaultPoolMaxAge),
		refillDelay: durationOrDefault(cfg.PoolRefillDelay, defaultPoolRefillDelay),
		dial:        Dial,
		now:         time.Now,
		sleep:       time.Sleep,
		idle:        make(map[poolKey][]pooledClient),
		refilling:   make(map[poolKey]bool),

		backoffInitial: defaultRefillBackoffInitial,
		backoffMax:     defaultRefillBackoffMax,
		rotateInterval: defaultRotateInterval,
		rotateIdleStop: defaultRotateIdleStop,
		failures:       make(map[poolKey]int),
		refillAfter:    make(map[poolKey]time.Time),
		rotating:       make(map[poolKey]*time.Timer),
		lastGet:        make(map[poolKey]time.Time),
	}
}

func durationOrDefault(value time.Duration, fallback time.Duration) time.Duration {
	if value >= 0 {
		return value
	}
	return fallback
}

func (p *Pool) SetDialFunc(dial DialFunc) {
	if p == nil || dial == nil {
		return
	}
	p.mu.Lock()
	p.dial = dial
	p.mu.Unlock()
}

func (p *Pool) Get(dc int, isMedia bool, targetIP string, domains []string) (*Client, bool) {
	if p == nil || p.maxIdle <= 0 {
		return nil, false
	}

	key := poolKey{dc: dc, isMedia: isMedia, targetIP: targetIP}
	now := p.now()

	var stale []*Client
	var hit *Client

	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return nil, false
	}
	p.lastGet[key] = now

	bucket := p.idle[key]
	kept := make([]pooledClient, 0, len(bucket))
	for _, entry := range bucket {
		if entry.client == nil || (p.maxAge > 0 && now.Sub(entry.created) > p.maxAge) {
			if entry.client != nil {
				stale = append(stale, entry.client)
			}
			continue
		}
		if !entry.client.IsReusable() {
			stale = append(stale, entry.client)
			continue
		}
		kept = append(kept, entry)
	}
	if n := len(kept); n > 0 {
		hit = kept[n-1].client
		kept = kept[:n-1]
	}
	if len(kept) == 0 {
		delete(p.idle, key)
	} else {
		p.idle[key] = kept
	}
	p.mu.Unlock()

	p.closeLater(stale)

	p.scheduleRefill(key, targetIP, domains)
	if hit != nil {
		return hit, true
	}
	return nil, false
}

func (p *Pool) Warmup(dcIPs map[int]string) {
	if p == nil || p.maxIdle <= 0 {
		return
	}

	for dc, targetIP := range dcIPs {
		if targetIP == "" {
			continue
		}
		for _, isMedia := range []bool{false, true} {
			p.scheduleRefill(poolKey{dc: dc, isMedia: isMedia, targetIP: targetIP}, targetIP, warmupDomains(dc, isMedia))
		}
	}
}

func (p *Pool) Close() {
	if p == nil {
		return
	}

	var toClose []*Client

	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return
	}
	p.closed = true
	for _, bucket := range p.idle {
		for _, entry := range bucket {
			if entry.client != nil {
				toClose = append(toClose, entry.client)
			}
		}
	}
	p.idle = make(map[poolKey][]pooledClient)
	p.refilling = make(map[poolKey]bool)
	for _, timer := range p.rotating {
		timer.Stop()
	}
	p.rotating = make(map[poolKey]*time.Timer)
	p.failures = make(map[poolKey]int)
	p.refillAfter = make(map[poolKey]time.Time)
	p.lastGet = make(map[poolKey]time.Time)
	p.mu.Unlock()

	for _, client := range toClose {
		_ = client.Close()
	}
}

func (p *Pool) scheduleRefill(key poolKey, targetIP string, domains []string) {
	if p == nil || p.maxIdle <= 0 {
		return
	}

	p.armRotation(key, targetIP, domains)

	p.mu.Lock()
	if p.closed || p.refilling[key] {
		p.mu.Unlock()
		return
	}
	if until, ok := p.refillAfter[key]; ok && p.now().Before(until) {
		p.mu.Unlock()
		return
	}
	p.refilling[key] = true
	p.mu.Unlock()

	go p.refill(key, targetIP, append([]string(nil), domains...))
}

// noteRefillResult resets the backoff after a successful refill and doubles it
// after a failed one (1s, 2s, 4s ... up to backoffMax). It returns the delay
// applied, or 0 on success.
func (p *Pool) noteRefillResult(key poolKey, connected bool) time.Duration {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return 0
	}
	if connected {
		delete(p.failures, key)
		delete(p.refillAfter, key)
		return 0
	}
	failures := p.failures[key] + 1
	p.failures[key] = failures
	delay := p.backoffInitial
	for i := 1; i < failures && delay < p.backoffMax; i++ {
		delay *= 2
	}
	if delay > p.backoffMax {
		delay = p.backoffMax
	}
	p.refillAfter[key] = p.now().Add(delay)
	return delay
}

// armRotation starts the per-bucket maintenance timer once. Using a timer
// instead of a goroutine keeps unused pools free of background goroutines.
func (p *Pool) armRotation(key poolKey, targetIP string, domains []string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed || p.rotating[key] != nil || p.rotateInterval <= 0 {
		return
	}
	doms := append([]string(nil), domains...)
	p.rotating[key] = time.AfterFunc(p.rotateInterval, func() { p.rotate(key, targetIP, doms) })
}

// rotate drops expired or dead idle connections and tops the bucket up before
// anybody needs it, so a request never pays for a handshake just because the
// previous batch aged out. Mirrors upstream _WsPool._rotate.
func (p *Pool) rotate(key poolKey, targetIP string, domains []string) {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return
	}
	now := p.now()
	if last, ok := p.lastGet[key]; ok && p.rotateIdleStop > 0 && now.Sub(last) > p.rotateIdleStop {
		// Nobody has used this bucket for a while: let it age out quietly.
		delete(p.rotating, key)
		p.mu.Unlock()
		return
	}

	bucket := p.idle[key]
	kept := make([]pooledClient, 0, len(bucket))
	var stale []*Client
	for _, entry := range bucket {
		if entry.client == nil || (p.maxAge > 0 && now.Sub(entry.created) > p.maxAge) || !entry.client.IsReusable() {
			if entry.client != nil {
				stale = append(stale, entry.client)
			}
			continue
		}
		kept = append(kept, entry)
	}
	if len(kept) == 0 {
		delete(p.idle, key)
	} else {
		p.idle[key] = kept
	}

	wake := p.rotateInterval
	if p.maxAge > 0 {
		for _, entry := range kept {
			if d := p.maxAge - now.Sub(entry.created); d < wake {
				wake = d
			}
		}
	}
	if until, ok := p.refillAfter[key]; ok {
		if d := until.Sub(now); d > 0 && d < wake {
			wake = d
		}
	}
	if wake < 50*time.Millisecond {
		wake = 50 * time.Millisecond
	}
	needRefill := len(kept) < p.maxIdle
	p.rotating[key] = time.AfterFunc(wake, func() { p.rotate(key, targetIP, domains) })
	p.mu.Unlock()

	p.closeLater(stale)
	if needRefill {
		p.scheduleRefill(key, targetIP, domains)
	}
}

func (p *Pool) refill(key poolKey, targetIP string, domains []string) {
	defer func() {
		p.mu.Lock()
		delete(p.refilling, key)
		p.mu.Unlock()
	}()

	connected := 0
	for {
		needed, stale := p.trimBucket(key)
		p.closeLater(stale)

		if needed <= 0 {
			break
		}

		if p.refillDelay <= 0 {
			// No pacing requested: open every missing connection at once, like
			// upstream, so a cold bucket fills in one round trip.
			connected += p.refillParallel(key, targetIP, domains, needed)
			break
		}

		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		client := p.connectOne(ctx, targetIP, domains)
		cancel()
		if client == nil {
			break
		}
		if !p.adopt(key, client) {
			return
		}
		connected++

		if needed > 1 {
			p.sleep(p.refillDelay)
		}
	}

	// A refill that produced nothing backs off exponentially so a blocked or
	// unreachable target is not hammered on every Get().
	p.noteRefillResult(key, connected > 0)
}

// refillParallel opens up to n connections concurrently and returns how many
// were stored in the bucket.
func (p *Pool) refillParallel(key poolKey, targetIP string, domains []string, n int) int {
	var wg sync.WaitGroup
	var mu sync.Mutex
	stored := 0
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
			defer cancel()
			client := p.connectOne(ctx, targetIP, domains)
			if client == nil {
				return
			}
			if p.adopt(key, client) {
				mu.Lock()
				stored++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	return stored
}

// adopt stores a freshly opened client in the idle bucket; it closes the
// client and reports false if the pool was closed meanwhile.
func (p *Pool) adopt(key poolKey, client *Client) bool {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		_ = client.Close()
		return false
	}
	p.idle[key] = append(p.idle[key], pooledClient{client: client, created: p.now()})
	p.mu.Unlock()
	return true
}

func (p *Pool) connectOne(ctx context.Context, targetIP string, domains []string) *Client {
	p.mu.Lock()
	dial := p.dial
	cfg := p.cfg
	p.mu.Unlock()

	for _, domain := range domains {
		client, err := dial(ctx, cfg, targetIP, domain)
		if err == nil {
			return client
		}
	}
	return nil
}

func warmupDomains(dc int, isMedia bool) []string {
	dcStr := strconv.Itoa(dc)
	if isMedia {
		return []string{"kws" + dcStr + "-1.web.telegram.org", "kws" + dcStr + ".web.telegram.org"}
	}
	return []string{"kws" + dcStr + ".web.telegram.org", "kws" + dcStr + "-1.web.telegram.org"}
}

func (p *Pool) closeLater(clients []*Client) {
	if len(clients) == 0 {
		return
	}

	go func(toClose []*Client) {
		for _, client := range toClose {
			if client != nil {
				_ = client.Close()
			}
		}
	}(append([]*Client(nil), clients...))
}

func (p *Pool) trimBucket(key poolKey) (int, []*Client) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.closed {
		return 0, nil
	}

	now := p.now()
	bucket := p.idle[key]
	if len(bucket) == 0 {
		delete(p.idle, key)
		return p.maxIdle, nil
	}

	kept := bucket[:0]
	stale := make([]*Client, 0)
	for _, entry := range bucket {
		if entry.client == nil || (p.maxAge > 0 && now.Sub(entry.created) > p.maxAge) {
			if entry.client != nil {
				stale = append(stale, entry.client)
			}
			continue
		}
		if !entry.client.IsReusable() {
			stale = append(stale, entry.client)
			continue
		}
		kept = append(kept, entry)
	}

	if len(kept) == 0 {
		delete(p.idle, key)
	} else {
		p.idle[key] = kept
	}

	return p.maxIdle - len(kept), stale
}
