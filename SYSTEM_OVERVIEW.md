# 系统功能与文件说明（全量）

本文档用于回答两件事：

1. 系统都包含哪些功能（按模块/接口/页面对应）。
2. 仓库中每个目录、每个关键文件分别负责什么（用于协作、交接与排查）。

---

## 1. 系统功能总览

### 1.1 系统入口与访问方式

- **后端监听**：默认 `:8080`（可用环境变量 `SC_LISTEN_ADDR` 修改）。
- **系统入口**：`GET /`
  - 若数据库内没有用户，则重定向到 `/app/register.html`
  - 否则重定向到 `/app/login.html`
- **前端静态资源**：`/app/*`（后端通过 Go `embed` 内嵌 `project/web/` 并静态托管）

### 1.1.1 在线/离线判定（重要）

- **GPS 设备在线**：`gps_devices.last_update` 在 60 秒内更新视为在线。
- **无人机在线**：当关联的 GPS 设备持续上报时，`drones.status` 会被更新为 `online`。
- **无人机离线**：主程序后台会定时检测，若 GPS 设备超过 60 秒未上报，会把 `gps_devices.status` 置为 `离线`，并将关联的 `drones.status` 置为 `offline`。

### 1.2 认证与会话（用户体系）

- **功能**
  - 用户注册/登录/登出
  - 基于 Token 的会话持久化（SQLite 表 `sessions`），默认有效期 24h
  - 定时清理过期会话
- **后端接口**（`/api/auth/*`）
  - `POST /api/auth/register`
  - `POST /api/auth/login`
  - `POST /api/auth/logout`
  - `GET /api/auth/validate`
- **页面**
  - `/app/register.html`
  - `/app/login.html`

### 1.3 系统状态监控（CPU/内存/磁盘/网络）

- **功能**
  - 指标快照查询
  - WebSocket 实时推送（前端定时刷新曲线/数值）
- **后端接口**
  - `GET /api/metrics/snapshot`
  - `WS /api/metrics/stream`
- **页面**
  - 仪表盘模块：`/app/modules/monitor.html`

### 1.4 硬件状态检测

- **功能**
  - 获取主机硬件与系统信息（主机名/OS/CPU/内存等）
  - 远程设备硬件指标采集（通过 hw-agent Pull/Push 模式）
  - 硬件设备管理（添加/搜索/刷新/删除/导出 CSV）
  - 自动状态判定（在线/离线）
  - 硬件类型支持：服务器、路由器、交换机、存储设备、无人机
- **后端接口**
  - `GET /api/hardware/snapshot`
  - `GET /api/hardware/items`
  - `POST /api/hardware/items`
  - `POST /api/hardware/items/refresh`
  - `POST /api/hardware/push`（Agent Push 模式）
  - `GET /api/hardware/items/stats`
- **数据落地**
  - SQLite 表：`hardware_items`
- **页面**
  - 仪表盘模块：`/app/modules/hardware.html`

### 1.5 音频（语音交互记录）管理

- **功能**
  - 上传音频文件（表单上传）
  - 音频列表（支持分页）
  - 下载音频
  - 删除音频（同时删除磁盘文件与数据库记录）
- **后端接口**
  - `POST /api/audio/upload`
  - `GET /api/audio/list?page=1&page_size=50`
  - `GET /api/audio/download/:id`
  - `DELETE /api/audio/:id`
- **数据落地**
  - SQLite 表：`recordings`
  - 文件目录：`project/data/recordings/`（运行时创建）
- **页面**
  - 仪表盘模块：`/app/modules/audio.html`

### 1.6 远程桌面控制（VNC/SSH/RDP）

- **功能**
  - VNC：浏览器端 noVNC 渲染远程桌面（后端 VNC TCP 到 WebSocket 代理）
  - SSH：浏览器端 xterm.js 终端（后端 SSH TCP 到 WebSocket 代理）
  - RDP：生成 .rdp 文件下载，用户通过本地 RDP 客户端连接
  - 设备在线/离线状态更新
  - 设备统一通过无人机注册自动创建（设备列表仅显示 `drone_id > 0` 的记录）
- **后端接口**
  - `WS /api/vnc/ws?target=IP:PORT`
  - `WS /api/ssh/ws`
  - `POST /api/devices/:id/status`（在线/离线）
  - `POST /api/user/stats/incr_connection`
