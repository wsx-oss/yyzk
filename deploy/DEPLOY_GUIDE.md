# SmartControl 部署指南 — Alpine Linux 服务器

## 服务器信息

| 项目       | 值                          |
| ---------- | --------------------------- |
| IP         | `206.168.191.189`           |
| 系统       | Alpine Linux                |
| SSH端口    | `42421`                     |
| Web端口    | `43215` (主应用)            |
| TCP网关端口 | `45332`                    |
| Agent端口  | `53217`                     |
| 用户       | `root`                      |
| 密码       | `OGtcmZBb&sVTKd9b`         |

## 端口分配

| 端口  | 用途           | 环境变量                    |
| ----- | -------------- | --------------------------- |
| 42421 | SSH 登录       | -                           |
| 43215 | Web 前端 + API | `SC_LISTEN_ADDR=:43215`     |
| 45332 | TCP 设备网关   | `SC_TCP_PORT=45332`         |
| 53217 | 硬件监控 Agent | `SC_AGENT_PORT=53217`       |

---

## 完整部署步骤

### 第一阶段：本地编译 (Windows)

在项目 **根目录** 打开 PowerShell，进入 `project` 子目录编译：

```powershell
cd project

# 设置交叉编译环境变量
$env:GOOS = "linux"
$env:GOARCH = "amd64"
$env:CGO_ENABLED = "0"

# 编译 (SQLite 使用纯 Go 实现 modernc.org/sqlite，无需 CGO)
go build -o smartcontrol -ldflags="-s -w" .

# 恢复环境变量
Remove-Item Env:GOOS -ErrorAction SilentlyContinue
Remove-Item Env:GOARCH -ErrorAction SilentlyContinue
Remove-Item Env:CGO_ENABLED -ErrorAction SilentlyContinue
```

编译成功后 `project/` 目录下会出现 `smartcontrol` 文件（Linux 二进制，无 `.exe` 后缀）。

> 也可以在 **项目根目录** 运行一键脚本：
>
> ```powershell
> powershell -ExecutionPolicy Bypass -File .\deploy\build-and-upload.ps1
> ```

---

### 第二阶段：上传文件到服务器

#### 步骤 2.1：先在服务器上创建目录

```powershell
ssh -p 42421 root@206.168.191.189
```

登录后执行：

```sh
mkdir -p /opt/smartcontrol/data/backups
mkdir -p /opt/smartcontrol/data/recordings
mkdir -p /opt/smartcontrol/knowledge_base
mkdir -p /opt/smartcontrol/logs
exit
```

#### 步骤 2.2：从本地上传文件

回到本地 PowerShell（在项目根目录）：

```powershell
# 上传编译好的二进制
scp -P 42421 project/smartcontrol root@206.168.191.189:/opt/smartcontrol/

# 上传部署脚本
scp -P 42421 deploy/setup-server.sh root@206.168.191.189:/opt/smartcontrol/
scp -P 42421 deploy/setup-mysql.sh root@206.168.191.189:/opt/smartcontrol/
scp -P 42421 deploy/smartcontrol.openrc root@206.168.191.189:/opt/smartcontrol/
scp -P 42421 deploy/migrate_to_new_server.py root@206.168.191.189:/opt/smartcontrol/

# 上传环境变量配置 (注意重命名为 .env)
scp -P 42421 deploy/.env.production root@206.168.191.189:/opt/smartcontrol/.env

# 上传 knowledge_base 目录
scp -P 42421 -r project/knowledge_base root@206.168.191.189:/opt/smartcontrol/

# 上传 data 文件
scp -P 42421 project/data/rl_policy.json root@206.168.191.189:/opt/smartcontrol/data/
scp -P 42421 project/data/sim_snapshots.json root@206.168.191.189:/opt/smartcontrol/data/

# 上传 SQLite 数据库 (用于数据迁移，如无需迁移可跳过)
scp -P 42421 project/app.db root@206.168.191.189:/opt/smartcontrol/

# 上传 mavlink.jar (如需要)
scp -P 42421 mavlink.jar root@206.168.191.189:/opt/smartcontrol/
```

