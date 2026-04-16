package tcpgateway

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"strings"
	"sync"
	"time"

	"smartcontrol/internal/db"
)

// DeviceConn holds metadata for a connected TCP device.
type DeviceConn struct {
	Conn      net.Conn
	RemoteAddr string
	AgentID   string
	ConnectedAt time.Time
	LastDataAt  time.Time
	BytesRecv   int64
	BytesSent   int64
}

// Gateway accepts raw TCP connections from devices that don't speak HTTP.
// Data received is parsed (JSON or raw) and forwarded to the database via
// the same push logic as the HTTP API.
type Gateway struct {
	listener net.Listener
	db       *db.DB
	addr     string

	mu      sync.RWMutex
	clients map[string]*DeviceConn // remoteAddr -> conn info

	onData   func(agentID string, data map[string]interface{}) // optional callback
	shutdown chan struct{}
	wg       sync.WaitGroup
}

// New creates a TCP gateway but does not start listening yet.
func New(addr string, database *db.DB) *Gateway {
	return &Gateway{
		db:       database,
		addr:     addr,
		clients:  make(map[string]*DeviceConn),
		shutdown: make(chan struct{}),
	}
}

// SetDataCallback sets an optional callback invoked for each parsed JSON message.
func (g *Gateway) SetDataCallback(fn func(agentID string, data map[string]interface{})) {
	g.onData = fn
}

// Start begins listening for TCP connections.
func (g *Gateway) Start() error {
	ln, err := net.Listen("tcp", g.addr)
	if err != nil {
		return fmt.Errorf("tcpgateway listen on %s: %w", g.addr, err)
	}
	g.listener = ln
	log.Printf("[TCPGateway] Listening on %s for raw device connections", g.addr)

	g.wg.Add(1)
	go g.acceptLoop()
	return nil
}

// Stop gracefully shuts down the gateway.
func (g *Gateway) Stop() {
	close(g.shutdown)
	if g.listener != nil {
		g.listener.Close()
	}
	// Close all client connections
	g.mu.Lock()
	for _, dc := range g.clients {
		dc.Conn.Close()
	}
	g.mu.Unlock()
	g.wg.Wait()
	log.Printf("[TCPGateway] Stopped")
}

// Clients returns a snapshot of currently connected devices.
func (g *Gateway) Clients() []DeviceConn {
	g.mu.RLock()
	defer g.mu.RUnlock()
	out := make([]DeviceConn, 0, len(g.clients))
	for _, dc := range g.clients {
		out = append(out, *dc)
	}
	return out
}

// ClientCount returns the number of currently connected devices.
func (g *Gateway) ClientCount() int {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return len(g.clients)
}

// SendToDevice sends raw bytes to a connected device identified by remoteAddr.
func (g *Gateway) SendToDevice(remoteAddr string, data []byte) error {
	g.mu.RLock()
	dc, ok := g.clients[remoteAddr]
	g.mu.RUnlock()
	if !ok {
		return fmt.Errorf("device %s not connected", remoteAddr)
	}
	dc.Conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	n, err := dc.Conn.Write(data)
	if err != nil {
		return err
	}
	g.mu.Lock()
	dc.BytesSent += int64(n)
	g.mu.Unlock()
	return nil
}

// BroadcastToAll sends data to all connected devices.
func (g *Gateway) BroadcastToAll(data []byte) int {
	g.mu.RLock()
	defer g.mu.RUnlock()
	sent := 0
	for _, dc := range g.clients {
		dc.Conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
		if _, err := dc.Conn.Write(data); err == nil {
			sent++
		}
	}
	return sent
}

func (g *Gateway) acceptLoop() {
	defer g.wg.Done()
	for {
		conn, err := g.listener.Accept()
		if err != nil {
			select {
			case <-g.shutdown:
				return
			default:
				log.Printf("[TCPGateway] Accept error: %v", err)
				time.Sleep(100 * time.Millisecond)
				continue
			}
		}
		g.wg.Add(1)
		go g.handleConn(conn)
	}
}

// isMavlinkStart returns true if the byte is a MAVLink v1 (0xFE) or v2 (0xFD) start marker.
func isMavlinkStart(b byte) bool {
	return b == 0xFE || b == 0xFD
}

