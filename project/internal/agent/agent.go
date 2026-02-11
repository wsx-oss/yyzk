package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"net/http"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/shirou/gopsutil/v3/cpu"
	"github.com/shirou/gopsutil/v3/host"
	"github.com/shirou/gopsutil/v3/mem"
	netio "github.com/shirou/gopsutil/v3/net"
)

// Metrics is the JSON response returned by the agent
type Metrics struct {
	Hostname         string  `json:"hostname"`
	CPUUsage         float64 `json:"cpu_usage"`
	MemUsage         float64 `json:"mem_usage"`
	Temperature      float64 `json:"temperature"`
	NetworkBandwidth string  `json:"network_bandwidth"`
	OS               string  `json:"os"`
	CPUModel         string  `json:"cpu_model"`
	CPUCores         int     `json:"cpu_cores"`
	MemTotalMB       uint64  `json:"mem_total_mb"`
	Uptime           uint64  `json:"uptime"`
	Timestamp        int64   `json:"timestamp"`
}

// metricsCache holds the latest metrics collected by the background goroutine.
// This allows /metrics to return instantly without blocking on cpu.Percent or WMI calls.
var (
	cacheMu       sync.RWMutex
	cachedMetrics Metrics
	cacheReady    bool
)

// backgroundCollector continuously samples CPU, temperature, and network in a loop.
// It updates the cache every ~1 second so that HTTP requests return instantly.
func backgroundCollector() {
	// Collect static info once
	var hostname, osName, cpuModel string
	var cpuCores int
	var memTotalMB uint64

	osName = runtime.GOOS
	if h, err := host.Info(); err == nil {
		hostname = h.Hostname
	}
	if infos, err := cpu.Info(); err == nil && len(infos) > 0 {
		cpuModel = infos[0].ModelName
	}
	if cores, err := cpu.Counts(true); err == nil {
		cpuCores = cores
	}
	if vm, err := mem.VirtualMemory(); err == nil {
		memTotalMB = vm.Total / 1024 / 1024
	}

	// Initialize previous network counters for delta calculation
	var prevBytesSent, prevBytesRecv uint64
	var prevNetTime time.Time
	if ios, err := netio.IOCounters(false); err == nil && len(ios) > 0 {
		prevBytesSent = ios[0].BytesSent
		prevBytesRecv = ios[0].BytesRecv
		prevNetTime = time.Now()
	}

	// Prime the CPU sampler (first call returns 0)
	cpu.Percent(500*time.Millisecond, false)

	for {
		func() {
			defer func() {
				if r := recover(); r != nil {
					log.Printf("[Agent] backgroundCollector recovered from panic: %v", r)
				}
			}()

			m := Metrics{
				Timestamp:  time.Now().Unix(),
				OS:         osName,
				Hostname:   hostname,
				CPUModel:   cpuModel,
				CPUCores:   cpuCores,
				MemTotalMB: memTotalMB,
			}

			// CPU usage - sample over 500ms for faster response while still accurate
			if percents, err := cpu.Percent(500*time.Millisecond, false); err == nil && len(percents) > 0 {
				m.CPUUsage = math.Round(percents[0]*100) / 100
			}

			// Memory - instant call
			if vm, err := mem.VirtualMemory(); err == nil {
				m.MemUsage = math.Round(vm.UsedPercent*100) / 100
				m.MemTotalMB = vm.Total / 1024 / 1024
			}

			// Uptime - instant call
			if h, err := host.Info(); err == nil {
				m.Uptime = h.Uptime
			}

			// Temperature - try gopsutil first
			if temps, err := host.SensorsTemperatures(); err == nil && len(temps) > 0 {
				var maxTemp float64
				for _, t := range temps {
					if t.Temperature > maxTemp && t.Temperature < 150 {
						maxTemp = t.Temperature
					}
				}
				m.Temperature = math.Round(maxTemp*100) / 100
			}
			// Fallback: on Windows, use WMI
			if m.Temperature == 0 && runtime.GOOS == "windows" {
				m.Temperature = readWindowsTemperature()
			}

			// Network bandwidth - calculate real-time speed (bytes/sec delta)
			if ios, err := netio.IOCounters(false); err == nil && len(ios) > 0 {
				now := time.Now()
				elapsed := now.Sub(prevNetTime).Seconds()
				if elapsed > 0 && prevNetTime.Unix() > 0 {
					deltaSent := ios[0].BytesSent - prevBytesSent
					deltaRecv := ios[0].BytesRecv - prevBytesRecv
					speedBps := float64(deltaSent+deltaRecv) / elapsed
					m.NetworkBandwidth = formatBandwidth(speedBps)
				} else {
					m.NetworkBandwidth = "0B/s"
				}
				prevBytesSent = ios[0].BytesSent
				prevBytesRecv = ios[0].BytesRecv
				prevNetTime = now
			}

			// Update cache
			cacheMu.Lock()
			cachedMetrics = m
			cacheReady = true
			cacheMu.Unlock()
		}()

		// Sleep briefly before next collection cycle (~500ms gap after the 500ms CPU sample = ~1s total)
		time.Sleep(500 * time.Millisecond)
	}
}