- **页面**
  - 仪表盘模块：`/app/modules/remote.html`
  - 独立页面：`/app/vnc.html`（noVNC）、`/app/ssh.html`（xterm.js）

### 1.7 设备管理（远程控制资产）

- **功能**
  - 设备列表（可按匹配权重排序）
  - 新增设备（协议限制：VNC/RDP/SSH）
  - 删除设备
  - 设置设备在线/离线与更新时间
- **后端接口**
  - `GET /api/devices?name=xxx&protocol=VNC`
  - `POST /api/devices`
  - `DELETE /api/devices/:id`
  - `POST /api/devices/:id/status`
- **数据落地**
  - SQLite 表：`devices`
- **页面**
  - 仪表盘模块：`/app/modules/remote.html`（通常在该模块内进行设备选择/连接）

### 1.8 告警（阈值告警 + 人工告警）

- **功能**
  - 监控指标阈值触发告警（后台定时检测）
  - 告警列表（分页）
  - 告警确认（ack）
  - 手动新增告警
- **后端接口**
  - `GET /api/alerts/list?page=1&page_size=50`
  - `POST /api/alerts/ack/:id`
  - `POST /api/alerts/new`
- **数据落地**
  - SQLite 表：`alerts`
- **页面**
  - 仪表盘模块：`/app/modules/alerts.html`

### 1.9 操作日志（审计/运维日志）

- **功能**
  - 追加日志（用于前端或其他组件写入操作事件）
  - 日志列表（分页）
- **后端接口**
  - `POST /api/logs/append`
  - `GET /api/logs/list?page=1&page_size=50`
- **数据落地**
  - SQLite 表：`logs`
- **页面**
  - 仪表盘模块：`/app/modules/logs.html`

### 1.10 软件更新管理（示例实现）

- **功能**
  - 更新记录列表
  - 新增更新记录
  - 检查更新（当前为示例返回）
- **后端接口**
  - `GET /api/updates/list`
  - `POST /api/updates/add`
  - `GET /api/updates/check`
- **数据落地**
  - SQLite 表：`updates`
- **页面**
  - 仪表盘模块：`/app/modules/updates.html`

### 1.11 数据同步状态

- **功能**
  - 多设备间数据库同步（全量/增量模式）
  - 同步任务管理（创建/启停/编辑/删除）
  - IP 有效性预检、实时进度追踪
  - 同步 18 张表（白名单定义于 `syncengine.SyncableTables`）：`hardware_items`、`updates`、`recordings`、`logs`、`alerts`、`video_sources`、`devices`、`drones`、`battery_records`、`battery_alerts`、`gps_devices`、`gps_history`、`gps_fence_alerts`、`flight_missions`、`mission_logs`、`perf_reports`、`flight_plans`、`user_stats`
- **后端接口**
  - `GET /api/sync/tasks`、`POST /api/sync/tasks`
  - `POST /api/sync/tasks/:id/start`、`POST /api/sync/tasks/:id/stop`
  - `POST /api/sync/tasks/stop-all`
  - `POST /api/sync/tasks/check-ip`
  - `GET /api/sync/export-data`、`POST /api/sync/import-data`
  - `GET /api/sync/tasks/stats`、`GET /api/sync/tasks/progress`、`GET /api/sync/tasks/info`
- **数据落地**
  - SQLite 表：`sync_tasks`
- **页面**
  - 仪表盘模块：`/app/modules/sync.html`

### 1.12 性能分析报告

- **功能**
  - 实时系统性能报告（CPU、内存、磁盘、网络）
  - 性能分析记录管理（查询/新增/导入/删除）
  - 四种图表：响应时间折线图、吞吐量柱状图、错误率饼图、模块性能雷达图
- **后端接口**
  - `GET /api/report/perf`
  - `GET /api/report/perf-list`
  - `POST /api/report/perf-add`
  - `POST /api/report/perf-import`
  - `DELETE /api/report/perf-delete/:id`
  - `POST /api/report/perf-collect`（自动采集当前性能数据并存储）
- **数据落地**
  - SQLite 表：`perf_reports`
- **页面**
  - 仪表盘模块：`/app/modules/performance.html`

### 1.13 视频监控

- **功能**
  - 无人机视频流统一查看和播放
  - 数据来源于无人机注册表的 `video_url` 字段（不再使用独立的 `video_sources` 表）
  - 支持 HTTP/HTTPS 视频流直接播放
