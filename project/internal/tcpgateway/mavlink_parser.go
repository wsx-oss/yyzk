package tcpgateway

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"sync"
	"time"

	"smartcontrol/internal/cache"
	"smartcontrol/internal/db"
)

// MAVLink message IDs
const (
	MsgHeartbeat        = 0
	MsgSysStatus        = 1
	MsgSystemTime       = 2
	MsgGPSRawInt        = 24
	MsgScaledIMU        = 26
	MsgScaledPressure   = 29
	MsgAttitude         = 30
	MsgAttitudeQuat     = 31
	MsgLocalPositionNED = 32
	MsgGlobalPositionInt = 33
	MsgRCChannelsRaw    = 35
	MsgServoOutputRaw   = 36
	MsgMissionCurrent   = 42
	MsgRCChannels       = 65
	MsgVFRHUD           = 74
	MsgCommandAck       = 77
	MsgHighresIMU       = 105
	MsgScaledIMU2       = 116
	MsgGPS2Raw          = 124
	MsgScaledIMU3       = 129
	MsgBatteryStatus    = 147
	MsgStatustext       = 253
	MsgExtendedSysState = 245
	MsgVibration        = 241
	MsgHomePosition     = 242
	MsgAutopilotVersion = 148
)

// MavlinkParser handles parsing and storing MAVLink telemetry data.
type MavlinkParser struct {
	database *db.DB

	// Throttle: per sysid+msgType last write time
	mu        sync.Mutex
	lastWrite map[string]time.Time
}

// throttle intervals per message type (avoid DB overload)
var throttleIntervals = map[int]time.Duration{
	MsgHeartbeat:         1 * time.Second,
	MsgAttitude:          500 * time.Millisecond,
	MsgGlobalPositionInt: 500 * time.Millisecond,
	MsgGPSRawInt:         2 * time.Second,
	MsgGPS2Raw:           2 * time.Second,
	MsgVFRHUD:            500 * time.Millisecond,
	MsgBatteryStatus:     2 * time.Second,
	MsgSysStatus:         2 * time.Second,
	MsgRCChannels:        1 * time.Second,
	MsgRCChannelsRaw:     1 * time.Second,
	MsgExtendedSysState:  2 * time.Second,
	MsgHomePosition:      5 * time.Second,
	MsgStatustext:        0, // always store
	MsgCommandAck:        0,
	MsgMissionCurrent:    1 * time.Second,
	MsgVibration:         2 * time.Second,
}

func NewMavlinkParser(database *db.DB) *MavlinkParser {
	return &MavlinkParser{
		database:  database,
		lastWrite: make(map[string]time.Time),
	}
}

// shouldWrite returns true if enough time has passed since last write for this key.
func (p *MavlinkParser) shouldWrite(sysID, msgID int) bool {
	interval, ok := throttleIntervals[msgID]
	if !ok {
		interval = 1 * time.Second
	}
	if interval == 0 {
		return true
	}

	key := fmt.Sprintf("%d:%d", sysID, msgID)
	p.mu.Lock()
	defer p.mu.Unlock()
	last, exists := p.lastWrite[key]
	if !exists || time.Since(last) >= interval {
		p.lastWrite[key] = time.Now()
		return true
	}
	return false
}

// ParseAndStore parses a MAVLink payload and stores the result in DB + Redis.
func (p *MavlinkParser) ParseAndStore(sysID, compID, msgID int, payload []byte) {
	parsed := p.parsePayload(msgID, payload)
	if parsed == nil {
		return
	}

	if !p.shouldWrite(sysID, msgID) {
		// Still update Redis even if DB write is throttled
		p.cacheToRedis(sysID, msgID, parsed)
		return
	}

	msgType := msgIDToType(msgID)
	jsonData, err := json.Marshal(parsed)
	if err != nil {
		return
	}

	// Mark drone online in Redis
	if cache.Available() {
		cache.Set(fmt.Sprintf("mavlink:drone:%d:online", sysID), "1", 15*time.Second)
	}

	// Upsert into mavlink_telemetry
	_, err = p.database.Exec(`INSERT INTO mavlink_telemetry (sys_id, comp_id, msg_type, payload, updated_at)
		VALUES (?, ?, ?, ?, NOW())
		ON DUPLICATE KEY UPDATE payload=VALUES(payload), updated_at=NOW()`,
		sysID, compID, msgType, string(jsonData))
	if err != nil {
		log.Printf("[MavlinkParser] DB upsert error sys=%d msg=%s: %v", sysID, msgType, err)
	}

	// Cache to Redis
	p.cacheToRedis(sysID, msgID, parsed)

	// For STATUSTEXT, also log to mavlink_message_log
	if msgID == MsgStatustext {
		sev, _ := parsed["severity"].(string)
		text, _ := parsed["text"].(string)
		p.database.Exec(`INSERT INTO mavlink_message_log (sys_id, msg_type, severity, message, created_at)
			VALUES (?, 'status_text', ?, ?, NOW())`, sysID, sev, text)
	}
}

