package taskpool

import (
	"context"
	"fmt"
	"math/rand"
	"sync/atomic"
	"testing"
	"time"
)

// ---------- Unit Tests ----------

func TestPoolBasic(t *testing.T) {
	p := New(PoolConfig{IOWorkers: 4, CPUWorkers: 2, QueueSize: 64, DefaultTimeout: 5 * time.Second})
	defer p.Shutdown(2 * time.Second)

	var counter int64
	for i := 0; i < 100; i++ {
		err := p.Submit(Task{
			Name:  fmt.Sprintf("test-%d", i),
			Group: "test",
			Mode:  ModeIO,
			Fn: func(ctx context.Context) error {
				atomic.AddInt64(&counter, 1)
				return nil
			},
		})
		if err != nil {
			t.Fatalf("submit failed: %v", err)
		}
	}
	// Wait for tasks to complete
	time.Sleep(500 * time.Millisecond)
	m := p.Metrics()
	if m.Completed < 100 {
		t.Errorf("expected 100 completed, got %d (pending=%d)", m.Completed, m.Pending)
	}
	if atomic.LoadInt64(&counter) != 100 {
		t.Errorf("expected counter=100, got %d", counter)
	}
}

func TestPoolTimeout(t *testing.T) {
	p := New(PoolConfig{IOWorkers: 2, CPUWorkers: 1, QueueSize: 16, DefaultTimeout: 200 * time.Millisecond})
	defer p.Shutdown(2 * time.Second)

	err := p.Submit(Task{
		Name:  "slow",
		Group: "test",
		Mode:  ModeIO,
		Fn: func(ctx context.Context) error {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(5 * time.Second):
				return nil
			}
		},
	})
	if err != nil {
		t.Fatalf("submit failed: %v", err)
	}
	time.Sleep(500 * time.Millisecond)
	m := p.Metrics()
	if m.Failed < 1 {
		t.Errorf("expected at least 1 failed (timeout), got %d", m.Failed)
	}
}

func TestPoolPanicRecovery(t *testing.T) {
	p := New(PoolConfig{IOWorkers: 2, CPUWorkers: 1, QueueSize: 16, DefaultTimeout: 5 * time.Second})
	defer p.Shutdown(2 * time.Second)

	_ = p.Submit(Task{
		Name:  "panic-task",
		Group: "test",
		Mode:  ModeIO,
		Fn: func(ctx context.Context) error {
			panic("test panic")
		},
	})
	time.Sleep(300 * time.Millisecond)
	m := p.Metrics()
	if m.Recovered < 1 {
		t.Errorf("expected recovered >= 1, got %d", m.Recovered)
	}
}

func TestPoolCPUTasks(t *testing.T) {
	p := New(PoolConfig{IOWorkers: 4, CPUWorkers: 2, QueueSize: 64, DefaultTimeout: 5 * time.Second})
	defer p.Shutdown(2 * time.Second)

	var counter int64
	for i := 0; i < 50; i++ {
		_ = p.Submit(Task{
			Name:  fmt.Sprintf("cpu-%d", i),
			Group: "compute",
			Mode:  ModeCPU,
			Fn: func(ctx context.Context) error {
				// Simulate CPU work
				sum := 0.0
				for j := 0; j < 10000; j++ {
					sum += float64(j) * 0.001
				}
				atomic.AddInt64(&counter, 1)
				return nil
			},
		})
	}
	time.Sleep(1 * time.Second)
	if atomic.LoadInt64(&counter) != 50 {
		t.Errorf("expected 50 CPU tasks done, got %d", counter)
	}
}

func TestPoolPeriodicTask(t *testing.T) {
	p := New(PoolConfig{IOWorkers: 4, CPUWorkers: 2, QueueSize: 64, DefaultTimeout: 5 * time.Second})
	defer p.Shutdown(2 * time.Second)

	var count int64
	p.SchedulePeriodic("tick", "test", 100*time.Millisecond, PriorityNormal, func(ctx context.Context) error {
		atomic.AddInt64(&count, 1)
		return nil
	})
	time.Sleep(550 * time.Millisecond)
	p.CancelPeriodic("tick")
	got := atomic.LoadInt64(&count)
	// Should run immediately + ~4-5 ticks in 550ms at 100ms interval
	if got < 4 {
		t.Errorf("expected periodic count >= 4, got %d", got)
	}
}

