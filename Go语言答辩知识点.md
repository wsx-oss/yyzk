# Go语言答辩知识点总结

> **项目名称**: CloudControl - 智能无人机人机交互与远程管控平台
> **后端技术栈**: Go + Gin框架 + SQLite数据库
> **适用场景**: 项目答辩、老师提问应对

---

## 📋 目录

1. [项目架构概览](#1-项目架构概览)
2. [Go语言基础知识](#2-go语言基础知识)
3. [核心框架与库](#3-核心框架与库)
4. [项目中的关键技术点](#4-项目中的关键技术点)
5. [常见答辩问题及回答](#5-常见答辩问题及回答)

---

## 1. 项目架构概览

### 1.1 项目结构

```
project/
├── main.go                    # 主程序入口
├── go.mod / go.sum            # Go依赖管理文件
├── cmd/agent/                 # 独立Agent程序（地面站数据采集）
├── internal/                  # 内部包（不对外暴露）
│   ├── db/                    # 数据库初始化与迁移
│   ├── handlers/              # API处理函数（业务逻辑）
│   ├── middleware/            # 中间件（日志、限流、认证）
│   ├── llm/                   # LLM智能规划模块
│   ├── agent/                 # 内嵌Agent
│   ├── monitor/               # 系统监控
│   └── syncengine/            # 数据同步引擎
└── web/                       # 前端静态资源（HTML/JS/CSS）
```

### 1.2 技术选型理由

- **Go语言**: 高并发性能优秀、编译型语言运行快、部署简单（单一可执行文件）
- **Gin框架**: 轻量级、高性能的Web框架，路由快速
- **SQLite**: 嵌入式数据库，无需独立服务器，适合边缘计算场景
- **WebSocket**: 实时双向通信，用于GPS/电池/飞行状态实时推送

---

## 2. Go语言基础知识

### 2.1 包（Package）管理

#### 什么是包？

Go程序由包组成，每个 `.go`文件开头都要声明所属的包。

**项目中的例子**:

```go
package main           // 主程序包，包含main()函数
package handlers       // handlers包，处理HTTP请求
package db            // db包，数据库相关功能
```

#### 导入包（import）

```go
import (
    "database/sql"              // 标准库：数据库接口
    "github.com/gin-gonic/gin"  // 第三方库：Gin框架
    "smartcontrol/internal/db"  // 项目内部包
)
```

**关键点**:

- 标准库直接写包名（如 `"fmt"`, `"time"`）
- 第三方库写完整路径（如 `"github.com/gin-gonic/gin"`）
- 项目内部包用模块名开头（`smartcontrol/internal/...`）

### 2.2 函数（Function）

#### 基本语法

```go
func 函数名(参数名 参数类型) 返回类型 {
    // 函数体
}
```

**项目中的例子**:

```go
// 无返回值函数
func main() {
    log.Printf("服务器启动")
}

// 单返回值函数
func getenv(k, def string) string {
    v := os.Getenv(k)
    if v == "" {
        return def
    }
    return v
}

// 多返回值函数（Go特色：常用于返回结果+错误）
func Open(path string) (*sql.DB, error) {
    database, err := sql.Open("sqlite", dsn)
    if err != nil {
        return nil, err  // 返回空值和错误
    }
    return database, nil  // 返回数据库连接和nil错误
}
```

#### 方法（Method）- 绑定到结构体的函数

```go
// API是一个结构体
type API struct {
    db *sql.DB
}

// DronesList是API的方法（注意前面的(a *API)）
func (a *API) DronesList(c *gin.Context) {
    // 可以通过a.db访问数据库
    rows, err := a.db.Query("SELECT * FROM drones")
}
```

### 2.3 结构体（Struct）

结构体是Go中定义复杂数据类型的方式（类似其他语言的类）。

**项目中的例子**:

```go
// 定义结构体
type Waypoint struct {
    Lat      float64 `json:"lat"`       // 纬度
    Lon      float64 `json:"lon"`       // 经度
    AltM     float64 `json:"alt_m"`     // 高度（米）
    SpeedMPS float64 `json:"speed_mps"` // 速度（米/秒）
    Action   string  `json:"action"`    // 动作
}

// 使用结构体
wp := Waypoint{
    Lat:      39.9042,
    Lon:      116.4074,
    AltM:     100.0,
    SpeedMPS: 8.0,
    Action:   "TAKEOFF",
}
```

**JSON标签**: `json:"lat"` 告诉Go在JSON序列化时使用 `lat`作为字段名。

### 2.4 接口（Interface）

接口定义一组方法签名，任何实现了这些方法的类型都满足该接口。

**项目中的例子**:

```go
// gin.HandlerFunc是一个函数类型，也是一种接口
type HandlerFunc func(*Context)

// 中间件函数返回HandlerFunc
func authMiddleware(token string) gin.HandlerFunc {
    return func(c *gin.Context) {
        // 认证逻辑
        c.Next()
    }
}
```

### 2.5 错误处理

Go没有异常机制，使用返回值传递错误。

**标准模式**:

```go
database, err := db.Open(dbPath)
if err != nil {
    log.Fatal(err)  // 处理错误：记录日志并退出
}
defer database.Close()  // defer确保函数结束时关闭数据库
```

**关键点**:

- `err != nil` 表示有错误发生
- `defer` 延迟执行，常用于资源清理（关闭文件、数据库连接等）

### 2.6 并发编程（Goroutine & Channel）

#### Goroutine - 轻量级线程

```go
// 启动后台任务
go runThresholdAlerts(database, thCPU, thMEM, thDISK, interval)
go runOfflineDetection(database, 10*time.Second)
```

**关键点**: `go` 关键字启动一个并发执行的函数，不阻塞主程序。

#### Channel - 协程间通信

```go
// 创建channel用于接收系统信号
quit := make(chan os.Signal, 1)
signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
<-quit  // 阻塞等待信号
```

### 2.7 指针（Pointer）

Go支持指针，但不支持指针运算。

```go
// *sql.DB 表示指向sql.DB的指针
func Open(path string) (*sql.DB, error) {
    // ...
}

// 使用指针接收者（修改会影响原对象）
func (a *API) DronesList(c *gin.Context) {
    // a是指针，可以修改API对象
}
```

**为什么用指针**:

- 避免大结构体的复制（性能优化）
- 允许函数修改原对象

---

## 3. 核心框架与库

### 3.1 Gin Web框架

#### 什么是Gin？

高性能的HTTP Web框架，用于构建RESTful API。

#### 核心概念

**1. 路由（Routing）**

```go
r := gin.New()  // 创建Gin引擎

// GET请求
r.GET("/api/drones", api.DronesList)

// POST请求
r.POST("/api/drones", api.DronesCreate)

// 带参数的路由
r.GET("/api/drones/:id", api.DronesGet)  // :id是路径参数

// 路由分组
authGroup := r.Group("/api")
authGroup.Use(authMiddleware(token))  // 应用中间件
```

**2. 上下文（Context）**

```go
func (a *API) DronesList(c *gin.Context) {
    // 获取查询参数
    name := c.Query("name")        // ?name=xxx
    status := c.Query("status")    // ?status=online
  
    // 获取路径参数
    id := c.Param("id")            // /api/drones/:id
  
    // 解析JSON请求体
    var req struct {
        Name string `json:"name"`
    }
    if err := c.BindJSON(&req); err != nil {
        c.JSON(400, gin.H{"error": "bad json"})
        return
    }
  
    // 返回JSON响应
    c.JSON(200, gin.H{
        "items": items,
        "total": total,
    })
}
```

**3. 中间件（Middleware）**

```go
// 中间件是处理请求前后的函数
func RequestLogger() gin.HandlerFunc {
    return func(c *gin.Context) {
        startTime := time.Now()
    
        c.Next()  // 执行后续处理器
    
        latency := time.Since(startTime)
        log.Printf("请求耗时: %v", latency)
    }
}

// 使用中间件
r.Use(RequestLogger())
r.Use(corsMiddleware())
```

### 3.2 数据库操作（database/sql）

#### 连接数据库

```go
import _ "modernc.org/sqlite"  // 导入SQLite驱动（_表示只执行init）

database, err := sql.Open("sqlite", dsn)
if err != nil {
    return nil, err
}
```

#### 执行SQL

**1. 查询单行**

```go
var count int
err := db.QueryRow("SELECT COUNT(*) FROM drones").Scan(&count)
```

**2. 查询多行**

```go
rows, err := db.Query("SELECT id, name, status FROM drones WHERE status=?", "online")
if err != nil {
    return err
}
defer rows.Close()  // 必须关闭

for rows.Next() {
    var id int
    var name, status string
    if err := rows.Scan(&id, &name, &status); err == nil {
        // 处理每一行
    }
}
```

**3. 执行写操作**

```go
res, err := db.Exec(
    "INSERT INTO drones(name, ip, status) VALUES(?,?,?)",
    "无人机1", "192.168.1.100", "offline",
)
if err != nil {
    return err
}
id, _ := res.LastInsertId()  // 获取插入的ID
```

**关键点**:

- `?` 是占位符，防止SQL注入
- `Scan()` 将查询结果扫描到变量
- `defer rows.Close()` 确保资源释放

### 3.3 WebSocket（实时通信）

#### 什么是WebSocket？

基于TCP的全双工通信协议，服务器可主动推送数据给客户端。

**项目中的应用**:

```go
import "github.com/gorilla/websocket"

// 升级HTTP连接为WebSocket
var upgrader = websocket.Upgrader{
    CheckOrigin: func(r *http.Request) bool {
        return true  // 允许跨域
    },
}

func (a *API) GpsStream(c *gin.Context) {
    ws, err := upgrader.Upgrade(c.Writer, c.Request, nil)
    if err != nil {
        return
    }
    defer ws.Close()
  
    // 订阅GPS主题
    hub.Subscribe("gps", ws)
    defer hub.Unsubscribe("gps", ws)
  
    // 保持连接
    for {
        if _, _, err := ws.ReadMessage(); err != nil {
            break
        }
    }
}

// 广播数据到所有订阅者
hub.Broadcast("gps", WSEvent{
    Type: "gps_update",
    Data: gin.H{"lat": 39.9, "lon": 116.4},
})
```

### 3.4 并发安全（sync包）

#### Mutex（互斥锁）

```go
import "sync"

type WSHub struct {
    mu      sync.RWMutex  // 读写锁
    clients map[string]map[*wsClient]struct{}
}

func (h *WSHub) Subscribe(topic string, conn *websocket.Conn) {
    h.mu.Lock()         // 加写锁
    defer h.mu.Unlock() // 函数结束时解锁
  
    // 修改共享数据
    if h.clients[topic] == nil {
        h.clients[topic] = make(map[*wsClient]struct{})
    }
    h.clients[topic][cl] = struct{}{}
}

func (h *WSHub) Broadcast(topic string, event WSEvent) {
    h.mu.RLock()        // 加读锁（允许多个goroutine同时读）
    defer h.mu.RUnlock()
  
    // 读取共享数据
    for cl := range h.clients[topic] {
        // ...
    }
}
```

**为什么需要锁**:
多个goroutine同时访问map会导致数据竞争（data race），使用锁保证线程安全。

---

## 4. 项目中的关键技术点

### 4.1 RESTful API设计

#### 什么是RESTful？

一种API设计风格，使用HTTP方法表示操作：

- **GET**: 查询资源
- **POST**: 创建资源
- **PUT/PATCH**: 更新资源
- **DELETE**: 删除资源

**项目中的例子**:

```go
// 无人机管理API
r.GET("/api/drones", api.DronesList)           // 获取列表
r.POST("/api/drones", api.DronesCreate)        // 创建
r.GET("/api/drones/:id", api.DronesGet)        // 获取单个
r.PUT("/api/drones/:id", api.DronesUpdate)     // 更新
r.DELETE("/api/drones/:id", api.DronesDelete)  // 删除
r.GET("/api/drones/stats", api.DronesStats)    // 统计信息
```

### 4.2 中间件机制

#### 认证中间件

```go
func authMiddleware(token string) gin.HandlerFunc {
    return func(c *gin.Context) {
        // 检查Authorization头
        hdr := c.GetHeader("Authorization")
        if strings.HasPrefix(hdr, "Bearer ") && 
           strings.TrimSpace(hdr[7:]) == token {
            c.Next()  // 认证通过，继续处理
            return
        }
    
        // 认证失败
        c.AbortWithStatusJSON(401, gin.H{"error": "unauthorized"})
    }
}
```

#### 日志中间件

```go
func RequestLogger() gin.HandlerFunc {
    return func(c *gin.Context) {
        startTime := time.Now()
        path := c.Request.URL.Path
    
        c.Next()  // 执行实际处理器
    
        latency := time.Since(startTime)
        statusCode := c.Writer.Status()
    
        log.Printf("[%s] %d | %v | %s | %s",
            time.Now().Format("2006-01-02 15:04:05"),
            statusCode,
            latency,
            c.ClientIP(),
            path,
        )
    }
}
```

#### 限流中间件

```go
type RateLimiter struct {
    rate  int           // 每分钟允许的请求数
    mu    sync.Mutex
    ips   map[string]*bucket
}

func (rl *RateLimiter) Middleware() gin.HandlerFunc {
    return func(c *gin.Context) {
        ip := c.ClientIP()
        if !rl.allow(ip) {
            c.AbortWithStatusJSON(429, gin.H{
                "error": "too many requests",
            })
            return
        }
        c.Next()
    }
}
```

### 4.3 数据库迁移（Migration）

**为什么需要迁移**:
数据库表结构会随项目迭代而变化，迁移脚本确保所有环境的数据库结构一致。

**项目实现**:

```go
func Migrate(db *sql.DB) error {
    // 创建表的SQL语句列表
    stmts := []string{
        `CREATE TABLE IF NOT EXISTS drones (
            id INTEGER PRIMARY KEY AUTOINCREMENT,
            name TEXT NOT NULL,
            ip TEXT DEFAULT '',
            status TEXT NOT NULL DEFAULT 'offline',
            created_at DATETIME DEFAULT CURRENT_TIMESTAMP
        )`,
        `CREATE INDEX IF NOT EXISTS idx_drones_name ON drones(name)`,
        // ... 更多表
    }
  
    // 依次执行
    for _, s := range stmts {
        if _, err := db.Exec(s); err != nil {
            return err
        }
    }
  
    // 安全添加新列（忽略"列已存在"错误）
    db.Exec(`ALTER TABLE drones ADD COLUMN video_url TEXT DEFAULT ''`)
  
    return nil
}
```

### 4.4 LLM智能飞行规划

#### 核心流程

1. **构建Prompt**: 将飞行需求、禁飞区、约束条件转换为自然语言
2. **调用LLM API**: 发送到通义千问/GPT等大模型
3. **解析JSON**: 提取航点、动作、预估信息
4. **验证与纠偏**: 检查禁飞区冲突，自动绕行

**关键代码**:

```go
func (c *Client) GeneratePlan(req *PlanRequest) (*PlanResult, error) {
    // 1. 构建用户提示词
    userPrompt := fmt.Sprintf(`起点: (%.6f, %.6f, %.1fm)
终点: (%.6f, %.6f, %.1fm)
任务动作: %s
禁飞区: %d个
最大高度: %.1fm
最大速度: %.1fm/s`,
        req.Start.Lat, req.Start.Lon, req.Start.AltM,
        req.Goal.Lat, req.Goal.Lon, req.Goal.AltM,
        actionsJSON,
        len(req.Constraints.NoFlyZones),
        req.Constraints.MaxAltM,
        req.Constraints.MaxSpeedMPS,
    )
  
    // 2. 调用LLM
    respText, err := c.call(systemPrompt, userPrompt)
  
    // 3. 解析JSON
    var result PlanResult
    if err := json.Unmarshal([]byte(respText), &result); err != nil {
        return nil, err
    }
  
    // 4. 验证并自动纠偏
    warnings := Validate(req, &result)
    if hasNFZViolation(warnings) {
        result = autoCorrectNFZ(req, &result)
    }
  
    return &result, nil
}
```

### 4.5 实时数据推送架构

#### WebSocket Hub模式

```go
// Hub管理所有WebSocket连接
type WSHub struct {
    mu      sync.RWMutex
    clients map[string]map[*wsClient]struct{}  // topic -> clients
}

// 订阅主题
func (h *WSHub) Subscribe(topic string, conn *websocket.Conn) {
    h.mu.Lock()
    defer h.mu.Unlock()
  
    if h.clients[topic] == nil {
        h.clients[topic] = make(map[*wsClient]struct{})
    }
    h.clients[topic][cl] = struct{}{}
}

// 广播事件
func (h *WSHub) Broadcast(topic string, event WSEvent) {
    data, _ := json.Marshal(event)
  
    h.mu.RLock()
    snapshot := make([]*wsClient, 0, len(h.clients[topic]))
    for cl := range h.clients[topic] {
        snapshot = append(snapshot, cl)
    }
    h.mu.RUnlock()
  
    // 向所有订阅者发送
    for _, cl := range snapshot {
        cl.conn.WriteMessage(websocket.TextMessage, data)
    }
}
```

**应用场景**:

- GPS位置实时更新
- 电池电量实时监控
- 飞行任务状态变更
- 系统告警推送

### 4.6 优雅关闭（Graceful Shutdown）

```go
srv := &http.Server{Addr: addr, Handler: r}

// 在goroutine中启动服务器
go func() {
    if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
        log.Fatalf("listen: %v", err)
    }
}()

// 监听系统信号
quit := make(chan os.Signal, 1)
signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
<-quit  // 阻塞等待信号

log.Printf("shutting down...")

// 5秒超时的优雅关闭
ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
defer cancel()

if err := srv.Shutdown(ctx); err != nil {
    log.Fatalf("Server Shutdown: %v", err)
}
```

**为什么需要优雅关闭**:

- 等待正在处理的请求完成
- 关闭数据库连接
- 保存未完成的任务
- 避免数据丢失

---

## 5. 常见答辩问题及回答

### Q1: 为什么选择Go语言而不是Python/Java？

**回答要点**:

1. **高并发性能**: Go的goroutine比线程更轻量，我们的系统需要同时处理多个无人机的实时数据推送，Go的并发模型非常适合
2. **部署简单**: Go编译成单一可执行文件，无需安装运行时环境，适合边缘计算场景（地面站电脑）
3. **内存占用低**: 相比Java，Go程序内存占用更小，适合资源受限的嵌入式设备
4. **开发效率**: 相比C/C++，Go有垃圾回收和更简洁的语法，开发效率更高

### Q2: Gin框架相比其他框架有什么优势？

**回答要点**:

1. **性能优秀**: Gin基于httprouter，路由查找速度快，性能测试中QPS高于大多数框架
2. **轻量级**: 核心代码简洁，学习曲线平缓
3. **中间件生态**: 支持中间件链式调用，我们实现了日志、认证、限流等中间件
4. **JSON处理**: 内置高效的JSON序列化，适合RESTful API开发

### Q3: 如何保证并发安全？

**回答要点**:

1. **数据库连接池**: `database/sql`包自带连接池，线程安全
2. **互斥锁**: WebSocket Hub使用 `sync.RWMutex`保护共享的clients map
3. **Channel通信**: 使用channel在goroutine间传递数据，避免共享内存
4. **原子操作**: 对于简单计数器，可以使用 `sync/atomic`包

**代码示例**:

```go
type WSHub struct {
    mu      sync.RWMutex  // 读写锁
    clients map[string]map[*wsClient]struct{}
}

// 写操作用Lock
func (h *WSHub) Subscribe(topic string, conn *websocket.Conn) {
    h.mu.Lock()
    defer h.mu.Unlock()
    // 修改map
}

// 读操作用RLock（允许多个goroutine同时读）
func (h *WSHub) Broadcast(topic string, event WSEvent) {
    h.mu.RLock()
    defer h.mu.RUnlock()
    // 读取map
}
```

### Q4: 如何处理数据库连接？

**回答要点**:

1. **连接池管理**: `sql.DB`是连接池，不是单个连接，可以被多个goroutine安全使用
2. **延迟关闭**: 使用 `defer database.Close()`确保程序退出时关闭连接
3. **错误处理**: 每次数据库操作都检查错误，避免空指针
4. **事务支持**: 对于需要原子性的操作，使用 `db.Begin()`开启事务

### Q5: WebSocket和HTTP轮询有什么区别？

**回答要点**:

| 特性     | WebSocket              | HTTP轮询               |
| -------- | ---------------------- | ---------------------- |
| 连接方式 | 长连接（全双工）       | 短连接（单向）         |
| 实时性   | 服务器主动推送，延迟低 | 客户端定时请求，有延迟 |
| 资源消耗 | 单次握手后保持连接     | 每次请求都要建立连接   |
| 适用场景 | 实时数据（GPS、电池）  | 低频更新数据           |

**我们的应用**:

- GPS位置每秒更新，用WebSocket推送
- 无人机列表查询，用HTTP GET

### Q6: 如何实现限流（Rate Limiting）？

**回答要点**:

1. **令牌桶算法**: 每个IP维护一个令牌桶，固定速率补充令牌
2. **滑动窗口**: 记录每个IP在时间窗口内的请求次数
3. **中间件实现**: 在Gin中间件中检查，超限返回429状态码

**项目实现**:

```go
type RateLimiter struct {
    rate     int           // 每分钟允许请求数
    window   time.Duration // 时间窗口
    mu       sync.Mutex
    ips      map[string]*bucket
}

func (rl *RateLimiter) Middleware() gin.HandlerFunc {
    return func(c *gin.Context) {
        ip := c.ClientIP()
        if !rl.allow(ip) {
            c.AbortWithStatusJSON(429, gin.H{
                "error": "too many requests",
            })
            return
        }
        c.Next()
    }
}
```

### Q7: LLM飞行规划的原理是什么？

**回答要点**:

1. **Prompt工程**: 将飞行需求、禁飞区、约束条件转换为结构化的自然语言提示词
2. **JSON输出**: 要求LLM输出严格的JSON格式（航点、动作、预估）
3. **后处理验证**:
   - 检查航点是否在禁飞区内
   - 验证高度、速度是否超限
   - 计算实际距离和电量消耗
4. **自动纠偏**: 如果LLM生成的路径穿越禁飞区，使用可视图算法（Visibility Graph）自动计算绕行路径

**关键技术**:

- **Haversine公式**: 计算地球表面两点间距离
- **射线法（Ray Casting）**: 判断点是否在多边形内
- **Dijkstra算法**: 在可视图中寻找最短绕行路径

### Q8: 如何保证API的安全性？

**回答要点**:

1. **Token认证**: 使用Bearer Token验证API请求
2. **CORS配置**: 限制跨域访问来源
3. **SQL注入防护**: 使用参数化查询（`?`占位符）
4. **限流保护**: 防止DDoS攻击
5. **HTTPS**: 生产环境使用TLS加密传输

**代码示例**:

```go
// 参数化查询防止SQL注入
db.Query("SELECT * FROM drones WHERE name=?", userInput)  // 安全
// db.Query("SELECT * FROM drones WHERE name='" + userInput + "'")  // 危险！

// Token认证
hdr := c.GetHeader("Authorization")
if strings.HasPrefix(hdr, "Bearer ") && hdr[7:] == token {
    c.Next()
} else {
    c.AbortWithStatusJSON(401, gin.H{"error": "unauthorized"})
}
```

### Q9: 如何监控系统运行状态？

**回答要点**:

1. **系统指标采集**: 使用 `gopsutil`库获取CPU、内存、磁盘使用率
2. **阈值告警**: 后台goroutine定时检查，超过阈值写入alerts表
3. **日志记录**: 中间件记录所有HTTP请求的耗时、状态码、IP
4. **性能分析**: 记录响应时间、吞吐量、错误率到perf_reports表

**代码示例**:

```go
func runThresholdAlerts(db *sql.DB, thCPU, thMEM, thDISK int, interval time.Duration) {
    ticker := time.NewTicker(interval)
    defer ticker.Stop()
  
    for {
        <-ticker.C
        m, _ := monitor.CollectMetrics()  // 采集指标
    
        if m.CPUPercent >= float64(thCPU) {
            db.Exec(`INSERT INTO alerts(category, severity, message) 
                     VALUES(?,?,?)`, 
                     "threshold", "warning", 
                     fmt.Sprintf("CPU利用率%.1f%%超过阈值%d%%", m.CPUPercent, thCPU))
        }
    }
}
```

### Q10: 项目的扩展性如何？

**回答要点**:

1. **模块化设计**: 按功能划分包（handlers、db、llm、monitor），职责清晰
2. **接口抽象**: 数据库、LLM客户端都可以替换实现
3. **水平扩展**:
   - 数据库可迁移到PostgreSQL/MySQL
   - 使用Redis做分布式缓存
   - 多实例部署+负载均衡
4. **微服务化**: 可将Agent、LLM规划、监控拆分为独立服务

---

## 📌 答辩技巧总结

### 1. 回答框架

1. **先说结论**: 直接回答问题核心
2. **举例说明**: 引用项目中的具体代码
3. **对比优势**: 说明为什么这样设计
4. **承认不足**: 诚实说明改进方向

### 2. 常用话术

- "我们选择Go是因为..."（技术选型）
- "这里使用了...设计模式"（展示理解）
- "为了保证...，我们采用了..."（问题导向）
- "这部分还可以优化，比如..."（展示思考）

### 3. 避免的坑

- ❌ 不要说"不知道"，可以说"这个我还在学习中"
- ❌ 不要背诵代码，用自己的话解释逻辑
- ❌ 不要夸大功能，诚实说明实现程度
- ✅ 多用"我们实现了..."而非"我做了..."（团队协作）

### 4. 加分项

- 提到**并发安全**、**错误处理**、**性能优化**
- 说明**为什么这样设计**而不是只说"怎么做"
- 展示对**Go语言特性**的理解（goroutine、channel、defer）
- 提及**工程实践**（日志、监控、测试）

---

## 🎯 核心知识点速记卡

### Go语言三大特色

1. **Goroutine**: 轻量级并发，`go func()`启动
2. **Channel**: 协程通信，`ch := make(chan int)`
3. **Defer**: 延迟执行，常用于资源清理

### Gin框架核心

1. **路由**: `r.GET/POST/PUT/DELETE(path, handler)`
2. **上下文**: `c.Query/Param/BindJSON/JSON`
3. **中间件**: `r.Use(middleware())`

### 数据库操作

1. **查询**: `db.Query()` + `rows.Scan()`
2. **单行**: `db.QueryRow().Scan()`
3. **写入**: `db.Exec()`
4. **防注入**: 使用 `?`占位符

### 并发安全

1. **互斥锁**: `sync.Mutex` / `sync.RWMutex`
2. **原子操作**: `sync/atomic`
3. **Channel**: 通过通信共享内存

### 错误处理

```go
result, err := someFunc()
if err != nil {
    // 处理错误
}
```
