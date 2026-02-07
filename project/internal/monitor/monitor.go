package monitor

import (
    "runtime"
    "time"

    "github.com/shirou/gopsutil/v3/cpu"
    "github.com/shirou/gopsutil/v3/disk"
    "github.com/shirou/gopsutil/v3/host"
    "github.com/shirou/gopsutil/v3/load"
    "github.com/shirou/gopsutil/v3/mem"
    netio "github.com/shirou/gopsutil/v3/net"
)

type MetricsSnapshot struct {
    CPUPercent      float64 `json:"cpu_percent"`
    MemPercent      float64 `json:"mem_percent"`
    Load1           float64 `json:"load1"`
    Uptime          uint64  `json:"uptime"`
    DiskUsedPercent float64 `json:"disk_used_percent"`
    NetBytesSent    uint64  `json:"net_bytes_sent"`
    NetBytesRecv    uint64  `json:"net_bytes_recv"`
    Timestamp       int64   `json:"ts"`
}

type HardwareSnapshot struct {
    Hostname  string `json:"hostname"`
    OS        string `json:"os"`
    Platform  string `json:"platform"`
    PlatformV string `json:"platform_version"`
    Kernel    string `json:"kernel"`
    CPUModel  string `json:"cpu_model"`
    CPUCores  int    `json:"cpu_cores"`
    MemTotal  uint64 `json:"mem_total"`
}

func rootPath() string {
    if runtime.GOOS == "windows" {
        return "C:\\"
    }
    return "/"
}

func CollectMetrics() (MetricsSnapshot, error) {
    m := MetricsSnapshot{}
    cpuPercents, err := cpu.Percent(0, false)
    if err == nil && len(cpuPercents) > 0 {
        m.CPUPercent = cpuPercents[0]
    }
    if vm, err := mem.VirtualMemory(); err == nil {
        m.MemPercent = vm.UsedPercent
    }
    if l, err := load.Avg(); err == nil {
        m.Load1 = l.Load1
    }
    if u, err := host.Uptime(); err == nil {
        m.Uptime = u
    }
    if du, err := disk.Usage(rootPath()); err == nil {
        m.DiskUsedPercent = du.UsedPercent
    }
    if ios, err := netio.IOCounters(false); err == nil && len(ios) > 0 {
        m.NetBytesRecv = ios[0].BytesRecv
        m.NetBytesSent = ios[0].BytesSent
    }
    m.Timestamp = time.Now().Unix()
    return m, nil
}

func HardwareInfo() (HardwareSnapshot, error) {
    var hs HardwareSnapshot
    h, err := host.Info()
    if err != nil {
        return hs, err
    }
    hs.Hostname = h.Hostname
    hs.OS = h.OS
    hs.Platform = h.Platform
    hs.PlatformV = h.PlatformVersion
    hs.Kernel = h.KernelVersion
    if infos, err := cpu.Info(); err == nil && len(infos) > 0 {
        hs.CPUModel = infos[0].ModelName
    }
    if counts, err := cpu.Counts(true); err == nil {
        hs.CPUCores = counts
    }
    if vm, err := mem.VirtualMemory(); err == nil {
        hs.MemTotal = vm.Total
    }
    return hs, nil
}

