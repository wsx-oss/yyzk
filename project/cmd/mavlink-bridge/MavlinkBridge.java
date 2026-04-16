import com.MAVLink.MAVLinkPacket;
import com.MAVLink.Parser;
import com.MAVLink.Messages.MAVLinkMessage;
import com.MAVLink.minimal.msg_heartbeat;
import com.MAVLink.common.*;

import java.io.*;
import java.net.*;
import java.sql.*;
import java.util.*;
import java.util.concurrent.*;
import java.util.concurrent.atomic.AtomicBoolean;

/**
 * MAVLink TCP Bridge - 使用 mavlink.jar 解析 9080 端口接收的 MAVLink 二进制数据
 * 解析后写入 MySQL 和 Redis，供 Go 后端读取。
 *
 * 支持的报文:
 *   - HEARTBEAT (0): 飞行模式、解锁状态
 *   - SYS_STATUS (1): 系统电压电流
 *   - GPS_RAW_INT (24): GPS1 原始定位
 *   - GLOBAL_POSITION_INT (33): 飞控解算位置
 *   - ATTITUDE (30): 姿态角
 *   - VFR_HUD (74): 油门/速度/高度
 *   - BATTERY_STATUS (147): 电池详情
 *   - RC_CHANNELS (65): 遥控通道
 *   - EXTENDED_SYS_STATE (245): 着陆状态
 *   - COMMAND_ACK (77): 命令反馈
 *   - STATUSTEXT (253): 状态消息
 *   - GPS2_RAW (124): GPS2 原始定位
 *   - HOME_POSITION (242): 返航点/备降点
 *   - MISSION_CURRENT (42): 当前航点
 *   - AUTOPILOT_VERSION (148): 飞控版本
 */
public class MavlinkBridge {

    // ---- 配置（从环境变量或命令行读取）----
    private static int TCP_PORT = 9080;
    private static String MYSQL_URL = "jdbc:mysql://127.0.0.1:3306/smartcontrol?useSSL=false&serverTimezone=Asia/Shanghai&characterEncoding=utf8mb4";
    private static String MYSQL_USER = "root";
    private static String MYSQL_PASS = "";
    private static String REDIS_HOST = "";
    private static int REDIS_PORT = 6379;
    private static String REDIS_PASS = "";

    // ---- 运行时状态 ----
    private static final AtomicBoolean running = new AtomicBoolean(true);
    private static Connection dbConn;
    private static Socket redisSocket;
    private static OutputStream redisOut;
    private static InputStream redisIn;
    private static final Map<Integer, Long> lastGpsWriteMs = new ConcurrentHashMap<>();
    private static final Map<Integer, Long> lastBatteryWriteMs = new ConcurrentHashMap<>();
    private static final Map<Integer, Long> lastAttitudeWriteMs = new ConcurrentHashMap<>();

    // GPS 写入间隔（毫秒）- 避免高频消息打爆数据库
    private static final long GPS_DB_INTERVAL_MS = 1000;
    private static final long BATTERY_DB_INTERVAL_MS = 2000;
    private static final long ATTITUDE_REDIS_INTERVAL_MS = 200;

    public static void main(String[] args) throws Exception {
        loadConfig();
        ensureTable();
        log("MAVLink Bridge starting on TCP port " + TCP_PORT);

        ServerSocket server = new ServerSocket(TCP_PORT);
        server.setReuseAddress(true);
        log("Listening on :" + TCP_PORT + " for MAVLink TCP connections");

        // 优雅关闭
        Runtime.getRuntime().addShutdownHook(new Thread(() -> {
            running.set(false);
            try { server.close(); } catch (Exception ignored) {}
            closeDB();
            closeRedis();
            log("MAVLink Bridge stopped");
        }));

        ExecutorService pool = Executors.newCachedThreadPool();
        while (running.get()) {
            try {
                Socket client = server.accept();
                log("New connection from " + client.getRemoteSocketAddress());
                pool.submit(() -> handleClient(client));
            } catch (Exception e) {
                if (running.get()) log("Accept error: " + e.getMessage());
            }
        }
        pool.shutdown();
    }

    // ========== TCP 连接处理 ==========

