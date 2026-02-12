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

	// Temperature is collected in a separate goroutine to avoid blocking the main loop
	tempMu    sync.RWMutex
	tempValue float64

	// CPU is collected in a separate goroutine with blocking 1s sample for accuracy
	cpuMu    sync.RWMutex
	cpuValue float64
)

// backgroundCPUCollector samples CPU usage in a dedicated goroutine.
// cpu.Percent(1s) blocks for 1 second to get an accurate reading.
// A sliding window of 3 samples (~3s rolling average) smooths the value.
func backgroundCPUCollector() {
	cpu.Percent(time.Second, false) // prime (first call returns 0)
	const windowSize = 3
	window := make([]float64, 0, windowSize)
	for {
		func() {
			defer func() {
				if r := recover(); r != nil {
					log.Printf("[Agent] backgroundCPUCollector recovered from panic: %v", r)
				}
			}()
			if percents, err := cpu.Percent(time.Second, false); err == nil && len(percents) > 0 {
				window = append(window, percents[0])
				if len(window) > windowSize {
					window = window[1:]
				}
				var sum float64
				for _, v := range window {
					sum += v
				}
				avg := math.Round(sum/float64(len(window))*100) / 100
				cpuMu.Lock()
				cpuValue = avg
				cpuMu.Unlock()
			}
		}()
	}
}

// backgroundTempCollector collects temperature in a separate goroutine.
// Temperature collection on Windows requires spawning PowerShell (~1-3s),
// so we run it independently to avoid blocking the fast metrics loop.
func backgroundTempCollector() {
	for {
		func() {
			defer func() {
				if r := recover(); r != nil {
					log.Printf("[Agent] backgroundTempCollector recovered from panic: %v", r)
				}
			}()
			var t float64
			if temps, err := host.SensorsTemperatures(); err == nil && len(temps) > 0 {
				var maxTemp float64
				for _, s := range temps {
					if s.Temperature > maxTemp && s.Temperature < 150 {
						maxTemp = s.Temperature
					}
				}
				t = math.Round(maxTemp*100) / 100
			}
			if t == 0 && runtime.GOOS == "windows" {
				t = readWindowsTemperature()
			}
			if t > 0 {
				tempMu.Lock()
				tempValue = t
				tempMu.Unlock()
			}
		}()
		time.Sleep(5 * time.Second)
	}
}

// backgroundCollector continuously samples memory, network, and assembles all metrics.
// CPU and temperature are collected by their own dedicated goroutines.
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

			// CPU usage - read from dedicated collector (accurate 1s blocking sample)
			cpuMu.RLock()
			m.CPUUsage = cpuValue
			cpuMu.RUnlock()

			// Memory - instant call
			if vm, err := mem.VirtualMemory(); err == nil {
				m.MemUsage = math.Round(vm.UsedPercent*100) / 100
				m.MemTotalMB = vm.Total / 1024 / 1024
			}

			// Uptime - instant call
			if h, err := host.Info(); err == nil {
				m.Uptime = h.Uptime
			}

			// Temperature - read from separate collector (non-blocking)
			tempMu.RLock()
			m.Temperature = tempValue
			tempMu.RUnlock()

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

// readWindowsTemperature tries multiple methods to read CPU temperature on Windows.
// Fallback order:
//  1. Performance Counter ThermalZoneInformation (NO admin required)
//  2. WMI MSAcpi_ThermalZoneTemperature (requires admin privileges)
//  3. OpenHardwareMonitor / LibreHardwareMonitor WMI interface
//  4. wmic command line as final fallback
//
// Returns temperature in Celsius, or 0 if all methods fail.
func readWindowsTemperature() float64 {
	// Method 1: Performance Counter (no admin needed, works with go run)
	if t := readTempPerfCounter(); t > 0 {
		return t
	}
	// Method 2: WMI MSAcpi_ThermalZoneTemperature (needs admin)
	if t := readTempWMIAcpi(); t > 0 {
		return t
	}
	// Method 3: OpenHardwareMonitor / LibreHardwareMonitor WMI
	if t := readTempOpenHardwareMonitor(); t > 0 {
		return t
	}
	// Method 4: wmic command line fallback
	if t := readTempWmic(); t > 0 {
		return t
	}
	log.Printf("[Agent] WARNING: All Windows temperature methods failed.")
	return 0
}