**如果更喜欢图形界面**，可用 WinSCP / FileZilla：
- 协议: SFTP
- 主机: `206.168.191.189`
- 端口: `42421`
- 用户: `root`

上传清单：

```text
/opt/smartcontrol/
├── smartcontrol              ← project/smartcontrol (Linux 二进制)
├── .env                      ← deploy/.env.production (上传后重命名为 .env)
├── smartcontrol.openrc       ← deploy/smartcontrol.openrc
├── setup-server.sh           ← deploy/setup-server.sh
├── setup-mysql.sh            ← deploy/setup-mysql.sh
├── migrate_to_new_server.py  ← deploy/migrate_to_new_server.py
├── app.db                    ← project/app.db (数据迁移用)
├── mavlink.jar               ← mavlink.jar (可选)
├── knowledge_base/           ← project/knowledge_base/ (整个目录)
├── logs/                     ← 空目录
└── data/
    ├── backups/
    ├── recordings/
    ├── rl_policy.json        ← project/data/rl_policy.json
    └── sim_snapshots.json    ← project/data/sim_snapshots.json
```

---

### 第三阶段：服务器环境初始化

SSH 登录服务器：

```powershell
ssh -p 42421 root@206.168.191.189
```

#### 步骤 3.1：运行环境初始化脚本

```sh
cd /opt/smartcontrol
chmod +x setup-server.sh setup-mysql.sh smartcontrol
sh setup-server.sh
```

此脚本会自动：
- `apk` 安装 MariaDB、Redis、Python3、iptables 等
- 启动 MariaDB 和 Redis 并设置开机自启
- 配置 iptables 放行端口 42421/43215/45332/53217
- 创建应用目录结构

#### 步骤 3.2：初始化数据库

```sh
sh setup-mysql.sh
```

创建：
- 数据库: `smartcontrol` (utf8mb4)
- 用户: `smartcontrol` / 密码: `SC_Db_2024!Secure`

#### 步骤 3.3：验证 MariaDB 和 Redis

```sh
# 验证 MariaDB
mysql -u smartcontrol -p'SC_Db_2024!Secure' smartcontrol -e "SELECT 1"

# 验证 Redis
redis-cli -a 'SmartControl2024!' ping
```

MariaDB 应返回 `1`，Redis 应返回 `PONG`。

---

### 第四阶段：配置环境变量

#### 步骤 4.1：编辑 .env 文件

```sh
vi /opt/smartcontrol/.env
```

**必须确认以下配置正确：**

```sh
SC_LISTEN_ADDR=:43215
SC_DB_DRIVER=mysql
SC_MYSQL_DSN=smartcontrol:SC_Db_2024!Secure@tcp(127.0.0.1:3306)/smartcontrol?charset=utf8mb4&parseTime=true
REDIS_HOST=127.0.0.1
REDIS_PORT=6379
REDIS_PASSWORD=SmartControl2024!
SC_TCP_PORT=45332
SC_AGENT_PORT=53217
SC_API_TOKEN=SmartControl_2026_Replace_With_Random_Token
```

**重要**：部署前必须替换以下值：

```sh
LLM_API_KEY=你的阿里云通义API密钥
AMAP_KEY=你的高德地图Key
SC_API_TOKEN=你自己生成的强随机Token
```

---

### 第五阶段：数据迁移 (SQLite → MariaDB)

> 如果是全新部署、不需要旧数据，**跳过此阶段**，直接到第六阶段。

#### 步骤 5.1：确认 Python 依赖已安装

`setup-server.sh` 已自动安装 `py3-pymysql`。如果跳过了该步骤，手动安装：

```sh
apk add py3-pymysql
```

#### 步骤 5.2：运行迁移脚本

```sh
cd /opt/smartcontrol
python3 migrate_to_new_server.py
```

脚本会：
1. 先启动 smartcontrol 让它自动在 MariaDB 中创建所有表
2. 从 `app.db` (SQLite) 读取数据
3. 逐表写入 MariaDB
4. 验证每张表行数一致

#### 步骤 5.3：验证迁移结果