// formatBandwidth formats bytes/sec into human-readable bandwidth string
func formatBandwidth(bytesPerSec float64) string {
	if bytesPerSec >= 1024*1024*1024 {
		return fmt.Sprintf("%.1fGB/s", bytesPerSec/(1024*1024*1024))
	} else if bytesPerSec >= 1024*1024 {
		return fmt.Sprintf("%.1fMB/s", bytesPerSec/(1024*1024))
	} else if bytesPerSec >= 1024 {
		return fmt.Sprintf("%.1fKB/s", bytesPerSec/1024)
	}
	return fmt.Sprintf("%.0fB/s", bytesPerSec)
}

// CollectMetrics returns the latest cached metrics (instant, non-blocking).
// If cache is not yet ready, it falls back to a synchronous collection.
func CollectMetrics() Metrics {
	cacheMu.RLock()
	ready := cacheReady
	m := cachedMetrics
	cacheMu.RUnlock()
	if ready {
		return m
	}
	// Fallback: synchronous collection (only on first call before cache is ready)
	return collectMetricsSync()
}

// collectMetricsSync is the synchronous fallback used before the background collector is ready
func collectMetricsSync() Metrics {
	m := Metrics{
		Timestamp: time.Now().Unix(),
		OS:        runtime.GOOS,
	}
	if h, err := host.Info(); err == nil {
		m.Hostname = h.Hostname
		m.Uptime = h.Uptime
	}
	if percents, err := cpu.Percent(500*time.Millisecond, false); err == nil && len(percents) > 0 {
		m.CPUUsage = math.Round(percents[0]*100) / 100
	}
	if infos, err := cpu.Info(); err == nil && len(infos) > 0 {
		m.CPUModel = infos[0].ModelName
	}
	if cores, err := cpu.Counts(true); err == nil {
		m.CPUCores = cores
	}
	if vm, err := mem.VirtualMemory(); err == nil {
		m.MemUsage = math.Round(vm.UsedPercent*100) / 100
		m.MemTotalMB = vm.Total / 1024 / 1024
	}
	if temps, err := host.SensorsTemperatures(); err == nil && len(temps) > 0 {
		var maxTemp float64
		for _, t := range temps {
			if t.Temperature > maxTemp && t.Temperature < 150 {
				maxTemp = t.Temperature
			}
		}
		m.Temperature = math.Round(maxTemp*100) / 100
	}
	if m.Temperature == 0 && runtime.GOOS == "windows" {
		m.Temperature = readWindowsTemperature()
	}
	if ios, err := netio.IOCounters(false); err == nil && len(ios) > 0 {
		m.NetworkBandwidth = "0B/s"
	}
	return m
}

// readWindowsTemperature reads CPU temperature on Windows via WMI MSAcpi_ThermalZoneTemperature.
// Returns temperature in Celsius, or 0 if unavailable.
func readWindowsTemperature() float64 {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "powershell", "-NoProfile", "-NonInteractive", "-Command",
		"(Get-WmiObject MSAcpi_ThermalZoneTemperature -Namespace 'root/wmi' -ErrorAction SilentlyContinue | Select-Object -First 1).CurrentTemperature")
	out, err := cmd.Output()
	if err != nil {
		return 0
	}
	s := strings.TrimSpace(string(out))
	if s == "" {
		return 0
	}
	val, err := strconv.ParseFloat(s, 64)
	if err != nil || val <= 0 {
		return 0
	}
	// WMI returns tenths of Kelvin, convert to Celsius
	celsius := (val / 10.0) - 273.15
	if celsius < 0 || celsius > 150 {
		return 0
	}
	return math.Round(celsius*100) / 100
}

// StartBackground starts the hardware agent HTTP server in the background on the given port.
// It returns immediately. The server runs until the process exits.
func StartBackground(port int) {
	// Start the background metrics collector
	go backgroundCollector()

	addr := fmt.Sprintf(":%d", port)

	mux := http.NewServeMux()
	mux.HandleFunc("/metrics", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
		metrics := CollectMetrics()
		json.NewEncoder(w).Encode(metrics)
	})
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		w.Write([]byte(`{"status":"ok"}`))
	})

	go func() {
		log.Printf("[Agent] Hardware Agent started on %s", addr)
		if err := http.ListenAndServe(addr, mux); err != nil {
			log.Printf("[Agent] Failed to start on %s: %v", addr, err)
		}
	}()
}
