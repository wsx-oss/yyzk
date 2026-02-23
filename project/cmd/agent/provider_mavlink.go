package main

import (
	"encoding/binary"
	"fmt"
	"log"
	"math"
	"net"
	"sync"
	"time"
)

// ============================================================================
// MAVLink v1/v2 minimal parser — extracts GPS, battery, and heartbeat data
// from a real flight controller (PX4 / ArduPilot / etc.) over UDP or serial.
//
// Supported MAVLink messages:
//   MSG_ID 33  — GLOBAL_POSITION_INT  (GPS position)
//   MSG_ID 147 — BATTERY_STATUS       (battery cells + remaining)
//   MSG_ID 0   — HEARTBEAT            (flight mode / armed state)
//   MSG_ID 24  — GPS_RAW_INT          (fallback GPS if 33 not available)
//   MSG_ID 1   — SYS_STATUS           (battery voltage/current/remaining)
// ============================================================================

const (
	mavMsgHeartbeat        = 0
	mavMsgSysStatus        = 1
	mavMsgGPSRawInt        = 24
	mavMsgGlobalPositionInt = 33
	mavMsgBatteryStatus    = 147
)

// MAVLink custom flight modes → our phase mapping
// ArduCopter modes (most common):
//   0=Stabilize, 2=AltHold, 3=Auto, 4=Guided, 5=Loiter, 6=RTL, 9=Land, 16=PosHold
// PX4 main modes are in base_mode + custom_mode fields.
var arduCopterModeToPhase = map[uint32]string{
	0:  "待命",     // Stabilize (on ground)
	2:  "待命",     // AltHold
	3:  "执行任务", // Auto (mission)
	4:  "巡航",     // Guided
	5:  "巡航",     // Loiter
	6:  "返航",     // RTL
	9:  "降落",     // Land
	16: "巡航",     // PosHold
}

// MAVLinkProvider reads real drone telemetry from a MAVLink stream.
type MAVLinkProvider struct {
	// Configuration
	listenAddr string // UDP listen address, e.g. ":14550"
	serialPort string // Serial port path, e.g. "COM3" or "/dev/ttyUSB0"
	serialBaud int    // Serial baud rate, e.g. 57600

	// Internal state (thread-safe)
	mu sync.RWMutex

	// GPS data
	gpsLat     float64
	gpsLng     float64
	gpsAlt     float64 // meters
	gpsSpeed   float64 // m/s ground speed
	gpsHeading float64 // degrees
	gpsAcc     float64 // accuracy in meters (HDOP-based)
	gpsReady   bool
	gpsTime    time.Time

	// Battery data
	batVoltage    float64 // volts
	batCurrent    float64 // amps
	batLevel      int     // percentage 0-100
	batTemp       float64 // celsius
	batHealth     int     // percentage
	batCycles     int
	batRemaining  string  // estimated remaining time
	batReady      bool
	batTime       time.Time

	// Flight mode
	flightPhase string
	isArmed     bool
	customMode  uint32
	baseMode    uint8
	flightReady bool

	// Connection
	conn   net.PacketConn
	stopCh chan struct{}
}

// NewMAVLinkProvider creates a provider that listens for MAVLink telemetry.
// Typical usage: UDP port 14550 (default MAVLink ground station port).
func NewMAVLinkProvider(listenAddr string) *MAVLinkProvider {
	if listenAddr == "" {
		listenAddr = ":14550"
	}
	return &MAVLinkProvider{
		listenAddr: listenAddr,
		batHealth:  100,
		batLevel:   -1,
		stopCh:     make(chan struct{}),
	}
}

func (p *MAVLinkProvider) Name() string { return "MAVLink" }

func (p *MAVLinkProvider) Start() error {
	conn, err := net.ListenPacket("udp", p.listenAddr)
	if err != nil {
		return fmt.Errorf("MAVLink UDP listen on %s failed: %w", p.listenAddr, err)
	}
	p.conn = conn
	log.Printf("[MAVLink] Listening on UDP %s for flight controller telemetry", p.listenAddr)

	go p.readLoop()
	return nil
}