- **后端接口**
  - 复用 `GET /api/drones`（含 `video_url` 字段）
- **页面**
  - 仪表盘模块：`/app/modules/video.html`

### 1.14 无人机管理

- **功能**
  - 无人机注册/编辑/删除
  - 注册时自动创建关联的远程设备（`devices`）和 GPS 设备（`gps_devices`）
  - 删除时级联清理所有关联数据（设备、GPS、电池、飞行任务等）
  - 支持 SSH/VNC/RDP 三种连接协议
  - 视频流地址配置（`video_url`）
  - 无人机统计（在线/离线/协议分布）
- **后端接口**
  - `GET /api/drones`
  - `POST /api/drones`
  - `PUT /api/drones/:id`
  - `DELETE /api/drones/:id`
  - `GET /api/drones/stats`
- **数据落地**
  - SQLite 表：`drones`
- **页面**
  - 仪表盘模块：`/app/modules/drones.html`

### 1.15 GPS/位置信息

- **功能**
  - 无人机实时位置监控（Leaflet + OpenStreetMap 地图）
  - GPS 设备管理（添加/编辑/删除）
  - 手动和 Agent 自动推送 GPS 数据
  - 电子围栏（超出范围触发报警）
  - 历史轨迹查看
  - WebSocket 事件驱动的实时监控（`gps_update`）
- **后端接口**
  - `GET /api/gps/devices`、`POST /api/gps/devices`
  - `POST /api/gps/devices/:id/push`、`POST /api/gps/push`
  - `GET /api/gps/devices/:id/history`
  - `GET /api/gps/fence-alerts`
  - `WS /api/gps/stream`（实时事件推送）
- **数据落地**
  - SQLite 表：`gps_devices`、`gps_history`、`gps_fence_alerts`
- **页面**
  - 仪表盘模块：`/app/modules/gps.html`

### 1.16 电池监控

- **功能**
  - 实时电池指标监控（电量/电压/电流/温度/健康度）
  - 自动报警（低电量≤ 20%、严重低电≤ 10%、高温≥ 50°C、低健康度≤ 50%）
  - 手动上报和 Agent 自动推送
  - 历史趋势图表（最近 50 条记录）
  - WebSocket 事件驱动实时推送（数据变化时即时通知前端，零延迟）
- **后端接口**
  - `GET /api/battery/records`、`POST /api/battery/report`
  - `POST /api/battery/push`
  - `GET /api/battery/latest`、`GET /api/battery/history/:device_id`
  - `GET /api/battery/stats`、`GET /api/battery/alerts`
  - `WS /api/battery/stream`（实时事件推送）
- **数据落地**
  - SQLite 表：`battery_records`、`battery_alerts`
- **页面**
  - 仪表盘模块：`/app/modules/battery.html`

### 1.17 飞行任务管理

- **功能**
  - 飞行任务创建/编辑/删除
  - 飞行阶段状态机（待命→起飞→巡航→执行任务→返航→降落）
  - 任务日志记录
  - 批量导入/导出（JSON）
  - 与 GPS 模块联动选择执行无人机
  - WebSocket 事件驱动实时推送（任务创建/编辑/删除/阶段变更时即时通知前端）
- **后端接口**
  - `GET /api/flight/missions`、`POST /api/flight/missions`
  - `PUT /api/flight/missions/:id`、`DELETE /api/flight/missions/:id`
  - `POST /api/flight/missions/:id/phase`
  - `POST /api/flight/missions/import`
  - `GET /api/flight/missions/stats`
  - `WS /api/flight/stream`（实时事件推送）
- **数据落地**
  - SQLite 表：`flight_missions`、`mission_logs`
- **页面**
  - 仪表盘模块：`/app/modules/flight.html`

### 1.18 智能航线规划（LLM + 降级规划）

- **功能**
  - 基于大模型（LLM）的智能航线规划（支持多航点、动作序列、高度/速度配置）
  - LLM 不可用时自动降级为直线规划器
  - 规划结果以草稿形式保存，可采纳转为正式飞行任务
  - AMap（高德地图）地理编码/逆地理编码集成
  - 规划状态检查（LLM 和 AMap 可用性）