```sh
mysql -u smartcontrol -p'SC_Db_2024!Secure' smartcontrol -e "
SELECT 'users' AS tbl, COUNT(*) AS cnt FROM users
UNION ALL SELECT 'drones', COUNT(*) FROM drones
UNION ALL SELECT 'gps_devices', COUNT(*) FROM gps_devices
UNION ALL SELECT 'flight_missions', COUNT(*) FROM flight_missions
UNION ALL SELECT 'alerts', COUNT(*) FROM alerts;
"
```

---

### 第六阶段：启动服务

#### 步骤 6.1：手动测试启动

```sh
cd /opt/smartcontrol
./smartcontrol
```

看到以下输出说明启动成功：

```text
[DB] Connected to MySQL (timezone: Asia/Shanghai, every connection)
[main] listening on :43215
```

按 `Ctrl+C` 停止。

#### 步骤 6.2：安装 OpenRC 服务 (开机自启)

```sh
# 复制服务脚本
cp /opt/smartcontrol/smartcontrol.openrc /etc/init.d/smartcontrol

# 添加执行权限
chmod +x /etc/init.d/smartcontrol

# 设置开机自启
rc-update add smartcontrol default

# 启动服务
rc-service smartcontrol start

# 查看状态
rc-service smartcontrol status
```

#### 步骤 6.3：查看日志

```sh
# 实时查看标准输出
tail -f /opt/smartcontrol/logs/stdout.log

# 实时查看错误输出
tail -f /opt/smartcontrol/logs/stderr.log
```

---

### 第七阶段：验证部署

#### 步骤 7.1：服务器本地健康检查

```sh
curl http://localhost:43215/api/healthz
```

期望返回：

```json
{
  "status": "ok",
  "db_reachable": true,
  "redis_reachable": true,
  "llm_reachable": true,
  "patrol_online": false
}
```

#### 步骤 7.2：外网浏览器访问

```text
http://206.168.191.189:43215
```

应跳转到登录页（或注册页，如果是首次部署）。

#### 步骤 7.3：确认所有端口已监听

```sh
netstat -tlnp | grep -E '43215|45332|53217'
```

应看到三个端口都在 LISTEN 状态。

---

## 常用运维命令

```sh
# 启动服务
rc-service smartcontrol start

# 停止服务
rc-service smartcontrol stop

# 重启服务
rc-service smartcontrol restart

# 查看服务状态
rc-service smartcontrol status

# 查看日志 (最近100行)
tail -100 /opt/smartcontrol/logs/stdout.log

# 查看 MariaDB 状态
rc-service mariadb status

# 查看 Redis 状态
rc-service redis status

# 查看磁盘使用
df -h

# 查看内存使用
free -m

# 查看所有服务
rc-status
```

---

## 更新部署 (代码更新后)

本地 PowerShell（项目根目录）：

```powershell
# 1. 重新编译
cd project
$env:GOOS="linux"; $env:GOARCH="amd64"; $env:CGO_ENABLED="0"
go build -o smartcontrol -ldflags="-s -w" .
Remove-Item Env:GOOS -ErrorAction SilentlyContinue
Remove-Item Env:GOARCH -ErrorAction SilentlyContinue
Remove-Item Env:CGO_ENABLED -ErrorAction SilentlyContinue

# 2. 上传新二进制
scp -P 42421 smartcontrol root@206.168.191.189:/opt/smartcontrol/
```

服务器上：

```sh
# 3. 重启服务
rc-service smartcontrol restart
```

---

## 故障排查

| 问题               | 排查方法                                                    |
| ------------------ | ----------------------------------------------------------- |
| 无法访问页面       | `curl localhost:43215` 先确认本地通，再查 iptables           |
| MariaDB 连接失败   | `rc-service mariadb status` 检查是否运行，再验证 DSN        |
| Redis 连接失败     | `redis-cli -a 'SmartControl2024!' ping`                     |
| 二进制无法执行     | `chmod +x smartcontrol` 然后 `file smartcontrol` 确认是 ELF |
| 端口被占用         | `netstat -tlnp \| grep 43215` 然后 `kill` 占用进程         |
| 服务启动后立即退出 | `tail -50 /opt/smartcontrol/logs/stderr.log` 查看错误       |
| SSH 连接超时       | 检查本地网络是否能到达服务器 IP，或安全组是否放行 42421     |