    private static void handleClient(Socket client) {
        String remote = client.getRemoteSocketAddress().toString();
        try {
            client.setSoTimeout(120_000);
            InputStream in = client.getInputStream();
            OutputStream out = client.getOutputStream();
            Parser parser = new Parser();

            // 发送 GCS 心跳以触发飞控切换 MAVLink V2
            sendGCSHeartbeat(out);

            byte[] buf = new byte[4096];
            long lastHeartbeatSent = System.currentTimeMillis();

            while (running.get() && !client.isClosed()) {
                int available;
                try {
                    available = in.read(buf);
                } catch (SocketTimeoutException e) {
                    // 超时 -> 发送心跳保活
                    sendGCSHeartbeat(out);
                    continue;
                }
                if (available <= 0) break;

                for (int i = 0; i < available; i++) {
                    MAVLinkPacket packet = parser.mavlink_parse_char(buf[i] & 0xFF);
                    if (packet != null) {
                        processPacket(packet, remote, out);
                    }
                }

                // 每 1 秒发送 GCS 心跳
                long now = System.currentTimeMillis();
                if (now - lastHeartbeatSent > 1000) {
                    sendGCSHeartbeat(out);
                    lastHeartbeatSent = now;
                }
            }
        } catch (Exception e) {
            log("Connection " + remote + " error: " + e.getMessage());
        } finally {
            try { client.close(); } catch (Exception ignored) {}
            log("Connection closed: " + remote);
        }
    }

    // 发送 GCS 心跳包 (MAVLink V2 格式)
    private static void sendGCSHeartbeat(OutputStream out) {
        try {
            msg_heartbeat hb = new msg_heartbeat();
            hb.type = 6;           // MAV_TYPE_GCS
            hb.autopilot = 8;      // MAV_AUTOPILOT_INVALID
            hb.base_mode = 192;    // MAV_MODE_MANUAL_ARMED
            hb.custom_mode = 0;
            hb.system_status = 4;  // MAV_STATE_ACTIVE
            MAVLinkPacket pkt = hb.pack();
            pkt.sysid = 255;       // GCS
            pkt.compid = 1;
            pkt.isMavlink2 = true;
            byte[] bytes = pkt.encodePacket();
            out.write(bytes);
            out.flush();
        } catch (Exception e) {
            // ignore write errors
        }
    }

    // ========== MAVLink 消息处理 ==========

    private static void processPacket(MAVLinkPacket packet, String remote, OutputStream out) {
        try {
            MAVLinkMessage msg = packet.unpack();
            if (msg == null) return;

            int sysId = packet.sysid;
            long now = System.currentTimeMillis();

            if (msg instanceof msg_heartbeat) {
                handleHeartbeat(sysId, (msg_heartbeat) msg, remote);
            } else if (msg instanceof msg_global_position_int) {
                handleGlobalPosition(sysId, (msg_global_position_int) msg);
            } else if (msg instanceof msg_gps_raw_int) {
                handleGpsRaw(sysId, (msg_gps_raw_int) msg);
            } else if (msg instanceof msg_attitude) {
                handleAttitude(sysId, (msg_attitude) msg);
            } else if (msg instanceof msg_vfr_hud) {
                handleVfrHud(sysId, (msg_vfr_hud) msg);
            } else if (msg instanceof msg_battery_status) {
                handleBatteryStatus(sysId, (msg_battery_status) msg);
            } else if (msg instanceof msg_sys_status) {
                handleSysStatus(sysId, (msg_sys_status) msg);
            } else if (msg instanceof msg_rc_channels) {
                handleRcChannels(sysId, (msg_rc_channels) msg);
            } else if (msg instanceof msg_extended_sys_state) {
                handleExtendedSysState(sysId, (msg_extended_sys_state) msg);
            } else if (msg instanceof msg_statustext) {
                handleStatusText(sysId, (msg_statustext) msg, remote);
            } else if (msg instanceof msg_command_ack) {
                handleCommandAck(sysId, (msg_command_ack) msg);
            } else if (msg instanceof msg_home_position) {
                handleHomePosition(sysId, (msg_home_position) msg);
            } else if (msg instanceof msg_gps2_raw) {
                handleGps2Raw(sysId, (msg_gps2_raw) msg);
            } else if (msg instanceof msg_autopilot_version) {
                handleAutopilotVersion(sysId, (msg_autopilot_version) msg);
            } else if (msg instanceof msg_mission_current) {
                handleMissionCurrent(sysId, (msg_mission_current) msg);
            }

            // 所有消息都写入 Redis 实时缓存
            cacheToRedis(sysId, packet.msgid, msg);

        } catch (Exception e) {
            log("Process packet error (msgid=" + packet.msgid + "): " + e.getMessage());
        }
    }