func (p *MavlinkParser) cacheToRedis(sysID, msgID int, parsed map[string]interface{}) {
	if !cache.Available() {
		return
	}
	msgType := msgIDToType(msgID)
	jsonData, _ := json.Marshal(parsed)
	cache.Set(fmt.Sprintf("mavlink:drone:%d:%s", sysID, msgType), string(jsonData), 30*time.Second)
}

func msgIDToType(msgID int) string {
	switch msgID {
	case MsgHeartbeat:
		return "heartbeat"
	case MsgSysStatus:
		return "sys_status"
	case MsgSystemTime:
		return "system_time"
	case MsgGPSRawInt:
		return "gps_raw"
	case MsgAttitude:
		return "attitude"
	case MsgGlobalPositionInt:
		return "position"
	case MsgLocalPositionNED:
		return "local_position"
	case MsgRCChannels:
		return "rc_channels"
	case MsgRCChannelsRaw:
		return "rc_channels_raw"
	case MsgVFRHUD:
		return "vfr_hud"
	case MsgBatteryStatus:
		return "battery"
	case MsgCommandAck:
		return "command_ack"
	case MsgStatustext:
		return "status_text"
	case MsgExtendedSysState:
		return "landed_state"
	case MsgHomePosition:
		return "home_position"
	case MsgGPS2Raw:
		return "gps2_raw"
	case MsgMissionCurrent:
		return "mission_current"
	case MsgVibration:
		return "vibration"
	case MsgAutopilotVersion:
		return "autopilot_version"
	default:
		return fmt.Sprintf("msg_%d", msgID)
	}
}

// parsePayload decodes the binary payload into a map based on msgID.
func (p *MavlinkParser) parsePayload(msgID int, payload []byte) map[string]interface{} {
	switch msgID {
	case MsgHeartbeat:
		return p.parseHeartbeat(payload)
	case MsgSysStatus:
		return p.parseSysStatus(payload)
	case MsgGPSRawInt:
		return p.parseGPSRawInt(payload)
	case MsgAttitude:
		return p.parseAttitude(payload)
	case MsgGlobalPositionInt:
		return p.parseGlobalPositionInt(payload)
	case MsgVFRHUD:
		return p.parseVFRHUD(payload)
	case MsgBatteryStatus:
		return p.parseBatteryStatus(payload)
	case MsgRCChannels:
		return p.parseRCChannels(payload)
	case MsgRCChannelsRaw:
		return p.parseRCChannelsRaw(payload)
	case MsgStatustext:
		return p.parseStatustext(payload)
	case MsgCommandAck:
		return p.parseCommandAck(payload)
	case MsgExtendedSysState:
		return p.parseExtendedSysState(payload)
	case MsgHomePosition:
		return p.parseHomePosition(payload)
	case MsgGPS2Raw:
		return p.parseGPS2Raw(payload)
	case MsgMissionCurrent:
		return p.parseMissionCurrent(payload)
	case MsgLocalPositionNED:
		return p.parseLocalPositionNED(payload)
	case MsgVibration:
		return p.parseVibration(payload)
	default:
		return nil // Unknown message, skip
	}
}

// ---- Individual message parsers ----

// HEARTBEAT (msgid=0): custom_mode(u32) type(u8) autopilot(u8) base_mode(u8) system_status(u8) mavlink_version(u8)
func (p *MavlinkParser) parseHeartbeat(d []byte) map[string]interface{} {
	if len(d) < 9 {
		return nil
	}
	customMode := binary.LittleEndian.Uint32(d[0:4])
	mavType := d[4]
	autopilot := d[5]
	baseMode := d[6]
	sysStatus := d[7]

	armed := (baseMode & 0x80) != 0
	modeName := heartbeatModeName(mavType, customMode, baseMode)

	return map[string]interface{}{
		"custom_mode":   customMode,
		"mav_type":      mavTypeName(mavType),
		"mav_type_id":   mavType,
		"autopilot":     autopilot,
		"base_mode":     baseMode,
		"system_status": sysStatusName(sysStatus),
		"armed":         armed,
		"mode":          modeName,
	}
}

