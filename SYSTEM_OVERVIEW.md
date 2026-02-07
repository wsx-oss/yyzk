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

### 1.4 硬件信息采集

- **功能**
  - 获取主机硬件与系统信息（主机名/OS/CPU/内存等）
- **后端接口**
  - `GET /api/hardware/snapshot`
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

### 1.6 远程桌面控制（VNC WebSocket 代理 + noVNC）

- **功能**
  - 浏览器端 noVNC 渲染远程桌面
  - 后端提供 VNC TCP 到 WebSocket 的代理（浏览器与 VNC 服务之间的桥接）
  - 设备在线/离线状态更新
  - 记录用户远程连接次数
- **后端接口**
  - `WS /api/vnc/ws?target=IP:PORT`（默认 `127.0.0.1:5900`）
  - `POST /api/devices/:id/status`（在线/离线）
  - `POST /api/user/stats/incr_connection`
- **页面**
  - 仪表盘模块：`/app/modules/remote.html`
  - 独立页面：`/app/vnc.html`（noVNC + 连接/断开/冷却）

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
  - 获取同步状态
  - 设置同步状态（并更新时间）
- **后端接口**
  - `GET /api/sync/status`
  - `POST /api/sync/status`
- **数据落地**
  - SQLite 表：`sync_status`（固定 `id=1` 一条记录）
- **页面**
  - 仪表盘模块：`/app/modules/sync.html`

### 1.12 性能报告（汇总）

- **功能**
  - 输出一份“当前实时指标 + 历史事件计数”的快速报告
- **后端接口**
  - `GET /api/report/perf`
- **页面**
  - 仪表盘模块：`/app/modules/performance.html`

### 1.13 视频监控（浏览器摄像头预览/页面模块）

- **功能**
  - 当前实现主要是前端页面能力（调用浏览器 `getUserMedia` 进行视频预览）
- **后端接口**
  - 无强制依赖（以页面脚本为主）
- **页面**
  - 仪表盘模块：`/app/modules/video.html`

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
  - 涉及表：`recordings`、`logs`、`alerts`、`updates`、`sync_status`、`users`、`sessions`、`devices`、`user_stats`

### 2.3 业务接口层（Handlers）

- **`project/internal/handlers/auth.go`**

  - `/api/auth/*` 认证相关接口
  - 密码使用 bcrypt 保存
  - 登录后生成 Token 并写入 `sessions`
  - `cleanupExpiredSessions()` 定时清理过期会话
- **`project/internal/handlers/api.go`**

  - 注册主要业务接口 `RegisterRoutes()`
  - 监控指标快照/流、硬件信息、音频上传/列表/下载/删除
  - 告警列表/确认/新建
  - 日志追加/列表
  - 更新列表/新增/检查（示例返回）
  - 同步状态获取/设置
  - 性能报告汇总
  - VNC TCP 到 WebSocket 代理（`/api/vnc/ws`）
  - 设备管理（CRUD + 状态）
  - 用户统计（连接次数）

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

- **`modules/monitor.html`**：系统状态监控模块页面
- **`modules/audio.html`**：音频/语音交互记录模块页面
- **`modules/remote.html`**：远程桌面控制模块页面（通常与设备管理、连接入口相关）
- **`modules/video.html`**：视频监控模块页面（偏前端预览能力）
- **`modules/hardware.html`**：硬件信息模块页面
- **`modules/updates.html`**：软件更新管理模块页面
- **`modules/sync.html`**：数据同步状态模块页面
- **`modules/alerts.html`**：异常告警模块页面
- **`modules/performance.html`**：性能分析报告模块页面
- **`modules/logs.html`**：维护操作日志模块页面
- **`modules/common.js`**：模块页共用的请求/渲染辅助逻辑（供各模块复用）
- **`modules/common.css`**：模块页共用样式

### 3.3 远程桌面（noVNC）相关

- **`project/web/vnc.html`**

  - 独立的 VNC 远程桌面页面
  - 浏览器端加载 noVNC（CDN）
  - 连接时访问后端 WebSocket：`/api/vnc/ws?target=...`
  - 连接/断开做了前端冷却（避免频繁操作）
  - 连接成功后会触发：设备状态在线 + 用户连接次数自增
- **`project/web/vnc_simple.html`**

  - VNC 的简化版本页面（用于快速连接验证/演示）
- **`project/web/novnc-rfb.js`**

  - noVNC 相关脚本（本地版本文件）。
  - 说明：当前 `vnc.html` 主要通过 CDN 加载 noVNC；该文件可用于离线替代或历史遗留。

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
- **`智能工具人机交互与远程操控系统 *.docx/*.pdf/*.txt`**

  - 软件说明、源代码说明、立项申报书、功能特点等提交材料

---

## 6. 关键环境变量（后端）

- `SC_LISTEN_ADDR`：监听地址（默认 `:8080`）
- `SC_DB_PATH`：数据库路径（默认 `app.db`）
- `SC_API_TOKEN`：API 认证 Token（空字符串=关闭后端 API Token 校验；注意：与用户登录 Token 是两套机制）
- `SC_MAX_UPLOAD_MB`：上传大小限制（默认 64MB）
- `SC_THRESH_CPU` / `SC_THRESH_MEM` / `SC_THRESH_DISK`：阈值告警
- `SC_ALERT_INTERVAL_SEC`：阈值检测间隔（秒）

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
- **VNC 连接失败**

  - 后端：`project/internal/handlers/api.go`（`VNCProxyWS`）
  - 前端：`project/web/vnc.html`、`project/web/modules/remote.html`
  - 外部依赖：目标机 VNC 服务、端口 5900、防火墙/网络连通
- **音频上传/列表异常**

  - 后端：`project/internal/handlers/api.go`（`AudioUpload/AudioList/...`）
  - 前端：`project/web/modules/audio.html` 或 `project/web/script.js`
  - 数据：`recordings`、`project/data/recordings/`
