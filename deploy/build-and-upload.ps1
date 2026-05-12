# ============================================================
# Windows 本地：交叉编译 + 上传到服务器
# 用法: powershell -ExecutionPolicy Bypass -File deploy\build-and-upload.ps1
# ============================================================

$ErrorActionPreference = "Stop"

# ---- 配置 ----
$SERVER_IP   = "206.168.191.189"
$SERVER_PORT = "42421"
$SERVER_USER = "root"
$REMOTE_DIR  = "/opt/smartcontrol"
$PROJECT_DIR = Split-Path -Parent $PSScriptRoot  # 项目根目录下的 project/
$PROJECT_SRC = Join-Path $PROJECT_DIR "project"

Write-Host "==========================================" -ForegroundColor Cyan
Write-Host "  SmartControl 编译 & 上传" -ForegroundColor Cyan
Write-Host "==========================================" -ForegroundColor Cyan

# ---- Step 1: 交叉编译 Linux amd64 ----
Write-Host "`n[1/3] 交叉编译 Go 二进制 (linux/amd64)..." -ForegroundColor Yellow
$env:GOOS = "linux"
$env:GOARCH = "amd64"
$env:CGO_ENABLED = "0"

Push-Location $PROJECT_SRC
try {
    go build -o smartcontrol -ldflags="-s -w" .
    if ($LASTEXITCODE -ne 0) { throw "Go build failed" }
    Write-Host "  编译成功: smartcontrol (linux/amd64)" -ForegroundColor Green
} finally {
    # 恢复环境变量
    Remove-Item Env:GOOS -ErrorAction SilentlyContinue
    Remove-Item Env:GOARCH -ErrorAction SilentlyContinue
    Remove-Item Env:CGO_ENABLED -ErrorAction SilentlyContinue
    Pop-Location
}

# ---- Step 2: 准备上传文件列表 ----
Write-Host "`n[2/3] 准备上传文件..." -ForegroundColor Yellow

$DEPLOY_DIR = Join-Path $PROJECT_DIR "deploy"

# 创建临时打包目录
$TEMP_DIR = Join-Path $env:TEMP "smartcontrol-deploy"
if (Test-Path $TEMP_DIR) { Remove-Item -Recurse -Force $TEMP_DIR }
New-Item -ItemType Directory -Path $TEMP_DIR | Out-Null

# 复制二进制
Copy-Item (Join-Path $PROJECT_SRC "smartcontrol") $TEMP_DIR

# 复制 knowledge_base
$KB_SRC = Join-Path $PROJECT_SRC "knowledge_base"
if (Test-Path $KB_SRC) {
    Copy-Item -Recurse $KB_SRC (Join-Path $TEMP_DIR "knowledge_base")
}

# 复制 data 目录 (rl_policy.json, sim_snapshots.json)
$DATA_SRC = Join-Path $PROJECT_SRC "data"
if (Test-Path $DATA_SRC) {
    $DATA_DEST = Join-Path $TEMP_DIR "data"
    New-Item -ItemType Directory -Path $DATA_DEST | Out-Null
    $dataFiles = @("rl_policy.json", "sim_snapshots.json")
    foreach ($f in $dataFiles) {
        $src = Join-Path $DATA_SRC $f
        if (Test-Path $src) { Copy-Item $src $DATA_DEST }
    }
    # 创建 backups 和 recordings 子目录
    New-Item -ItemType Directory -Path (Join-Path $DATA_DEST "backups") -Force | Out-Null
    New-Item -ItemType Directory -Path (Join-Path $DATA_DEST "recordings") -Force | Out-Null
}

# 复制部署配置
Copy-Item (Join-Path $DEPLOY_DIR ".env.production") (Join-Path $TEMP_DIR ".env")
Copy-Item (Join-Path $DEPLOY_DIR "smartcontrol.openrc") $TEMP_DIR
Copy-Item (Join-Path $DEPLOY_DIR "setup-server.sh") $TEMP_DIR
Copy-Item (Join-Path $DEPLOY_DIR "setup-mysql.sh") $TEMP_DIR

# 复制 mavlink.jar (如果存在)
$MAVLINK_JAR = Join-Path $PROJECT_DIR "mavlink.jar"
if (Test-Path $MAVLINK_JAR) {
    Copy-Item $MAVLINK_JAR $TEMP_DIR
}

# 复制 SQLite 数据库 (用于迁移)
$SQLITE_DB = Join-Path $PROJECT_SRC "app.db"
if (Test-Path $SQLITE_DB) {
    Copy-Item $SQLITE_DB $TEMP_DIR
    Write-Host "  包含 app.db 用于数据迁移" -ForegroundColor Cyan
}

# 复制迁移脚本
$MIGRATE_SCRIPT = Join-Path $DEPLOY_DIR "migrate_to_new_server.py"
if (Test-Path $MIGRATE_SCRIPT) {
    Copy-Item $MIGRATE_SCRIPT $TEMP_DIR
}

Write-Host "  文件准备完毕: $TEMP_DIR" -ForegroundColor Green

# ---- Step 3: 上传到服务器 ----
Write-Host "`n[3/3] 上传到服务器 ${SERVER_IP}:${SERVER_PORT}..." -ForegroundColor Yellow
Write-Host "  请输入服务器密码进行上传" -ForegroundColor Cyan

# 使用 ssh/scp 上传 (需要用户输入密码)
ssh -p $SERVER_PORT "${SERVER_USER}@${SERVER_IP}" "mkdir -p ${REMOTE_DIR}"
if ($LASTEXITCODE -ne 0) {
    throw "远程目录创建失败: ${REMOTE_DIR}"
}

Get-ChildItem -Force $TEMP_DIR | ForEach-Object {
    scp -P $SERVER_PORT -r $_.FullName "${SERVER_USER}@${SERVER_IP}:${REMOTE_DIR}/"
    if ($LASTEXITCODE -ne 0) {
        throw "上传失败: $($_.FullName)"
    }
}

Write-Host "`n  上传成功！" -ForegroundColor Green

# 清理临时目录
# Remove-Item -Recurse -Force $TEMP_DIR

Write-Host "`n==========================================" -ForegroundColor Cyan
Write-Host "  编译上传完成！" -ForegroundColor Cyan
Write-Host "==========================================" -ForegroundColor Cyan
Write-Host ""
Write-Host "接下来 SSH 登录服务器完成部署:" -ForegroundColor Yellow
Write-Host "  ssh -p $SERVER_PORT ${SERVER_USER}@${SERVER_IP}" -ForegroundColor White
Write-Host ""
Write-Host "然后在服务器上执行:" -ForegroundColor Yellow
Write-Host "  cd /opt/smartcontrol" -ForegroundColor White
Write-Host "  sh setup-server.sh                              # 安装依赖" -ForegroundColor White
Write-Host "  sh setup-mysql.sh                               # 初始化数据库" -ForegroundColor White
Write-Host "  chmod +x smartcontrol                           # 添加执行权限" -ForegroundColor White
Write-Host "  cp smartcontrol.openrc /etc/init.d/smartcontrol # 安装服务" -ForegroundColor White
Write-Host "  chmod +x /etc/init.d/smartcontrol               # 服务可执行" -ForegroundColor White
Write-Host "  rc-update add smartcontrol default              # 开机自启" -ForegroundColor White
Write-Host "  rc-service smartcontrol start                   # 启动服务" -ForegroundColor White