// SYS_STATUS (msgid=1): sensors_present(u32) sensors_enabled(u32) sensors_health(u32)
//   load(u16) voltage_battery(u16) current_battery(i16) battery_remaining(i8)
//   drop_rate_comm(u16) errors_comm(u16) ...
func (p *MavlinkParser) parseSysStatus(d []byte) map[string]interface{} {
	if len(d) < 31 {
		return nil
	}
	load := binary.LittleEndian.Uint16(d[12:14])
	vbat := binary.LittleEndian.Uint16(d[14:16])
	cbat := int16(binary.LittleEndian.Uint16(d[16:18]))
	batRem := int8(d[30])
	dropRate := binary.LittleEndian.Uint16(d[18:20])

	return map[string]interface{}{
		"load":            float64(load) / 10.0,
		"voltage_battery": int(vbat),
		"current_battery": int(cbat),
		"battery_remaining": int(batRem),
		"drop_rate_comm":  int(dropRate),
	}
}

// GPS_RAW_INT (msgid=24): time_usec(u64) lat(i32) lon(i32) alt(i32) eph(u16) epv(u16) vel(u16) cog(u16) fix_type(u8) satellites_visible(u8)
func (p *MavlinkParser) parseGPSRawInt(d []byte) map[string]interface{} {
	if len(d) < 30 {
		return nil
	}
	lat := int32(binary.LittleEndian.Uint32(d[8:12]))
	lon := int32(binary.LittleEndian.Uint32(d[12:16]))
	alt := int32(binary.LittleEndian.Uint32(d[16:20]))
	eph := binary.LittleEndian.Uint16(d[20:22])
	epv := binary.LittleEndian.Uint16(d[22:24])
	vel := binary.LittleEndian.Uint16(d[24:26])
	cog := binary.LittleEndian.Uint16(d[26:28])
	fixType := d[28]
	sats := d[29]

	return map[string]interface{}{
		"lat":        float64(lat) / 1e7,
		"lon":        float64(lon) / 1e7,
		"alt":        float64(alt) / 1000.0,
		"eph":        float64(eph) / 100.0,
		"epv":        float64(epv) / 100.0,
		"vel":        float64(vel) / 100.0,
		"cog":        float64(cog) / 100.0,
		"fix_type":   int(fixType),
		"fix_name":   gpsFixName(fixType),
		"satellites": int(sats),
	}
}

// ATTITUDE (msgid=30): time_boot_ms(u32) roll(f32) pitch(f32) yaw(f32) rollspeed(f32) pitchspeed(f32) yawspeed(f32)
func (p *MavlinkParser) parseAttitude(d []byte) map[string]interface{} {
	if len(d) < 28 {
		return nil
	}
	roll := math.Float32frombits(binary.LittleEndian.Uint32(d[4:8]))
	pitch := math.Float32frombits(binary.LittleEndian.Uint32(d[8:12]))
	yaw := math.Float32frombits(binary.LittleEndian.Uint32(d[12:16]))
	rollspeed := math.Float32frombits(binary.LittleEndian.Uint32(d[16:20]))
	pitchspeed := math.Float32frombits(binary.LittleEndian.Uint32(d[20:24]))
	yawspeed := math.Float32frombits(binary.LittleEndian.Uint32(d[24:28]))

	return map[string]interface{}{
		"roll":       rad2deg(float64(roll)),
		"pitch":      rad2deg(float64(pitch)),
		"yaw":        rad2deg(float64(yaw)),
		"rollspeed":  float64(rollspeed),
		"pitchspeed": float64(pitchspeed),
		"yawspeed":   float64(yawspeed),
	}
}