// readTempPerfCounter reads temperature via Win32_PerfFormattedData_Counters_ThermalZoneInformation.
// This method does NOT require administrator privileges.
// Uses HighPrecisionTemperature (tenths of Kelvin) for better precision.
func readTempPerfCounter() float64 {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "powershell", "-NoProfile", "-NonInteractive", "-Command",
		"(Get-CimInstance Win32_PerfFormattedData_Counters_ThermalZoneInformation -ErrorAction SilentlyContinue | Sort-Object HighPrecisionTemperature -Descending | Select-Object -First 1).HighPrecisionTemperature")
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
	// HighPrecisionTemperature is in tenths of Kelvin, convert to Celsius
	celsius := (val / 10.0) - 273.15
	if celsius < 0 || celsius > 150 {
		return 0
	}
	return math.Round(celsius*100) / 100
}

// readTempWMIAcpi reads temperature via MSAcpi_ThermalZoneTemperature (requires admin).
func readTempWMIAcpi() float64 {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "powershell", "-NoProfile", "-NonInteractive", "-Command",
		"(Get-CimInstance -Namespace 'root/wmi' -ClassName MSAcpi_ThermalZoneTemperature -ErrorAction SilentlyContinue | Select-Object -First 1).CurrentTemperature")
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

// readTempOpenHardwareMonitor reads temperature from OpenHardwareMonitor or LibreHardwareMonitor WMI interface.
// These tools expose sensor data via WMI namespace root/OpenHardwareMonitor or root/LibreHardwareMonitor.
func readTempOpenHardwareMonitor() float64 {
	namespaces := []string{"root/LibreHardwareMonitor", "root/OpenHardwareMonitor"}
	for _, ns := range namespaces {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		cmd := exec.CommandContext(ctx, "powershell", "-NoProfile", "-NonInteractive", "-Command",
			fmt.Sprintf("(Get-CimInstance -Namespace '%s' -ClassName Sensor -ErrorAction SilentlyContinue | Where-Object { $_.SensorType -eq 'Temperature' -and $_.Name -like '*CPU*' } | Select-Object -First 1).Value", ns))
		out, err := cmd.Output()
		cancel()
		if err != nil {
			continue
		}
		s := strings.TrimSpace(string(out))
		if s == "" {
			continue
		}
		val, err := strconv.ParseFloat(s, 64)
		if err != nil || val <= 0 || val > 150 {
			continue
		}
		return math.Round(val*100) / 100
	}
	return 0
}

// readTempWmic uses the wmic command line tool as a final fallback.
func readTempWmic() float64 {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "wmic", "/namespace:\\\\root\\wmi", "PATH", "MSAcpi_ThermalZoneTemperature", "get", "CurrentTemperature")
	out, err := cmd.Output()
	if err != nil {
		return 0
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	for _, line := range lines {
		s := strings.TrimSpace(line)
		if s == "" || strings.Contains(strings.ToLower(s), "currenttemperature") {
			continue
		}
		val, err := strconv.ParseFloat(s, 64)
		if err != nil || val <= 0 {
			continue
		}
		celsius := (val / 10.0) - 273.15
		if celsius < 0 || celsius > 150 {
			continue
		}
		return math.Round(celsius*100) / 100
	}
	return 0
}

// StartBackground starts the hardware agent HTTP server in the background on the given port.
// It returns immediately. The server runs until the process exits.
func StartBackground(port int) {
	// Start dedicated collectors and the main metrics assembler
	go backgroundCPUCollector()
	go backgroundTempCollector()
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
