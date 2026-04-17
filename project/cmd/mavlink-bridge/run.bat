@echo off
REM MAVLink Bridge 启动脚本
REM 使用 mavlink.jar + MySQL JDBC 驱动解析 MAVLink TCP 数据

setlocal

REM 项目根目录
set PROJECT_ROOT=%~dp0..\..\..

REM 加载 .env 配置
if exist "%~dp0..\..\..\.env" (
    for /f "usebackq tokens=1,* delims==" %%a in ("%~dp0..\..\..\.env") do (
        set "line=%%a"
        if not "!line:~0,1!"=="#" (
            set "%%a=%%b"
        )
    )
)
if exist "%~dp0..\..\..\.env" (
    for /f "usebackq eol=# tokens=1,* delims==" %%a in ("%~dp0..\..\.env") do (
        set "%%a=%%b"
    )
)

REM JAR 路径
set MAVLINK_JAR=%PROJECT_ROOT%\mavlink.jar
set MYSQL_JDBC=mysql-connector-j-9.1.0.jar

REM 如果没有 MySQL JDBC 驱动，自动下载
if not exist "%~dp0%MYSQL_JDBC%" (
    echo Downloading MySQL JDBC driver...
    powershell -Command "Invoke-WebRequest -Uri 'https://repo1.maven.org/maven2/com/mysql/mysql-connector-j/9.1.0/mysql-connector-j-9.1.0.jar' -OutFile '%~dp0%MYSQL_JDBC%'"
)

REM 编译
echo Compiling MavlinkBridge.java ...
javac -cp "%MAVLINK_JAR%;%~dp0%MYSQL_JDBC%" -d "%~dp0classes" "%~dp0MavlinkBridge.java"
if errorlevel 1 (
    echo Compile failed!
    pause
    exit /b 1
)

echo Starting MAVLink Bridge ...
java -cp "%~dp0classes;%MAVLINK_JAR%;%~dp0%MYSQL_JDBC%" MavlinkBridge

pause
