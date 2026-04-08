
# CloudControl

<div align="center">

**企业级智能管控平台 | 实时监控 | 远程控制 | 性能分析**

基于 Go 1.24 + Gin + SQLite + WebSocket + noVNC 的完整解决方案

[![Go Version](https://img.shields.io/badge/Go-1.24+-00ADD8?style=flat&logo=go)](https://go.dev/)
[![License](https://img.shields.io/badge/License-MIT-green.svg)](LICENSE)

</div>

---

## 📋 目录

- [项目简介](#项目简介)
- [运行与停止速查](#运行与停止速查)
- [快速开始](#快速开始)
- [系统访问地址](#系统访问地址)
- [环境变量配置](#环境变量配置)
- [17大功能模块](#17大功能模块)
- [API接口文档](#api接口文档)
- [部署指南](#部署指南)
- [停止运行](#停止运行)
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

## ⚡ 运行与停止速查

以下命令基于仓库当前结构整理，默认都在 `project/` 目录执行。

### 本地开发运行

```bash
cd project
go mod tidy
go run .
```

启动成功后访问：

- `http://127.0.0.1:8080/`
- `http://127.0.0.1:8080/api/healthz`

预期健康检查返回：

```json
{"status":"ok"}
```

### Windows 后台运行

源码直接后台运行：

```powershell
Start-Process -FilePath "go" -ArgumentList "run","." -WorkingDirectory (Get-Location).Path -RedirectStandardOutput "run.out.log" -RedirectStandardError "run.err.log"
```

编译后后台运行：

```powershell
go build -o sc.exe .
Start-Process -FilePath ".\sc.exe" -WorkingDirectory (Get-Location).Path
```

### 停止运行

前台运行时，在当前终端按 `Ctrl + C`。

Windows 后台运行时，优先按端口查询 PID 再停止：

```powershell
Get-NetTCPConnection -LocalPort 8080 | Select-Object OwningProcess
Stop-Process -Id <PID> -Force
```

如果是编译后的 `sc.exe`，也可以直接按进程名停止：

```powershell
Get-Process sc | Stop-Process -Force
```

---

## 🚀 快速开始

### 1. 环境准备

```bash
cd project
go version
go mod tidy
```

建议使用 Go 1.24 及以上版本。

### 2. 配置环境变量

项目启动时会自动读取当前工作目录下的 `.env` 文件；如果没有该文件，则使用系统环境变量和内置默认值。

常用变量如下：

- `SC_LISTEN_ADDR`：Web 服务监听地址，默认 `:8080`
- `SC_DB_PATH`：SQLite 数据库路径，默认 `app.db`
- `SC_AGENT_PORT`：内嵌硬件 Agent 端口，默认 `9100`
- `SC_API_TOKEN`：API 认证 Token，留空表示关闭
- `LLM_API_KEY`、`LLM_BASE_URL`、`LLM_MODEL`：智能规划相关配置
- `AMAP_KEY`：高德地图 Key

示例 `.env`：

```env
SC_LISTEN_ADDR=:8080
SC_DB_PATH=app.db
SC_AGENT_PORT=9100
SC_API_TOKEN=
LLM_API_KEY=
LLM_BASE_URL=https://dashscope.aliyuncs.com/compatible-mode/v1
LLM_MODEL=qwen-plus
AMAP_KEY=
```

### 3. 启动服务

开发模式（推荐用于本地调试）：

```bash
go run .
```

如果需要后台运行并输出日志，可以使用：

```powershell
Start-Process -FilePath "go" -ArgumentList "run","." -WorkingDirectory (Get-Location).Path -RedirectStandardOutput "run.out.log" -RedirectStandardError "run.err.log"
```

编译后运行（推荐用于部署）：

```bash
# Windows
go build -o sc.exe .
.\sc.exe

# Linux / macOS
go build -o sc .
./sc
```

Windows 后台运行示例：

```powershell
Start-Process -FilePath ".\sc.exe" -WorkingDirectory (Get-Location).Path
```

Linux 后台运行示例：

```bash
nohup ./sc > run.out.log 2> run.err.log &
```

### 4. 访问系统与运行验证

浏览器打开: `http://127.0.0.1:8080`

启动成功标志：
```
listening on :8080
```

健康检查：

```bash
curl http://127.0.0.1:8080/api/healthz
```

预期返回：

```json
{"status":"ok"}
```

说明：

- 主 Web 服务默认监听 `8080`
- 内嵌硬件 Agent 默认监听 `9100`
- 项目默认以当前工作目录作为运行根目录，因此建议始终在 `project/` 目录内执行启动命令
- 前端静态资源已经通过 `embed` 打进二进制，部署时不需要单独拷贝 `web/` 目录
- 音频上传目录会在首次上传时自动创建为 `data/recordings/`

### 5. 首次使用

1. 访问注册页面创建账号
2. 登录后进入仪表盘
3. 左侧导航切换17大功能模块

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
| 4 | 飞行任务管理 | 任务创建/编辑/删除、编辑信息自动预填（含起点/终点回填）、飞行阶段状态机、任务日志、导入导出、WebSocket 实时推送、AI 智能分析 |
| 5 | 禁飞区管理 | 圆形/多边形禁飞区创建/编辑/删除、地图可视化、智能规划自动绕行 |
| 6 | 系统状态监控 | 主服务器 CPU、内存、磁盘、网络实时监控（WebSocket） |
| 7 | 硬件状态检测 | 远程设备硬件指标采集（Agent 模式）、自动刷新、导出 CSV、AI 智能诊断 |
| 8 | 远程桌面控制 | VNC/SSH/RDP 三种协议远程连接（浏览器内 VNC + SSH） |
| 9 | 视频监控 | 无人机视频流统一查看和播放（数据来源于无人机注册表） |
| 10 | 语音交互记录 | 音频文件上传/下载/播放/删除、交互统计 |
| 11 | 异常报警 | 报警记录管理、导入导出、类型/优先级统计 |
| 12 | 维护操作日志 | 操作审计日志、导入导出、类型/结果统计 |
| 13 | 软件更新管理 | 更新发布/编辑/删除、自动/强制更新、导入导出 |
| 14 | 数据同步状态 | 多设备间数据库同步（全量/增量）、独立任务管理 |
| 15 | 性能分析报告 | 响应时间/吞吐量/错误率多维度分析、四种图表 |
| 16 | AI决策记录 | 思维链（CoT）推理记录查看、历史分析归档、多场景（飞行规划/故障诊断/报警分析/电池分析） |
| 17 | 无人机连接与部署 | hw-agent 地面站部署、MAVLink中继、Push模式、跨网络方案 |

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

## 📦 17大功能模块

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

**功能**: 任务创建/编辑/删除、编辑信息自动预填并保留（支持仅修改指定字段）、起点/终点可从路线文本自动回填并地图标注、飞行阶段状态机（待命→起飞→巡航→执行任务→返航→降落）、任务日志、WebSocket 事件驱动实时推送、智能航线规划（LLM + 降级规划）、AI 智能分析（任务详情直接展示创建任务时保存的完整思维链）

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

**功能**: 远程设备硬件指标采集（hw-agent Pull/Push 模式）、自动刷新、导出 CSV、AI 智能诊断（一键触发故障分析）

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
| AI分析 | `POST /api/cot/analyze` | 统一 AI 分析入口（思维链推理） |
| AI分析 | `GET /api/cot/chains` | 思维链记录列表 |
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

### 单机部署（Windows / Linux）

推荐用于课程展示、内网服务器或单机环境。部署时建议优先使用编译后的二进制，而不是长期使用 `go run .`。

```bash
# 进入项目目录
cd project

# 下载依赖
go mod tidy

# Windows 构建
go build -o sc.exe .

# Linux 构建
go build -o sc .
```

部署时至少保留以下内容：

- 可执行文件 `sc.exe` 或 `sc`
- `.env`（如果你使用文件方式管理环境变量）
- 数据库文件 `app.db`（已有数据时）
- `data/` 目录（已有录音文件时）

说明：

- 程序默认会在当前工作目录下读写 `app.db`、`.env` 和 `data/`
- 首次上传录音时会自动创建 `data/recordings/`
- 静态页面已嵌入二进制，不需要额外部署 `web/`
- 因此部署目录应保持为一个独立工作目录，并从该目录启动程序

### Windows 部署命令

前台运行：

```powershell
.\sc.exe
```

后台运行：

```powershell
Start-Process -FilePath ".\sc.exe" -WorkingDirectory (Get-Location).Path
```

### Linux / 服务器部署命令

前台运行：

```bash
./sc
```

后台运行：

```bash
nohup ./sc > run.out.log 2> run.err.log &
```

### 交叉编译 Linux

```powershell
$env:GOOS="linux"
$env:GOARCH="amd64"
go build -o sc .
```

### systemd 服务（Linux 可选）

创建 `/etc/systemd/system/smartcontrol.service`：

```ini
[Unit]
Description=CloudControl
After=network.target

[Service]
Type=simple
ExecStart=/opt/smartcontrol/sc
WorkingDirectory=/opt/smartcontrol
Restart=on-failure
Environment=SC_LISTEN_ADDR=:8080

[Install]
WantedBy=multi-user.target
```

启动：
```bash
sudo systemctl enable smartcontrol
sudo systemctl start smartcontrol
sudo systemctl status smartcontrol
```

### Docker 说明

当前仓库未提供 `Dockerfile` 或 `docker-compose.yml`，因此 README 不再给出不可直接执行的 Docker 命令。

---

## 🛑 停止运行

### 前台运行时停止

直接在启动该进程的终端按 `Ctrl + C`。

### Windows 后台运行时停止（通用方法）

无论你是通过 `go run .` 还是 `sc.exe` 启动，都可以先按端口定位进程：

```powershell
Get-NetTCPConnection -LocalPort 8080 | Select-Object OwningProcess
Stop-Process -Id <PID> -Force
```

### Windows 后台运行时停止（编译产物）

按进程名停止：

```powershell
Get-Process sc | Stop-Process -Force
```

### Windows 后台运行时停止（源码运行）

如果你是通过 `go run .` 后台启动，实际进程通常是 `smartcontrol`，也可以直接停止：

```powershell
Get-Process smartcontrol | Stop-Process -Force
```

### Linux 后台运行时停止

按进程名停止：

```bash
pkill -f "./sc"
```

按端口定位后停止：

```bash
lsof -i :8080
kill <PID>
```

### systemd 停止

```bash
sudo systemctl stop smartcontrol
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
  - 右键点击 `sc.exe` 或 `agent.exe` → "以管理员身份运行"
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
```powershell
$env:SC_LISTEN_ADDR=":9000"
go run .
```

**Q: 忘记密码怎么办？**
```bash
rm app.db  # 删除数据库重新注册
```

**Q: 如何启用 API 认证？**
```powershell
$env:SC_API_TOKEN="your-token"
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
│   └── agent/main.go               # hw-agent 独立程序（部署在地面站电脑上，MAVLink中继：读取飞控数据→推送云端后端）
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
│   │   ├── cot.go                  # 思维链 AI 分析 API（统一分析入口 + CRUD）
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
│       ├── noflyzone.html          # 禁飞区管理
│       ├── cot.html                # AI决策记录（思维链推理历史）
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

### v3.0.0 (最新)

**新增**:
- ✅ 禁飞区管理模块（圆形/多边形，地图可视化，传递给 LLM 规划）
- ✅ AI决策记录页面（cot.html，独立导航入口，思维链历史归档）
- ✅ NFZ 几何纠偏引擎（可视图 + Dijkstra，确保航线零容忍穿越禁飞区）
- ✅ 多候选方案生成（最多3条备选航线，地图并排预览+对比选择）
- ✅ 案例检索增强（CBR，自动注入历史相似任务案例到 LLM 提示）
- ✅ 降级规划也应用 NFZ 纠偏（直线规划同样绕开禁飞区）
- ✅ 任务名称实时重名检测（输入即提示，提交前二次校验）
- ✅ 地面站中继架构明确化（hw-agent 部署在地面站电脑，通过 MAVLink 读取飞控数据）

### v2.0.0

**新增**:
- ✅ 无人机统一管理模块（注册/编辑/删除/连接）
- ✅ GPS/位置信息模块（Leaflet 地图、电子围栏、历史轨迹）
- ✅ 电池监控模块（自动报警、历史趋势、WebSocket 实时推送）
- ✅ 飞行任务管理模块（飞行阶段状态机、任务日志、WebSocket 实时推送）
- ✅ hw-agent Push 模式（自动推送硬件+GPS+电池数据）
- ✅ SSH 浏览器内终端（xterm.js）
- ✅ RDP 连接支持（生成 .rdp 文件）
- ✅ 数据同步引擎（全量/增量同步，18 张表）
- ✅ 视频监控与无人机注册表联动
- ✅ 智能航线规划（LLM 大模型 + 降级直线规划）
- ✅ 高德地图地理编码/逆编码集成
- ✅ 思维链（CoT）AI 分析（内联集成到飞行/硬件/报警/电池模块）

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
