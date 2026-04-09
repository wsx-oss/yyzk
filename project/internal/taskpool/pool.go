package taskpool

import (
	"context"
	"fmt"
	"log"
	"runtime/debug"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

// Priority levels for tasks.
const (
	PriorityRealtime = 0 // highest – real-time tasks execute first
	PriorityHigh     = 1
	PriorityNormal   = 2
	PriorityLow      = 3
)

// TaskMode describes the execution mode for a task.
type TaskMode int

const (
	ModeIO  TaskMode = iota // I/O-bound: runs in the shared goroutine pool
	ModeCPU                 // CPU-bound: runs in a dedicated goroutine (limited concurrency)
)

// Task represents a unit of work submitted to the pool.
type Task struct {
	Name     string        // human-readable name (for logging / metrics)
	Group    string        // logical group: "patrol", "simulation", "route", "stats", etc.
	Priority int           // 0 = realtime, 3 = low
	Mode     TaskMode      // IO or CPU
	Timeout  time.Duration // per-task timeout; 0 = use pool default
	Fn       func(ctx context.Context) error
}

// taskEntry is the internal wrapper used by the priority queue.
type taskEntry struct {
	task      Task
	submitted time.Time
}

// PoolConfig holds tuning knobs for the worker pool.
type PoolConfig struct {
	IOWorkers      int           // number of I/O goroutine workers (default 16)
	CPUWorkers     int           // number of CPU goroutine workers (default 4)
	QueueSize      int           // max pending tasks before Submit blocks (default 1024)
	DefaultTimeout time.Duration // default per-task timeout (default 30s)
}

func (c PoolConfig) withDefaults() PoolConfig {
	if c.IOWorkers <= 0 {
		c.IOWorkers = 16
	}
	if c.CPUWorkers <= 0 {
		c.CPUWorkers = 4
	}
	if c.QueueSize <= 0 {
		c.QueueSize = 1024
	}
	if c.DefaultTimeout <= 0 {
		c.DefaultTimeout = 30 * time.Second
	}
	return c
}

// Pool is the unified concurrent task execution engine.
type Pool struct {
	cfg    PoolConfig
	ctx    context.Context
	cancel context.CancelFunc

	ioCh  chan taskEntry
	cpuCh chan taskEntry

	wg sync.WaitGroup

	// metrics (atomic)
	submitted  int64
	completed  int64
	failed     int64
	recovered  int64
	ioActive   int64
	cpuActive  int64
	totalNanos int64 // cumulative task duration in nanoseconds

	// per-group metrics
	groupMu    sync.RWMutex
	groupStats map[string]*GroupMetrics

	// periodic tasks
	periodicMu    sync.Mutex
	periodicTasks map[string]context.CancelFunc
}

// GroupMetrics holds per-group counters.
type GroupMetrics struct {
	Submitted int64
	Completed int64
	Failed    int64
	TotalNs   int64
}

// New creates and starts a worker pool.
func New(cfg PoolConfig) *Pool {
	cfg = cfg.withDefaults()
	ctx, cancel := context.WithCancel(context.Background())
	p := &Pool{
		cfg:           cfg,
		ctx:           ctx,
		cancel:        cancel,
		ioCh:          make(chan taskEntry, cfg.QueueSize),
		cpuCh:         make(chan taskEntry, cfg.QueueSize/2+1),
		groupStats:    make(map[string]*GroupMetrics),
		periodicTasks: make(map[string]context.CancelFunc),
	}
	// start IO workers
	for i := 0; i < cfg.IOWorkers; i++ {
		p.wg.Add(1)
		go p.worker(p.ioCh, &p.ioActive)
	}
	// start CPU workers
	for i := 0; i < cfg.CPUWorkers; i++ {
		p.wg.Add(1)
		go p.worker(p.cpuCh, &p.cpuActive)
	}
	log.Printf("[TaskPool] started: IO workers=%d, CPU workers=%d, queue=%d, timeout=%v",
		cfg.IOWorkers, cfg.CPUWorkers, cfg.QueueSize, cfg.DefaultTimeout)
	return p
}

// Submit enqueues a task for execution. Non-blocking if queue has capacity;
// blocks if the queue is full. Returns error if pool is shut down.
func (p *Pool) Submit(t Task) error {
	select {
	case <-p.ctx.Done():
		return fmt.Errorf("pool is shut down")
	default:
	}
	entry := taskEntry{task: t, submitted: time.Now()}
	atomic.AddInt64(&p.submitted, 1)
	p.addGroupSubmit(t.Group)

	ch := p.ioCh
	if t.Mode == ModeCPU {
		ch = p.cpuCh
	}
	select {
	case ch <- entry:
		return nil
	case <-p.ctx.Done():
		return fmt.Errorf("pool is shut down")
	}
}

// SubmitFunc is a convenience wrapper.
func (p *Pool) SubmitFunc(name, group string, fn func(ctx context.Context) error) error {
	return p.Submit(Task{Name: name, Group: group, Priority: PriorityNormal, Mode: ModeIO, Fn: fn})
}

// SchedulePeriodic registers a repeating task. If a task with the same name
// already exists it is replaced. The task runs immediately, then every interval.
func (p *Pool) SchedulePeriodic(name, group string, interval time.Duration, priority int, fn func(ctx context.Context) error) {
	p.periodicMu.Lock()
	// cancel existing
	if cancel, ok := p.periodicTasks[name]; ok {
		cancel()
	}
	taskCtx, taskCancel := context.WithCancel(p.ctx)
	p.periodicTasks[name] = taskCancel
	p.periodicMu.Unlock()

	p.wg.Add(1)
	go func() {
		defer p.wg.Done()
		// run once immediately
		_ = p.Submit(Task{Name: name, Group: group, Priority: priority, Mode: ModeIO, Fn: fn})

		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-taskCtx.Done():
				return
			case <-ticker.C:
				_ = p.Submit(Task{Name: name, Group: group, Priority: priority, Mode: ModeIO, Fn: fn})
			}
		}
	}()
	log.Printf("[TaskPool] scheduled periodic task %q every %v", name, interval)
}