func (g *Gateway) handleConn(conn net.Conn) {
	defer g.wg.Done()
	remote := conn.RemoteAddr().String()
	log.Printf("[TCPGateway] New connection from %s", remote)

	dc := &DeviceConn{
		Conn:        conn,
		RemoteAddr:  remote,
		ConnectedAt: time.Now(),
		LastDataAt:  time.Now(),
	}
	g.mu.Lock()
	g.clients[remote] = dc
	g.mu.Unlock()

	defer func() {
		conn.Close()
		g.mu.Lock()
		delete(g.clients, remote)
		g.mu.Unlock()
		log.Printf("[TCPGateway] Connection closed: %s (agent=%s)", remote, dc.AgentID)
	}()

	reader := bufio.NewReaderSize(conn, 8192)

	// Peek at the first byte to detect MAVLink binary stream
	conn.SetReadDeadline(time.Now().Add(30 * time.Second))
	firstBytes, err := reader.Peek(1)
	if err != nil {
		log.Printf("[TCPGateway] Peek error from %s: %v", remote, err)
		return
	}

	if isMavlinkStart(firstBytes[0]) {
		// MAVLink binary stream detected
		log.Printf("[TCPGateway] MAVLink binary stream detected from %s (magic=0x%02X)", remote, firstBytes[0])
		host, _, _ := net.SplitHostPort(dc.RemoteAddr)
		dc.AgentID = "mavlink-" + host
		g.handleMavlinkConn(dc, reader)
		return
	}

	// Regular JSON/text protocol – send welcome
	welcome := map[string]interface{}{
		"status":  "connected",
		"message": "TCP gateway ready. Send JSON (newline-delimited) or raw data.",
		"time":    time.Now().Format(time.RFC3339),
	}
	if wb, err := json.Marshal(welcome); err == nil {
		conn.Write(append(wb, '\n'))
	}

	for {
		select {
		case <-g.shutdown:
			return
		default:
		}

		// Set read deadline to detect dead connections
		conn.SetReadDeadline(time.Now().Add(120 * time.Second))

		line, err := reader.ReadBytes('\n')
		if err != nil {
			if err == io.EOF {
				return
			}
			if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
				// Send keepalive ping
				ping := []byte(`{"type":"ping"}` + "\n")
				if _, werr := conn.Write(ping); werr != nil {
					return
				}
				continue
			}
			// For non-newline data, try reading whatever is available
			if reader.Buffered() > 0 {
				buf := make([]byte, reader.Buffered())
				n, _ := reader.Read(buf)
				if n > 0 {
					g.processRawData(dc, buf[:n])
				}
			}
			return
		}

		line = []byte(strings.TrimSpace(string(line)))
		if len(line) == 0 {
			continue
		}

		g.mu.Lock()
		dc.BytesRecv += int64(len(line))
		dc.LastDataAt = time.Now()
		g.mu.Unlock()

		// Try to parse as JSON
		var msg map[string]interface{}
		if err := json.Unmarshal(line, &msg); err == nil {
			g.processJSON(dc, msg)
		} else {
			g.processRawData(dc, line)
		}
	}
}

// handleMavlinkConn handles a connection detected as MAVLink binary.
// It reads raw bytes, logs them, and stores them for the Java MAVLink bridge.
// The actual MAVLink parsing is done by the Java MavlinkBridge service.
func (g *Gateway) handleMavlinkConn(dc *DeviceConn, reader *bufio.Reader) {
	log.Printf("[TCPGateway] Handling MAVLink connection from %s (agent=%s)", dc.RemoteAddr, dc.AgentID)

	buf := make([]byte, 4096)
	var mavlinkBytesTotal int64

	for {
		select {
		case <-g.shutdown:
			return
		default:
		}

		dc.Conn.SetReadDeadline(time.Now().Add(120 * time.Second))
		n, err := reader.Read(buf)
		if n > 0 {
			mavlinkBytesTotal += int64(n)
			g.mu.Lock()
			dc.BytesRecv += int64(n)
			dc.LastDataAt = time.Now()
			g.mu.Unlock()

			// Log MAVLink packet summary (first byte is start marker)
			data := buf[:n]
			g.logMavlinkFrames(dc, data)
		}
		if err != nil {
			if err == io.EOF {
				log.Printf("[TCPGateway] MAVLink connection EOF from %s (total %d bytes)", dc.RemoteAddr, mavlinkBytesTotal)
				return
			}
			if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
				continue
			}
			log.Printf("[TCPGateway] MAVLink read error from %s: %v", dc.RemoteAddr, err)
			return
		}
	}
}

