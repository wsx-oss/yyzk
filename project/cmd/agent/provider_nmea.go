package main

import (
	"bufio"
	"fmt"
	"log"
	"math"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

// NMEAProvider reads GPS data from a serial NMEA device or gpsd TCP connection.
// Battery and flight phase data are not available from NMEA, so they return defaults.
//
// Supported NMEA sentences: $GPGGA, $GPRMC, $GNRMC, $GNGGA
//
// Usage:
//   - Serial: source = "/dev/ttyUSB0" or "COM3" (reads line-by-line)
//   - gpsd:   source = "tcp:localhost:2947" (connects via TCP, sends ?WATCH)
//   - File:   source = "/path/to/nmea.log" (replays from file, useful for testing)
type NMEAProvider struct {
	source string // serial port path, "tcp:host:port", or file path

	mu      sync.RWMutex
	lat     float64
	lng     float64
	alt     float64
	speed   float64 // m/s
	heading float64
	acc     float64
	ready   bool
	lastFix time.Time

	stopCh chan struct{}
}

func NewNMEAProvider(source string) *NMEAProvider {
	return &NMEAProvider{
		source: source,
		stopCh: make(chan struct{}),
	}
}

func (p *NMEAProvider) Name() string { return "NMEA-GPS" }

func (p *NMEAProvider) Start() error {
	if p.source == "" {
		return fmt.Errorf("NMEA source not specified")
	}

	if strings.HasPrefix(p.source, "tcp:") {
		// gpsd TCP connection
		addr := strings.TrimPrefix(p.source, "tcp:")
		go p.readTCP(addr)
	} else {
		// Serial port or file
		go p.readSerial(p.source)
	}

	log.Printf("[NMEA] Reading GPS from: %s", p.source)
	return nil
}

func (p *NMEAProvider) Stop() {
	close(p.stopCh)
}

func (p *NMEAProvider) IsReady() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.ready
}

func (p *NMEAProvider) Tick() {
	// No-op; state updated by read goroutine
}

func (p *NMEAProvider) GPSPayload(agentID string) map[string]interface{} {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return map[string]interface{}{
		"agent_id":  agentID,
		"latitude":  math.Round(p.lat*1e6) / 1e6,
		"longitude": math.Round(p.lng*1e6) / 1e6,
		"altitude":  math.Round(p.alt*10) / 10,
		"speed":     math.Round(p.speed*10) / 10,
		"heading":   math.Round(p.heading*10) / 10,
		"accuracy":  math.Round(p.acc*10) / 10,
	}
}

// BatteryPayload returns default/unknown battery data (NMEA has no battery info).
func (p *NMEAProvider) BatteryPayload(agentID string) map[string]interface{} {
	return map[string]interface{}{
		"agent_id":       agentID,
		"voltage":        0,
		"current_val":    0,
		"level":          -1, // unknown
		"temperature":    0,
		"health":         100,
		"charge_cycles":  0,
		"remaining_time": "未知",
	}
}

// FlightPhase returns empty — NMEA devices don't report flight mode.
func (p *NMEAProvider) FlightPhase() string { return "" }

func (p *NMEAProvider) FlightPayload(agentID string) map[string]interface{} {
	return map[string]interface{}{
		"agent_id": agentID,
		"phase":    "",
	}
}

// readTCP connects to a gpsd-style TCP server and reads NMEA sentences.
func (p *NMEAProvider) readTCP(addr string) {
	for {
		select {
		case <-p.stopCh:
			return
		default:
		}

		conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
		if err != nil {
			log.Printf("[NMEA] TCP connect to %s failed: %v, retrying in 3s...", addr, err)
			time.Sleep(3 * time.Second)
			continue
		}

		// For gpsd, send watch command
		if strings.Contains(addr, "2947") {
			conn.Write([]byte(`?WATCH={"enable":true,"nmea":true}` + "\n"))
		}

		scanner := bufio.NewScanner(conn)
		for scanner.Scan() {
			select {
			case <-p.stopCh:
				conn.Close()
				return
			default:
			}
			p.parseLine(scanner.Text())
		}
		conn.Close()
		log.Printf("[NMEA] TCP connection lost, reconnecting in 3s...")
		time.Sleep(3 * time.Second)
	}
}