    // ---------- HEARTBEAT (ID=0) ----------
    private static void handleHeartbeat(int sysId, msg_heartbeat hb, String remote) {
        // 跳过 GCS 心跳（sysId=255 或 type=6）
        if (sysId == 255 || hb.type == 6) return;

        int customMode = (int)(hb.custom_mode & 0xFFFFFFFFL);
        int mainMode = (customMode >> 16) & 0xFF;
        int subMode = (customMode >> 24) & 0xFF;
        boolean armed = (hb.base_mode & 0x80) != 0;

        String modeName = decodeFlightMode(mainMode, subMode);
        String status = armed ? "armed" : "disarmed";

        // 更新 drones 表状态（如果不存在则自动创建 - auto-discovery）
        String agentIdHb = "mavlink-" + sysId;
        int droneRows = execUpdate("UPDATE drones SET status='online', updated_at=NOW() WHERE agent_id=?", agentIdHb);
        if (droneRows == 0) {
            // Auto-create drone entry
            String droneName = "无人机-" + sysId;
            execSQL("INSERT IGNORE INTO drones(name, model, description, ip, ssh_port, vnc_port, rdp_port, protocol, username, password, " +
                    "agent_id, initial_lat, initial_lng, initial_alt, fence_enabled, fence_lat, fence_lng, fence_radius, " +
                    "auto_connect, log_enabled, status, video_url, created_at, updated_at) " +
                    "VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,NOW(),NOW())",
                    droneName, "MAVLink自动发现", "通过MAVLink心跳自动注册", "", 22, 5900, 3389, "SSH", "", "",
                    agentIdHb, 0, 0, 0, 0, 0, 0, 500, 0, 0, "online", "");
            // Auto-create linked gps_device
            execSQL("INSERT IGNORE INTO gps_devices(name, agent_id, device_type, latitude, longitude, altitude, speed, heading, status, fence_enabled, last_update) " +
                    "VALUES(?,?,?,?,?,?,?,?,?,0,NOW())",
                    droneName, agentIdHb, "无人机", 0, 0, 0, 0, 0, "等待连接");
            // Link them together
            execSQL("UPDATE drones d JOIN gps_devices g ON g.agent_id = d.agent_id " +
                    "SET d.linked_gps_device_id = g.id WHERE d.agent_id = ? AND (d.linked_gps_device_id IS NULL OR d.linked_gps_device_id = 0)", agentIdHb);
            log("Auto-created drone for agent " + agentIdHb);
        }

        // 写入遥测表
        upsertTelemetry(sysId, "heartbeat",
                String.format("{\"armed\":%s,\"mode\":\"%s\",\"main_mode\":%d,\"sub_mode\":%d,\"mav_type\":%d}",
                        armed, modeName, mainMode, subMode, hb.type));

        // Redis 缓存
        redisSet("mavlink:drone:" + sysId + ":heartbeat",
                String.format("{\"armed\":%s,\"mode\":\"%s\",\"main_mode\":%d,\"sub_mode\":%d,\"mav_type\":%d,\"ts\":%d}",
                        armed, modeName, mainMode, subMode, hb.type, System.currentTimeMillis()), 30);
    }

    // ---------- GLOBAL_POSITION_INT (ID=33) ----------
    private static void handleGlobalPosition(int sysId, msg_global_position_int pos) {
        double lat = pos.lat / 1e7;
        double lon = pos.lon / 1e7;
        double alt = pos.alt / 1000.0;
        double relAlt = pos.relative_alt / 1000.0;
        double vx = pos.vx / 100.0; // cm/s -> m/s
        double vy = pos.vy / 100.0;
        double vz = pos.vz / 100.0;
        int hdg = pos.hdg;
        double speed = Math.sqrt(vx * vx + vy * vy);

        // 节流写数据库
        long now = System.currentTimeMillis();
        Long last = lastGpsWriteMs.get(sysId);
        if (last == null || now - last >= GPS_DB_INTERVAL_MS) {
            lastGpsWriteMs.put(sysId, now);

            // 更新 gps_devices 表
            String agentId = "mavlink-" + sysId;
            int rowsUpdated = execUpdate(
                    "UPDATE gps_devices SET latitude=?, longitude=?, altitude=?, speed=?, heading=?, status='在线', last_update=NOW() WHERE agent_id=?",
                    lat, lon, alt, speed, (double) hdg, agentId);

            // 写入 gps_history
            if (rowsUpdated > 0) {
                execSQL("INSERT INTO gps_history(device_id, latitude, longitude, altitude, speed, heading, created_at) " +
                                "SELECT id, ?, ?, ?, ?, ?, NOW() FROM gps_devices WHERE agent_id=? LIMIT 1",
                        lat, lon, alt, speed, (double) hdg, agentId);
            }

            // 写入遥测表
            upsertTelemetry(sysId, "global_position",
                    String.format("{\"lat\":%.7f,\"lon\":%.7f,\"alt\":%.2f,\"rel_alt\":%.2f,\"vx\":%.2f,\"vy\":%.2f,\"vz\":%.2f,\"hdg\":%d,\"speed\":%.2f}",
                            lat, lon, alt, relAlt, vx, vy, vz, hdg, speed));
        }

        // Redis 实时缓存 (高频)
        redisSet("mavlink:drone:" + sysId + ":position",
                String.format("{\"lat\":%.7f,\"lon\":%.7f,\"alt\":%.2f,\"rel_alt\":%.2f,\"speed\":%.2f,\"hdg\":%d,\"vx\":%.2f,\"vy\":%.2f,\"vz\":%.2f,\"ts\":%d}",
                        lat, lon, alt, relAlt, speed, hdg, vx, vy, vz, now), 15);
    }