// logMavlinkFrames scans raw bytes for MAVLink frame markers and logs basic info.
func (g *Gateway) logMavlinkFrames(dc *DeviceConn, data []byte) {
	for i := 0; i < len(data); {
		if data[i] == 0xFE && i+6 <= len(data) {
			// MAVLink v1: FE len seq sys comp msgid payload[len] ckl ckh
			payloadLen := int(data[i+1])
			frameLen := 6 + payloadLen + 2 // header(6) + payload + checksum(2)
			if i+frameLen <= len(data) {
				msgID := int(data[i+5])
				sysID := int(data[i+3])
				log.Printf("[TCPGateway] MAVLink v1 frame: sysid=%d msgid=%d len=%d from %s",
					sysID, msgID, payloadLen, dc.RemoteAddr)
				// Store frame summary in device_tcp_log
				g.storeRawMessage(dc.AgentID, "mavlink_v1", map[string]interface{}{
					"sys_id": sysID, "msg_id": msgID, "payload_len": payloadLen,
					"hex": fmt.Sprintf("%x", data[i:i+frameLen]),
				})
				i += frameLen
				continue
			}
		} else if data[i] == 0xFD && i+10 <= len(data) {
			// MAVLink v2: FD len incompat_flags compat_flags seq sys comp msgid(3) payload[len] ckl ckh [sig13]
			payloadLen := int(data[i+1])
			msgID := int(data[i+7]) | int(data[i+8])<<8 | int(data[i+9])<<16
			sysID := int(data[i+5])
			frameLen := 10 + payloadLen + 2 // header(10) + payload + checksum(2)
			// Check for signature
			if i+3 <= len(data) && (data[i+2]&0x01) != 0 {
				frameLen += 13
			}
			if i+frameLen <= len(data) {
				log.Printf("[TCPGateway] MAVLink v2 frame: sysid=%d msgid=%d len=%d from %s",
					sysID, msgID, payloadLen, dc.RemoteAddr)
				g.storeRawMessage(dc.AgentID, "mavlink_v2", map[string]interface{}{
					"sys_id": sysID, "msg_id": msgID, "payload_len": payloadLen,
					"hex": fmt.Sprintf("%x", data[i:i+frameLen]),
				})
				i += frameLen
				continue
			}
		}
		i++
	}
}

// processJSON handles a parsed JSON message from a device.
func (g *Gateway) processJSON(dc *DeviceConn, msg map[string]interface{}) {
	// Extract agent_id if present
	if aid, ok := msg["agent_id"].(string); ok && aid != "" {
		dc.AgentID = aid
	}

	// Determine message type and route accordingly
	msgType, _ := msg["type"].(string)
	if msgType == "" {
		// Try to infer type from fields
		if _, ok := msg["cpu_usage"]; ok {
			msgType = "hardware"
		} else if _, ok := msg["latitude"]; ok {
			msgType = "gps"
		} else if _, ok := msg["battery_level"]; ok {
			msgType = "battery"
		} else if _, ok := msg["phase"]; ok {
			msgType = "flight"
		}
	}

	agentID := dc.AgentID
	if agentID == "" {
		// Use IP as fallback agent_id
		host, _, _ := net.SplitHostPort(dc.RemoteAddr)
		agentID = "tcp-" + host
		dc.AgentID = agentID
	}

	switch msgType {
	case "hardware":
		g.pushHardware(agentID, msg)
	case "gps":
		g.pushGPS(agentID, msg)
	case "battery":
		g.pushBattery(agentID, msg)
	case "flight":
		g.pushFlight(agentID, msg)
	case "ping", "heartbeat":
		// Respond with pong
		pong, _ := json.Marshal(map[string]interface{}{
			"type": "pong",
			"time": time.Now().Format(time.RFC3339),
		})
		dc.Conn.Write(append(pong, '\n'))
	default:
		// Store as generic device data
		g.storeRawMessage(agentID, msgType, msg)
	}

	// Fire optional callback
	if g.onData != nil {
		g.onData(agentID, msg)
	}

	// Send ACK
	ack, _ := json.Marshal(map[string]interface{}{
		"status":   "ok",
		"agent_id": agentID,
		"type":     msgType,
	})
	dc.Conn.Write(append(ack, '\n'))
}