// GLOBAL_POSITION_INT (msgid=33): time_boot_ms(u32) lat(i32) lon(i32) alt(i32) relative_alt(i32) vx(i16) vy(i16) vz(i16) hdg(u16)
func (p *MavlinkParser) parseGlobalPositionInt(d []byte) map[string]interface{} {
	if len(d) < 28 {
		return nil
	}
	lat := int32(binary.LittleEndian.Uint32(d[4:8]))
	lon := int32(binary.LittleEndian.Uint32(d[8:12]))
	alt := int32(binary.LittleEndian.Uint32(d[12:16]))
	relAlt := int32(binary.LittleEndian.Uint32(d[16:20]))
	vx := int16(binary.LittleEndian.Uint16(d[20:22]))
	vy := int16(binary.LittleEndian.Uint16(d[22:24]))
	vz := int16(binary.LittleEndian.Uint16(d[24:26]))
	hdg := binary.LittleEndian.Uint16(d[26:28])

	speed := math.Sqrt(float64(vx)*float64(vx)+float64(vy)*float64(vy)) / 100.0

	return map[string]interface{}{
		"lat":     float64(lat) / 1e7,
		"lon":     float64(lon) / 1e7,
		"alt":     float64(alt) / 1000.0,
		"rel_alt": float64(relAlt) / 1000.0,
		"vx":      float64(vx) / 100.0,
		"vy":      float64(vy) / 100.0,
		"vz":      float64(vz) / 100.0,
		"speed":   speed,
		"hdg":     float64(hdg) / 100.0,
	}
}

// VFR_HUD (msgid=74): airspeed(f32) groundspeed(f32) heading(i16) throttle(u16) alt(f32) climb(f32)
func (p *MavlinkParser) parseVFRHUD(d []byte) map[string]interface{} {
	if len(d) < 20 {
		return nil
	}
	airspeed := math.Float32frombits(binary.LittleEndian.Uint32(d[0:4]))
	groundspeed := math.Float32frombits(binary.LittleEndian.Uint32(d[4:8]))
	alt := math.Float32frombits(binary.LittleEndian.Uint32(d[8:12]))
	climb := math.Float32frombits(binary.LittleEndian.Uint32(d[12:16]))
	heading := int16(binary.LittleEndian.Uint16(d[16:18]))
	throttle := binary.LittleEndian.Uint16(d[18:20])

	return map[string]interface{}{
		"airspeed":    float64(airspeed),
		"groundspeed": float64(groundspeed),
		"alt":         float64(alt),
		"climb":       float64(climb),
		"heading":     int(heading),
		"throttle":    int(throttle),
	}
}

// BATTERY_STATUS (msgid=147): id(u8) func(u8) type(u8) temperature(i16) voltages[10](u16) current(i16) current_consumed(i32) energy_consumed(i32) battery_remaining(i8)
func (p *MavlinkParser) parseBatteryStatus(d []byte) map[string]interface{} {
	if len(d) < 36 {
		return nil
	}
	currentConsumed := int32(binary.LittleEndian.Uint32(d[0:4]))
	energyConsumed := int32(binary.LittleEndian.Uint32(d[4:8]))
	temp := int16(binary.LittleEndian.Uint16(d[8:10]))
	// voltages[0..9] at d[10..30]
	volt0 := binary.LittleEndian.Uint16(d[10:12])
	current := int16(binary.LittleEndian.Uint16(d[30:32]))
	batRem := int8(d[35])

	voltage := float64(volt0) / 1000.0 // mV -> V
	if volt0 == 0xFFFF {
		voltage = 0
	}

	return map[string]interface{}{
		"voltage":          voltage,
		"current":          float64(current) / 100.0, // cA -> A
		"remaining":        int(batRem),
		"temperature":      float64(temp) / 100.0, // cdegC -> degC
		"current_consumed": int(currentConsumed),
		"energy_consumed":  int(energyConsumed),
	}
}

// RC_CHANNELS (msgid=65): time_boot_ms(u32) chancount(u8) chan1..chan18(u16) rssi(u8)
func (p *MavlinkParser) parseRCChannels(d []byte) map[string]interface{} {
	if len(d) < 42 {
		return nil
	}
	chanCount := d[4]
	channels := make([]int, 0, 18)
	for ch := 0; ch < 18 && 5+ch*2+2 <= len(d); ch++ {
		off := 5 + ch*2
		channels = append(channels, int(binary.LittleEndian.Uint16(d[off:off+2])))
	}
	rssi := 0
	if len(d) >= 42 {
		rssi = int(d[41])
	}
	return map[string]interface{}{
		"chan_count": int(chanCount),
		"channels":  channels,
		"rssi":      rssi,
	}
}

// RC_CHANNELS_RAW (msgid=35): time_boot_ms(u32) chan1..chan8(u16) port(u8) rssi(u8)
func (p *MavlinkParser) parseRCChannelsRaw(d []byte) map[string]interface{} {
	if len(d) < 22 {
		return nil
	}
	channels := make([]int, 0, 8)
	for ch := 0; ch < 8; ch++ {
		off := 4 + ch*2
		channels = append(channels, int(binary.LittleEndian.Uint16(d[off:off+2])))
	}
	port := d[20]
	rssi := d[21]
	return map[string]interface{}{
		"channels": channels,
		"port":     int(port),
		"rssi":     int(rssi),
	}
}