// CancelPeriodic stops a named periodic task.
func (p *Pool) CancelPeriodic(name string) {
	p.periodicMu.Lock()
	defer p.periodicMu.Unlock()
	if cancel, ok := p.periodicTasks[name]; ok {
		cancel()
		delete(p.periodicTasks, name)
	}
}

// Shutdown gracefully waits for all workers to finish.
func (p *Pool) Shutdown(timeout time.Duration) {
	log.Println("[TaskPool] shutting down...")
	p.cancel()
	done := make(chan struct{})
	go func() {
		p.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		log.Println("[TaskPool] shutdown complete")
	case <-time.After(timeout):
		log.Println("[TaskPool] shutdown timed out, some workers may still be running")
	}
}

// worker is the main loop for a pool goroutine.
func (p *Pool) worker(ch <-chan taskEntry, activeCounter *int64) {
	defer p.wg.Done()
	for {
		select {
		case <-p.ctx.Done():
			return
		case entry, ok := <-ch:
			if !ok {
				return
			}
			atomic.AddInt64(activeCounter, 1)
			p.executeTask(entry)
			atomic.AddInt64(activeCounter, -1)
		}
	}
}

// executeTask runs a single task with timeout and panic recovery.
func (p *Pool) executeTask(entry taskEntry) {
	t := entry.task
	timeout := t.Timeout
	if timeout <= 0 {
		timeout = p.cfg.DefaultTimeout
	}

	ctx, cancel := context.WithTimeout(p.ctx, timeout)
	defer cancel()

	start := time.Now()
	var err error

	func() {
		defer func() {
			if r := recover(); r != nil {
				err = fmt.Errorf("panic in task %q: %v\n%s", t.Name, r, debug.Stack())
				atomic.AddInt64(&p.recovered, 1)
				log.Printf("[TaskPool] RECOVERED panic in task %q: %v", t.Name, r)
			}
		}()
		err = t.Fn(ctx)
	}()

	elapsed := time.Since(start)
	atomic.AddInt64(&p.totalNanos, int64(elapsed))

	if err != nil {
		atomic.AddInt64(&p.failed, 1)
		p.addGroupFail(t.Group)
		if ctx.Err() == context.DeadlineExceeded {
			log.Printf("[TaskPool] task %q timed out after %v", t.Name, timeout)
		} else {
			log.Printf("[TaskPool] task %q failed (%v): %v", t.Name, elapsed, err)
		}
	} else {
		atomic.AddInt64(&p.completed, 1)
		p.addGroupComplete(t.Group, int64(elapsed))
	}
}

// ---------- Metrics ----------