- **后端接口**
  - `POST /api/flight/missions/plan`（创建规划请求）
  - `GET /api/flight/missions/plans`（规划列表）
  - `GET /api/flight/missions/plan/:id`（获取规划详情）
  - `POST /api/flight/missions/plan/:id/adopt`（采纳规划→创建飞行任务）
  - `POST /api/flight/missions/plan/:id/discard`（丢弃规划）
  - `GET /api/flight/missions/plan/status`（LLM/AMap 状态检查）
  - `POST /api/amap/geocode`（地址→坐标）
  - `POST /api/amap/regeocode`（坐标→地址）
- **数据落地**
  - SQLite 表：`flight_plans`
- **环境变量**
  - `LLM_API_KEY`、`LLM_BASE_URL`、`LLM_MODEL`（大模型配置）
  - `AMAP_KEY`（高德地图 Key）

---

## 2. 后端（Go + Gin + SQLite）文件说明（`project/`）

### 2.1 入口与路由

- **`project/main.go`**
  - 程序入口
  - 读取环境变量（监听地址、数据库路径、API Token、上传限制、阈值告警参数等）
  - 打开数据库并执行迁移
  - 初始化 Gin 引擎与中间件（恢复、日志、CORS、限流）
  - 注册认证路由与业务路由
  - 内嵌并托管前端静态资源到 `/app`
  - `/` 路由做首次使用跳转逻辑
  - 启动后台阈值告警任务

### 2.2 数据库层

- **`project/internal/db/db.go`**
  - `Open(path)`：打开 SQLite（WAL 模式、外键开启）
  - `Migrate(db)`：创建并初始化表结构、索引、以及 `sync_status` 初始行
  - 涉及表：`recordings`、`logs`、`alerts`、`updates`、`sync_tasks`、`users`、`sessions`、`devices`、`user_stats`、`drones`、`gps_devices`、`gps_history`、`gps_fence_alerts`、`battery_records`、`battery_alerts`、`flight_missions`、`mission_logs`、`hardware_items`、`perf_reports`、`flight_plans`、`video_sources`(已废弃)

### 2.3 业务接口层（Handlers）

- **`project/internal/handlers/auth.go`**

  - `/api/auth/*` 认证相关接口
  - 密码使用 bcrypt 保存
  - 登录后生成 Token 并写入 `sessions`
  - `cleanupExpiredSessions()` 定时清理过期会话
- **`project/internal/handlers/api.go`**

  - 注册主要业务接口 `RegisterRoutes()`
  - 监控指标快照/流、硬件信息、音频上传/列表/下载/删除
  - 告警列表/确认/新建/解决/导入/统计
  - 日志追加/列表/编辑/删除/导入/统计
  - 更新列表/新增/编辑/删除/导入/统计/检查
  - 同步任务管理（CRUD + 启停 + 进度 + 统计）
  - 性能报告（实时 + 列表 + 新增 + 导入 + 删除）
  - VNC/SSH TCP 到 WebSocket 代理
  - 设备管理（CRUD + 状态）
  - 硬件设备管理（CRUD + 刷新 + Agent Push）
  - 用户统计（连接次数）
- **`project/internal/handlers/drones.go`**

  - 无人机管理 API（注册/编辑/删除/列表/统计）
  - 注册时自动创建关联设备和 GPS 设备
  - 删除时级联清理
- **`project/internal/handlers/gps.go`**

  - GPS/位置信息 API（设备管理、位置推送、历史轨迹、围栏报警）
- **`project/internal/handlers/battery.go`**

  - 电池监控 API（数据上报、最新状态、历史记录、报警、Agent Push、WebSocket 实时推送）
- **`project/internal/handlers/flight.go`**

  - 飞行任务管理 API（任务 CRUD、阶段更新、日志、导入、统计、WebSocket 实时推送）
- **`project/internal/handlers/flight_plan.go`**

  - 智能航线规划 API（LLM 规划创建/采纳/丢弃/列表/状态检查、AMap 地理编码/逆编码）
- **`project/internal/handlers/wshub.go`**

  - WebSocket 事件广播中心（按 topic 分组管理客户端连接，支持 Subscribe/Unsubscribe/Broadcast，线程安全）

### 2.4 中间件

- **`project/internal/middleware/logger.go`**

  - `RequestLogger()`：输出请求日志（方法/路径/耗时/状态码/IP）
  - `Recovery()`：捕获 panic 并返回 500