func TestPoolGroupMetrics(t *testing.T) {
	p := New(PoolConfig{IOWorkers: 4, CPUWorkers: 2, QueueSize: 64, DefaultTimeout: 5 * time.Second})
	defer p.Shutdown(2 * time.Second)

	for i := 0; i < 20; i++ {
		group := "alpha"
		if i%2 == 0 {
			group = "beta"
		}
		_ = p.Submit(Task{
			Name:  fmt.Sprintf("g-%d", i),
			Group: group,
			Mode:  ModeIO,
			Fn:    func(ctx context.Context) error { return nil },
		})
	}
	time.Sleep(300 * time.Millisecond)
	m := p.Metrics()
	if m.Groups["alpha"].Completed != 10 {
		t.Errorf("expected alpha.completed=10, got %d", m.Groups["alpha"].Completed)
	}
	if m.Groups["beta"].Completed != 10 {
		t.Errorf("expected beta.completed=10, got %d", m.Groups["beta"].Completed)
	}
}

func TestRunBatch(t *testing.T) {
	p := New(PoolConfig{IOWorkers: 8, CPUWorkers: 2, QueueSize: 128, DefaultTimeout: 5 * time.Second})
	defer p.Shutdown(2 * time.Second)

	tasks := make([]Task, 30)
	var counter int64
	for i := range tasks {
		tasks[i] = Task{
			Name:  fmt.Sprintf("batch-%d", i),
			Group: "batch_test",
			Mode:  ModeIO,
			Fn: func(ctx context.Context) error {
				atomic.AddInt64(&counter, 1)
				return nil
			},
		}
	}
	errs := p.RunBatch(tasks)
	for i, e := range errs {
		if e != nil {
			t.Errorf("task %d failed: %v", i, e)
		}
	}
	if atomic.LoadInt64(&counter) != 30 {
		t.Errorf("expected 30, got %d", counter)
	}
}

// ---------- Stress / Benchmark Tests ----------

// BenchmarkPoolIOThroughput measures IO task throughput under high load.
func BenchmarkPoolIOThroughput(b *testing.B) {
	p := New(PoolConfig{IOWorkers: 16, CPUWorkers: 4, QueueSize: 2048, DefaultTimeout: 10 * time.Second})
	defer p.Shutdown(3 * time.Second)

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_ = p.Submit(Task{
				Name:  "bench-io",
				Group: "bench",
				Mode:  ModeIO,
				Fn: func(ctx context.Context) error {
					time.Sleep(time.Duration(rand.Intn(100)) * time.Microsecond)
					return nil
				},
			})
		}
	})
	// drain
	time.Sleep(200 * time.Millisecond)
	m := p.Metrics()
	b.ReportMetric(float64(m.Completed), "completed")
	b.ReportMetric(m.AvgDurationMs, "avg_ms")
}

// BenchmarkPoolCPUThroughput measures CPU task throughput under load.
func BenchmarkPoolCPUThroughput(b *testing.B) {
	p := New(PoolConfig{IOWorkers: 4, CPUWorkers: 4, QueueSize: 512, DefaultTimeout: 10 * time.Second})
	defer p.Shutdown(3 * time.Second)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = p.Submit(Task{
			Name:  "bench-cpu",
			Group: "bench_cpu",
			Mode:  ModeCPU,
			Fn: func(ctx context.Context) error {
				sum := 0.0
				for j := 0; j < 5000; j++ {
					sum += float64(j) * 0.001
				}
				return nil
			},
		})
	}
	time.Sleep(500 * time.Millisecond)
}

