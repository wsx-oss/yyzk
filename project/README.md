# 智能工具人机交互与远程操控系统

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
- [10大功能模块](#10大功能模块)
- [API接口文档](#api接口文档)
- [部署指南](#部署指南)
- [故障排查](#故障排查)

---

## 🎯 项目简介

智能工具人机交互与远程操控系统是一套面向企业级应用的综合性管控平台。

### 核心特性

**🔐 安全认证**
- 用户注册/登录系统（bcrypt 密码加密）
- 会话管理（Token 持久化，24小时过期）
- API Token 认证保护

**🚀 性能优化**
- 数据库索引优化（查询速度提升 50-80%）
- 分页支持（所有列表接口）
- 请求限流（100 req/min）

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
3. 左侧导航切换10大功能模块

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

### 功能模块（在仪表盘内）

所有功能都通过仪表盘左侧导航栏访问：

| # | 模块名称 | 功能说明 |
|---|----------|----------|
| 1 | 系统状态监控 | CPU、内存、磁盘、网络实时监控 |
| 2 | 语音交互记录 | 音频文件上传/下载/播放/删除 |
| 3 | 远程桌面控制 | 基于 noVNC 的远程桌面（需目标机VNC服务）|
| 4 | 视频监控 | RTSP/HTTP 视频流接入和预览 |
| 5 | 硬件状态检测 | 硬件温度、状态监控 |
| 6 | 软件更新管理 | 更新包管理和部署 |
| 7 | 数据同步状态 | 同步任务监控 |
| 8 | 异常报警 | 告警记录和响应管理 |
| 9 | 性能分析报告 | 多维度性能分析 |
| 10 | 维护操作日志 | 操作审计日志 |

### API 端点

| 端点 | 说明 |
|------|------|
| `http://127.0.0.1:8080/api/healthz` | 健康检查 |
| `ws://127.0.0.1:8080/api/metrics/stream` | WebSocket 实时监控流 |
| `ws://127.0.0.1:8080/api/vnc/ws?target=IP:Port` | VNC WebSocket 代理 |

---

## ⚙️ 环境变量配置

### 基础配置

| 变量名 | 默认值 | 说明 |
|--------|--------|------|
| `SC_LISTEN_ADDR` | `:8080` | 监听地址和端口 |
| `SC_DB_PATH` | `app.db` | 数据库文件路径 |
| `SC_API_TOKEN` | `""` | API认证Token（空=关闭）|
| `SC_MAX_UPLOAD_MB` | `64` | 文件上传限制(MB) |

### 告警配置

| 变量名 | 默认值 | 说明 |
|--------|--------|------|
| `SC_THRESH_CPU` | `85` | CPU告警阈值(%) |
| `SC_THRESH_MEM` | `85` | 内存告警阈值(%) |
| `SC_THRESH_DISK` | `90` | 磁盘告警阈值(%) |
| `SC_ALERT_INTERVAL_SEC` | `10` | 检测间隔(秒) |

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

## 📦 10大功能模块

### 1. 系统状态监控

**功能**: CPU、内存、磁盘、网络实时监控

**API**:
- `GET /api/metrics/snapshot` - 获取快照
- `WS /api/metrics/stream` - 实时数据流

**示例**:
```bash
curl http://127.0.0.1:8080/api/metrics/snapshot
```

### 2. 语音交互记录

**功能**: 音频文件管理

**API**:
- `POST /api/audio/upload` - 上传
- `GET /api/audio/list?page=1&page_size=20` - 列表（分页）
- `GET /api/audio/download/:id` - 下载
- `DELETE /api/audio/:id` - 删除

### 3. 远程桌面控制

**功能**: VNC 远程桌面

**前置条件**: 目标机器需安装 VNC 服务器（默认端口5900）

**API**:
- `WS /api/vnc/ws?target=192.168.1.100:5900` - VNC代理

### 4-10. 其他模块

其他功能模块详细说明请查看在线文档。

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
| 音频 | `GET /api/audio/list` | 列表（支持分页）|
| 音频 | `DELETE /api/audio/:id` | 删除 |
| 告警 | `GET /api/alerts/list` | 列表（支持分页）|
| 告警 | `POST /api/alerts/ack/:id` | 确认 |
| 日志 | `GET /api/logs/list` | 列表（支持分页）|

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

```
project/
├── main.go                    # 程序入口
├── go.mod                     # 依赖管理
├── internal/
│   ├── db/db.go              # 数据库
│   ├── handlers/             # API处理
│   ├── monitor/              # 监控
│   ├── middleware/           # 中间件
│   └── utils/                # 工具
├── web/                      # 前端页面
│   ├── index.html
│   ├── login.html
│   ├── dashboard.html
│   └── modules/              # 功能模块
└── data/                     # 数据目录
    └── recordings/           # 录音文件
```

---

## 🔄 版本更新

### v1.1.0 (最新)

**新增**:
- ✅ 完整会话管理系统
- ✅ 3个新认证接口
- ✅ 音频删除功能
- ✅ 列表分页支持
- ✅ API限流保护
- ✅ CORS跨域支持

**优化**:
- ✅ 6个数据库索引
- ✅ 性能提升50-80%
- ✅ 安全性增强

---

## 📧 技术支持

- 📮 提交 Issue
- 💬 查看文档
- 🔍 搜索常见问题

---

**Made with ❤️ by Smart Control Team**