func (p *MAVLinkProvider) Stop() {
	close(p.stopCh)
	if p.conn != nil {
		p.conn.Close()
	}
}

func (p *MAVLinkProvider) IsReady() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.gpsReady
}

func (p *MAVLinkProvider) Tick() {
	// No-op for real data; state is updated by the read loop.
	// Update remaining time estimate based on current drain rate
	p.mu.Lock()
	if p.batLevel >= 0 && p.batCurrent > 0.1 {
		// Very rough estimate: assume linear drain
		minutesLeft := int(float64(p.batLevel) * 0.3 / p.batCurrent * 60)
		if minutesLeft < 0 {
			minutesLeft = 0
		}
		p.batRemaining = fmt.Sprintf("%d分钟", minutesLeft)
	}
	p.mu.Unlock()
}

func (p *MAVLinkProvider) GPSPayload(agentID string) map[string]interface{} {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return map[string]interface{}{
		"agent_id":  agentID,
		"latitude":  math.Round(p.gpsLat*1e6) / 1e6,
		"longitude": math.Round(p.gpsLng*1e6) / 1e6,
		"altitude":  math.Round(p.gpsAlt*10) / 10,
		"speed":     math.Round(p.gpsSpeed*10) / 10,
		"heading":   math.Round(p.gpsHeading*10) / 10,
		"accuracy":  math.Round(p.gpsAcc*10) / 10,
	}
}

func (p *MAVLinkProvider) BatteryPayload(agentID string) map[string]interface{} {
	p.mu.RLock()
	defer p.mu.RUnlock()
	level := p.batLevel
	if level < 0 {
		level = 0
	}
	return map[string]interface{}{
		"agent_id":       agentID,
		"voltage":        math.Round(p.batVoltage*10) / 10,
		"current_val":    math.Round(p.batCurrent*10) / 10,
		"level":          level,
		"temperature":    math.Round(p.batTemp*10) / 10,
		"health":         p.batHealth,
		"charge_cycles":  p.batCycles,
		"remaining_time": p.batRemaining,
	}
}

func (p *MAVLinkProvider) FlightPhase() string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.flightPhase
}

func (p *MAVLinkProvider) FlightPayload(agentID string) map[string]interface{} {
	return map[string]interface{}{
		"agent_id": agentID,
		"phase":    p.FlightPhase(),
	}
}

// readLoop continuously reads MAVLink packets from the UDP connection.
func (p *MAVLinkProvider) readLoop() {
	buf := make([]byte, 1024)
	for {
		select {
		case <-p.stopCh:
			return
		default:
		}

		p.conn.SetReadDeadline(time.Now().Add(5 * time.Second))
		n, _, err := p.conn.ReadFrom(buf)
		if err != nil {
			if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
				continue // normal timeout, keep listening
			}
			select {
			case <-p.stopCh:
				return
			default:
				log.Printf("[MAVLink] Read error: %v", err)
				time.Sleep(100 * time.Millisecond)
				continue
			}
		}

		p.parsePackets(buf[:n])
	}
}

// parsePackets scans a buffer for MAVLink v1 (0xFE) and v2 (0xFD) frames.
func (p *MAVLinkProvider) parsePackets(data []byte) {
	for i := 0; i < len(data); {
		if data[i] == 0xFE && i+8 <= len(data) {
			// MAVLink v1: [STX, LEN, SEQ, SYS, COMP, MSGID, PAYLOAD..., CK_A, CK_B]
			payloadLen := int(data[i+1])
			frameLen := 6 + payloadLen + 2
			if i+frameLen > len(data) {
				break
			}
			msgID := uint32(data[i+5])
			payload := data[i+6 : i+6+payloadLen]
			p.handleMessage(msgID, payload)
			i += frameLen
		} else if data[i] == 0xFD && i+12 <= len(data) {
			// MAVLink v2: [STX, LEN, INCOMPAT, COMPAT, SEQ, SYS, COMP, MSGID(3), PAYLOAD..., CK_A, CK_B, (SIG)]
			payloadLen := int(data[i+1])
			msgID := uint32(data[i+7]) | uint32(data[i+8])<<8 | uint32(data[i+9])<<16
			frameLen := 10 + payloadLen + 2
			incompatFlags := data[i+2]
			if incompatFlags&0x01 != 0 {
				frameLen += 13 // signature
			}
			if i+frameLen > len(data) {
				break
			}
			payload := data[i+10 : i+10+payloadLen]
			p.handleMessage(msgID, payload)
			i += frameLen
		} else {
			i++
		}
	}
}