// Metrics returns a snapshot of pool metrics.
func (p *Pool) Metrics() PoolMetrics {
	submitted := atomic.LoadInt64(&p.submitted)
	completed := atomic.LoadInt64(&p.completed)
	failed := atomic.LoadInt64(&p.failed)
	recovered := atomic.LoadInt64(&p.recovered)
	totalNs := atomic.LoadInt64(&p.totalNanos)

	var avgMs float64
	done := completed + failed
	if done > 0 {
		avgMs = float64(totalNs) / float64(done) / 1e6
	}

	groups := map[string]GroupMetricsSnapshot{}
	p.groupMu.RLock()
	for k, v := range p.groupStats {
		gs := GroupMetricsSnapshot{
			Submitted: atomic.LoadInt64(&v.Submitted),
			Completed: atomic.LoadInt64(&v.Completed),
			Failed:    atomic.LoadInt64(&v.Failed),
		}
		gDone := gs.Completed + gs.Failed
		if gDone > 0 {
			gs.AvgMs = float64(atomic.LoadInt64(&v.TotalNs)) / float64(gDone) / 1e6
		}
		groups[k] = gs
	}
	p.groupMu.RUnlock()

	return PoolMetrics{
		IOWorkers:     p.cfg.IOWorkers,
		CPUWorkers:    p.cfg.CPUWorkers,
		IOActive:      int(atomic.LoadInt64(&p.ioActive)),
		CPUActive:     int(atomic.LoadInt64(&p.cpuActive)),
		IOQueueLen:    len(p.ioCh),
		CPUQueueLen:   len(p.cpuCh),
		IOQueueCap:    cap(p.ioCh),
		CPUQueueCap:   cap(p.cpuCh),
		Submitted:     submitted,
		Completed:     completed,
		Failed:        failed,
		Recovered:     recovered,
		Pending:       submitted - completed - failed,
		AvgDurationMs: avgMs,
		Groups:        groups,
	}
}

// PoolMetrics is the public metrics snapshot.
type PoolMetrics struct {
	IOWorkers     int                              `json:"io_workers"`
	CPUWorkers    int                              `json:"cpu_workers"`
	IOActive      int                              `json:"io_active"`
	CPUActive     int                              `json:"cpu_active"`
	IOQueueLen    int                              `json:"io_queue_len"`
	CPUQueueLen   int                              `json:"cpu_queue_len"`
	IOQueueCap    int                              `json:"io_queue_cap"`
	CPUQueueCap   int                              `json:"cpu_queue_cap"`
	Submitted     int64                            `json:"submitted"`
	Completed     int64                            `json:"completed"`
	Failed        int64                            `json:"failed"`
	Recovered     int64                            `json:"recovered"`
	Pending       int64                            `json:"pending"`
	AvgDurationMs float64                          `json:"avg_duration_ms"`
	Groups        map[string]GroupMetricsSnapshot  `json:"groups"`
}

// GroupMetricsSnapshot is a per-group metrics snapshot.
type GroupMetricsSnapshot struct {
	Submitted int64   `json:"submitted"`
	Completed int64   `json:"completed"`
	Failed    int64   `json:"failed"`
	AvgMs     float64 `json:"avg_ms"`
}

func (p *Pool) getGroup(group string) *GroupMetrics {
	if group == "" {
		group = "default"
	}
	p.groupMu.RLock()
	g, ok := p.groupStats[group]
	p.groupMu.RUnlock()
	if ok {
		return g
	}
	p.groupMu.Lock()
	defer p.groupMu.Unlock()
	// double-check
	if g, ok = p.groupStats[group]; ok {
		return g
	}
	g = &GroupMetrics{}
	p.groupStats[group] = g
	return g
}

func (p *Pool) addGroupSubmit(group string) {
	atomic.AddInt64(&p.getGroup(group).Submitted, 1)
}
func (p *Pool) addGroupFail(group string) {
	atomic.AddInt64(&p.getGroup(group).Failed, 1)
}
func (p *Pool) addGroupComplete(group string, elapsedNs int64) {
	g := p.getGroup(group)
	atomic.AddInt64(&g.Completed, 1)
	atomic.AddInt64(&g.TotalNs, elapsedNs)
}

// ---------- Batch executor helper ----------

// RunBatch submits N tasks and waits for all to complete. Useful for
// fan-out patterns like refreshing all hardware items concurrently.
func (p *Pool) RunBatch(tasks []Task) []error {
	// sort by priority so higher-priority tasks are submitted first
	sorted := make([]Task, len(tasks))
	copy(sorted, tasks)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Priority < sorted[j].Priority })

	errs := make([]error, len(tasks))
	var wg sync.WaitGroup
	for i, t := range sorted {
		wg.Add(1)
		idx := i
		origFn := t.Fn
		t.Fn = func(ctx context.Context) error {
			defer wg.Done()
			err := origFn(ctx)
			errs[idx] = err
			return err
		}
		if err := p.Submit(t); err != nil {
			errs[idx] = err
			wg.Done()
		}
	}
	wg.Wait()
	return errs
}