// STATUSTEXT (msgid=253): severity(u8) text[50](char)
func (p *MavlinkParser) parseStatustext(d []byte) map[string]interface{} {
	if len(d) < 2 {
		return nil
	}
	severity := d[0]
	textEnd := len(d)
	if textEnd > 51 {
		textEnd = 51
	}
	text := string(d[1:textEnd])
	// Trim null bytes
	for i := range text {
		if text[i] == 0 {
			text = text[:i]
			break
		}
	}
	return map[string]interface{}{
		"severity":   severityName(severity),
		"severity_id": int(severity),
		"text":       text,
	}
}

// COMMAND_ACK (msgid=77): command(u16) result(u8)
func (p *MavlinkParser) parseCommandAck(d []byte) map[string]interface{} {
	if len(d) < 3 {
		return nil
	}
	cmd := binary.LittleEndian.Uint16(d[0:2])
	result := d[2]
	return map[string]interface{}{
		"command": int(cmd),
		"result":  int(result),
	}
}

// EXTENDED_SYS_STATE (msgid=245): vtol_state(u8) landed_state(u8)
func (p *MavlinkParser) parseExtendedSysState(d []byte) map[string]interface{} {
	if len(d) < 2 {
		return nil
	}
	return map[string]interface{}{
		"vtol_state":   int(d[0]),
		"landed_state": int(d[1]),
		"state":        landedStateName(d[1]),
	}
}

// HOME_POSITION (msgid=242): lat(i32) lon(i32) alt(i32) ...
func (p *MavlinkParser) parseHomePosition(d []byte) map[string]interface{} {
	if len(d) < 12 {
		return nil
	}
	lat := int32(binary.LittleEndian.Uint32(d[0:4]))
	lon := int32(binary.LittleEndian.Uint32(d[4:8]))
	alt := int32(binary.LittleEndian.Uint32(d[8:12]))
	return map[string]interface{}{
		"lat": float64(lat) / 1e7,
		"lon": float64(lon) / 1e7,
		"alt": float64(alt) / 1000.0,
	}
}

// GPS2_RAW (msgid=124): same structure as GPS_RAW_INT
func (p *MavlinkParser) parseGPS2Raw(d []byte) map[string]interface{} {
	return p.parseGPSRawInt(d) // Same binary layout
}

// MISSION_CURRENT (msgid=42): seq(u16)
func (p *MavlinkParser) parseMissionCurrent(d []byte) map[string]interface{} {
	if len(d) < 2 {
		return nil
	}
	return map[string]interface{}{
		"seq": int(binary.LittleEndian.Uint16(d[0:2])),
	}
}

// LOCAL_POSITION_NED (msgid=32): time_boot_ms(u32) x(f32) y(f32) z(f32) vx(f32) vy(f32) vz(f32)
func (p *MavlinkParser) parseLocalPositionNED(d []byte) map[string]interface{} {
	if len(d) < 28 {
		return nil
	}
	x := math.Float32frombits(binary.LittleEndian.Uint32(d[4:8]))
	y := math.Float32frombits(binary.LittleEndian.Uint32(d[8:12]))
	z := math.Float32frombits(binary.LittleEndian.Uint32(d[12:16]))
	vx := math.Float32frombits(binary.LittleEndian.Uint32(d[16:20]))
	vy := math.Float32frombits(binary.LittleEndian.Uint32(d[20:24]))
	vz := math.Float32frombits(binary.LittleEndian.Uint32(d[24:28]))
	return map[string]interface{}{
		"x": float64(x), "y": float64(y), "z": float64(z),
		"vx": float64(vx), "vy": float64(vy), "vz": float64(vz),
	}
}

// VIBRATION (msgid=241): time_usec(u64) vibration_x(f32) vibration_y(f32) vibration_z(f32) clipping_0(u32) clipping_1(u32) clipping_2(u32)
func (p *MavlinkParser) parseVibration(d []byte) map[string]interface{} {
	if len(d) < 32 {
		return nil
	}
	vx := math.Float32frombits(binary.LittleEndian.Uint32(d[8:12]))
	vy := math.Float32frombits(binary.LittleEndian.Uint32(d[12:16]))
	vz := math.Float32frombits(binary.LittleEndian.Uint32(d[16:20]))
	clip0 := binary.LittleEndian.Uint32(d[20:24])
	clip1 := binary.LittleEndian.Uint32(d[24:28])
	clip2 := binary.LittleEndian.Uint32(d[28:32])
	return map[string]interface{}{
		"vibration_x": float64(vx), "vibration_y": float64(vy), "vibration_z": float64(vz),
		"clipping_0": int(clip0), "clipping_1": int(clip1), "clipping_2": int(clip2),
	}
}