// processRawData handles non-JSON data from a device.
func (g *Gateway) processRawData(dc *DeviceConn, data []byte) {
	agentID := dc.AgentID
	if agentID == "" {
		host, _, _ := net.SplitHostPort(dc.RemoteAddr)
		agentID = "tcp-" + host
		dc.AgentID = agentID
	}

	log.Printf("[TCPGateway] Raw data from %s (%s): %d bytes: %x",
		dc.RemoteAddr, agentID, len(data), data)

	g.storeRawMessage(agentID, "raw", map[string]interface{}{
		"hex":  fmt.Sprintf("%x", data),
		"text": string(data),
		"size": len(data),
	})

	// Still send ACK so the device knows data was received
	ack := []byte(`{"status":"ok","type":"raw_received"}` + "\n")
	dc.Conn.Write(ack)
}

// pushHardware inserts/updates hardware metrics in the DB (same logic as HardwarePush HTTP handler).
func (g *Gateway) pushHardware(agentID string, msg map[string]interface{}) {
	cpuUsage, _ := toFloat(msg["cpu_usage"])
	memUsage, _ := toFloat(msg["mem_usage"])
	temperature, _ := toFloat(msg["temperature"])
	networkBW, _ := msg["network_bandwidth"].(string)
	hostname, _ := msg["hostname"].(string)
	osName, _ := msg["os"].(string)
	cpuModel, _ := msg["cpu_model"].(string)
	cpuCores, _ := toInt(msg["cpu_cores"])
	memTotalMB, _ := toInt(msg["mem_total_mb"])

	desc := fmt.Sprintf("OS:%s CPU:%s Cores:%d Mem:%dMB [TCP]", osName, cpuModel, cpuCores, memTotalMB)
	name := hostname
	if name == "" {
		name = agentID
	}

	var id int
	err := g.db.QueryRow("SELECT id FROM hardware_items WHERE ip = ?", agentID).Scan(&id)
	if err != nil {
		// Auto-create
		res, err := g.db.Exec(`INSERT INTO hardware_items(name, type, ip, status, description, temperature, cpu_usage, mem_usage, network_bandwidth, detected_at, created_at, updated_at)
			VALUES(?, 'TCP设备', ?, '在线', ?, ?, ?, ?, ?, datetime('now'), datetime('now'), datetime('now'))`,
			name, agentID, desc, temperature, cpuUsage, memUsage, networkBW)
		if err != nil {
			log.Printf("[TCPGateway] DB insert hardware error: %v", err)
			return
		}
		newID, _ := res.LastInsertId()
		log.Printf("[TCPGateway] Auto-created hardware_item id=%d for TCP device %s", newID, agentID)
		return
	}

	g.db.Exec(`UPDATE hardware_items SET status='在线', temperature=?, cpu_usage=?, mem_usage=?, network_bandwidth=?, description=?, detected_at=datetime('now'), updated_at=datetime('now') WHERE id=?`,
		temperature, cpuUsage, memUsage, networkBW, desc, id)
}

// pushGPS inserts GPS data into the DB.
func (g *Gateway) pushGPS(agentID string, msg map[string]interface{}) {
	lat, _ := toFloat(msg["latitude"])
	lng, _ := toFloat(msg["longitude"])
	alt, _ := toFloat(msg["altitude"])
	speed, _ := toFloat(msg["speed"])

	// Find GPS device linked via agent_id
	var devID int
	err := g.db.QueryRow(`SELECT g.id FROM gps_devices g INNER JOIN drones d ON d.linked_gps_device_id=g.id WHERE d.agent_id=?`, agentID).Scan(&devID)
	if err != nil {
		// Try direct lookup by agent_id as device name
		err = g.db.QueryRow(`SELECT id FROM gps_devices WHERE device_id=?`, agentID).Scan(&devID)
	}
	if err != nil {
		log.Printf("[TCPGateway] GPS push: no device found for agent %s, storing as log", agentID)
		g.storeRawMessage(agentID, "gps", msg)
		return
	}

	g.db.Exec(`UPDATE gps_devices SET latitude=?, longitude=?, altitude=?, speed=?, status='在线', last_update=datetime('now') WHERE id=?`,
		lat, lng, alt, speed, devID)
	g.db.Exec(`INSERT INTO gps_history(device_id, latitude, longitude, altitude, speed, recorded_at) VALUES(?,?,?,?,?,datetime('now'))`,
		devID, lat, lng, alt, speed)
}