// handleMessage processes a single MAVLink message by ID.
func (p *MAVLinkProvider) handleMessage(msgID uint32, payload []byte) {
	switch msgID {
	case mavMsgGlobalPositionInt:
		p.parseGlobalPositionInt(payload)
	case mavMsgGPSRawInt:
		p.parseGPSRawInt(payload)
	case mavMsgHeartbeat:
		p.parseHeartbeat(payload)
	case mavMsgSysStatus:
		p.parseSysStatus(payload)
	case mavMsgBatteryStatus:
		p.parseBatteryStatus(payload)
	}
}

// parseGlobalPositionInt handles MSG_ID 33 — primary GPS source.
// Fields: time_boot_ms(4), lat(4), lon(4), alt(4), relative_alt(4), vx(2), vy(2), vz(2), hdg(2)
func (p *MAVLinkProvider) parseGlobalPositionInt(payload []byte) {
	if len(payload) < 28 {
		return
	}
	lat := float64(int32(binary.LittleEndian.Uint32(payload[4:8]))) / 1e7
	lon := float64(int32(binary.LittleEndian.Uint32(payload[8:12]))) / 1e7
	alt := float64(int32(binary.LittleEndian.Uint32(payload[12:16]))) / 1000.0 // mm → m
	relAlt := float64(int32(binary.LittleEndian.Uint32(payload[16:20]))) / 1000.0
	vx := float64(int16(binary.LittleEndian.Uint16(payload[20:22]))) / 100.0 // cm/s → m/s
	vy := float64(int16(binary.LittleEndian.Uint16(payload[22:24]))) / 100.0
	hdg := float64(binary.LittleEndian.Uint16(payload[26:28])) / 100.0 // cdeg → deg

	speed := math.Sqrt(vx*vx + vy*vy)

	p.mu.Lock()
	p.gpsLat = lat
	p.gpsLng = lon
	if relAlt > 0 {
		p.gpsAlt = relAlt // prefer relative altitude (AGL)
	} else {
		p.gpsAlt = alt
	}
	p.gpsSpeed = speed
	if hdg < 360 {
		p.gpsHeading = hdg
	}
	p.gpsReady = true
	p.gpsTime = time.Now()
	p.mu.Unlock()
}

// parseGPSRawInt handles MSG_ID 24 — fallback GPS if GLOBAL_POSITION_INT not available.
// Fields: time_usec(8), fix_type(1), lat(4), lon(4), alt(4), eph(2), epv(2), vel(2), cog(2), satellites(1)
func (p *MAVLinkProvider) parseGPSRawInt(payload []byte) {
	if len(payload) < 30 {
		return
	}

	p.mu.RLock()
	hasGlobalPos := p.gpsReady && time.Since(p.gpsTime) < 5*time.Second
	p.mu.RUnlock()
	if hasGlobalPos {
		return // prefer GLOBAL_POSITION_INT
	}

	fixType := payload[8]
	if fixType < 2 {
		return // no fix
	}

	lat := float64(int32(binary.LittleEndian.Uint32(payload[9:13]))) / 1e7
	lon := float64(int32(binary.LittleEndian.Uint32(payload[13:17]))) / 1e7
	alt := float64(int32(binary.LittleEndian.Uint32(payload[17:21]))) / 1000.0
	eph := float64(binary.LittleEndian.Uint16(payload[21:23])) / 100.0 // HDOP
	vel := float64(binary.LittleEndian.Uint16(payload[25:27])) / 100.0 // m/s
	cog := float64(binary.LittleEndian.Uint16(payload[27:29])) / 100.0 // degrees

	p.mu.Lock()
	p.gpsLat = lat
	p.gpsLng = lon
	p.gpsAlt = alt
	p.gpsSpeed = vel
	p.gpsHeading = cog
	p.gpsAcc = eph
	p.gpsReady = true
	p.gpsTime = time.Now()
	p.mu.Unlock()
}

