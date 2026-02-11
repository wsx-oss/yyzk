package main

import (
	"context"
	"encoding/json"
	"flag"
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

// AgentMetrics is the JSON response returned by the agent
type AgentMetrics struct {
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

var (
	cacheMu       sync.RWMutex
	cachedMetrics AgentMetrics
	cacheReady    bool
)

// backgroundCollector continuously samples CPU, temperature, and network in a loop.
func backgroundCollector() {
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

	var prevBytesSent, prevBytesRecv uint64
	var prevNetTime time.Time
	if ios, err := netio.IOCounters(false); err == nil && len(ios) > 0 {
		prevBytesSent = ios[0].BytesSent
		prevBytesRecv = ios[0].BytesRecv
		prevNetTime = time.Now()
	}

	cpu.Percent(500*time.Millisecond, false)

	for {
		func() {
			defer func() {
				if r := recover(); r != nil {
					log.Printf("[Agent] backgroundCollector recovered from panic: %v", r)
				}
			}()

			m := AgentMetrics{
				Timestamp:  time.Now().Unix(),
				OS:         osName,
				Hostname:   hostname,
				CPUModel:   cpuModel,
				CPUCores:   cpuCores,
				MemTotalMB: memTotalMB,
			}

			if percents, err := cpu.Percent(500*time.Millisecond, false); err == nil && len(percents) > 0 {
				m.CPUUsage = math.Round(percents[0]*100) / 100
			}

			if vm, err := mem.VirtualMemory(); err == nil {
				m.MemUsage = math.Round(vm.UsedPercent*100) / 100
				m.MemTotalMB = vm.Total / 1024 / 1024
			}

			if h, err := host.Info(); err == nil {
				m.Uptime = h.Uptime
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

			cacheMu.Lock()
			cachedMetrics = m
			cacheReady = true
			cacheMu.Unlock()
		}()

		time.Sleep(500 * time.Millisecond)
	}
}

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

func getMetrics() AgentMetrics {
	cacheMu.RLock()
	ready := cacheReady
	m := cachedMetrics
	cacheMu.RUnlock()
	if ready {
		return m
	}
	return AgentMetrics{Timestamp: time.Now().Unix(), OS: runtime.GOOS, NetworkBandwidth: "0B/s"}
}

// readWindowsTemperature reads CPU temperature on Windows via WMI MSAcpi_ThermalZoneTemperature.
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
	celsius := (val / 10.0) - 273.15
	if celsius < 0 || celsius > 150 {
		return 0
	}
	return math.Round(celsius*100) / 100
}

func main() {
	port := flag.Int("port", 9100, "Agent HTTP listen port")
	flag.Parse()

	go backgroundCollector()

	addr := fmt.Sprintf(":%d", *port)

	http.HandleFunc("/metrics", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
		json.NewEncoder(w).Encode(getMetrics())
	})

	http.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		w.Write([]byte(`{"status":"ok"}`))
	})

	log.Printf("Hardware Agent started on %s", addr)
	log.Printf("  GET /metrics  - collect hardware metrics")
	log.Printf("  GET /health   - health check")
	log.Fatal(http.ListenAndServe(addr, nil))
}