    // ---------- GPS_RAW_INT (ID=24) ----------
    private static void handleGpsRaw(int sysId, msg_gps_raw_int gps) {
        double lat = gps.lat / 1e7;
        double lon = gps.lon / 1e7;
        double alt = gps.alt / 1000.0;
        int fixType = gps.fix_type;
        int satellites = gps.satellites_visible;
        double eph = gps.eph / 100.0; // cm -> m
        double epv = gps.epv / 100.0;
        double vel = gps.vel / 100.0; // cm/s -> m/s

        String fixName = decodeFixType(fixType);

        upsertTelemetry(sysId, "gps_raw",
                String.format("{\"lat\":%.7f,\"lon\":%.7f,\"alt\":%.2f,\"fix_type\":%d,\"fix_name\":\"%s\",\"satellites\":%d,\"eph\":%.2f,\"epv\":%.2f,\"vel\":%.2f}",
                        lat, lon, alt, fixType, fixName, satellites, eph, epv, vel));

        redisSet("mavlink:drone:" + sysId + ":gps_raw",
                String.format("{\"lat\":%.7f,\"lon\":%.7f,\"alt\":%.2f,\"fix_type\":%d,\"fix_name\":\"%s\",\"satellites\":%d,\"eph\":%.2f,\"epv\":%.2f,\"vel\":%.2f,\"ts\":%d}",
                        lat, lon, alt, fixType, fixName, satellites, eph, epv, vel, System.currentTimeMillis()), 15);
    }

    // ---------- ATTITUDE (ID=30) ----------
    private static void handleAttitude(int sysId, msg_attitude att) {
        double roll = Math.toDegrees(att.roll);
        double pitch = Math.toDegrees(att.pitch);
        double yaw = Math.toDegrees(att.yaw);
        double rollSpeed = Math.toDegrees(att.rollspeed);
        double pitchSpeed = Math.toDegrees(att.pitchspeed);
        double yawSpeed = Math.toDegrees(att.yawspeed);

        long now = System.currentTimeMillis();
        Long last = lastAttitudeWriteMs.get(sysId);
        if (last == null || now - last >= 2000) {
            lastAttitudeWriteMs.put(sysId, now);
            upsertTelemetry(sysId, "attitude",
                    String.format("{\"roll\":%.2f,\"pitch\":%.2f,\"yaw\":%.2f,\"roll_speed\":%.2f,\"pitch_speed\":%.2f,\"yaw_speed\":%.2f}",
                            roll, pitch, yaw, rollSpeed, pitchSpeed, yawSpeed));
        }

        // Redis 高频缓存（姿态用于前端 3D 展示）
        if (last == null || now - last >= ATTITUDE_REDIS_INTERVAL_MS) {
            lastAttitudeWriteMs.put(sysId, now);
            redisSet("mavlink:drone:" + sysId + ":attitude",
                    String.format("{\"roll\":%.2f,\"pitch\":%.2f,\"yaw\":%.2f,\"roll_speed\":%.2f,\"pitch_speed\":%.2f,\"yaw_speed\":%.2f,\"ts\":%d}",
                            roll, pitch, yaw, rollSpeed, pitchSpeed, yawSpeed, now), 10);
        }
    }

    // ---------- VFR_HUD (ID=74) ----------
    private static void handleVfrHud(int sysId, msg_vfr_hud hud) {
        upsertTelemetry(sysId, "vfr_hud",
                String.format("{\"airspeed\":%.2f,\"groundspeed\":%.2f,\"alt\":%.2f,\"climb\":%.2f,\"heading\":%d,\"throttle\":%d}",
                        hud.airspeed, hud.groundspeed, hud.alt, hud.climb, hud.heading, hud.throttle));

        redisSet("mavlink:drone:" + sysId + ":vfr_hud",
                String.format("{\"airspeed\":%.2f,\"groundspeed\":%.2f,\"alt\":%.2f,\"climb\":%.2f,\"heading\":%d,\"throttle\":%d,\"ts\":%d}",
                        hud.airspeed, hud.groundspeed, hud.alt, hud.climb, hud.heading, hud.throttle, System.currentTimeMillis()), 15);
    }