// pushBattery inserts battery data into the DB.
func (g *Gateway) pushBattery(agentID string, msg map[string]interface{}) {
	level, _ := toFloat(msg["battery_level"])
	voltage, _ := toFloat(msg["voltage"])
	temp, _ := toFloat(msg["temperature"])

	var droneID int
	err := g.db.QueryRow(`SELECT id FROM drones WHERE agent_id=?`, agentID).Scan(&droneID)
	if err != nil {
		log.Printf("[TCPGateway] Battery push: no drone found for agent %s", agentID)
		g.storeRawMessage(agentID, "battery", msg)
		return
	}

	g.db.Exec(`UPDATE drones SET battery_level=?, updated_at=datetime('now') WHERE id=?`, level, droneID)
	g.db.Exec(`INSERT INTO battery_history(drone_id, level, voltage, temperature, recorded_at) VALUES(?,?,?,?,datetime('now'))`,
		droneID, level, voltage, temp)
}

// pushFlight inserts flight phase data.
func (g *Gateway) pushFlight(agentID string, msg map[string]interface{}) {
	phase, _ := msg["phase"].(string)
	if phase == "" {
		return
	}
	var missionID int
	err := g.db.QueryRow(`SELECT fm.id FROM flight_missions fm INNER JOIN drones d ON d.id=fm.drone_id WHERE d.agent_id=? ORDER BY fm.id DESC LIMIT 1`, agentID).Scan(&missionID)
	if err != nil {
		g.storeRawMessage(agentID, "flight", msg)
		return
	}
	g.db.Exec(`UPDATE flight_missions SET phase=?, updated_at=datetime('now') WHERE id=?`, phase, missionID)
}

// storeRawMessage stores any unrecognized message into a device_tcp_log table.
func (g *Gateway) storeRawMessage(agentID, msgType string, data map[string]interface{}) {
	raw, _ := json.Marshal(data)
	g.db.Exec(`INSERT INTO device_tcp_log(agent_id, msg_type, payload, received_at) VALUES(?,?,?,datetime('now'))`,
		agentID, msgType, string(raw))
}

// EnsureTable creates the device_tcp_log table if it doesn't exist.
func (g *Gateway) EnsureTable() error {
	_, err := g.db.Exec(`CREATE TABLE IF NOT EXISTS device_tcp_log (
		id INTEGER PRIMARY KEY AUTO_INCREMENT,
		agent_id VARCHAR(255) NOT NULL,
		msg_type VARCHAR(64) NOT NULL DEFAULT 'raw',
		payload TEXT,
		received_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		INDEX idx_tcp_log_agent (agent_id),
		INDEX idx_tcp_log_time (received_at)
	)`)
	if err != nil {
		// Try SQLite syntax
		_, err = g.db.Exec(`CREATE TABLE IF NOT EXISTS device_tcp_log (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			agent_id TEXT NOT NULL,
			msg_type TEXT NOT NULL DEFAULT 'raw',
			payload TEXT,
			received_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`)
	}
	return err
}

// --- helpers ---

func toFloat(v interface{}) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case json.Number:
		f, err := n.Float64()
		return f, err == nil
	}
	return 0, false
}

func toInt(v interface{}) (int, bool) {
	switch n := v.(type) {
	case float64:
		return int(n), true
	case float32:
		return int(n), true
	case int:
		return n, true
	case int64:
		return int(n), true
	case json.Number:
		i, err := n.Int64()
		return int(i), err == nil
	}
	return 0, false
}
