
# CloudControl

<div align="center">

**企业级智能管控平台 | 实时监控 | 远程控制 | 性能分析**

基于 Go 1.25 + Gin + MySQL + Redis + WebSocket + noVNC + RAG + RL 的完整解决方案

[![Go Version](https://img.shields.io/badge/Go-1.25+-00ADD8?style=flat&logo=go)](https://go.dev/)
[![License](https://img.shields.io/badge/License-MIT-green.svg)](LICENSE)

</div>

---

## 目录

- [项目简介](#项目简介)
- [运行与停止速查](#运行与停止速查)
- [快速开始](#快速开始)
- [系统访问地址](#系统访问地址)
- [环境变量配置](#环境变量配置)
- [22大功能模块](#22大功能模块)
- [API接口文档](#api接口文档)
- [项目结构](#项目结构)
- [部署指南](#部署指南)
  - [本地编译](#本地编译)
  - [Ubuntu 22 服务器部署（完整流程）](#ubuntu-22-服务器部署完整流程)
  - [自动更新脚本](#自动更新脚本)
  - [服务器运维速查](#服务器运维速查)
- [故障排查](#故障排查)
- [版本更新](#版本更新)
- [团队协作规范](#团队协作规范)

---

## 项目简介

CloudControl 是一套面向企业级应用的综合性管控平台。

**技术栈**：
- **后端**：Go + Gin，位于 `project/`，数据库已从 SQLite 迁移至 **MySQL**（Aiven 云数据库），通过环境变量 `SC_DB_DRIVER` 切换驱动，内置 SQL 方言自动适配层（`AdaptSQL`），共 34 张业务表
- **前端**：静态页面位于 `project/web/`，由后端内嵌静态资源并通过 `/app` 提供访问
- **时区**：统一为 **Asia/Shanghai (UTC+8)**，Go 后端 `time.Local` 与 MySQL 会话时区均已配置
- **Redis**：已集成 `go-redis/v9` 客户端库，通过 `.env` 配置连接（`REDIS_HOST` / `REDIS_PORT` / `REDIS_PASSWORD` / `REDIS_DB`）。用于 **会话缓存**（Token 验证快速路径）、**分布式限流**（替代进程内令牌桶）、**统计缓存同步**（6 类业务统计写入 Redis）、**通知未读数缓存**。Redis 不可用时自动降级为纯内存模式，不影响系统正常运行
- **RAG 知识库**：内置 BM25 关键词检索引擎（`internal/rag/rag.go`），知识库文档存储于数据库 `knowledge_docs` 表（首次运行自动从 `knowledge_base/` 目录导入 20 篇领域文档），为 AI 助手提供检索增强生成能力
- **全量数据库存储**：所有业务数据（备份 SQL、语音录音、仿真快照、RL 策略、知识库文档）均持久化至数据库，不再依赖本地文件系统，支持云端部署与多实例水平扩展

### 核心特性

- **安全认证**：用户注册/登录（bcrypt 加密）、会话管理（Token 24h 过期，Redis 缓存加速验证）、API Token 认证
- **性能优化**：数据库索引优化、分页支持、Redis 分布式限流（500次/分钟/IP，自动降级内存限流）、统计缓存 Redis 同步
- **安全防护**：CORS 跨域、SQL 注入防护、输入验证、Panic 自动恢复
- **实时监控**：CPU/内存/磁盘/网络监控、WebSocket 实时推送、阈值自动告警
- **智能能力**：LLM 航线规划 + NFZ 纠偏 + 多候选方案、CoT 思维链 AI 分析、智能巡检与通知中心、RAG 检索增强生成（BM25 + 20 篇知识库文档）
- **并发框架**：统一 Worker Pool（IO/CPU 双池）、优先级调度、超时控制、panic 恢复、DB 批写、WS 节流、统计缓存
- **仿真与强化学习**：多实例无人机仿真引擎、任务模板（巡逻/巡检/配送/测绘/搜救）、碰撞规避、禁飞区地理围栏检测、Leaflet 实时地图（贝塞尔曲线轨迹）、RL 训练评估与策略导出
- **全量 DB 持久化**：备份 SQL 内容、语音二进制、仿真快照、RL 策略、知识库文档全部存储于数据库，消除本地文件依赖

---

## 阶段实施状态（5-8）

- **阶段 5（并发能力建设）**：已完成。`internal/taskpool/pool.go` 已提供统一并发执行模型（IO/CPU 双池、优先级、超时、恢复、指标）。
- **阶段 6（并发应用落地）**：已完成。
  - 模拟机遥测 DB 写入通过 TaskPool 异步执行（`SimTelemetryPusher.OnTelemetry` → IO pool）。
  - LLM 航线规划通过 CPU pool 异步执行（`FlightPlanCreate` → `route_planning` 组）。
  - 监控/告警/巡检后台并发化、统计缓存异步刷新、WebSocket 节流、DB 批写均已接入。
  - 并发压测：`pool_test.go` 包含 9 个测试（5000 并发提交、混合 IO+CPU+Periodic 压力测试、benchmark），全部通过。
- **阶段 7（模拟无人机群）**：已完成。支持多实例批次管理、异常注入、7x24 连续仿真、RL 训练评估。
- **阶段 8（统计与可视化）**：已完成。`/api/sim/stats` 与 `simulation.html` 看板已联动，支持实时指标、趋势图、分布图、RL 曲线。

### RL 对实机决策的价值说明

- 当前 RL 模块可作为**实机航线规划/控制策略的先验决策器**：用于筛选策略方向、验证奖励设计、提前暴露风险动作。
- 但当前仍属于 **sim-to-real 过渡阶段**，不建议直接无保护上机。
- 实机部署前建议增加：

  1. 真实飞控约束映射（速度/高度/转向边界与机型参数绑定）
  2. 域随机化与参数辨识（风场、传感噪声、载重、电池衰减）
  3. HIL/SIL 分层验证与安全包线（地理围栏、失联返航、动作熔断）
  4. 人在回路审批（策略建议 -> 人工确认 -> 执行）

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
go version   # 建议 Go 1.25+
go mod tidy
```

### 2. 配置环境变量

项目启动时自动读取 `project/.env`，无则使用系统环境变量和内置默认值。

示例 `.env`（MySQL + Redis，当前默认）：

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
REDIS_HOST=127.0.0.1
REDIS_PORT=6379
REDIS_PASSWORD=
REDIS_DB=0
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
3. 左侧导航切换 22 大功能模块

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

| 变量名 | 默认值 | 说明 |
|--------|--------|------|
| `LLM_API_KEY` | `""` | 大模型 API Key（留空降级为直线规划） |
| `LLM_BASE_URL` | `https://openapi.monica.im/v1` | 大模型 API Base URL |
| `LLM_MODEL` | `gpt-4.1` | 大模型名称 |
| `LLM_RPM` | `30` | LLM 请求速率限制（次/分钟），令牌桶限流 |
| `LLM_TIMEOUT_SEC` | `60` | LLM 单次请求超时（秒） |
| `AMAP_KEY` | `""` | 高德地图 Key（地址解析/逆编码） |
| `TIANDITU_KEY` | `""` | 天地图 Key（影像底图 + 标注图层） |

### Redis 配置

| 变量名 | 默认值 | 说明 |
|--------|--------|------|
| `REDIS_HOST` | `""` | Redis 服务器地址（留空则禁用 Redis，降级为纯内存模式） |
| `REDIS_PORT` | `6379` | Redis 端口 |
| `REDIS_PASSWORD` | `""` | Redis 密码 |
| `REDIS_DB` | `0` | Redis 数据库编号 |

### MAVLink Bridge 配置

| 变量名 | 默认值 | 说明 |
|--------|--------|------|
| `MAVLINK_TCP_PORT` | `9080` | MAVLink Bridge 监听端口（Java 服务） |
| `MAVLINK_MYSQL_URL` | 自动拼接 | JDBC 连接字符串（默认从 MYSQL_* 变量拼接） |

> **MAVLink 集成说明**：系统通过两层架构处理 MAVLink 数据：
> 1. **Go TCP 网关**（端口 9080）：自动检测 MAVLink 二进制流（0xFE/0xFD 魔数），记录帧概要到 `device_tcp_log`
> 2. **Java MavlinkBridge**（`project/cmd/mavlink-bridge/`）：使用 `mavlink.jar` 完整解析 15+ MAVLink 报文，写入 `mavlink_telemetry` 表 + Redis 实时缓存
>
> 启动 Java 服务：`cd project/cmd/mavlink-bridge && run.bat`（Windows）或 `./run.sh`（Linux），需要 JDK 11+

> **Redis 集成说明**：已通过 `go-redis/v9` 接入，启动时自动连接。当前用于 4 个场景：
> 1. **会话缓存**：Token 验证优先查 Redis（`session:<token>`），命中直接返回，未命中回退 MySQL 并回填缓存，TTL 随会话过期时间
> 2. **分布式限流**：基于 `INCR + EXPIRE` 的滑动窗口计数器（`ratelimit:<ip>`），替代进程内令牌桶，支持多实例部署
> 3. **统计缓存同步**：6 类业务统计（无人机/GPS/电池/告警/硬件/飞行任务）每次刷新同步写入 Redis（`stats:<key>`），读取时优先 Redis
> 4. **通知未读数缓存**：`notif:unread_count` 缓存 30s，创建/已读/全部已读时自动失效
>
> **降级策略**：`REDIS_HOST` 为空或 Redis 不可达时，所有功能自动降级为纯内存模式，不影响系统正常运行。

---

## 22大功能模块

| # | 模块名称 | 功能说明 |
|---|----------|----------|
| 1 | 无人机管理 | 注册/编辑/删除/连接（SSH/VNC/RDP）、状态监控、视频流配置 |
| 2 | GPS/位置信息 | 实时位置、Leaflet 地图、电子围栏、历史轨迹、WebSocket 推送 |
| 3 | 电池监控 | 电量/电压/温度/健康度、自动报警、历史趋势、WebSocket 推送 |
| 4 | 飞行任务管理 | 两步向导（航线规划+任务配置）、LLM 智能规划、动作点选/间隔执行、动作执行反馈、飞行阶段状态机、AI 思维链分析 |
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
| 18 | 备份与数据回滚 | 自动/手动备份（SQL 内容存储于数据库）、数据恢复与回滚、备份记录管理、恢复进度追踪、备份文件上传恢复 |
| 19 | AI 智能助手 | 右下角浮窗、多轮对话、快捷指令、知识库问答、模块跳转、数据查询 |
| 20 | 消息通知中心 | 右上角铃铛、未读计数、类型筛选、AI 定时巡检、点击跳转、批量已读 |
| 21 | 并发任务监控 | 统一 Worker Pool 指标、IO/CPU 池状态、任务组分布、完成/失败趋势图、自动刷新 |
| 22 | 仿真模拟引擎 | 批次创建/启停/删除、异常注入、任务模板、碰撞规避、禁飞区围栏检测、贝塞尔曲线轨迹、实时地图、RL 训练与策略导出 |
| 23 | MAVLink 遥测 | 通过 mavlink.jar 解析 TCP 9080 端口 MAVLink v1/v2 二进制协议，支持 HEARTBEAT/GPS/ATTITUDE/BATTERY 等 15+ 报文解析，数据写入 MySQL + Redis 实时缓存，前端遥测仪表盘（SSE 推送）、高度/速度趋势图、状态消息日志 |

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
| 备份 | DELETE | `/api/backup/:id` | 删除备份记录 |
| 备份 | GET | `/api/backup/download/:id` | 下载备份 SQL 内容 |
| 备份 | POST | `/api/backup/auto-toggle` | 开关自动备份 |
| 备份 | POST | `/api/backup/cleanup` | 清理旧备份（保留最近 N 份） |
| 通知 | GET | `/api/notifications` | 通知列表（支持类型筛选） |
| 通知 | POST | `/api/notifications/read/:id` | 标记通知已读 |
| 通知 | POST | `/api/notifications/read-all` | 全部标记已读 |
| AI助手 | POST | `/api/ai-assistant/chat` | AI 助手对话 |
| AI助手 | GET | `/api/ai-assistant/suggestions` | 获取快捷指令建议 |
| 并发池 | GET | `/api/taskpool/metrics` | 任务池指标快照（workers/队列/任务组统计） |
| 并发池 | GET | `/api/taskpool/llm-rpm` | LLM RPM 限流指标（当前/上限/失败/平均延迟） |
| 统计缓存 | GET | `/api/stats/cached` | 聚合统计缓存（6 类业务数据 + pool 指标） |
| RAG | GET | `/api/ai/rag/search?q=xxx` | 知识库检索（BM25 关键词匹配） |
| RAG | GET | `/api/ai/rag/stats` | 知识库状态（chunk 数量/文档数） |
| 健康检查 | GET | `/api/healthz` | 系统健康检查（db/redis/llm/patrol/uptime） |
| 仿真 | GET | `/api/sim/metrics` | 仿真引擎运行指标 |
| 仿真 | POST | `/api/sim/batches` | 创建仿真批次（支持 mission 模板） |
| 仿真 | GET | `/api/sim/instances` | 仿真实例列表 |
| 仿真 | POST | `/api/sim/instances/:id/anomaly` | 单实例异常注入 |
| 仿真 | WS | `/api/sim/stream` | 仿真实时事件流 |
| 仿真 | GET | `/api/sim/stats` | 仿真统计看板数据（趋势/占比/学习曲线） |
| 强化学习 | POST | `/api/sim/rl/start` | 启动 RL 训练 |
| 强化学习 | GET | `/api/sim/rl/status` | 获取训练状态与指标 |
| 强化学习 | GET | `/api/sim/rl/history` | 获取持久化训练历史 |
| 强化学习 | GET | `/api/sim/rl/export-policy` | 导出策略摘要（实机部署参考） |
| MAVLink | GET | `/api/mavlink/drones` | MAVLink 无人机列表（含遥测汇总） |
| MAVLink | GET | `/api/mavlink/drones/:sysid/telemetry` | 指定无人机全部遥测（Redis 优先） |
| MAVLink | GET | `/api/mavlink/drones/:sysid/position` | 实时位置（经纬度/高度/速度） |
| MAVLink | GET | `/api/mavlink/drones/:sysid/attitude` | 实时姿态（横滚/俯仰/偏航） |
| MAVLink | GET | `/api/mavlink/drones/:sysid/battery` | 实时电池（电压/电流/剩余/温度） |
| MAVLink | GET | `/api/mavlink/drones/:sysid/status` | 组合状态视图 |
| MAVLink | GET | `/api/mavlink/messages` | 状态消息日志（分页/过滤） |
| MAVLink | GET | `/api/mavlink/overview` | 概览统计（在线/告警） |
| MAVLink | SSE | `/api/mavlink/stream?sys_id=N` | 实时遥测推送（500ms 间隔） |

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
│   ├── agent/main.go               # hw-agent 独立程序（地面站 MAVLink 中继）
│   └── mavlink-bridge/             # MAVLink 二进制协议解析服务（Java）
│       ├── MavlinkBridge.java      # TCP 服务，使用 mavlink.jar 解析 MAVLink 报文，写入 MySQL + Redis
│       ├── run.bat / run.sh        # 一键编译启动脚本（自动下载 MySQL JDBC 驱动）
│       └── classes/                # 编译输出目录
├── internal/
│   ├── agent/agent.go              # 内嵌 Agent（主服务自动启动）
│   ├── cache/
│   │   └── redis.go                # Redis 客户端封装（连接池、KV/JSON/Hash 操作、健康检查、优雅降级）
│   ├── db/
│   │   ├── db.go                   # 数据库连接、SQL 兼容层（AdaptSQL）、DB/Tx 包装器
│   │   ├── migrate_mysql.go        # MySQL 建表 DDL（36 张表，含 mavlink_telemetry / mavlink_message_log）
│   │   └── migrate_sqlite.go       # SQLite 建表 DDL
│   ├── taskpool/                    # 统一并发框架
│   │   ├── pool.go                 # Worker Pool 引擎（IO/CPU 双池、优先级、超时、panic 恢复、周期调度、LLM RPM 限流、指标）
│   │   └── batcher.go              # 辅助工具（WriteBatcher / Throttler / StatsCache）
│   ├── handlers/                    # API 处理函数
│   │   ├── api.go                  # 通用 API（设备、硬件、报警、日志、更新、同步等）
│   │   ├── auth.go                 # 用户认证（注册/登录/登出/Token 验证）
│   │   ├── drones.go               # 无人机管理
│   │   ├── gps.go                  # GPS/位置信息（已接入批写 + WS 节流）
│   │   ├── battery.go              # 电池监控（已接入批写 + WS 节流）
│   │   ├── flight.go               # 飞行任务管理
│   │   ├── flight_plan.go          # 智能航线规划（LLM + AMap）
│   │   ├── noflyzone.go            # 禁飞区管理
│   │   ├── cot.go                  # 思维链 AI 分析
│   │   ├── backup.go               # 备份与数据回滚（自动/手动/恢复/进度追踪，SQL 内容存储于 DB）
│   │   ├── ai_assistant.go         # AI 智能助手（多轮对话/知识库/快捷指令/RAG 集成）
│   │   ├── notification.go         # 消息通知中心
│   │   ├── patrol.go               # AI 定时巡检（含离线健康检查 + 通知冷却抑制）
│   │   ├── pool_integration.go     # 并发整合层（批写/节流/统计缓存/任务提交辅助）
│   │   ├── taskpool_api.go         # 并发池指标 API（/api/taskpool/metrics + /api/taskpool/llm-rpm）
│   │   ├── rag_endpoints.go        # RAG 知识库查询 API（/api/ai/rag/*）
│   │   ├── rag_integration.go      # RAG 与 AI 助手集成层（从 knowledge_docs 表加载知识库）
│   │   ├── simulation.go           # 仿真引擎 API（批次/实例/异常/RL）
│   │   ├── mavlink_api.go          # MAVLink 遥测 API（无人机列表/遥测/位置/姿态/电池/SSE 流）
│   │   ├── sim_integration.go      # 仿真引擎与 DB/WS 集成（快照/策略通过 data_store 表持久化）
│   │   └── wshub.go                # WebSocket 事件广播
│   ├── llm/
│   │   ├── llm.go                  # LLM 大模型调用封装（含 RPM 令牌桶限流）
│   │   └── reasoning.go            # LLM 推理链解析
│   ├── rag/rag.go                  # RAG 检索引擎（BM25 关键词检索 + 倒排索引 + 批量加载 LoadTexts）
│   ├── cot/cot.go                  # CoT 思维链推理模块
│   ├── amap/amap.go                # 高德地图 API 封装
│   ├── middleware/                  # 中间件（认证、限流、日志）
│   ├── monitor/monitor.go          # 系统指标采集
│   ├── syncengine/engine.go        # 数据同步引擎（18 张表）
│   ├── utils/                       # 通用工具函数
│   └── simulation/                 # 仿真与强化学习引擎
│       ├── engine.go               # 多实例仿真引擎（批次管理+碰撞规避+禁飞区围栏+DB快照回调）
│       ├── instance.go             # 单机仿真状态机与动作执行（含低电量自动检测）
│       ├── rl.go                   # Q-learning 训练器与经验回放（11 维状态空间+DB策略回调）
│       └── types.go                # 仿真核心数据结构与任务类型
├── web/
│   ├── index.html                  # 系统首页
│   ├── dashboard.html              # 仪表盘导航（含侧边栏滚动）
│   ├── vnc.html / ssh.html         # VNC/SSH 客户端页面
│   └── modules/                    # 22 个功能模块页面
│       ├── drones.html / gps.html / battery.html / flight.html / mavlink.html
│       ├── noflyzone.html / cot.html / hardware.html / remote.html
│       ├── video.html / monitor.html / alerts.html / logs.html
│       ├── audio.html / updates.html / sync.html / performance.html
│       ├── backup.html             # 备份与数据回滚管理
│       ├── concurrency.html        # 并发任务监控仪表盘
│       ├── simulation.html         # 仿真模拟引擎（地图 + RL）
│       ├── curve-utils.js          # 贝塞尔/Catmull-Rom 曲线轨迹工具库
│       ├── ai-assistant.js         # 右下角 AI 智能助手浮窗
│       ├── notification-bell.js    # 右上角消息通知铃铛
│       └── common.js / common.css  # 公共工具函数和样式
├── knowledge_base/                    # RAG 知识库种子文档（20 篇 .md，首次运行自动导入 DB）
│   ├── platform_overview.md           # 平台总览
│   ├── drone_operations.md            # 无人机操作指南
│   ├── flight_planning.md             # 航线规划知识
│   ├── battery_safety.md              # 电池安全规范
│   ├── simulation_guide.md            # 仿真引擎使用指南
│   ├── rl_policy_guide.md             # 强化学习策略指南
│   ├── emergency_procedures.md        # 应急处理流程
│   └── ...                            # 其余 13 篇领域文档
└── data/                              # （历史目录，数据已全量迁移至数据库）
    └── README                         # 所有数据现已存储于 DB（knowledge_docs / data_store / backup_records / recordings）
```

---

## 部署指南

### 本地编译

```bash
cd project
go mod tidy
go build -o sc.exe .     # Windows
go build -o sc .          # Linux
```

部署时保留：可执行文件 + `.env`（静态页面已 embed 打入二进制，所有数据存储于数据库）。

### Windows 交叉编译 Linux 二进制

```powershell
$env:GOOS="linux"; $env:GOARCH="amd64"; go build -o sc .
```

---

### Ubuntu 22 服务器部署（完整流程）

> 以下以 `root` 用户、项目目录 `~/yyzk/Intelligent-Tool-Human-Computer-Interaction-and-Remote-Control-System` 为例。
> 若使用非 root 用户，将所有 `/root/` 替换为 `$HOME` 路径，并将 systemd 中 `User=root` 改为对应用户名。

#### 前置条件

| 依赖 | 要求 |
|------|------|
| OS | Ubuntu 22.04 LTS |
| Go | 1.25+（从官方 tarball 安装） |
| MySQL | 已运行，数据库已创建 |
| Redis | 已运行 |
| Nginx | apt 安装 |
| 域名 | 已添加至 Cloudflare（DNS A 记录指向服务器 IP，开启代理橙色云） |

#### 1. 安装 Go

```bash
wget https://go.dev/dl/go1.25.0.linux-amd64.tar.gz
sudo rm -rf /usr/local/go
sudo tar -C /usr/local -xzf go1.25.0.linux-amd64.tar.gz
rm go1.25.0.linux-amd64.tar.gz

echo 'export PATH=$PATH:/usr/local/go/bin' | sudo tee /etc/profile.d/go.sh
source /etc/profile.d/go.sh
go version   # 确认 go1.25.0
```

#### 2. 安装 Nginx

```bash
sudo apt update && sudo apt install -y nginx git
```

#### 3. 配置 GitHub SSH Key（私有仓库）

```bash
ssh-keygen -t ed25519 -C "deploy-server" -f ~/.ssh/github_deploy -N ""
cat ~/.ssh/github_deploy.pub
# → 复制输出，到 GitHub → Settings → SSH and GPG keys → New SSH key 粘贴

cat >> ~/.ssh/config << 'EOF'
Host github.com
    HostName github.com
    User git
    IdentityFile ~/.ssh/github_deploy
    StrictHostKeyChecking accept-new
EOF

ssh -T git@github.com   # 测试连通
```

#### 4. 克隆仓库 & 编译

```bash
mkdir -p ~/yyzk && cd ~/yyzk
git clone git@github.com:wsx-oss/Intelligent-Tool-Human-Computer-Interaction-and-Remote-Control-System.git
cd Intelligent-Tool-Human-Computer-Interaction-and-Remote-Control-System/project

CGO_ENABLED=0 go build -o smartcontrol main.go
echo "编译完成: $(ls -lh smartcontrol)"
```

#### 5. 创建 systemd 服务

创建 `/etc/systemd/system/yyzk.service`：

```ini
[Unit]
Description=云翼智控 SmartControl Server
After=network.target mysql.service redis.service
Wants=mysql.service redis.service

[Service]
Type=simple
User=root
WorkingDirectory=/root/yyzk/Intelligent-Tool-Human-Computer-Interaction-and-Remote-Control-System/project
EnvironmentFile=-/root/yyzk/Intelligent-Tool-Human-Computer-Interaction-and-Remote-Control-System/project/.env
ExecStart=/root/yyzk/Intelligent-Tool-Human-Computer-Interaction-and-Remote-Control-System/project/smartcontrol
Restart=on-failure
RestartSec=5
LimitNOFILE=65536
KillSignal=SIGTERM
TimeoutStopSec=15

[Install]
WantedBy=multi-user.target
```

```bash
sudo systemctl daemon-reload
sudo systemctl enable yyzk
sudo systemctl start yyzk

# 验证
sudo systemctl status yyzk
curl -s http://127.0.0.1:8080/api/healthz
```

#### 6. Nginx 反向代理 + Cloudflare Origin Certificate

**6.1 生成 Cloudflare Origin 证书**

1. Cloudflare Dashboard → 域名 → **SSL/TLS → Origin Server → Create Certificate**
2. 保持默认（RSA, 15 年），点 Create
3. 将证书和私钥保存到服务器：

```bash
sudo mkdir -p /etc/ssl/cloudflare
sudo nano /etc/ssl/cloudflare/origin.pem      # 粘贴 Origin Certificate
sudo nano /etc/ssl/cloudflare/origin.key       # 粘贴 Private Key
sudo chmod 600 /etc/ssl/cloudflare/origin.key
```

**6.2 Cloudflare 设置**

- **DNS → Records**：A 记录 `@` 和 `www` 指向服务器 IP，**Proxy 开启**（橙色云）
- **SSL/TLS → Overview**：设为 **Full (strict)**

**6.3 Nginx 配置**

创建 `/etc/nginx/sites-available/yyzk`：

```nginx
server {
    listen 80;
    server_name yyzk.online www.yyzk.online;
    return 301 https://$host$request_uri;
}

server {
    listen 443 ssl http2;
    server_name yyzk.online www.yyzk.online;

    ssl_certificate     /etc/ssl/cloudflare/origin.pem;
    ssl_certificate_key /etc/ssl/cloudflare/origin.key;
    ssl_protocols       TLSv1.2 TLSv1.3;
    ssl_ciphers         HIGH:!aNULL:!MD5;

    # Cloudflare Real-IP 还原
    set_real_ip_from 173.245.48.0/20;
    set_real_ip_from 103.21.244.0/22;
    set_real_ip_from 103.22.200.0/22;
    set_real_ip_from 103.31.4.0/22;
    set_real_ip_from 141.101.64.0/18;
    set_real_ip_from 108.162.192.0/18;
    set_real_ip_from 190.93.240.0/20;
    set_real_ip_from 188.114.96.0/20;
    set_real_ip_from 197.234.240.0/22;
    set_real_ip_from 198.41.128.0/17;
    set_real_ip_from 162.158.0.0/15;
    set_real_ip_from 104.16.0.0/13;
    set_real_ip_from 104.24.0.0/14;
    set_real_ip_from 172.64.0.0/13;
    set_real_ip_from 131.0.72.0/22;
    real_ip_header CF-Connecting-IP;

    client_max_body_size 64m;

    location / {
        proxy_pass http://127.0.0.1:8080;
        proxy_http_version 1.1;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;

        # WebSocket 支持（VNC / SSH / metrics stream 等）
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection "upgrade";
        proxy_read_timeout 86400s;
        proxy_send_timeout 86400s;
    }
}
```

```bash
sudo ln -sf /etc/nginx/sites-available/yyzk /etc/nginx/sites-enabled/
sudo rm -f /etc/nginx/sites-enabled/default
sudo nginx -t && sudo systemctl reload nginx
```

#### 7. 防火墙

```bash
sudo ufw allow 80/tcp
sudo ufw allow 443/tcp
sudo ufw allow 22/tcp
sudo ufw --force enable
```

#### 8. 验证部署

```bash
curl -s http://127.0.0.1:8080/api/healthz          # 本地
curl -s https://yyzk.online/api/healthz             # 外网（通过 Cloudflare）
```

---

### 自动更新脚本

脚本位于服务器 `~/yyzk/update.sh`，通过 crontab 每小时自动检查 GitHub 是否有新提交，有则自动拉取、编译、重启。

#### 核心流程

1. `git fetch` 检查远端是否有新提交，无更新直接退出
2. 备份 `project/knowledge_base/` 和 `project/data/`（防止运行时数据被覆盖）
3. `git pull --ff-only` 拉取最新代码
4. 恢复备份的 `knowledge_base/` 和 `data/`
5. 编译新二进制到临时文件 `smartcontrol.new`
6. 编译失败则不动现有服务，旧版本继续运行
7. 编译成功后保留旧二进制为 `smartcontrol.rollback`，替换并重启服务
8. 健康检查（`/api/healthz`，兼容 429 限流响应），失败则自动回滚

#### 脚本完整内容

创建 `~/yyzk/update.sh`：

```bash
#!/usr/bin/env bash
set -Eeuo pipefail

REPO_DIR="$HOME/yyzk/Intelligent-Tool-Human-Computer-Interaction-and-Remote-Control-System"
PROJECT_DIR="$REPO_DIR/project"
BINARY_NAME="smartcontrol"
SERVICE_NAME="yyzk"

LOG_DIR="$HOME/yyzk"
LOG_FILE="$LOG_DIR/update.log"
LOCK_FILE="$LOG_DIR/update.lock"
BACKUP_DIR="$HOME/yyzk/backups"

MAX_BACKUPS=5
HEALTH_URL="http://127.0.0.1:8080/api/healthz"

mkdir -p "$LOG_DIR" "$BACKUP_DIR"

# 文件锁，防止多个实例同时执行
exec 9>"$LOCK_FILE"
if ! flock -n 9; then
    echo "[$(date '+%Y-%m-%d %H:%M:%S')] 已有更新任务在运行，跳过" >> "$LOG_FILE"
    exit 0
fi

# 日志统一写入 LOG_FILE（crontab 不要再用 >> 重定向）
exec >> "$LOG_FILE" 2>&1

log() {
    echo "[$(date '+%Y-%m-%d %H:%M:%S')] $*"
}

rollback() {
    log "开始回滚..."
    if [ -f "$PROJECT_DIR/${BINARY_NAME}.rollback" ]; then
        mv -f "$PROJECT_DIR/${BINARY_NAME}.rollback" "$PROJECT_DIR/$BINARY_NAME"
        chmod +x "$PROJECT_DIR/$BINARY_NAME"
        systemctl restart "$SERVICE_NAME" || true
        sleep 3
        if systemctl is-active --quiet "$SERVICE_NAME"; then
            log "已回滚到上一版本，服务已恢复"
        else
            log "回滚后服务仍未恢复，请手动排查"
        fi
    else
        log "未找到 ${BINARY_NAME}.rollback，无法自动回滚"
    fi
}

check_service_healthy() {
    local code
    code="$(curl -s -o /dev/null -m 5 -w '%{http_code}' "$HEALTH_URL" || true)"
    case "$code" in
        200) return 0 ;;
        429) log "健康检查返回 429（限流），视为存活"; return 0 ;;
    esac
    # HTTP 检查不通过时，兜底看 systemd 状态
    if systemctl is-active --quiet "$SERVICE_NAME"; then
        log "HTTP 返回 $code，但 systemd 显示服务在运行，视为存活"
        return 0
    fi
    return 1
}

get_remote_ref() {
    if git show-ref --verify --quiet refs/remotes/origin/main; then
        echo "origin/main"; return 0
    fi
    if git show-ref --verify --quiet refs/remotes/origin/master; then
        echo "origin/master"; return 0
    fi
    return 1
}

get_local_branch() {
    git symbolic-ref --quiet --short HEAD 2>/dev/null || echo "main"
}

# ===== 主流程 =====
log "===== 开始检查更新 ====="

cd "$REPO_DIR"
git fetch --prune origin >/dev/null 2>&1

REMOTE_REF="$(get_remote_ref || true)"
if [ -z "${REMOTE_REF:-}" ]; then
    log "无法找到 origin/main 或 origin/master"; exit 1
fi

LOCAL_HEAD="$(git rev-parse HEAD)"
REMOTE_HEAD="$(git rev-parse "$REMOTE_REF")"

if [ "$LOCAL_HEAD" = "$REMOTE_HEAD" ]; then
    log "无更新 (HEAD=$LOCAL_HEAD), 跳过"; exit 0
fi

log "检测到更新: $LOCAL_HEAD → $REMOTE_HEAD"

# 备份运行时数据
TIMESTAMP="$(date '+%Y%m%d_%H%M%S')"
SNAP_DIR="$BACKUP_DIR/$TIMESTAMP"
mkdir -p "$SNAP_DIR"
[ -d "$PROJECT_DIR/knowledge_base" ] && cp -a "$PROJECT_DIR/knowledge_base" "$SNAP_DIR/" && log "已备份 knowledge_base/"
[ -d "$PROJECT_DIR/data" ] && cp -a "$PROJECT_DIR/data" "$SNAP_DIR/" && log "已备份 data/"

# 拉取代码
CURRENT_BRANCH="$(get_local_branch)"
git pull --ff-only origin "$CURRENT_BRANCH"
log "git pull 完成"

# 恢复运行时数据
[ -d "$SNAP_DIR/knowledge_base" ] && rm -rf "$PROJECT_DIR/knowledge_base" && cp -a "$SNAP_DIR/knowledge_base" "$PROJECT_DIR/" && log "已恢复 knowledge_base/"
[ -d "$SNAP_DIR/data" ] && rm -rf "$PROJECT_DIR/data" && cp -a "$SNAP_DIR/data" "$PROJECT_DIR/" && log "已恢复 data/"

# 编译
cd "$PROJECT_DIR"
log "开始编译..."
if ! CGO_ENABLED=0 /usr/local/go/bin/go build -o "${BINARY_NAME}.new" main.go; then
    log "编译失败，旧版本继续运行"; rm -f "${BINARY_NAME}.new"; exit 1
fi
chmod +x "${BINARY_NAME}.new"
log "编译成功: $(ls -lh "${BINARY_NAME}.new")"

# 替换二进制（保留回滚副本）
[ -f "$BINARY_NAME" ] && cp -f "$BINARY_NAME" "${BINARY_NAME}.rollback"
mv -f "${BINARY_NAME}.new" "$BINARY_NAME"
chmod +x "$BINARY_NAME"

# 重启服务
log "重启服务..."
if ! systemctl restart "$SERVICE_NAME"; then
    log "systemctl restart 失败，回滚"; rollback; exit 1
fi

# 健康检查（最多等 23 秒）
sleep 3
HEALTHY=false
for i in $(seq 1 10); do
    if check_service_healthy; then HEALTHY=true; break; fi
    sleep 2
done

if [ "$HEALTHY" != "true" ]; then
    log "健康检查失败，回滚..."; rollback; exit 1
fi

log "新版本部署成功: $(git rev-parse --short HEAD)"

# 清理旧备份（只保留最近 N 份）
find "$BACKUP_DIR" -mindepth 1 -maxdepth 1 -type d | sort -r | tail -n +$((MAX_BACKUPS + 1)) | xargs -r rm -rf

log "===== 更新流程完成 ====="
```

```bash
chmod +x ~/yyzk/update.sh
```

#### 设置 Crontab

```bash
crontab -e
# 添加以下一行（每小时第 0 分钟执行，日志由脚本自行管理，不要加 >> 重定向）
0 * * * * /bin/bash /root/yyzk/update.sh
```

> **注意**：如果使用非 root 用户，需配置 sudoers 免密重启服务：
> ```bash
> echo "用户名 ALL=(ALL) NOPASSWD: /bin/systemctl restart yyzk, /bin/systemctl stop yyzk, /bin/systemctl start yyzk" | sudo tee /etc/sudoers.d/yyzk-deploy
> ```

---

### 服务器运维速查

#### 服务管理

```bash
sudo systemctl status yyzk          # 查看服务状态
sudo systemctl start yyzk           # 启动
sudo systemctl stop yyzk            # 停止
sudo systemctl restart yyzk         # 重启
```

#### 查看日志

```bash
# 应用实时日志（systemd journal）
sudo journalctl -u yyzk -f

# 应用最近 200 行日志
sudo journalctl -u yyzk --no-pager -n 200

# 自动更新脚本日志
tail -50 ~/yyzk/update.log

# Nginx 访问/错误日志
tail -f /var/log/nginx/access.log
tail -f /var/log/nginx/error.log
```

#### 手动触发更新

```bash
bash ~/yyzk/update.sh
tail -50 ~/yyzk/update.log          # 查看结果
```

#### 手动回滚

```bash
cd ~/yyzk/Intelligent-Tool-Human-Computer-Interaction-and-Remote-Control-System/project
mv smartcontrol.rollback smartcontrol
sudo systemctl restart yyzk
```

#### 健康检查

```bash
curl -s http://127.0.0.1:8080/api/healthz | python3 -m json.tool
```

返回示例：

```json
{
    "status": "ok",
    "uptime_s": 3600,
    "db_reachable": true,
    "redis_reachable": true,
    "llm_reachable": true,
    "patrol_online": true
}
```

#### 服务器目录结构

```text
~/yyzk/
├── Intelligent-Tool-Human-Computer-Interaction-and-Remote-Control-System/   # Git 仓库
│   └── project/
│       ├── smartcontrol              # 当前运行的二进制
│       ├── smartcontrol.rollback     # 上一版本（用于回滚）
│       ├── .env                      # 环境变量（随仓库分发）
│       ├── knowledge_base/           # RAG 知识库（运行时数据，更新时自动保护）
│       └── data/                     # 备份/录音/RL策略/仿真快照（运行时数据，更新时自动保护）
├── update.sh                         # 自动更新脚本
├── update.log                        # 更新日志
├── update.lock                       # 执行锁（防并发）
└── backups/                          # 更新前的 knowledge_base/ 和 data/ 快照（保留最近 5 份）
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

### v4.0.0 (最新)

- **全量数据迁移至数据库**：消除对本地 `knowledge_base/` 和 `data/` 目录的运行时依赖
  - 新增 `knowledge_docs` 表：RAG 知识库文档存储于 DB，首次运行自动从 `knowledge_base/` 目录种子导入，后续从 DB 加载
  - 新增 `data_store` 表：通用键值存储，用于仿真快照（`sim_snapshots`）和 RL 策略（`rl_policy`）持久化
  - `backup_records` 新增 `sql_content LONGTEXT` 列：备份 SQL 内容直接存储于数据库，不再生成本地文件
  - `recordings` 新增 `file_data LONGBLOB` 列：语音录音二进制直接存储于数据库，上传/下载/删除全部走 DB
  - `InitSharedRAG(db)` 签名变更：接受数据库实例参数，支持 DB 加载 + 文件系统种子
  - 仿真引擎新增 `SaveSnapshotFunc` / `LoadSnapshotFunc` 回调：快照持久化由文件切换为 DB
  - RL 训练器新增 `SavePolicyFunc` / `LoadPolicyFunc` 回调：策略持久化由文件切换为 DB
  - 备份模块移除 `backupDir` 字段和所有文件 I/O，`dumpToFile` → `dumpToString`
  - 录音模块移除 `saveUploadedFile` 辅助函数，上传直写 DB BLOB
  - 数据库表总数从 32 张扩展至 34 张
  - 所有变更保持 SQLite / MySQL 双驱动兼容（`AdaptSQL` 自动处理 `INSERT OR REPLACE` → `REPLACE INTO`）

### v3.9.0

- Redis 缓存集成（MySQL + Redis 双层架构）
  - 新增 `internal/cache/redis.go`：`go-redis/v9` 客户端封装，连接池（20 连接 / 5 空闲）、KV/JSON/Hash 操作、健康检查、优雅降级
  - 会话缓存加速：`auth.go` Token 验证优先查 Redis（`session:<token>`），命中直接返回，未命中回退 MySQL 并回填，TTL 随会话过期
  - 分布式限流：`ratelimit.go` 基于 Redis `INCR + EXPIRE` 滑动窗口计数器（`ratelimit:<ip>`），支持多实例部署，Redis 不可用自动降级内存限流
  - 统计缓存同步：`pool_integration.go` 6 类业务统计（无人机/GPS/电池/告警/硬件/飞行任务）每次刷新同步写入 Redis（`stats:<key>`），`/api/stats/cached` 优先读 Redis
  - 通知未读数缓存：`notification.go` 缓存 `notif:unread_count`（30s TTL），创建/已读/全部已读时自动失效
  - `/api/healthz` 新增 `redis_reachable` 字段
  - 全局降级策略：`REDIS_HOST` 为空或 Redis 不可达时，所有功能自动降级为纯内存模式

### v3.8.0

- 离线状态通知抑制
  - `patrol.go` 无人机下线通知 5 分钟/台冷却期，防止通知风暴
  - 巡检前预检 DB ping + LLM HEAD，离线时跳过巡检不生成通知
  - `notification-bell.js` 检测 `navigator.onLine` + fetch 失败计数，显示离线 badge，暂停轮询
  - `/api/healthz` 增强：返回 `db_reachable`、`llm_reachable`、`patrol_online`、`uptime_s`
- 并发任务 RPM 优化
  - `llm.go` 新增 `golang.org/x/time/rate` 令牌桶限流器，按 `LLM_RPM`（默认 30）控制
  - `concurrency.html` 新增 LLM RPM 监控卡片（当前分钟/上限/排队/平均延迟）
  - `/api/taskpool/llm-rpm` 独立 RPM 指标端点
  - LLM 超时支持 `LLM_TIMEOUT_SEC` 环境变量配置（默认 60s）
- 模拟机低电量异常自动检测
  - `instance.go` ≤20% 电量自动注入 `low_battery` 警告；≤15% 升级为 Critical 并强制返航
  - 电量恢复 >20% 后自动清除异常标签

### v3.7.0

- 仿真引擎禁飞区地理围栏集成
  - `engine.go` 新增 `SetNoFlyZones()` / `CheckGeofence()` / `pointInPolygon()`（ray-casting 点在多边形内检测）
  - `instance.go` 每 tick 自动检测禁飞区违规，填入 `TelemetrySnapshot.GeofenceViolation`
  - `rl.go` RL 状态空间新增 `InNoFlyZone` 维度（11 维），奖励函数增加 -3.0 禁飞区惩罚 + 纠正动作奖励
  - `sim_integration.go` 启动时自动从 `no_fly_zones` 表加载禁飞区多边形数据
- 贝塞尔曲线轨迹优化（TODO-11）
  - 新增 `curve-utils.js` 工具库：`smoothRoute`（三次贝塞尔）、`smoothTrail`（Catmull-Rom 样条）、`animatePolyline`（渐进动画）
  - `simulation.html`：实时轨迹 Catmull-Rom 平滑 + 航线贝塞尔曲线化 + Canvas 渲染器 + 「曲线轨迹」开关
  - `gps.html`：历史轨迹 Catmull-Rom 平滑 + 「曲线轨迹」开关 + `redrawHistoryTrail()`
  - `flight.html`：5 处航线展示全部贝塞尔曲线化 + 拖拽航点实时重绘
  - 弧度自适应算法：短距(<100m)小弧度 → 中距(500-2000m)中弧度 → 长距(>2km)大弧度，急转弯自动加大曲率

### v3.6.0

- MySQL 迁移完整性审计与修复
  - 补全仿真 5 张表（`sim_batches` / `sim_instances` / `sim_events` / `sim_telemetry_log` / `rl_training_log`）及索引到 `migrate_mysql.go`
  - 确认全部 32 张 MySQL 表与 SQLite 架构完全对齐（含 ALTER TABLE 新增列）
  - 所有 SQL 查询经 `AdaptSQL` 兼容性审查通过
- 仿真实例恢复致命 Bug 修复（`engine.go`）
  - `RestoreFromSnapshots` 恢复状态为 Running 的实例时，`Start()` 因状态已是 Running 而跳过 goroutine 创建，导致实例“冻结”不 tick
  - 修复：恢复后先重置为 Stopped 再调用 Start()
- 任务池饱和修复（`pool.go` + `sim_integration.go`）
  - 新增 `TrySubmit` 非阻塞提交，防止云 MySQL 高延迟时阻塞仿真 goroutine
  - 仿真遥测 DB 写入频率降低（GPS/Flight 每 3s、Battery/Log 每 10s）
  - 队列从持续满载 1024/1024 降至 0/1024
- 电池仿真优化（`instance.go`）
  - 改用 `float64` 精确追踪电量，修复整数截断导致的 1%/s 异常掉电
  - 新增 idle 充电逻辑（模拟地面充电）+ 满电自动重启，实现 7×24 连续仿真

### v3.5.0

- 新增仿真模拟引擎完整模块（`simulation.html` + `internal/simulation/*`）
  - 支持批量创建/启动/停止/删除仿真批次与实例
  - 支持异常注入（低电量/偏航/失联/温度异常）
- 新增任务模板系统（巡逻/巡检/配送/测绘/搜救）
  - 批次创建可按任务类型自动生成航点与任务描述
  - 支持 mission 字段贯穿 Batch/Instance 配置
- 新增防碰撞能力（`collisionAvoidanceLoop`）
  - 运行中实例高频距离检测
  - 自动航向/高度避让，降低近距离冲突风险
- 新增仿真地图可视化（Leaflet）
  - 实时显示无人机位置、航向、轨迹与航线
  - 支持图例、状态统计、自动刷新
- 强化学习训练链路增强（`internal/simulation/rl.go`）
  - 动作真实作用到环境、真实 next state、动作相关奖励
  - 训练批大小与平均奖励统计口径修正
  - 训练历史持久化与前端展示
- 新增 RL 策略导出接口：`/api/sim/rl/export-policy`

### v3.4.0

- 新增统一并发框架（`internal/taskpool/`）
  - Worker Pool 引擎：IO 16 线程 + CPU 4 线程、优先级队列、4 级优先级
  - 每任务独立超时控制（默认 30s）+ panic recover + 故障计数
  - 周期任务调度器（`SchedulePeriodic`）支持命名、替换、取消
  - 全局 + 按组指标采集（提交/完成/失败/平均耗时/队列长度）
- 新增辅助并发工具（`taskpool/batcher.go`）
  - `WriteBatcher`：DB 高频写入批量化（GPS 50条/2s、电池 30条/3s）
  - `Throttler`：WebSocket 推送节流（GPS 200ms、电池 500ms）
  - `StatsCache`：6 类统计缓存异步刷新（10-15s）
- 将后台任务接入 Pool 调度（替代原始 go func + ticker 模式）
  - 阈值告警、离线检测、6 个 AI 巡检任务全部迁入 Pool
- GPS/电池 handler 接入批量写入 + WS 节流
- 新增并发监控前端页面（`concurrency.html`）
  - 汇总卡片、Worker 池进度条、完成/失败趋势图、任务组环形图 + 详情表
- 新增 API 端点：`/api/taskpool/metrics`、`/api/stats/cached`
- 优雅关闭顺序：flush batchers → stop caches → shutdown pool → shutdown HTTP

### v3.3.0

- 飞行任务创建流程重构为两步向导（`flight.html`）
  - 第一步：航线规划（起终点、禁飞区、智能规划）
  - 第二步：任务配置（名称、无人机、动作、目标、描述）
  - 航线确认后方可进入任务配置，支持步骤前后切换
- 新增动作执行模式系统
  - 点选模式：在航线地图上点选多个执行位置，自动吸附最近航线段
  - 间隔模式：选择航线起止范围，设定间隔距离（米），自动计算执行次数
  - 动作配置地图交互（Leaflet 地图 + 航线可视化 + 点位标记）
  - 配置完成后展示动作执行计划预览（点数/间隔/次数汇总）
- 新增动作执行反馈模块
  - 任务详情中展示每个动作的执行状态（待执行/执行中/已完成）
  - 动作卡片含图标、进度条、执行方式、已执行/计划次数
  - 底部统计面板：动作总数 / 计划执行次数 / 已完成次数 / 完成率
- 飞行动作优化为 4 种：拍照、录像、投放、空气传感器
- 隐藏"飞行路线"字段，改为智能规划自动填充
- 前端所有地图默认位置统一为郑州大学（34.748, 113.655）
  - flight.html / gps.html / noflyzone.html 全部更新
  - 起点/终点示例更新为郑州大学主校区/北校区

### v3.2.0

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
- SQLite → MySQL 数据迁移完成（1473+ 行数据，26 张表，后续扩展至 32 张表）
- 数据库连接池配置（25 最大连接 / 10 空闲连接）
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