    // ---------- BATTERY_STATUS (ID=147) ----------
    private static void handleBatteryStatus(int sysId, msg_battery_status bat) {
        long now = System.currentTimeMillis();
        Long last = lastBatteryWriteMs.get(sysId);
        if (last != null && now - last < BATTERY_DB_INTERVAL_MS) {
            return; // 节流
        }
        lastBatteryWriteMs.put(sysId, now);

        int remaining = bat.battery_remaining; // 0-100%
        double temperature = bat.temperature / 100.0; // cdegC -> degC
        double currentA = bat.current_battery / 100.0; // cA -> A

        // 计算电压: 检查是否为特殊编码格式
        double voltageV = 0;
        if (bat.voltages != null && bat.voltages.length >= 10) {
            if (bat.voltages[1] == 65535 && bat.voltages[2] == 65535) {
                // 特殊格式：voltages[4-5] 为 uint32 电压(mV)
                long v = ((long)(bat.voltages[4] & 0xFFFF)) | (((long)(bat.voltages[5] & 0xFFFF)) << 16);
                voltageV = v / 1000.0;
            } else {
                // 标准格式：各节电压之和 (mV)
                for (int i = 0; i < 10; i++) {
                    if (bat.voltages[i] != 65535 && bat.voltages[i] != 0) {
                        voltageV += bat.voltages[i] / 1000.0;
                    }
                }
            }
        }

        String agentId = "mavlink-" + sysId;

        // 更新 drones 表电池
        execSQL("UPDATE drones SET battery_level=?, updated_at=NOW() WHERE agent_id=?", remaining, agentId);

        // 写入 battery_records（关联到 gps_device）
        execSQL("INSERT INTO battery_records(device_id, device_name, voltage, current_val, level, temperature, health, status, created_at) " +
                        "SELECT g.id, g.name, ?, ?, ?, ?, 100, CASE WHEN ?<20 THEN '低电量' WHEN ?<10 THEN '严重低电量' ELSE '正常' END, NOW() " +
                        "FROM gps_devices g WHERE g.agent_id=? LIMIT 1",
                voltageV, currentA, remaining, temperature, remaining, remaining, agentId);

        upsertTelemetry(sysId, "battery",
                String.format("{\"voltage\":%.2f,\"current\":%.2f,\"remaining\":%d,\"temperature\":%.1f,\"id\":%d}",
                        voltageV, currentA, remaining, temperature, bat.id));

        redisSet("mavlink:drone:" + sysId + ":battery",
                String.format("{\"voltage\":%.2f,\"current\":%.2f,\"remaining\":%d,\"temperature\":%.1f,\"id\":%d,\"ts\":%d}",
                        voltageV, currentA, remaining, temperature, bat.id, now), 30);
    }

    // ---------- SYS_STATUS (ID=1) ----------
    private static void handleSysStatus(int sysId, msg_sys_status sys) {
        double voltage = sys.voltage_battery / 1000.0; // mV -> V
        double current = sys.current_battery / 100.0;  // cA -> A
        int remaining = sys.battery_remaining;

        upsertTelemetry(sysId, "sys_status",
                String.format("{\"voltage\":%.2f,\"current\":%.2f,\"remaining\":%d,\"drop_rate\":%.1f,\"errors_comm\":%d}",
                        voltage, current, remaining, sys.drop_rate_comm / 100.0, sys.errors_comm));

        redisSet("mavlink:drone:" + sysId + ":sys_status",
                String.format("{\"voltage\":%.2f,\"current\":%.2f,\"remaining\":%d,\"ts\":%d}",
                        voltage, current, remaining, System.currentTimeMillis()), 15);
    }

    // ---------- RC_CHANNELS (ID=65) ----------
    private static void handleRcChannels(int sysId, msg_rc_channels rc) {
        StringBuilder sb = new StringBuilder("{");
        sb.append("\"chancount\":").append(rc.chancount);
        sb.append(",\"rssi\":").append(rc.rssi);
        sb.append(",\"channels\":[");
        int[] chans = {rc.chan1_raw, rc.chan2_raw, rc.chan3_raw, rc.chan4_raw,
                rc.chan5_raw, rc.chan6_raw, rc.chan7_raw, rc.chan8_raw,
                rc.chan9_raw, rc.chan10_raw, rc.chan11_raw, rc.chan12_raw,
                rc.chan13_raw, rc.chan14_raw, rc.chan15_raw, rc.chan16_raw,
                rc.chan17_raw, rc.chan18_raw};
        for (int i = 0; i < chans.length; i++) {
            if (i > 0) sb.append(",");
            sb.append(chans[i]);
        }
        sb.append("]}");

        upsertTelemetry(sysId, "rc_channels", sb.toString());
        redisSet("mavlink:drone:" + sysId + ":rc_channels", sb.toString(), 10);
    }

    // ---------- EXTENDED_SYS_STATE (ID=245) ----------
    private static void handleExtendedSysState(int sysId, msg_extended_sys_state ext) {
        String landedState;
        switch (ext.landed_state) {
            case 1: landedState = "on_ground"; break;
            case 2: landedState = "in_air"; break;
            case 3: landedState = "takeoff"; break;
            case 4: landedState = "landing"; break;
            default: landedState = "unknown"; break;
        }

        upsertTelemetry(sysId, "extended_sys_state",
                String.format("{\"landed_state\":\"%s\",\"landed_state_id\":%d}", landedState, ext.landed_state));
        redisSet("mavlink:drone:" + sysId + ":landed_state",
                String.format("{\"state\":\"%s\",\"id\":%d,\"ts\":%d}", landedState, ext.landed_state, System.currentTimeMillis()), 15);
    }