// parseHeartbeat handles MSG_ID 0 — flight mode and armed state.
// Fields: custom_mode(4), type(1), autopilot(1), base_mode(1), system_status(1), mavlink_version(1)
func (p *MAVLinkProvider) parseHeartbeat(payload []byte) {
	if len(payload) < 9 {
		return
	}
	customMode := binary.LittleEndian.Uint32(payload[0:4])
	baseMode := payload[6]
	armed := baseMode&0x80 != 0

	// Determine flight phase from mode
	var phase string
	if !armed {
		phase = "待命"
	} else if ph, ok := arduCopterModeToPhase[customMode]; ok {
		phase = ph
	} else {
		// Unknown mode while armed → default to 巡航
		phase = "巡航"
	}

	// Special: if armed and mode is Stabilize/AltHold and altitude is low → 起飞
	p.mu.Lock()
	p.customMode = customMode
	p.baseMode = baseMode
	p.isArmed = armed
	if armed && (customMode == 0 || customMode == 2) && p.gpsAlt > 0 && p.gpsAlt < 5 {
		phase = "起飞"
	}
	p.flightPhase = phase
	p.flightReady = true
	p.mu.Unlock()
}

// parseSysStatus handles MSG_ID 1 — system-level battery info.
// Fields: ... voltage_battery(2) at offset 14, current_battery(2) at offset 16, battery_remaining(1) at offset 30
func (p *MAVLinkProvider) parseSysStatus(payload []byte) {
	if len(payload) < 31 {
		return
	}
	voltage := float64(binary.LittleEndian.Uint16(payload[14:16])) / 1000.0 // mV → V
	current := float64(int16(binary.LittleEndian.Uint16(payload[16:18]))) / 100.0 // cA → A
	remaining := int8(payload[30]) // -1 if not available

	p.mu.Lock()
	if voltage > 0 {
		p.batVoltage = voltage
	}
	if current >= 0 {
		p.batCurrent = current
	}
	if remaining >= 0 {
		p.batLevel = int(remaining)
		p.batReady = true
	}
	p.batTime = time.Now()
	p.mu.Unlock()
}

// parseBatteryStatus handles MSG_ID 147 — detailed battery info.
// Fields: ... voltages(10*2) at offset 10, current_battery(2) at offset 30,
//         current_consumed(4) at offset 32, energy_consumed(4) at offset 36,
//         temperature(2) at offset 40, battery_remaining(1) at offset 42
func (p *MAVLinkProvider) parseBatteryStatus(payload []byte) {
	if len(payload) < 42 {
		return
	}

	// Sum cell voltages for total voltage
	var totalMV uint32
	for i := 0; i < 10; i++ {
		cellMV := binary.LittleEndian.Uint16(payload[10+i*2 : 12+i*2])
		if cellMV == 0xFFFF {
			break // unused cell
		}
		totalMV += uint32(cellMV)
	}

	current := float64(int16(binary.LittleEndian.Uint16(payload[30:32]))) / 100.0 // cA → A
	temp := float64(int16(binary.LittleEndian.Uint16(payload[40:42]))) / 100.0    // cdegC → degC

	remaining := int8(-1)
	if len(payload) > 42 {
		remaining = int8(payload[42])
	}

	p.mu.Lock()
	if totalMV > 0 {
		p.batVoltage = float64(totalMV) / 1000.0
	}
	if current >= 0 {
		p.batCurrent = current
	}
	if temp > -100 && temp < 200 {
		p.batTemp = temp
	}
	if remaining >= 0 {
		p.batLevel = int(remaining)
	}
	p.batReady = true
	p.batTime = time.Now()
	p.mu.Unlock()
}