// readSerial reads NMEA sentences from a serial port or file.
func (p *NMEAProvider) readSerial(path string) {
	for {
		select {
		case <-p.stopCh:
			return
		default:
		}

		f, err := os.Open(path)
		if err != nil {
			log.Printf("[NMEA] Open %s failed: %v, retrying in 3s...", path, err)
			time.Sleep(3 * time.Second)
			continue
		}

		scanner := bufio.NewScanner(f)
		for scanner.Scan() {
			select {
			case <-p.stopCh:
				f.Close()
				return
			default:
			}
			p.parseLine(scanner.Text())
		}
		f.Close()

		// If it was a regular file (not a device), we're done
		info, _ := os.Stat(path)
		if info != nil && info.Mode().IsRegular() {
			log.Printf("[NMEA] Finished reading file %s", path)
			return
		}

		// Serial device closed unexpectedly, retry
		log.Printf("[NMEA] Device %s closed, reopening in 3s...", path)
		time.Sleep(3 * time.Second)
	}
}

// parseLine processes a single NMEA sentence.
func (p *NMEAProvider) parseLine(line string) {
	line = strings.TrimSpace(line)
	if len(line) < 6 || line[0] != '$' {
		return
	}

	// Remove checksum
	if idx := strings.Index(line, "*"); idx > 0 {
		line = line[:idx]
	}

	fields := strings.Split(line, ",")
	sentenceType := fields[0]

	switch sentenceType {
	case "$GPGGA", "$GNGGA":
		p.parseGGA(fields)
	case "$GPRMC", "$GNRMC":
		p.parseRMC(fields)
	}
}

// parseGGA handles GGA sentences (fix data including altitude).
// $GPGGA,hhmmss.ss,lat,N/S,lon,E/W,quality,numSV,HDOP,alt,M,sep,M,diffAge,diffStation*cs
func (p *NMEAProvider) parseGGA(fields []string) {
	if len(fields) < 10 {
		return
	}
	quality, _ := strconv.Atoi(fields[6])
	if quality == 0 {
		return // no fix
	}

	lat := parseNMEACoord(fields[2], fields[3])
	lng := parseNMEACoord(fields[4], fields[5])
	alt, _ := strconv.ParseFloat(fields[9], 64)
	hdop, _ := strconv.ParseFloat(fields[8], 64)

	p.mu.Lock()
	p.lat = lat
	p.lng = lng
	p.alt = alt
	p.acc = hdop * 2.5 // rough HDOP → meters conversion
	p.ready = true
	p.lastFix = time.Now()
	p.mu.Unlock()
}

// parseRMC handles RMC sentences (speed and heading).
// $GPRMC,hhmmss.ss,status,lat,N/S,lon,E/W,speedKnots,trackAngle,date,magVar,magVarDir*cs
func (p *NMEAProvider) parseRMC(fields []string) {
	if len(fields) < 8 {
		return
	}
	if fields[2] != "A" {
		return // not active fix
	}

	lat := parseNMEACoord(fields[3], fields[4])
	lng := parseNMEACoord(fields[5], fields[6])
	speedKnots, _ := strconv.ParseFloat(fields[7], 64)
	speed := speedKnots * 0.514444 // knots → m/s

	var heading float64
	if len(fields) > 8 && fields[8] != "" {
		heading, _ = strconv.ParseFloat(fields[8], 64)
	}

	p.mu.Lock()
	p.lat = lat
	p.lng = lng
	p.speed = speed
	p.heading = heading
	p.ready = true
	p.lastFix = time.Now()
	p.mu.Unlock()
}

// parseNMEACoord converts NMEA coordinate format (ddmm.mmmm) to decimal degrees.
func parseNMEACoord(raw, dir string) float64 {
	if raw == "" {
		return 0
	}
	val, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return 0
	}
	deg := math.Floor(val / 100)
	min := val - deg*100
	result := deg + min/60.0
	if dir == "S" || dir == "W" {
		result = -result
	}
	return result
}