// TestStressConcurrentSubmit fires 5000 tasks from 50 goroutines simultaneously.
func TestStressConcurrentSubmit(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping stress test in short mode")
	}
	p := New(PoolConfig{IOWorkers: 16, CPUWorkers: 4, QueueSize: 4096, DefaultTimeout: 10 * time.Second})
	defer p.Shutdown(5 * time.Second)

	const goroutines = 50
	const tasksPerGoroutine = 100
	total := int64(goroutines * tasksPerGoroutine)
	var completed int64
	var submitErrors int64

	done := make(chan struct{})
	for g := 0; g < goroutines; g++ {
		go func(gid int) {
			for i := 0; i < tasksPerGoroutine; i++ {
				err := p.Submit(Task{
					Name:     fmt.Sprintf("stress-%d-%d", gid, i),
					Group:    "stress",
					Priority: rand.Intn(4),
					Mode:     ModeIO,
					Fn: func(ctx context.Context) error {
						time.Sleep(time.Duration(rand.Intn(500)) * time.Microsecond)
						atomic.AddInt64(&completed, 1)
						return nil
					},
				})
				if err != nil {
					atomic.AddInt64(&submitErrors, 1)
				}
			}
			done <- struct{}{}
		}(g)
	}
	for g := 0; g < goroutines; g++ {
		<-done
	}

	// Wait for all tasks to drain
	deadline := time.After(10 * time.Second)
	for {
		c := atomic.LoadInt64(&completed)
		se := atomic.LoadInt64(&submitErrors)
		if c+se >= total {
			break
		}
		select {
		case <-deadline:
			t.Fatalf("timed out: completed=%d submitErrors=%d total=%d", c, se, total)
		case <-time.After(50 * time.Millisecond):
		}
	}

	m := p.Metrics()
	t.Logf("Stress test: submitted=%d completed=%d failed=%d recovered=%d avg_ms=%.2f",
		m.Submitted, m.Completed, m.Failed, m.Recovered, m.AvgDurationMs)

	if m.Completed+m.Failed < total-submitErrors {
		t.Errorf("expected %d processed, got completed=%d failed=%d", total-submitErrors, m.Completed, m.Failed)
	}
}

// TestStressMixedWorkload simulates a realistic workload with IO, CPU, and periodic tasks.
func TestStressMixedWorkload(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping mixed workload stress test in short mode")
	}
	p := New(PoolConfig{IOWorkers: 16, CPUWorkers: 4, QueueSize: 2048, DefaultTimeout: 10 * time.Second})
	defer p.Shutdown(5 * time.Second)

	var ioCompleted, cpuCompleted, periodicCount int64

	// Periodic task simulating monitoring
	p.SchedulePeriodic("monitor", "monitoring", 50*time.Millisecond, PriorityHigh, func(ctx context.Context) error {
		atomic.AddInt64(&periodicCount, 1)
		return nil
	})

	// IO tasks simulating telemetry writes
	for i := 0; i < 1000; i++ {
		_ = p.Submit(Task{
			Name:     fmt.Sprintf("io-%d", i),
			Group:    "telemetry",
			Priority: PriorityRealtime,
			Mode:     ModeIO,
			Fn: func(ctx context.Context) error {
				time.Sleep(time.Duration(rand.Intn(200)) * time.Microsecond)
				atomic.AddInt64(&ioCompleted, 1)
				return nil
			},
		})
	}

	// CPU tasks simulating route planning
	for i := 0; i < 100; i++ {
		_ = p.Submit(Task{
			Name:     fmt.Sprintf("cpu-%d", i),
			Group:    "route_planning",
			Priority: PriorityHigh,
			Mode:     ModeCPU,
			Fn: func(ctx context.Context) error {
				sum := 0.0
				for j := 0; j < 10000; j++ {
					sum += float64(j) * 0.001
				}
				atomic.AddInt64(&cpuCompleted, 1)
				return nil
			},
		})
	}

	// Wait for completion
	deadline := time.After(10 * time.Second)
	for {
		io := atomic.LoadInt64(&ioCompleted)
		cpu := atomic.LoadInt64(&cpuCompleted)
		if io >= 1000 && cpu >= 100 {
			break
		}
		select {
		case <-deadline:
			t.Fatalf("timed out: io=%d/1000 cpu=%d/100", io, cpu)
		case <-time.After(50 * time.Millisecond):
		}
	}

	p.CancelPeriodic("monitor")
	pc := atomic.LoadInt64(&periodicCount)
	m := p.Metrics()

	t.Logf("Mixed workload: io=%d cpu=%d periodic=%d | pool: submitted=%d completed=%d failed=%d avg=%.2fms",
		ioCompleted, cpuCompleted, pc, m.Submitted, m.Completed, m.Failed, m.AvgDurationMs)

	if pc < 1 {
		t.Errorf("expected periodic count >= 1, got %d", pc)
	}
	if gm, ok := m.Groups["telemetry"]; ok {
		t.Logf("  telemetry group: submitted=%d completed=%d avg=%.2fms", gm.Submitted, gm.Completed, gm.AvgMs)
	}
	if gm, ok := m.Groups["route_planning"]; ok {
		t.Logf("  route_planning group: submitted=%d completed=%d avg=%.2fms", gm.Submitted, gm.Completed, gm.AvgMs)
	}
}
