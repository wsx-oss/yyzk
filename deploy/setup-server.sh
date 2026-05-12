#!/bin/sh
# ============================================================
# SmartControl 服务器初始化脚本 (Alpine Linux)
# 在新服务器上运行此脚本来安装所有依赖
# 用法: sh setup-server.sh
# ============================================================
set -e

echo "=========================================="
echo "  SmartControl 服务器环境初始化 (Alpine)"
echo "=========================================="

# 1. 更新系统
echo "[1/6] 更新系统包..."
apk update && apk upgrade

# 2. 安装必要工具
echo "[2/6] 安装基础工具..."
apk add curl wget git unzip htop net-tools bash python3 py3-pip py3-pymysql iptables

# 3. 安装 MariaDB (Alpine 上 MySQL 的替代，完全兼容)
echo "[3/6] 安装 MariaDB..."
if ! command -v mysql > /dev/null 2>&1; then
    apk add mariadb mariadb-client mariadb-common
    # 初始化数据库目录
    /etc/init.d/mariadb setup
    # 启动并设置开机自启
    rc-service mariadb start
    rc-update add mariadb default
    echo "MariaDB 已安装并启动"
else
    echo "MariaDB 已存在，跳过安装"
    rc-service mariadb start 2>/dev/null || true
fi

# 4. 安装 Redis
echo "[4/6] 安装 Redis..."
if ! command -v redis-server > /dev/null 2>&1; then
    apk add redis
    # 配置 Redis: 仅本地访问，设置密码
    sed -i 's/^bind .*/bind 127.0.0.1 ::1/' /etc/redis.conf
    sed -i 's/^# requirepass .*/requirepass SmartControl2024!/' /etc/redis.conf
    # 如果上面的 sed 没有匹配到（没有注释行），直接追加
    if ! grep -q "^requirepass" /etc/redis.conf; then
        echo "requirepass SmartControl2024!" >> /etc/redis.conf
    fi
    rc-service redis start
    rc-update add redis default
    echo "Redis 已安装并启动 (密码: SmartControl2024!)"
else
    echo "Redis 已存在，跳过安装"
    rc-service redis start 2>/dev/null || true
fi

# 5. 创建应用目录
echo "[5/6] 创建应用目录..."
mkdir -p /opt/smartcontrol
mkdir -p /opt/smartcontrol/data
mkdir -p /opt/smartcontrol/data/backups
mkdir -p /opt/smartcontrol/data/recordings
mkdir -p /opt/smartcontrol/knowledge_base
mkdir -p /opt/smartcontrol/logs

# 6. 配置防火墙 (iptables)
echo "[6/6] 配置防火墙 (iptables)..."
# 允许已建立的连接
iptables -A INPUT -m state --state ESTABLISHED,RELATED -j ACCEPT
# 允许本地回环
iptables -A INPUT -i lo -j ACCEPT
# 允许 SSH
iptables -A INPUT -p tcp --dport 42421 -j ACCEPT
# 允许 SmartControl Web
iptables -A INPUT -p tcp --dport 43215 -j ACCEPT
# 允许 TCP 网关
iptables -A INPUT -p tcp --dport 45332 -j ACCEPT
# 允许 Agent
iptables -A INPUT -p tcp --dport 53217 -j ACCEPT
# 保存规则 (Alpine 方式)
rc-service iptables save 2>/dev/null || /etc/init.d/iptables save 2>/dev/null || true
rc-update add iptables default 2>/dev/null || true
echo "防火墙已配置"

echo ""
echo "=========================================="
echo "  服务器环境初始化完成！"
echo "=========================================="
echo ""
echo "下一步:"
echo "  1. 配置 MariaDB 数据库 (运行 sh setup-mysql.sh)"
echo "  2. 上传应用二进制和配置文件"
echo "  3. 启动服务"
