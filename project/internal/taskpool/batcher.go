package taskpool

import (
	"context"
	"log"
	"sync"
	"time"
)

// WriteBatcher collects individual write operations and flushes them in
// batches to reduce database contention and I/O overhead.
type WriteBatcher struct {
	mu       sync.Mutex
	buf      []func()
	maxSize  int
	interval time.Duration
	ctx      context.Context
	cancel   context.CancelFunc
	wg       sync.WaitGroup
	name     string
}

// NewWriteBatcher creates a batcher that flushes when the buffer reaches
// maxSize items OR interval elapses, whichever comes first.
func NewWriteBatcher(name string, maxSize int, interval time.Duration) *WriteBatcher {
	if maxSize <= 0 {
		maxSize = 50
	}
	if interval <= 0 {
		interval = 2 * time.Second
	}
	ctx, cancel := context.WithCancel(context.Background())
	b := &WriteBatcher{
		buf:      make([]func(), 0, maxSize),
		maxSize:  maxSize,
		interval: interval,
		ctx:      ctx,
		cancel:   cancel,
		name:     name,
	}
	b.wg.Add(1)
	go b.ticker()
	return b
}

// Add enqueues a write operation. If the buffer is full it is flushed
// immediately on the caller's goroutine.
func (b *WriteBatcher) Add(fn func()) {
	b.mu.Lock()
	b.buf = append(b.buf, fn)
	if len(b.buf) >= b.maxSize {
		batch := b.buf
		b.buf = make([]func(), 0, b.maxSize)
		b.mu.Unlock()
		b.flush(batch)
		return
	}
	b.mu.Unlock()
}

// Flush forces an immediate flush of all pending writes.
func (b *WriteBatcher) Flush() {
	b.mu.Lock()
	batch := b.buf
	b.buf = make([]func(), 0, b.maxSize)
	b.mu.Unlock()
	b.flush(batch)
}

// Stop flushes remaining writes and stops the background ticker.
func (b *WriteBatcher) Stop() {
	b.cancel()
	b.wg.Wait()
	b.Flush()
}

func (b *WriteBatcher) ticker() {
	defer b.wg.Done()
	t := time.NewTicker(b.interval)
	defer t.Stop()
	for {
		select {
		case <-b.ctx.Done():
			return
		case <-t.C:
			b.Flush()
		}
	}
}

func (b *WriteBatcher) flush(batch []func()) {
	if len(batch) == 0 {
		return
	}
	for _, fn := range batch {
		func() {
			defer func() {
				if r := recover(); r != nil {
					log.Printf("[WriteBatcher:%s] panic in flush: %v", b.name, r)
				}
			}()
			fn()
		}()
	}
}

// ---------- Throttler ----------

// Throttler limits the rate of an operation. Useful for WebSocket push
// to avoid flooding clients with high-frequency updates.
type Throttler struct {
	mu       sync.Mutex
	interval time.Duration
	last     time.Time
	pending  interface{}
	timer    *time.Timer
	fn       func(data interface{})
}

// NewThrottler creates a throttler that calls fn at most once per interval.
// If multiple updates arrive within one interval, only the latest is used.
func NewThrottler(interval time.Duration, fn func(data interface{})) *Throttler {
	return &Throttler{
		interval: interval,
		fn:       fn,
	}
}

// Update provides new data. If the throttle window has passed, it fires
// immediately. Otherwise it schedules a deferred fire with the latest data.
func (t *Throttler) Update(data interface{}) {
	t.mu.Lock()
	defer t.mu.Unlock()

	now := time.Now()
	if now.Sub(t.last) >= t.interval {
		t.last = now
		t.pending = nil
		if t.timer != nil {
			t.timer.Stop()
			t.timer = nil
		}
		go t.fn(data)
		return
	}

	// within throttle window – store latest and schedule
	t.pending = data
	if t.timer == nil {
		remaining := t.interval - now.Sub(t.last)
		t.timer = time.AfterFunc(remaining, func() {
			t.mu.Lock()
			d := t.pending
			t.pending = nil
			t.timer = nil
			t.last = time.Now()
			t.mu.Unlock()
			if d != nil {
				t.fn(d)
			}
		})
	}
}

// ---------- StatsCache ----------

// StatsCache provides async-refreshed cached data. Instead of computing
// expensive stats on every request, a background goroutine refreshes
// the cache at a set interval.
type StatsCache struct {
	mu       sync.RWMutex
	data     interface{}
	ctx      context.Context
	cancel   context.CancelFunc
	wg       sync.WaitGroup
}

// NewStatsCache starts a background refresh loop. computeFn is called
// every interval to produce new cached data.
func NewStatsCache(interval time.Duration, computeFn func() interface{}) *StatsCache {
	ctx, cancel := context.WithCancel(context.Background())
	sc := &StatsCache{ctx: ctx, cancel: cancel}

	// compute once synchronously so cache is warm from the start
	sc.data = computeFn()

	sc.wg.Add(1)
	go func() {
		defer sc.wg.Done()
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				newData := computeFn()
				sc.mu.Lock()
				sc.data = newData
				sc.mu.Unlock()
			}
		}
	}()
	return sc
}

// Get returns the latest cached data.
func (sc *StatsCache) Get() interface{} {
	sc.mu.RLock()
	defer sc.mu.RUnlock()
	return sc.data
}

// Stop terminates the background refresh.
func (sc *StatsCache) Stop() {
	sc.cancel()
	sc.wg.Wait()
}