    // ---------- STATUSTEXT (ID=253) ----------
    private static void handleStatusText(int sysId, msg_statustext st, String remote) {
        String text = new String(st.text).trim().replace("\0", "");
        int severity = st.severity;
        String sevName = decodeSeverity(severity);

        log("[DRONE-" + sysId + "] " + sevName + ": " + text);

        // 写入 alerts 表
        if (severity <= 4) { // WARNING 及以上
            execSQL("INSERT INTO alerts(category, severity, message) VALUES('mavlink', ?, ?)",
                    sevName, "[无人机" + sysId + "] " + text);
        }

        upsertTelemetry(sysId, "statustext",
                String.format("{\"severity\":%d,\"severity_name\":\"%s\",\"text\":\"%s\"}",
                        severity, sevName, text.replace("\"", "\\\"").replace("\n", " ")));
    }

    // ---------- COMMAND_ACK (ID=77) ----------
    private static void handleCommandAck(int sysId, msg_command_ack ack) {
        upsertTelemetry(sysId, "command_ack",
                String.format("{\"command\":%d,\"result\":%d,\"progress\":%d}", ack.command, ack.result, ack.progress));
        redisSet("mavlink:drone:" + sysId + ":command_ack",
                String.format("{\"command\":%d,\"result\":%d,\"ts\":%d}", ack.command, ack.result, System.currentTimeMillis()), 30);
    }

    // ---------- HOME_POSITION (ID=242) ----------
    private static void handleHomePosition(int sysId, msg_home_position home) {
        double lat = home.latitude / 1e7;
        double lon = home.longitude / 1e7;
        double alt = home.altitude / 1000.0;

        upsertTelemetry(sysId, "home_position",
                String.format("{\"lat\":%.7f,\"lon\":%.7f,\"alt\":%.2f}", lat, lon, alt));
        redisSet("mavlink:drone:" + sysId + ":home_position",
                String.format("{\"lat\":%.7f,\"lon\":%.7f,\"alt\":%.2f,\"ts\":%d}", lat, lon, alt, System.currentTimeMillis()), 60);
    }

    // ---------- GPS2_RAW (ID=124) ----------
    private static void handleGps2Raw(int sysId, msg_gps2_raw gps) {
        double lat = gps.lat / 1e7;
        double lon = gps.lon / 1e7;
        double alt = gps.alt / 1000.0;

        upsertTelemetry(sysId, "gps2_raw",
                String.format("{\"lat\":%.7f,\"lon\":%.7f,\"alt\":%.2f,\"fix_type\":%d,\"satellites\":%d}",
                        lat, lon, alt, gps.fix_type, gps.satellites_visible));
        redisSet("mavlink:drone:" + sysId + ":gps2_raw",
                String.format("{\"lat\":%.7f,\"lon\":%.7f,\"alt\":%.2f,\"fix_type\":%d,\"satellites\":%d,\"ts\":%d}",
                        lat, lon, alt, gps.fix_type, gps.satellites_visible, System.currentTimeMillis()), 15);
    }

    // ---------- AUTOPILOT_VERSION (ID=148) ----------
    private static void handleAutopilotVersion(int sysId, msg_autopilot_version ver) {
        long swVer = ver.flight_sw_version & 0xFFFFFFFFL;
        int a = (int)((swVer >> 24) & 0xFF);
        int b = (int)((swVer >> 16) & 0xFF);
        int c = (int)((swVer >> 8) & 0xFF);
        String firmware = a + "." + b + "." + c;

        // 提取序列号
        String serial = "";
        if (ver.uid2 != null && ver.uid2.length >= 12) {
            long uid0 = ((long)(ver.uid2[0] & 0xFF)) | ((long)(ver.uid2[1] & 0xFF) << 8) |
                    ((long)(ver.uid2[2] & 0xFF) << 16) | ((long)(ver.uid2[3] & 0xFF) << 24);
            long uid1 = ((long)(ver.uid2[4] & 0xFF)) | ((long)(ver.uid2[5] & 0xFF) << 8) |
                    ((long)(ver.uid2[6] & 0xFF) << 16) | ((long)(ver.uid2[7] & 0xFF) << 24);
            long uid2 = ((long)(ver.uid2[8] & 0xFF)) | ((long)(ver.uid2[9] & 0xFF) << 8) |
                    ((long)(ver.uid2[10] & 0xFF) << 16) | ((long)(ver.uid2[11] & 0xFF) << 24);
            serial = String.format("%X%X%X", uid0, uid1, uid2).toUpperCase();
        }

        // 更新 drones 表的序列号和型号
        String agentId = "mavlink-" + sysId;
        if (!serial.isEmpty()) {
            execSQL("UPDATE drones SET serial_number=?, model=? WHERE agent_id=?",
                    serial, "Firmware " + firmware + " Board " + ver.board_version, agentId);
        }

        upsertTelemetry(sysId, "autopilot_version",
                String.format("{\"firmware\":\"%s\",\"serial\":\"%s\",\"board_version\":%d}",
                        firmware, serial, ver.board_version));
    }

