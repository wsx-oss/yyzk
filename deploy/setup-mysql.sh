#!/bin/sh
# ============================================================
# MariaDB 数据库初始化脚本 (Alpine Linux)
# 创建 smartcontrol 数据库和用户
# 用法: sh setup-mysql.sh
# ============================================================
set -e

DB_NAME="smartcontrol"
DB_USER="smartcontrol"
DB_PASS="SC_Db_2024!Secure"

echo "=========================================="
echo "  MariaDB 数据库初始化 (Alpine)"
echo "=========================================="

# 确保 MariaDB 已启动
rc-service mariadb status > /dev/null 2>&1 || rc-service mariadb start

# 先运行安全初始化 (设置 root 密码、删除匿名用户等)
# 如果已经初始化过，跳过
if mysql -u root -e "SELECT 1" > /dev/null 2>&1; then
    echo "MariaDB root 无密码访问正常，继续配置..."
else
    echo "MariaDB root 需要密码或 socket 认证，尝试直接连接..."
fi

# 创建数据库和用户
mysql -u root <<EOF
CREATE DATABASE IF NOT EXISTS ${DB_NAME}
    CHARACTER SET utf8mb4
    COLLATE utf8mb4_unicode_ci;

CREATE USER IF NOT EXISTS '${DB_USER}'@'localhost' IDENTIFIED BY '${DB_PASS}';
CREATE USER IF NOT EXISTS '${DB_USER}'@'127.0.0.1' IDENTIFIED BY '${DB_PASS}';
GRANT ALL PRIVILEGES ON ${DB_NAME}.* TO '${DB_USER}'@'localhost';
GRANT ALL PRIVILEGES ON ${DB_NAME}.* TO '${DB_USER}'@'127.0.0.1';
FLUSH PRIVILEGES;

-- 设置时区
SET GLOBAL time_zone = '+08:00';
EOF

echo ""
echo "MariaDB 数据库配置完成！"
echo "  数据库: ${DB_NAME}"
echo "  用户:   ${DB_USER}"
echo "  密码:   ${DB_PASS}"
echo ""
echo "MySQL DSN: ${DB_USER}:${DB_PASS}@tcp(127.0.0.1:3306)/${DB_NAME}?charset=utf8mb4&parseTime=true"
echo ""
echo "验证连接:"
echo "  mysql -u ${DB_USER} -p'${DB_PASS}' ${DB_NAME} -e 'SELECT 1'"
