package main

import (
	"encoding/json"
	"flag"
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

func collectMetrics() AgentMetrics {
	m := AgentMetrics{
		Timestamp: time.Now().Unix(),
		OS:        runtime.GOOS,
	}

	// Hostname
	if h, err := host.Info(); err == nil {
		m.Hostname = h.Hostname
		m.Uptime = h.Uptime
	}

	// CPU usage (sample over 1 second for accuracy)
	if percents, err := cpu.Percent(time.Second, false); err == nil && len(percents) > 0 {
		m.CPUUsage = math.Round(percents[0]*100) / 100
	}

	// CPU model and cores
	if infos, err := cpu.Info(); err == nil && len(infos) > 0 {
		m.CPUModel = infos[0].ModelName
	}
	if cores, err := cpu.Counts(true); err == nil {
		m.CPUCores = cores
	}

	// Memory usage
	if vm, err := mem.VirtualMemory(); err == nil {
		m.MemUsage = math.Round(vm.UsedPercent*100) / 100
		m.MemTotalMB = vm.Total / 1024 / 1024
	}

	// Temperature - try to read from sensors
	if temps, err := host.SensorsTemperatures(); err == nil && len(temps) > 0 {
		var maxTemp float64
		for _, t := range temps {
			if t.Temperature > maxTemp && t.Temperature < 150 {
				maxTemp = t.Temperature
			}
		}
		m.Temperature = math.Round(maxTemp*100) / 100
	}

	// Network bandwidth (total bytes sent + recv, formatted)
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

func main() {
	port := flag.Int("port", 9100, "Agent HTTP listen port")
	flag.Parse()

	addr := fmt.Sprintf(":%d", *port)

	http.HandleFunc("/metrics", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Access-Control-Allow-Origin", "*")
		metrics := collectMetrics()
		json.NewEncoder(w).Encode(metrics)
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