    // ---------- MISSION_CURRENT (ID=42) ----------
    private static void handleMissionCurrent(int sysId, msg_mission_current mc) {
        upsertTelemetry(sysId, "mission_current",
                String.format("{\"seq\":%d}", mc.seq));
        redisSet("mavlink:drone:" + sysId + ":mission_current",
                String.format("{\"seq\":%d,\"ts\":%d}", mc.seq, System.currentTimeMillis()), 15);
    }

    // ========== Redis 实时缓存 ==========

    private static void cacheToRedis(int sysId, int msgId, MAVLinkMessage msg) {
        // 更新在线状态
        redisSet("mavlink:drone:" + sysId + ":online", "1", 15);
        // 更新最后活跃时间
        redisSet("mavlink:drone:" + sysId + ":last_seen", String.valueOf(System.currentTimeMillis()), 60);
    }

    // ========== 数据库操作 ==========

    private static void ensureTable() {
        // 创建 mavlink_telemetry 表
        execSQL("CREATE TABLE IF NOT EXISTS mavlink_telemetry (" +
                "id BIGINT AUTO_INCREMENT PRIMARY KEY," +
                "sys_id INT NOT NULL," +
                "msg_type VARCHAR(64) NOT NULL," +
                "payload JSON," +
                "updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP," +
                "created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP," +
                "UNIQUE KEY uk_sys_msg (sys_id, msg_type)," +
                "INDEX idx_telemetry_time (updated_at)" +
                ") ENGINE=InnoDB DEFAULT CHARSET=utf8mb4");

        // 创建 mavlink_message_log 表 (用于 STATUSTEXT 等重要日志)
        execSQL("CREATE TABLE IF NOT EXISTS mavlink_message_log (" +
                "id BIGINT AUTO_INCREMENT PRIMARY KEY," +
                "sys_id INT NOT NULL," +
                "msg_type VARCHAR(64) NOT NULL," +
                "severity VARCHAR(32) DEFAULT ''," +
                "message TEXT," +
                "created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP," +
                "INDEX idx_mavlog_sys (sys_id)," +
                "INDEX idx_mavlog_time (created_at)" +
                ") ENGINE=InnoDB DEFAULT CHARSET=utf8mb4");

        log("Database tables ensured");
    }

    private static void upsertTelemetry(int sysId, String msgType, String payload) {
        execSQL("INSERT INTO mavlink_telemetry(sys_id, msg_type, payload, updated_at) VALUES(?,?,?,NOW()) " +
                "ON DUPLICATE KEY UPDATE payload=VALUES(payload), updated_at=NOW()", sysId, msgType, payload);
    }

    private static Connection getDB() {
        try {
            if (dbConn == null || dbConn.isClosed()) {
                Class.forName("com.mysql.cj.jdbc.Driver");
                dbConn = DriverManager.getConnection(MYSQL_URL, MYSQL_USER, MYSQL_PASS);
                dbConn.setAutoCommit(true);
                log("MySQL connected: " + MYSQL_URL);
            }
            return dbConn;
        } catch (Exception e) {
            log("MySQL connect error: " + e.getMessage());
            return null;
        }
    }

    private static void execSQL(String sql, Object... params) {
        try {
            Connection conn = getDB();
            if (conn == null) return;
            try (PreparedStatement ps = conn.prepareStatement(sql)) {
                for (int i = 0; i < params.length; i++) {
                    ps.setObject(i + 1, params[i]);
                }
                ps.executeUpdate();
            }
        } catch (Exception e) {
            log("SQL error: " + e.getMessage() + " | SQL: " + sql);
        }
    }

    private static int execUpdate(String sql, Object... params) {
        try {
            Connection conn = getDB();
            if (conn == null) return 0;
            try (PreparedStatement ps = conn.prepareStatement(sql)) {
                for (int i = 0; i < params.length; i++) {
                    ps.setObject(i + 1, params[i]);
                }
                return ps.executeUpdate();
            }
        } catch (Exception e) {
            log("SQL error: " + e.getMessage());
            return 0;
        }
    }

    private static void closeDB() {
        try { if (dbConn != null) dbConn.close(); } catch (Exception ignored) {}
    }

    // ========== Redis 操作（RESP 协议直连，无需额外依赖）==========

