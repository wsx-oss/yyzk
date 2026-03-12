
# CloudControl

<div align="center">

**企业级智能管控平台 | 实时监控 | 远程控制 | 性能分析**

基于 Go 1.20 + Gin + SQLite + WebSocket + noVNC 的完整解决方案

[![Go Version](https://img.shields.io/badge/Go-1.20+-00ADD8?style=flat&logo=go)](https://go.dev/)
[![License](https://img.shields.io/badge/License-MIT-green.svg)](LICENSE)

</div>

---

## 📋 目录

- [项目简介](#项目简介)
- [快速开始](#快速开始)
- [系统访问地址](#系统访问地址)
- [环境变量配置](#环境变量配置)
- [15大功能模块](#15大功能模块)
- [API接口文档](#api接口文档)
- [部署指南](#部署指南)
- [故障排查](#故障排查)

---

## 🎯 项目简介

CloudControl是一套面向企业级应用的综合性管控平台。

### 核心特性

**🔐 安全认证**
- 用户注册/登录系统（bcrypt 密码加密）
- 会话管理（Token 持久化，24小时过期）
- API Token 认证保护

**🚀 性能优化**
- 数据库索引优化（查询速度提升 50-80%）
- 分页支持（所有列表接口）
- 请求限流（每 IP 每分钟 500 次）

**🛡️ 安全防护**
- CORS 跨域支持
- SQL 注入防护
- 输入验证和清理
- Panic 自动恢复

**📊 实时监控**
- CPU、内存、磁盘、网络监控
- WebSocket 实时推送
- 阈值自动告警

---

## 🚀 快速开始

### 1. 安装依赖

```bash
cd project
go mod tidy
```

### 1.1 环境变量文件（团队协作推荐）

- 仓库提供：`project/.env.example`（可提交）
- 你本地运行时使用：`project/.env`（已被 `.gitignore` 忽略，不会提交到 GitHub）

首次运行建议：

```bash
# 在 project/ 目录下
cp .env.example .env
```

然后按需填写：`SC_API_TOKEN`、`LLM_API_KEY`、`AMAP_KEY` 等。

### 2. 启动服务

```bash
# 开发模式
go run .

# 或编译后运行
go build -o sc.exe .
./sc.exe
```

### 3. 访问系统

浏览器打开: `http://127.0.0.1:8080`

启动成功标志：
```
listening on :8080
```

### 4. 首次使用

1. 访问注册页面创建账号
2. 登录后进入仪表盘
3. 左侧导航切换15大功能模块

---

## 🌐 系统访问地址

### 主要页面

| 地址 | 说明 | 登录要求 |
|------|------|---------|
| `http://127.0.0.1:8080/` | 系统入口（自动跳转首页）| ❌ |
| `http://127.0.0.1:8080/app/index.html` | 系统首页 | ❌ |
| `http://127.0.0.1:8080/app/register.html` | 用户注册（用户名≥3位，密码≥6位）| ❌ |
| `http://127.0.0.1:8080/app/login.html` | 用户登录 | ❌ |
| `http://127.0.0.1:8080/app/dashboard.html` | 控制仪表盘（主界面）| ✅ |
| `http://127.0.0.1:8080/app/vnc.html` | VNC 远程桌面（独立页面）| ✅ |
| `http://127.0.0.1:8080/app/ssh.html` | SSH 终端（独立页面）| ✅ |

### 功能模块（在仪表盘内）

所有功能都通过仪表盘左侧导航栏访问：

| # | 模块名称 | 功能说明 |
|---|----------|----------|
| 1 | 无人机管理 | 无人机注册、编辑、连接（SSH/VNC/RDP）、状态监控、视频流配置 |
| 2 | GPS/位置信息 | 无人机实时位置、地图可视化、电子围栏、历史轨迹 |
| 3 | 电池监控 | 电量/电压/温度/健康度监控、自动报警、历史趋势、WebSocket 实时推送 |
| 4 | 飞行任务管理 | 任务创建/编辑/删除、飞行阶段状态机、任务日志、导入导出、WebSocket 实时推送 |
| 5 | 系统状态监控 | 主服务器 CPU、内存、磁盘、网络实时监控（WebSocket） |
| 6 | 硬件状态检测 | 远程设备硬件指标采集（Agent 模式）、自动刷新、导出 CSV |
| 7 | 远程桌面控制 | VNC/SSH/RDP 三种协议远程连接（浏览器内 VNC + SSH） |
| 8 | 视频监控 | 无人机视频流统一查看和播放（数据来源于无人机注册表） |
| 9 | 语音交互记录 | 音频文件上传/下载/播放/删除、交互统计 |
| 10 | 异常报警 | 报警记录管理、导入导出、类型/优先级统计 |
| 11 | 维护操作日志 | 操作审计日志、导入导出、类型/结果统计 |
| 12 | 软件更新管理 | 更新发布/编辑/删除、自动/强制更新、导入导出 |
| 13 | 数据同步状态 | 多设备间数据库同步（全量/增量）、独立任务管理 |
| 14 | 性能分析报告 | 响应时间/吞吐量/错误率多维度分析、四种图表 |
| 15 | 无人机连接与部署 | hw-agent 部署、Push/Pull 模式、跨网络方案 |

### API 端点

| 端点 | 说明 |
|------|------|
| `http://127.0.0.1:8080/api/healthz` | 健康检查 |
| `ws://127.0.0.1:8080/api/metrics/stream` | WebSocket 实时监控流 |
| `ws://127.0.0.1:8080/api/vnc/ws?target=IP:Port` | VNC WebSocket 代理 |
| `ws://127.0.0.1:8080/api/ssh/ws` | SSH WebSocket 代理 |

---

## ⚙️ 环境变量配置

### 基础配置

| 变量名 | 默认值 | 说明 |
|--------|--------|------|
| `SC_LISTEN_ADDR` | `:8080` | 监听地址和端口 |
| `SC_DB_PATH` | `app.db` | 数据库文件路径 |
| `SC_API_TOKEN` | `""` | API认证Token（空=关闭）|
| `SC_MAX_UPLOAD_MB` | `64` | 文件上传限制(MB) |
| `SC_TRUSTED_PROXIES` | `127.0.0.1` | 受信任的代理 IP（多个用英文逗号分隔） |
| `SC_AGENT_PORT` | `9100` | 内嵌硬件监控 Agent 端口 |

### 告警配置

| 变量名 | 默认值 | 说明 |
|--------|--------|------|
| `SC_THRESH_CPU` | `85` | CPU告警阈值(%) |
| `SC_THRESH_MEM` | `85` | 内存告警阈值(%) |
| `SC_THRESH_DISK` | `90` | 磁盘告警阈值(%) |
| `SC_ALERT_INTERVAL_SEC` | `10` | 检测间隔(秒) |

### 智能规划 / 地图（可选）

| 变量名 | 默认值 | 说明 |
|--------|--------|------|
| `LLM_API_KEY` | `""` | 大模型 API Key（留空将自动降级为直线规划） |
| `LLM_BASE_URL` | `https://dashscope.aliyuncs.com/compatible-mode/v1` | 大模型 API Base URL |
| `LLM_MODEL` | `qwen-plus` | 大模型名称 |
| `AMAP_KEY` | `""` | 高德地图 Key（用于地址解析/逆地理编码） |

### 配置示例

**Windows**:
```powershell
$env:SC_LISTEN_ADDR=":9000"
$env:SC_API_TOKEN="my-token"
go run .
```

**Linux/macOS**:
```bash
export SC_LISTEN_ADDR=":9000"
export SC_API_TOKEN="my-token"
go run .
```

---

## 📦 15大功能模块

### 1. 无人机管理

**功能**: 无人机注册、编辑、删除、连接（SSH/VNC/RDP）、状态监控、视频流配置。注册无人机时自动创建关联的远程设备和 GPS 设备。

**API**:
- `GET /api/drones` - 无人机列表
- `POST /api/drones` - 注册无人机
- `PUT /api/drones/:id` - 编辑无人机
- `DELETE /api/drones/:id` - 删除无人机（级联删除关联数据）
- `GET /api/drones/stats` - 无人机统计

### 2. GPS/位置信息

**功能**: 无人机实时位置、Leaflet 地图可视化、电子围栏、历史轨迹、Agent 自动推送、WebSocket 实时事件

**API**:
- `GET /api/gps/devices` - GPS 设备列表
- `POST /api/gps/devices/:id/push` - 手动推送位置
- `POST /api/gps/push` - Agent 自动推送
- `GET /api/gps/devices/:id/history` - 历史轨迹
- `GET /api/gps/fence-alerts` - 围栏报警列表
- `WS /api/gps/stream` - 实时事件推送（gps_update）

**在线/离线**：GPS 设备超过 60 秒未上报会被后台标记为离线，同时关联无人机状态会被置为 `offline`。

### 3. 电池监控

**功能**: 电量/电压/电流/温度/健康度监控，自动报警，历史趋势图表，WebSocket 事件驱动实时推送

**API**:
- `GET /api/battery/latest` - 最新电池状态
- `POST /api/battery/report` - 上报电池数据
- `POST /api/battery/push` - Agent 自动推送
- `GET /api/battery/history/:device_id` - 历史记录
- `GET /api/battery/alerts` - 电池报警列表
- `WS /api/battery/stream` - 实时事件推送（数据变化时即时通知）

### 4. 飞行任务管理

**功能**: 任务创建/编辑/删除、飞行阶段状态机（待命→起飞→巡航→执行任务→返航→降落）、任务日志、WebSocket 事件驱动实时推送、智能航线规划（LLM + 降级规划）

**API**:
- `GET /api/flight/missions` - 任务列表
- `POST /api/flight/missions` - 创建任务
- `POST /api/flight/missions/:id/phase` - 更新飞行阶段
- `POST /api/flight/missions/import` - 批量导入
- `WS /api/flight/stream` - 实时事件推送（任务变更时即时通知）
- `POST /api/flight/missions/plan` - 创建智能航线规划（LLM/降级）
- `GET /api/flight/missions/plans` - 规划列表
- `POST /api/flight/missions/plan/:id/adopt` - 采纳规划→创建任务
- `POST /api/flight/missions/plan/:id/discard` - 丢弃规划
- `GET /api/flight/missions/plan/status` - LLM/AMap 状态检查
- `POST /api/amap/geocode` - 地址→坐标（高德地图）
- `POST /api/amap/regeocode` - 坐标→地址（高德地图）

### 5. 系统状态监控

**功能**: 主服务器 CPU、内存、磁盘、网络实时监控（WebSocket 每秒推送）

**API**:
- `GET /api/metrics/snapshot` - 获取快照
- `WS /api/metrics/stream` - 实时数据流

### 6. 硬件状态检测

**功能**: 远程设备硬件指标采集（hw-agent Pull/Push 模式）、自动刷新、导出 CSV

**API**:
- `GET /api/hardware/items` - 硬件列表
- `POST /api/hardware/items` - 添加硬件（自动检测 Agent）
- `POST /api/hardware/items/refresh` - 批量刷新
- `POST /api/hardware/push` - Agent 推送硬件数据

### 7. 远程桌面控制

**功能**: VNC/SSH/RDP 三种协议远程连接，VNC 和 SSH 在浏览器内完成，RDP 生成 .rdp 文件

**API**:
- `GET /api/devices` - 设备列表
- `POST /api/devices` - 新增设备
- `POST /api/devices/:id/status` - 设置状态
- `WS /api/vnc/ws` - VNC 代理
- `WS /api/ssh/ws` - SSH 代理

### 8. 视频监控

**功能**: 无人机视频流统一查看和播放，数据来源于无人机注册表的 `video_url` 字段

**API**: 复用 `GET /api/drones` 接口

### 9. 语音交互记录

**功能**: 音频文件上传/下载/播放/删除、交互统计

**API**:
- `POST /api/audio/upload` - 上传
- `GET /api/audio/list` - 列表（分页）
- `GET /api/audio/download/:id` - 下载/播放
- `DELETE /api/audio/:id` - 删除

### 10. 异常报警

**功能**: 报警记录管理、导入导出、类型/优先级统计

**API**:
- `GET /api/alerts/list` - 报警列表（分页）
- `POST /api/alerts/new` - 新增报警
- `POST /api/alerts/resolve/:id` - 标记已解决
- `GET /api/alerts/stats` - 报警统计

### 11-15. 其他模块

- **维护操作日志**: `GET /api/logs/list`、`POST /api/logs/append`、`GET /api/logs/stats`
- **软件更新管理**: `GET /api/updates/list`、`POST /api/updates/add`、`GET /api/updates/stats`
- **数据同步状态**（18 张表同步）: `GET /api/sync/tasks`、`POST /api/sync/tasks/:id/start`、`POST /api/sync/tasks/:id/stop`
- **性能分析报告**: `GET /api/report/perf-list`、`GET /api/report/perf`

各模块详细功能说明请参考 `功能介绍/` 目录下的对应文档。

---

## 📡 API接口文档

### 认证接口

| 方法 | 端点 | 说明 |
|------|------|------|
| POST | `/api/auth/register` | 用户注册 |
| POST | `/api/auth/login` | 用户登录 |
| POST | `/api/auth/logout` | 用户登出 |
| GET | `/api/auth/validate` | 验证Token |

**注册示例**:
```bash
curl -X POST http://127.0.0.1:8080/api/auth/register \
  -H "Content-Type: application/json" \
  -d '{"username":"admin","password":"password123"}'
```

**登录示例**:
```bash
curl -X POST http://127.0.0.1:8080/api/auth/login \
  -H "Content-Type: application/json" \
  -d '{"username":"admin","password":"password123"}'
```

### 监控接口

| 方法 | 端点 | 说明 |
|------|------|------|
| GET | `/api/healthz` | 健康检查 |
| GET | `/api/metrics/snapshot` | 系统指标快照 |
| WS | `/api/metrics/stream` | 实时监控流 |

### 其他接口

| 分类 | 端点 | 说明 |
|------|------|------|
| 无人机 | `GET /api/drones` | 无人机列表（含 video_url） |
| 无人机 | `POST /api/drones` | 注册无人机 |
| 无人机 | `GET /api/drones/stats` | 无人机统计 |
| GPS | `GET /api/gps/devices` | GPS 设备列表 |
| GPS | `POST /api/gps/push` | Agent GPS 推送 |
| 电池 | `GET /api/battery/latest` | 最新电池状态 |
| 电池 | `POST /api/battery/push` | Agent 电池推送 |
| 飞行 | `GET /api/flight/missions` | 任务列表 |
| 飞行规划 | `POST /api/flight/missions/plan` | 智能航线规划（LLM） |
| 硬件 | `GET /api/hardware/items` | 硬件列表 |
| 硬件 | `POST /api/hardware/push` | Agent 硬件推送 |
| 设备 | `GET /api/devices` | 远程设备列表 |
| 音频 | `GET /api/audio/list` | 列表（支持分页）|
| 告警 | `GET /api/alerts/list` | 列表（支持分页）|
| 日志 | `GET /api/logs/list` | 列表（支持分页）|
| 同步 | `GET /api/sync/tasks` | 同步任务列表 |

### 分页参数

| 参数 | 默认值 | 说明 |
|------|--------|------|
| `page` | 1 | 页码 |
| `page_size` | 50 | 每页数量（最大200）|

**分页示例**:
```bash
curl "http://127.0.0.1:8080/api/alerts/list?page=2&page_size=20"
```

---

## 🚀 部署指南

### 编译

```bash
# 当前平台
go build -o sc.exe .

# 交叉编译 Linux
$env:GOOS="linux"
go build -o sc .
```

### Docker 部署

```bash
docker build -t smartcontrol .
docker run -d -p 8080:8080 smartcontrol
```

### systemd 服务

创建 `/etc/systemd/system/smartcontrol.service`:

```ini
[Unit]
Description=Smart Control System
After=network.target

[Service]
Type=simple
ExecStart=/opt/smartcontrol/sc
WorkingDirectory=/opt/smartcontrol
Restart=on-failure

[Install]
WantedBy=multi-user.target
```

启动：
```bash
sudo systemctl enable smartcontrol
sudo systemctl start smartcontrol
```

---

## 🛠 故障排查

### 1. 端口被占用

```bash
# Windows
netstat -ano | findstr :8080
taskkill /PID <进程ID> /F

# Linux
lsof -i :8080
kill -9 <进程ID>
```

### 2. VNC 连接失败

**检查清单**:
- ✅ 目标机器已安装 VNC 服务
- ✅ VNC 服务已启动
- ✅ 防火墙已放行 5900 端口
- ✅ 网络连通性正常

### 3. 硬件温度显示为 0℃

**原因**: Windows 系统下读取 CPU 温度需要特殊权限或第三方工具支持。

**解决方案（任选其一）**:

- **方案一：以管理员权限运行**（推荐）
  - 右键点击 `smartcontrol.exe` 或 `hw-agent.exe` → "以管理员身份运行"
  - 管理员权限下 WMI `MSAcpi_ThermalZoneTemperature` 接口可正常返回温度数据

- **方案二：安装 LibreHardwareMonitor**
  - 下载 [LibreHardwareMonitor](https://github.com/LibreHardwareMonitor/LibreHardwareMonitor)
  - 以管理员身份运行 LibreHardwareMonitor，保持后台运行
  - Agent 会自动通过其 WMI 接口读取温度（无需主程序管理员权限）

- **方案三：安装 OpenHardwareMonitor**
  - 与 LibreHardwareMonitor 类似，Agent 同样支持

**温度采集优先级**:
1. gopsutil SensorsTemperatures（Linux 原生支持）
2. WMI MSAcpi_ThermalZoneTemperature（Windows，需管理员）
3. LibreHardwareMonitor / OpenHardwareMonitor WMI 接口
4. wmic 命令行回退

### 4. 数据库锁定

```bash
# 关闭所有实例
# 删除锁文件
rm app.db-shm app.db-wal
# 重新启动
```

---

## ❓ 常见问题

**Q: 如何修改端口？**
```bash
export SC_LISTEN_ADDR=":9000"
go run .
```

**Q: 忘记密码怎么办？**
```bash
rm app.db  # 删除数据库重新注册
```

**Q: 如何启用 API 认证？**
```bash
export SC_API_TOKEN="your-token"
go run .
```

**Q: 数据存储在哪里？**
- 数据库：`app.db`
- 录音文件：`data/recordings/`

---

## 📁 项目结构

```text
project/
├── main.go                        # 主程序入口
├── go.mod / go.sum                 # Go 依赖管理
├── app.db                          # SQLite 数据库文件
├── cmd/
│   └── agent/main.go               # hw-agent 独立程序（部署在无人机/目标设备上）
├── internal/
│   ├── agent/agent.go              # 内嵌 Agent（主服务启动时自动运行本机 Agent）
│   ├── db/db.go                    # 数据库初始化与表结构定义
│   ├── handlers/                   # API 处理函数
│   │   ├── api.go                  # 通用 API（设备、硬件、报警、日志、更新、同步、性能等）
│   │   ├── drones.go               # 无人机管理 API
│   │   ├── gps.go                  # GPS/位置信息 API
│   │   ├── battery.go              # 电池监控 API
│   │   ├── flight.go               # 飞行任务管理 API
│   │   ├── flight_plan.go          # 智能航线规划 API（LLM + AMap）
│   │   └── wshub.go                # WebSocket 事件广播中心
│   ├── llm/llm.go                 # LLM 大模型调用封装（航线规划 + 降级规划器）
│   ├── amap/amap.go               # 高德地图 API 封装（地理编码/逆编码）
│   ├── middleware/                 # 中间件（认证等）
│   ├── monitor/monitor.go          # 系统指标采集
│   └── syncengine/engine.go        # 数据同步引擎（18 张表白名单）
├── web/
│   ├── index.html                  # 登录页
│   ├── dashboard.html              # 仪表盘导航
│   ├── vnc.html / ssh.html         # VNC/SSH 客户端页面
│   └── modules/                    # 15 个功能模块页面
│       ├── drones.html             # 无人机管理
│       ├── gps.html                # GPS/位置信息
│       ├── battery.html            # 电池监控
│       ├── flight.html             # 飞行任务管理
│       ├── hardware.html           # 硬件状态检测
│       ├── remote.html             # 远程桌面控制
│       ├── video.html              # 视频监控
│       ├── monitor.html            # 系统状态监控
│       ├── alerts.html             # 异常报警
│       ├── logs.html               # 维护操作日志
│       ├── audio.html              # 语音交互记录
│       ├── updates.html            # 软件更新管理
│       ├── sync.html               # 数据同步状态
│       ├── performance.html        # 性能分析报告
│       └── common.js / common.css  # 公共工具函数和样式
└── data/
    └── recordings/                 # 语音文件存储目录
```

---

## 🔄 版本更新

### v2.0.0 (最新)

**新增**:
- ✅ 无人机统一管理模块（注册/编辑/删除/连接）
- ✅ GPS/位置信息模块（Leaflet 地图、电子围栏、历史轨迹）
- ✅ 电池监控模块（自动报警、历史趋势、WebSocket 实时推送）
- ✅ 飞行任务管理模块（飞行阶段状态机、任务日志、WebSocket 实时推送）
- ✅ hw-agent Push 模式（自动推送硬件+GPS+电池数据）
- ✅ SSH 浏览器内终端（xterm.js）
- ✅ RDP 连接支持（生成 .rdp 文件）
- ✅ 数据同步引擎（全量/增量同步）18 张表）
- ✅ 视频监控与无人机注册表联动
- ✅ 智能航线规划（LLM 大模型 + 降级直线规划）
- ✅ 高德地图地理编码/逆编码集成

### v1.1.0

**新增**:
- ✅ 完整会话管理系统
- ✅ 认证接口
- ✅ 列表分页支持
- ✅ API限流保护
- ✅ CORS跨域支持

**优化**:
- ✅ 数据库索引优化
- ✅ 性能提升
- ✅ 安全性增强

---

## 📧 技术支持

- 📮 提交 Issue
- 💬 查看文档
- 🔍 搜索常见问题

---

**Made with ❤️ by Smart Control Team**