// ---- Enum helpers ----

func rad2deg(r float64) float64 {
	return r * 180.0 / math.Pi
}

func mavTypeName(t uint8) string {
	names := map[uint8]string{
		0: "GENERIC", 1: "FIXED_WING", 2: "QUADROTOR", 3: "COAXIAL",
		4: "HELICOPTER", 6: "GCS", 10: "GROUND_ROVER", 11: "SURFACE_BOAT",
		13: "HEXAROTOR", 14: "OCTOROTOR", 15: "TRICOPTER",
		19: "VTOL_DUOROTOR", 20: "VTOL_QUADROTOR", 29: "DODECAROTOR",
	}
	if n, ok := names[t]; ok {
		return n
	}
	return fmt.Sprintf("TYPE_%d", t)
}

func sysStatusName(s uint8) string {
	names := map[uint8]string{
		0: "UNINIT", 1: "BOOT", 2: "CALIBRATING", 3: "STANDBY",
		4: "ACTIVE", 5: "CRITICAL", 6: "EMERGENCY", 7: "POWEROFF", 8: "FLIGHT_TERMINATION",
	}
	if n, ok := names[s]; ok {
		return n
	}
	return fmt.Sprintf("STATE_%d", s)
}

func gpsFixName(f uint8) string {
	names := map[uint8]string{
		0: "NO_GPS", 1: "NO_FIX", 2: "2D_FIX", 3: "3D_FIX",
		4: "DGPS", 5: "RTK_FLOAT", 6: "RTK_FIXED",
	}
	if n, ok := names[f]; ok {
		return n
	}
	return fmt.Sprintf("FIX_%d", f)
}

func severityName(s uint8) string {
	names := map[uint8]string{
		0: "emergency", 1: "alert", 2: "critical", 3: "error",
		4: "warning", 5: "notice", 6: "info", 7: "debug",
	}
	if n, ok := names[s]; ok {
		return n
	}
	return fmt.Sprintf("sev_%d", s)
}

func landedStateName(s uint8) string {
	names := map[uint8]string{
		0: "UNDEFINED", 1: "ON_GROUND", 2: "IN_AIR", 3: "TAKEOFF", 4: "LANDING",
	}
	if n, ok := names[s]; ok {
		return n
	}
	return fmt.Sprintf("LANDED_%d", s)
}

func heartbeatModeName(mavType uint8, customMode uint32, baseMode uint8) string {
	// ArduPilot Copter modes (type=2 quadrotor)
	if mavType == 2 {
		copterModes := map[uint32]string{
			0: "STABILIZE", 1: "ACRO", 2: "ALT_HOLD", 3: "AUTO",
			4: "GUIDED", 5: "LOITER", 6: "RTL", 7: "CIRCLE",
			9: "LAND", 11: "DRIFT", 13: "SPORT", 14: "FLIP",
			15: "AUTOTUNE", 16: "POSHOLD", 17: "BRAKE", 18: "THROW",
			19: "AVOID_ADSB", 20: "GUIDED_NOGPS", 21: "SMART_RTL",
			22: "FLOWHOLD", 23: "FOLLOW", 24: "ZIGZAG", 25: "SYSTEMID",
			26: "AUTOROTATE", 27: "AUTO_RTL",
		}
		if n, ok := copterModes[customMode]; ok {
			return n
		}
	}
	// ArduPilot Plane modes (type=1 fixed_wing)
	if mavType == 1 {
		planeModes := map[uint32]string{
			0: "MANUAL", 1: "CIRCLE", 2: "STABILIZE", 3: "TRAINING",
			4: "ACRO", 5: "FLY_BY_WIRE_A", 6: "FLY_BY_WIRE_B",
			7: "CRUISE", 8: "AUTOTUNE", 10: "AUTO", 11: "RTL",
			12: "LOITER", 14: "AVOID_ADSB", 15: "GUIDED",
		}
		if n, ok := planeModes[customMode]; ok {
			return n
		}
	}
	// PX4 modes via base_mode
	if (baseMode & 0x01) != 0 { // custom mode flag
		return fmt.Sprintf("CUSTOM_%d", customMode)
	}
	return fmt.Sprintf("MODE_%d", customMode)
}