- **`project/internal/middleware/ratelimit.go`**

  - 简单令牌桶限流（按 IP 维度）
  - 默认在 `main.go` 中配置为：每 IP 每分钟 100 次

### 2.5 监控采集

- **`project/internal/monitor/monitor.go`**
  - 使用 `gopsutil` 采集 CPU/内存/负载/磁盘/网络/开机时间等
  - `CollectMetrics()`：输出监控指标快照
  - `HardwareInfo()`：输出硬件/系统信息

### 2.6 同步引擎

- **`project/internal/syncengine/engine.go`**
  - 独立 goroutine 管理每个同步任务
  - 全量同步（清空目标表 + 写入）和增量同步（基于时间戳）
  - 数据导出/导入、事务保护、安全白名单（18 张表）

### 2.6.1 LLM 与地图集成

- **`project/internal/llm/llm.go`**
  - 大模型（LLM）调用封装：自动加载 `.env`、构建 Prompt、解析返回的航线 JSON
  - 降级规划器（`simplePlanner`）：LLM 不可用时生成起点→终点直线航线
  - 数据结构：`PlanRequest`、`PlanResult`、`Coordinate`、`Waypoint`、`Action`
- **`project/internal/amap/amap.go`**
  - 高德地图 Web Service API 封装（地理编码 + 逆地理编码）
  - 环境变量 `AMAP_KEY` 控制可用性

### 2.7 Agent 程序

- **`project/cmd/agent/main.go`**
  - 独立的 hw-agent 程序，部署在无人机/目标设备上
  - 采集 CPU、内存、温度、网络、GPS、电池数据
  - Pull 模式：暴露 `/metrics` 和 `/health` HTTP 端点
  - Push 模式：主动推送数据到主控端
- **`project/internal/agent/agent.go`**
  - 内嵌 Agent，主服务启动时自动运行本机 Agent（端口 9100）

### 2.6 工具函数

- **`project/internal/utils/pagination.go`**
  - `GetPagination()`：解析分页参数 `page/page_size`，并计算 `offset`
  - `ValidateID()`：校验路由参数是否为合法数字 id
  - `SanitizeString()`：基础字符串长度清理

---

## 3. 前端（静态页面）文件说明（`project/web/`）

### 3.1 页面入口

- **`project/web/index.html`**

  - 系统首页/入口页（通常提供跳转或展示入口信息）
- **`project/web/register.html`**

  - 用户注册页面
- **`project/web/login.html`**

  - 用户登录页面
- **`project/web/dashboard.html`**

  - 登录后的主界面（左侧导航 + iframe 加载各模块页面）
  - 默认加载 `modules/monitor.html`

### 3.2 功能模块页面（iframe 内加载：`project/web/modules/`）

- **`modules/drones.html`**：无人机管理模块页面
- **`modules/gps.html`**：GPS/位置信息模块页面
- **`modules/battery.html`**：电池监控模块页面
- **`modules/flight.html`**：飞行任务管理模块页面
- **`modules/monitor.html`**：系统状态监控模块页面
- **`modules/audio.html`**：音频/语音交互记录模块页面
- **`modules/remote.html`**：远程桌面控制模块页面（VNC/SSH/RDP）
- **`modules/video.html`**：视频监控模块页面（数据来源于无人机注册表）
- **`modules/hardware.html`**：硬件状态检测模块页面
- **`modules/updates.html`**：软件更新管理模块页面
- **`modules/sync.html`**：数据同步状态模块页面
- **`modules/alerts.html`**：异常告警模块页面
- **`modules/performance.html`**：性能分析报告模块页面
- **`modules/logs.html`**：维护操作日志模块页面
- **`modules/common.js`**：模块页共用的请求/渲染辅助逻辑（供各模块复用）
- **`modules/common.css`**：模块页共用样式

### 3.3 远程桌面相关

- **`project/web/vnc.html`**

  - 独立的 VNC 远程桌面页面（noVNC + WebSocket 代理）
  - 连接/断开做了前端冷却（避免频繁操作）
  - 连接成功后触发设备状态在线 + 用户连接次数自增
- **`project/web/ssh.html`**

  - 独立的 SSH 终端页面（xterm.js + WebSocket 代理）
- **`project/web/vnc_simple.html`**

  - VNC 的简化版本页面（用于快速连接验证/演示）
- **`project/web/novnc-rfb.js`**

  - noVNC 相关脚本（本地版本文件，可用于离线替代）

