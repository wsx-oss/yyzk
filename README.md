
# CloudControl

<div align="center">

**企业级智能管控平台 | 实时监控 | 远程控制 | 性能分析**

基于 Go 1.24 + Gin + MySQL + WebSocket + noVNC 的完整解决方案

[![Go Version](https://img.shields.io/badge/Go-1.24+-00ADD8?style=flat&logo=go)](https://go.dev/)
[![License](https://img.shields.io/badge/License-MIT-green.svg)](LICENSE)

</div>

---

## 目录

- [项目简介](#项目简介)
- [运行与停止速查](#运行与停止速查)
- [快速开始](#快速开始)
- [系统访问地址](#系统访问地址)
- [环境变量配置](#环境变量配置)
- [20大功能模块](#20大功能模块)
- [API接口文档](#api接口文档)
- [项目结构](#项目结构)
- [部署指南](#部署指南)
- [故障排查](#故障排查)
- [版本更新](#版本更新)
- [团队协作规范](#团队协作规范)

---

## 项目简介

CloudControl 是一套面向企业级应用的综合性管控平台。

**技术栈**：
- **后端**：Go + Gin，位于 `project/`，数据库已从 SQLite 迁移至 **MySQL**（Aiven 云数据库），通过环境变量 `SC_DB_DRIVER` 切换驱动，内置 SQL 方言自动适配层（`AdaptSQL`）
- **前端**：静态页面位于 `project/web/`，由后端内嵌静态资源并通过 `/app` 提供访问
- **时区**：统一为 **Asia/Shanghai (UTC+8)**，Go 后端 `time.Local` 与 MySQL 会话时区均已配置

### 核心特性

- **安全认证**：用户注册/登录（bcrypt 加密）、会话管理（Token 24h 过期）、API Token 认证
- **性能优化**：数据库索引优化、分页支持、请求限流（500次/分钟/IP）
- **安全防护**：CORS 跨域、SQL 注入防护、输入验证、Panic 自动恢复
- **实时监控**：CPU/内存/磁盘/网络监控、WebSocket 实时推送、阈值自动告警
- **智能能力**：LLM 航线规划 + NFZ 纠偏 + 多候选方案、CoT 思维链 AI 分析、智能巡检与通知中心

---

## 运行与停止速查

以下命令基于仓库当前结构，默认在 `project/` 目录执行。

### 本地开发运行

```bash
cd project
go mod tidy
go run .
```

启动成功后访问：
- `http://127.0.0.1:8080/`
- 健康检查：`http://127.0.0.1:8080/api/healthz`

### Windows 后台运行

```powershell
# 源码直接后台运行
Start-Process -FilePath "go" -ArgumentList "run","." -WorkingDirectory (Get-Location).Path -RedirectStandardOutput "run.out.log" -RedirectStandardError "run.err.log"

# 编译后后台运行
go build -o sc.exe .
Start-Process -FilePath ".\sc.exe" -WorkingDirectory (Get-Location).Path
```

### 停止运行

- 前台运行：按 `Ctrl + C`
- Windows 后台：

```powershell
Get-NetTCPConnection -LocalPort 8080 | Select-Object OwningProcess
Stop-Process -Id <PID> -Force
```

---

## 快速开始

### 1. 环境准备

```bash
cd project
go version   # 建议 Go 1.24+
go mod tidy
```

### 2. 配置环境变量

项目启动时自动读取 `project/.env`，无则使用系统环境变量和内置默认值。

示例 `.env`（MySQL，当前默认）：

```env
SC_LISTEN_ADDR=:8080
SC_DB_DRIVER=mysql
MYSQL_DSN=user:pass@tcp(host:port)/dbname?parseTime=true&charset=utf8mb4&loc=Asia%2FShanghai
SC_AGENT_PORT=9100
SC_API_TOKEN=
LLM_API_KEY=
LLM_BASE_URL=https://openapi.monica.im/v1
LLM_MODEL=gpt-4.1
AMAP_KEY=
```

示例 `.env`（SQLite 模式）：

```env
SC_DB_DRIVER=sqlite
SC_DB_PATH=app.db
```

### 3. 启动服务

```bash
go run .
```

### 4. 首次使用

1. 访问 `http://127.0.0.1:8080/` → 自动跳转注册页
2. 创建账号并登录
3. 左侧导航切换 17 大功能模块

---

## 系统访问地址

### 主要页面

| 地址 | 说明 | 登录要求 |
|------|------|---------|
| `http://127.0.0.1:8080/` | 系统入口（自动跳转）| - |
| `http://127.0.0.1:8080/app/register.html` | 用户注册 | - |
| `http://127.0.0.1:8080/app/login.html` | 用户登录 | - |
| `http://127.0.0.1:8080/app/dashboard.html` | 控制仪表盘（主界面）| 需登录 |
| `http://127.0.0.1:8080/app/vnc.html` | VNC 远程桌面 | 需登录 |
| `http://127.0.0.1:8080/app/ssh.html` | SSH 终端 | 需登录 |

---

## 环境变量配置

### 基础配置

| 变量名 | 默认值 | 说明 |
|--------|--------|------|
| `SC_LISTEN_ADDR` | `:8080` | 监听地址和端口 |
| `SC_DB_DRIVER` | `sqlite` | 数据库驱动（`sqlite` 或 `mysql`） |
| `SC_DB_PATH` | `app.db` | SQLite 数据库文件路径 |
| `SC_MYSQL_DSN` / `MYSQL_DSN` | - | MySQL DSN（仅 mysql 模式生效） |
| `SC_API_TOKEN` | `""` | API 认证 Token（空=关闭）|
| `SC_MAX_UPLOAD_MB` | `64` | 文件上传限制(MB) |
| `SC_TRUSTED_PROXIES` | `127.0.0.1` | 受信任的代理 IP |
| `SC_AGENT_PORT` | `9100` | 内嵌硬件监控 Agent 端口 |

### 告警配置

| 变量名 | 默认值 | 说明 |
|--------|--------|------|
| `SC_THRESH_CPU` | `85` | CPU 告警阈值(%) |
| `SC_THRESH_MEM` | `85` | 内存告警阈值(%) |
| `SC_THRESH_DISK` | `90` | 磁盘告警阈值(%) |
| `SC_ALERT_INTERVAL_SEC` | `10` | 检测间隔(秒) |

### 智能规划 / 地图（可选）

| 变量名 | 说明 |
|--------|------|
| `LLM_API_KEY` | 大模型 API Key（留空降级为直线规划） |
| `LLM_BASE_URL` | 大模型 API Base URL |
| `LLM_MODEL` | 大模型名称 |
| `AMAP_KEY` | 高德地图 Key（地址解析/逆编码） |

---

## 20大功能模块

| # | 模块名称 | 功能说明 |
|---|----------|----------|
| 1 | 无人机管理 | 注册/编辑/删除/连接（SSH/VNC/RDP）、状态监控、视频流配置 |
| 2 | GPS/位置信息 | 实时位置、Leaflet 地图、电子围栏、历史轨迹、WebSocket 推送 |
| 3 | 电池监控 | 电量/电压/温度/健康度、自动报警、历史趋势、WebSocket 推送 |
| 4 | 飞行任务管理 | 任务 CRUD、飞行阶段状态机、LLM 智能规划、AI 思维链分析 |
| 5 | 禁飞区管理 | 圆形/多边形禁飞区、地图可视化、智能规划自动绕行 |
| 6 | 系统状态监控 | CPU/内存/磁盘/网络实时监控（WebSocket 每秒推送） |
| 7 | 硬件状态检测 | Agent 模式采集、自动刷新、CSV 导出、AI 智能诊断 |
| 8 | 远程桌面控制 | VNC/SSH/RDP 三协议、浏览器内 VNC + SSH |
| 9 | 视频监控 | 无人机视频流统一查看和播放 |
| 10 | 语音交互记录 | 音频上传/下载/播放/删除、交互统计 |
| 11 | 异常报警 | 报警管理、导入导出、类型/优先级统计 |
| 12 | 维护操作日志 | 操作审计、导入导出、类型/结果统计 |
| 13 | 软件更新管理 | 更新发布/编辑/删除、自动/强制更新 |
| 14 | 数据同步状态 | 多设备数据库同步（全量/增量）、18 张表 |
| 15 | 性能分析报告 | 响应时间/吞吐量/错误率、四种图表 |
| 16 | AI决策记录 | CoT 思维链推理历史、多场景分析归档 |
| 17 | 无人机连接与部署 | hw-agent 地面站部署、MAVLink 中继、Push 模式 |
| 18 | 备份与数据回滚 | 自动/手动备份、数据恢复与回滚、备份记录管理、恢复进度追踪、备份文件上传恢复 |
| 19 | AI 智能助手 | 右下角浮窗、多轮对话、快捷指令、知识库问答、模块跳转、数据查询 |
| 20 | 消息通知中心 | 右上角铃铛、未读计数、类型筛选、AI 定时巡检、点击跳转、批量已读 |

---

## API接口文档

### 认证接口

| 方法 | 端点 | 说明 |
|------|------|------|
| POST | `/api/auth/register` | 用户注册 |
| POST | `/api/auth/login` | 用户登录 |
| POST | `/api/auth/logout` | 用户登出 |
| GET | `/api/auth/validate` | 验证 Token |

### 核心业务接口

| 分类 | 方法 | 端点 | 说明 |
|------|------|------|------|
| 无人机 | GET | `/api/drones` | 无人机列表 |
| 无人机 | POST | `/api/drones` | 注册无人机 |
| 无人机 | GET | `/api/drones/stats` | 无人机统计 |
| GPS | GET | `/api/gps/devices` | GPS 设备列表 |
| GPS | POST | `/api/gps/push` | Agent GPS 推送 |
| GPS | WS | `/api/gps/stream` | GPS 实时推送 |
| 电池 | GET | `/api/battery/latest` | 最新电池状态 |
| 电池 | POST | `/api/battery/push` | Agent 电池推送 |
| 电池 | WS | `/api/battery/stream` | 电池实时推送 |
| 飞行 | GET | `/api/flight/missions` | 任务列表 |
| 飞行 | POST | `/api/flight/missions` | 创建任务 |
| 飞行规划 | POST | `/api/flight/missions/plan` | 智能航线规划（LLM） |
| 飞行规划 | GET | `/api/flight/missions/plans` | 规划列表 |
| 硬件 | GET | `/api/hardware/items` | 硬件列表 |
| 硬件 | POST | `/api/hardware/push` | Agent 硬件推送 |
| 设备 | GET | `/api/devices` | 远程设备列表 |
| AI分析 | POST | `/api/cot/analyze` | 统一 AI 分析入口 |
| AI分析 | GET | `/api/cot/chains` | 思维链记录列表 |
| 告警 | GET | `/api/alerts/list` | 报警列表 |
| 告警 | GET | `/api/alerts/stats` | 报警统计 |
| 日志 | GET | `/api/logs/list` | 操作日志列表 |
| 监控 | GET | `/api/metrics/snapshot` | 系统指标快照 |
| 监控 | WS | `/api/metrics/stream` | 实时监控流 |
| 同步 | GET | `/api/sync/tasks` | 同步任务列表 |
| 性能 | GET | `/api/report/perf` | 性能报告 |
| 备份 | GET | `/api/backup/list` | 备份记录列表 |
| 备份 | GET | `/api/backup/status` | 备份状态概览（总数/成功/失败/空间） |
| 备份 | POST | `/api/backup/manual` | 手动触发备份 |
| 备份 | POST | `/api/backup/restore/:id` | 从指定备份恢复数据 |
| 备份 | POST | `/api/backup/restore-upload` | 上传 SQL 文件恢复数据 |
| 备份 | GET | `/api/backup/restore-progress` | 恢复进度查询（轮询） |
| 备份 | DELETE | `/api/backup/:id` | 删除备份记录及文件 |
| 备份 | GET | `/api/backup/download/:id` | 下载备份文件 |
| 备份 | POST | `/api/backup/auto-toggle` | 开关自动备份 |
| 备份 | POST | `/api/backup/cleanup` | 清理旧备份（保留最近 N 份） |
| 通知 | GET | `/api/notifications` | 通知列表（支持类型筛选） |
| 通知 | POST | `/api/notifications/read/:id` | 标记通知已读 |
| 通知 | POST | `/api/notifications/read-all` | 全部标记已读 |
| AI助手 | POST | `/api/ai-assistant/chat` | AI 助手对话 |
| AI助手 | GET | `/api/ai-assistant/suggestions` | 获取快捷指令建议 |

### 分页参数

所有列表接口支持 `page`（默认 1）和 `page_size`（默认 50，最大 200）。

---

## 项目结构

```text
project/
├── main.go                        # 主程序入口（含时区初始化 Asia/Shanghai）
├── go.mod / go.sum                 # Go 依赖管理
├── .env                            # 环境变量配置
├── app.db                          # SQLite 数据库（历史备份，已迁移至 MySQL）
├── migrate_sqlite_to_mysql.py      # SQLite → MySQL 数据迁移脚本
├── cmd/
│   └── agent/main.go               # hw-agent 独立程序（地面站 MAVLink 中继）
├── internal/
│   ├── agent/agent.go              # 内嵌 Agent（主服务自动启动）
│   ├── db/
│   │   ├── db.go                   # 数据库连接、SQL 兼容层（AdaptSQL）、DB/Tx 包装器
│   │   ├── migrate_mysql.go        # MySQL 建表 DDL
│   │   └── migrate_sqlite.go       # SQLite 建表 DDL
│   ├── handlers/                   # API 处理函数
│   │   ├── api.go                  # 通用 API（设备、硬件、报警、日志、更新、同步等）
│   │   ├── auth.go                 # 用户认证（注册/登录/登出/Token 验证）
│   │   ├── drones.go               # 无人机管理
│   │   ├── gps.go                  # GPS/位置信息
│   │   ├── battery.go              # 电池监控
│   │   ├── flight.go               # 飞行任务管理
│   │   ├── flight_plan.go          # 智能航线规划（LLM + AMap）
│   │   ├── noflyzone.go            # 禁飞区管理
│   │   ├── cot.go                  # 思维链 AI 分析
│   │   ├── backup.go               # 备份与数据回滚（自动/手动/恢复/进度追踪）
│   │   ├── ai_assistant.go         # AI 智能助手（多轮对话/知识库/快捷指令）
│   │   ├── notification.go         # 消息通知中心
│   │   ├── patrol.go               # AI 定时巡检（模拟机/日志/设备安全检查）
│   │   └── wshub.go                # WebSocket 事件广播
│   ├── llm/llm.go                  # LLM 大模型调用封装
│   ├── amap/amap.go                # 高德地图 API 封装
│   ├── middleware/                  # 中间件（认证、限流、日志）
│   ├── monitor/monitor.go          # 系统指标采集
│   └── syncengine/engine.go        # 数据同步引擎（18 张表）
├── web/
│   ├── index.html                  # 系统首页
│   ├── dashboard.html              # 仪表盘导航（含侧边栏滚动）
│   ├── vnc.html / ssh.html         # VNC/SSH 客户端页面
│   └── modules/                    # 20 个功能模块页面
│       ├── drones.html / gps.html / battery.html / flight.html
│       ├── noflyzone.html / cot.html / hardware.html / remote.html
│       ├── video.html / monitor.html / alerts.html / logs.html
│       ├── audio.html / updates.html / sync.html / performance.html
│       ├── backup.html             # 备份与数据回滚管理
│       ├── ai-assistant.js         # 右下角 AI 智能助手浮窗
│       ├── notification-bell.js    # 右上角消息通知铃铛
│       └── common.js / common.css  # 公共工具函数和样式
└── data/
    ├── backups/                    # 数据库备份文件存储
    └── recordings/                 # 语音文件存储
```

---

## 部署指南

### 单机部署

```bash
cd project
go mod tidy
go build -o sc.exe .     # Windows
go build -o sc .          # Linux
```

部署时保留：可执行文件 + `.env` + `data/`（静态页面已 embed 打入二进制）。

### Linux systemd

```ini
[Unit]
Description=CloudControl
After=network.target

[Service]
Type=simple
ExecStart=/opt/smartcontrol/sc
WorkingDirectory=/opt/smartcontrol
Restart=on-failure

[Install]
WantedBy=multi-user.target
```

### 交叉编译

```powershell
$env:GOOS="linux"; $env:GOARCH="amd64"; go build -o sc .
```

---

## 故障排查

### 端口被占用

```powershell
# Windows
netstat -ano | findstr :8080
taskkill /PID <进程ID> /F
```

### VNC 连接失败

检查：目标已安装 VNC 服务 → 已启动 → 防火墙放行 5900 → 网络连通。

### 硬件温度显示 0℃

Windows 需管理员权限运行，或安装 LibreHardwareMonitor / OpenHardwareMonitor。

### 数据库锁定（SQLite 模式）

```bash
rm app.db-shm app.db-wal  # 删除锁文件后重启
```

---

## 版本更新

### v3.2.0 (最新)

- 新增备份与数据回滚模块（`backup.go` + `backup.html`）
  - 自动备份（24h 周期 + 启动首次）、手动备份、备份记录管理
  - 两种数据恢复方式：从备份记录恢复 / 上传 SQL 文件恢复
  - 恢复前自动创建安全备份、异步恢复进度追踪
  - 修复 `confirmAction()` 模态框确认失效 Bug
  - 优化用户交互：操作即时 Toast 反馈 + 恢复进度弹窗
- 新增 AI 智能助手（`ai_assistant.go` + `ai-assistant.js`）
  - 右下角浮窗入口、多轮对话、快捷指令、知识库问答、模块跳转
- 新增消息通知中心（`notification.go` + `notification-bell.js`）
  - 右上角铃铛、未读计数、类型筛选、点击跳转、批量已读
- 新增 AI 定时巡检（`patrol.go`）
  - 定时检查模拟机、日志、设备安全，发现问题自动生成通知

### v3.1.0

- MySQL 数据库支持（`SC_DB_DRIVER` 切换）
- SQL 兼容层自动转换（AdaptSQL）
- SQLite → MySQL 数据迁移完成（1473+ 行数据，26 张表）
- 系统时区统一 Asia/Shanghai (UTC+8)
- 侧边栏菜单滚动优化、CoT 智能决策菜单可见

### v3.0.0

- 禁飞区管理、AI 决策记录（CoT）、NFZ 纠偏引擎
- 多候选方案生成、案例检索增强（CBR）
- 任务名称实时重名检测、地面站中继架构

### v2.0.0

- 无人机统一管理、GPS/电池/飞行任务模块
- hw-agent Push 模式、SSH/RDP 支持
- 数据同步引擎、智能航线规划（LLM）
- 高德地图集成、思维链 AI 分析

### v1.1.0

- 完整会话管理、认证接口、分页支持、限流保护

---

## 团队协作规范

> 以下为团队协作开发规范（面向课程设计/学生团队），请所有成员在开始开发前通读并严格遵守。

### 团队分工原则

- **模块负责制**：每个功能模块设 1 名负责人（Owner），负责需求拆分、接口约定、代码评审合入。
- **接口优先**：跨模块协作时先对齐接口（API/数据结构/文件格式），再并行开发。
- **小步快跑**：一次提交只做一件事；一次 PR（合并请求）只解决一个主题。
- **评审必需**：合入必须需经过至少一人评审通过，由队长统一提交，确保能正常运行且无报错。
- **问题先同步**：发现阻塞问题（环境、接口、数据、冲突）先在群内同步，再继续开发。

### Git 提交规范（强制）

**所有提交信息必须使用如下格式：** `M.D - 已完成的功能/修复内容`

- `M`：里程碑/大版本号（例如第 2 次阶段验收）
- `D`：该里程碑下的功能点序号

**示例**：`2.7 - 已完成远程控制` / `1.3 - 已修复登录态丢失问题` / `3.1 - 已完成音频列表分页接口`

### 代码规范

- **Go 后端**：提交前 `gofmt`、按 `internal/<领域>` 组织、路由集中注册、SQL 可读
- **前端**：小写+短横线命名、不引入大体量依赖、接口变更同步页面
- **通用**：命名清晰、错误必须处理、同模块风格一致

### 禁止行为

- 绕过提交信息格式
- 直接向 `main` 推送
- 在公共分支执行 `git push --force`
- 提交个人临时文件（`node_modules/`、本地数据库等）
- 未通知他人就改动公共接口

---

**Made with CloudControl Team**
