#!/bin/bash
# MAVLink Bridge 启动脚本
# 使用 mavlink.jar + MySQL JDBC 驱动解析 MAVLink TCP 数据

set -e
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PROJECT_ROOT="$SCRIPT_DIR/../../.."

# 加载 .env
if [ -f "$PROJECT_ROOT/project/.env" ]; then
    export $(grep -v '^#' "$PROJECT_ROOT/project/.env" | xargs)
fi

MAVLINK_JAR="$PROJECT_ROOT/mavlink.jar"
MYSQL_JDBC="$SCRIPT_DIR/mysql-connector-j-9.1.0.jar"

# 下载 MySQL JDBC 驱动（如果不存在）
if [ ! -f "$MYSQL_JDBC" ]; then
    echo "Downloading MySQL JDBC driver..."
    curl -L -o "$MYSQL_JDBC" \
        "https://repo1.maven.org/maven2/com/mysql/mysql-connector-j/9.1.0/mysql-connector-j-9.1.0.jar"
fi

# 编译
echo "Compiling MavlinkBridge.java ..."
mkdir -p "$SCRIPT_DIR/classes"
javac -cp "$MAVLINK_JAR:$MYSQL_JDBC" -d "$SCRIPT_DIR/classes" "$SCRIPT_DIR/MavlinkBridge.java"

echo "Starting MAVLink Bridge ..."
exec java -cp "$SCRIPT_DIR/classes:$MAVLINK_JAR:$MYSQL_JDBC" MavlinkBridge