### 3.4 通用静态资源

- **`project/web/script.js`**

  - 一个集成式页面脚本示例：
    - 连接监控 WS 流并更新 Chart 曲线
    - 音频录制与上传、列表刷新
    - 打开 VNC 页面
    - 拉取告警/日志、检查更新、设置同步状态
- **`project/web/styles.css`**

  - 通用样式（`vnc.html` 等页面使用）

---

## 4. 数据与运行产物（运行时目录/文件）

- **`project/app.db`**

  - SQLite 数据库文件（本仓库中已存在一份样例/开发数据）
  - 生产/部署时可通过 `SC_DB_PATH` 指定新路径
- **`project/data/recordings/`**（运行时创建）

  - 音频上传后保存的文件目录

---

## 5. 仓库根目录（`./`）资料与脚本说明

> 根目录主要放课程/比赛材料与说明文档；可运行项目在 `project/` 下。

- **`README.md`**

  - 团队协作开发规范（分工、提交规范、代码规范、目录约定等）
- **`project/README.md`**

  - 可运行系统的使用说明（快速开始、环境变量、模块与接口概览、排障等）
- **`generate_proposal.py` / `proposal_content.py`**

  - 用于生成/组织项目申报书或材料内容的脚本与模板（与后端运行无强依赖）
- **`比赛立项准备行动指南.md`、`观察端和被控端的配置方法.md`、`项目功能架构流程图.md`、`项目完整使用指南.md`**

  - 项目文档与指导材料
- **`CloudControl *.docx/*.pdf/*.txt`**

  - 软件说明、源代码说明、立项申报书、功能特点等提交材料

---

## 6. 关键环境变量（后端）

- `SC_LISTEN_ADDR`：监听地址（默认 `:8080`）
- `SC_DB_PATH`：数据库路径（默认 `app.db`）
- `SC_API_TOKEN`：API 认证 Token（空字符串=关闭后端 API Token 校验；注意：与用户登录 Token 是两套机制）
- `SC_MAX_UPLOAD_MB`：上传大小限制（默认 64MB）
- `SC_TRUSTED_PROXIES`：受信任的代理 IP（默认 `127.0.0.1`，多个用英文逗号分隔）
- `SC_AGENT_PORT`：内嵌硬件监控 Agent 端口（默认 `9100`）
- `SC_THRESH_CPU` / `SC_THRESH_MEM` / `SC_THRESH_DISK`：阈值告警
- `SC_ALERT_INTERVAL_SEC`：阈值检测间隔（秒）

智能规划/地图相关：

- `LLM_API_KEY`：大模型 API Key
- `LLM_BASE_URL`：大模型 API Base URL
- `LLM_MODEL`：大模型名称
- `AMAP_KEY`：高德地图 Web 服务 Key（用于地理编码/逆地理编码）

---

## 7. 快速定位：功能到文件/接口

- **登录/注册问题**

  - 后端：`project/internal/handlers/auth.go`
  - 前端：`project/web/login.html`、`project/web/register.html`
  - 数据：`users`、`sessions`
- **监控数据不刷新**

  - 后端：`project/internal/handlers/api.go`（`MetricsStream`）
  - 采集：`project/internal/monitor/monitor.go`
  - 前端：`project/web/modules/monitor.html` 或 `project/web/script.js`
- **VNC/SSH 连接失败**

  - 后端：`project/internal/handlers/api.go`（`VNCProxyWS`、`SSHProxyWS`）
  - 前端：`project/web/vnc.html`、`project/web/ssh.html`、`project/web/modules/remote.html`
  - 外部依赖：目标机 VNC/SSH 服务、端口、防火墙/网络连通
- **无人机管理问题**

  - 后端：`project/internal/handlers/drones.go`
  - 前端：`project/web/modules/drones.html`
  - 数据：`drones`、`devices`、`gps_devices`
- **GPS/电池/飞行任务问题**

  - 后端：`project/internal/handlers/gps.go`、`battery.go`、`flight.go`
  - 前端：`project/web/modules/gps.html`、`battery.html`、`flight.html`
- **音频上传/列表异常**

  - 后端：`project/internal/handlers/api.go`（`AudioUpload/AudioList/...`）
  - 前端：`project/web/modules/audio.html` 或 `project/web/script.js`
  - 数据：`recordings`、`project/data/recordings/`
