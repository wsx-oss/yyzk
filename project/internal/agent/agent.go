package agent

import (
	"encoding/json"
	"fmt"
	"log"
	"math"
	"net/http"
	"runtime"
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

// CollectMetrics gathers real hardware metrics from the local machine
func CollectMetrics() Metrics {
	m := Metrics{
		Timestamp: time.Now().Unix(),
		OS:        runtime.GOOS,
	}

	if h, err := host.Info(); err == nil {
		m.Hostname = h.Hostname
		m.Uptime = h.Uptime
	}

	if percents, err := cpu.Percent(time.Second, false); err == nil && len(percents) > 0 {
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

	if ios, err := netio.IOCounters(false); err == nil && len(ios) > 0 {
		totalBytes := ios[0].BytesSent + ios[0].BytesRecv
		if totalBytes > 1024*1024*1024 {
			m.NetworkBandwidth = fmt.Sprintf("%.1fGB", float64(totalBytes)/(1024*1024*1024))
		} else if totalBytes > 1024*1024 {
			m.NetworkBandwidth = fmt.Sprintf("%.1fMB", float64(totalBytes)/(1024*1024))
		} else if totalBytes > 1024 {
			m.NetworkBandwidth = fmt.Sprintf("%.1fKB", float64(totalBytes)/1024)
		} else {
			m.NetworkBandwidth = fmt.Sprintf("%dB", totalBytes)
		}
	}

	return m
}

// StartBackground starts the hardware agent HTTP server in the background on the given port.
// It returns immediately. The server runs until the process exits.
func StartBackground(port int) {
	addr := fmt.Sprintf(":%d", port)

	mux := http.NewServeMux()
	mux.HandleFunc("/metrics", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Access-Control-Allow-Origin", "*")
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