    private static synchronized void redisSet(String key, String value, int expireSeconds) {
        if (REDIS_HOST.isEmpty()) return;
        try {
            ensureRedis();
            if (redisOut == null) return;

            // SETEX key seconds value
            String cmd = "*4\r\n$5\r\nSETEX\r\n$" + key.length() + "\r\n" + key +
                    "\r\n$" + String.valueOf(expireSeconds).length() + "\r\n" + expireSeconds +
                    "\r\n$" + value.getBytes("UTF-8").length + "\r\n" + value + "\r\n";
            redisOut.write(cmd.getBytes("UTF-8"));
            redisOut.flush();

            // 读取响应（非阻塞消费）
            if (redisIn.available() > 0) {
                byte[] buf = new byte[redisIn.available()];
                redisIn.read(buf);
            }
        } catch (Exception e) {
            // 连接断开，重置
            closeRedis();
        }
    }

    private static void ensureRedis() {
        if (REDIS_HOST.isEmpty()) return;
        try {
            if (redisSocket == null || redisSocket.isClosed()) {
                redisSocket = new Socket(REDIS_HOST, REDIS_PORT);
                redisSocket.setSoTimeout(1000);
                redisOut = redisSocket.getOutputStream();
                redisIn = redisSocket.getInputStream();

                // AUTH if needed
                if (!REDIS_PASS.isEmpty()) {
                    String auth = "*2\r\n$4\r\nAUTH\r\n$" + REDIS_PASS.length() + "\r\n" + REDIS_PASS + "\r\n";
                    redisOut.write(auth.getBytes());
                    redisOut.flush();
                    Thread.sleep(100);
                    if (redisIn.available() > 0) {
                        byte[] buf = new byte[redisIn.available()];
                        redisIn.read(buf);
                    }
                }
                log("Redis connected: " + REDIS_HOST + ":" + REDIS_PORT);
            }
        } catch (Exception e) {
            log("Redis connect error: " + e.getMessage());
            closeRedis();
        }
    }

    private static void closeRedis() {
        try { if (redisSocket != null) redisSocket.close(); } catch (Exception ignored) {}
        redisSocket = null;
        redisOut = null;
        redisIn = null;
    }

    // ========== 辅助方法 ==========

    private static String decodeFlightMode(int mainMode, int subMode) {
        switch (mainMode) {
            case 2: return "定高模式";
            case 3:
                switch (subMode) {
                    case 0: return "定点模式";
                    case 2: return "定点避障模式";
                    default: return "定点模式";
                }
            case 4:
                switch (subMode) {
                    case 2: return "自动起飞";
                    case 3: return "自动跟踪";
                    case 4: return "自动任务";
                    case 5: return "自动返航";
                    case 6: return "自动降落";
                    default: return "自动模式";
                }
            case 6: return "Offboard模式";
            default: return "未知模式(" + mainMode + ")";
        }
    }

    private static String decodeFixType(int fixType) {
        switch (fixType) {
            case 0: case 1: return "无定位";
            case 2: return "2D定位";
            case 3: return "3D定位";
            case 4: return "3D-DGPS";
            case 5: return "RTK浮动解";
            case 6: return "RTK固定解";
            case 7: return "静态固定解";
            default: return "未知(" + fixType + ")";
        }
    }

    private static String decodeSeverity(int severity) {
        switch (severity) {
            case 0: return "emergency";
            case 1: return "alert";
            case 2: return "critical";
            case 3: return "error";
            case 4: return "warning";
            case 5: return "notice";
            case 6: return "info";
            case 7: return "debug";
            default: return "unknown";
        }
    }

    private static void loadConfig() {
        TCP_PORT = intEnv("MAVLINK_TCP_PORT", intEnv("SC_TCP_PORT", 9080));
        MYSQL_URL = env("MAVLINK_MYSQL_URL", buildMysqlUrl());
        MYSQL_USER = env("MYSQL_USER", "root");
        MYSQL_PASS = env("MYSQL_PASSWORD", "");
        REDIS_HOST = env("REDIS_HOST", "");
        REDIS_PORT = intEnv("REDIS_PORT", 6379);
        REDIS_PASS = env("REDIS_PASSWORD", "");
    }

    private static String buildMysqlUrl() {
        String host = env("MYSQL_HOST", "127.0.0.1");
        String port = env("MYSQL_PORT", "3306");
        String db = env("MYSQL_DATABASE", "defaultdb");
        return "jdbc:mysql://" + host + ":" + port + "/" + db +
                "?useSSL=false&serverTimezone=Asia/Shanghai&characterEncoding=utf8mb4&allowPublicKeyRetrieval=true";
    }

    private static String env(String key, String def) {
        String v = System.getenv(key);
        return (v != null && !v.isEmpty()) ? v : def;
    }

    private static int intEnv(String key, int def) {
        try { return Integer.parseInt(System.getenv(key)); }
        catch (Exception e) { return def; }
    }

    private static void log(String msg) {
        System.out.printf("[%tF %<tT] [MavlinkBridge] %s%n", new java.util.Date(), msg);
    }
}
