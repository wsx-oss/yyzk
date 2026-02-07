# 10 大模块：当前是否“静态展示”与可跑通性核查 + 真功能落地路线

---

## 0. 判定标准（我如何判断“静态/可跑通”）

- **真功能（可跑通）**：模块页（`project/web/modules/*.html`）里存在真实 `fetch('/api/...')` / `WebSocket('/api/...')` 调用，且后端 `handlers` 中有对应实现（涉及 DB/文件/WS）。
- **半真功能（后端有，前端未接）**：后端已有对应 API（`project/internal/handlers/api.go`），但模块页仅写死表格/图表，按钮函数只弹 `showToast()`。
- **静态展示（占位）**：前端展示写死，后端也缺少与之匹配的完整数据模型/接口（或接口是硬编码 demo）。

---

## 1. 总览结论（10 大模块一眼看懂）

| 模块             | 当前状态                          | 是否实时数据                                    | 关键证据（代码位置）                                                                                       |
| ---------------- | --------------------------------- | ----------------------------------------------- | ---------------------------------------------------------------------------------------------------------- |
| 1. 系统状态监控  | 半真功能                          | **模块页不实时**；项目里另有实时实现      | 后端 `GET/WS /api/metrics/*` 已实现；`modules/monitor.html` 图表/表格写死；`web/script.js` 真实连 WS |
| 2. 语音交互记录  | 半真功能                          | **模块页不实时**；项目里另有真实 API 调用 | 后端 `/api/audio/*` 完整；`modules/audio.html` 表格写死；`web/script.js` 有上传/列表                 |
| 3. 远程桌面控制  | 真功能（VNC）/部分占位（RDP/SSH） | VNC 可实时连接                                  | `modules/remote.html` 真请求 `/api/devices`；`vnc.html` 真连 `/api/vnc/ws`；RDP/SSH UI 占位        |
| 4. 视频监控      | 静态展示                          | 否                                              | `modules/video.html` 仅静态表格 + `<video>` 空 source；无后端视频源管理/推流接口                       |
| 5. 硬件状态检测  | 半真功能                          | 模块页不实时                                    | 后端 `GET /api/hardware/snapshot` 已实现；`modules/hardware.html` 写死                                 |
| 6. 软件更新管理  | 静态/半真功能混合                 | 模块页不实时；check 为 demo                     | 后端 `updates/list/add` 可用；`updates/check` 硬编码返回；`modules/updates.html` 写死                |
| 7. 数据同步状态  | 半真功能                          | 模块页不实时                                    | 后端 `/api/sync/status` 已实现；`modules/sync.html` 写死                                               |
| 8. 异常报警      | 半真功能                          | 模块页不实时                                    | 后端 `/api/alerts/*` 已实现；`modules/alerts.html` 写死                                                |
| 9. 性能分析报告  | 半真功能                          | 模块页不实时                                    | 后端 `GET /api/report/perf` 已实现；`modules/performance.html` 写死                                    |
| 10. 维护操作日志 | 半真功能                          | 模块页不实时                                    | 后端 `/api/logs/*` 已实现；`modules/logs.html` 写死                                                    |

---

## 2. 模块逐项核查：现状、问题、最小落地方案

> 说明：下文“最小方案”尽量优先复用你们已经写好的后端接口，先把模块页从静态变成动态；需要新增后端能力的会单独标出。

### 2.1 模块 1：系统状态监控（M1）

- **现状**：

  - `project/web/modules/monitor.html`：
    - 表格数据写死（如 `2024-10-01 10:00`）
    - 图表数据写死（labels/data 固定）
    - `search()/refreshData()` 仅 toast，没有调用 API
  - 但后端与另一套前端代码具备实时能力：
    - 后端：`GET /api/metrics/snapshot`、`WS /api/metrics/stream`（`internal/handlers/api.go`）
    - 前端实时实现：`project/web/script.js`（`new WebSocket('.../api/metrics/stream')`）
- **结论**：**半真功能**（能力在，但模块页没接线）。
- **最小落地方案（建议步骤）**：

  1. 在 `modules/monitor.html` 中新增真实数据状态（数组/表格数据源）。
  2. 复用 `common.js` 的 `api()`：
     - 首次加载调用 `GET /api/metrics/snapshot` 填充数值。
  3. 增加 WebSocket：连接 `WS /api/metrics/stream`，按 1s 更新图表与“最新值”。
  4. “查询”按钮：如果要按时间筛选，你们需要新增后端“历史指标存储”能力（当前只采集实时）。
- **需要新增的后端能力（可选，取决于需求）**：

  - 若要支持“起止时间/历史表格”：需要新表 `metrics_history`，后台定时落库（例如每 5s/10s）。

---

### 2.2 模块 2：语音交互记录（M2）

- **现状**：

  - 后端已有完整音频管理 API：`/api/audio/upload/list/download/delete`（落库 `recordings` + 保存文件到 `data/recordings/`）。
  - `modules/audio.html`：表格写死、按钮无 API。
  - `project/web/index.html + project/web/script.js`：已能录音上传、拉列表。
- **结论**：**半真功能**。
- **最小落地方案（建议步骤）**：

  1. 在 `modules/audio.html` 增加：
     - `loadAudioList(page,page_size)` 调 `GET /api/audio/list` 渲染表格。
  2. 给每行加：
     - 下载链接 `/api/audio/download/:id`
     - 删除按钮调用 `DELETE /api/audio/:id`，成功后刷新列表。
  3. 若要“录制/上传”：
     - 直接把 `script.js` 里 `MediaRecorder + /api/audio/upload` 逻辑搬到 `modules/audio.html`。

---

### 2.3 模块 3：远程桌面控制（M3）

- **现状**：

  - `modules/remote.html` 已对接后端：
    - 设备列表：`GET /api/devices`
    - 新增：`POST /api/devices`
    - 删除：`DELETE /api/devices/:id`
  - VNC 连接：打开 `vnc.html`，其内部通过 noVNC 连接 `WS /api/vnc/ws?target=...`。
  - 设备状态更新：`vnc.html` 调 `POST /api/devices/:id/status`。
  - 统计：`GET /api/user/stats` / `POST /api/user/stats/incr_connection`。
- **结论**：

  - **VNC 路线：真功能，可跑通**（前提：目标机器有 VNC 服务 + 网络通）。
  - **RDP/SSH：目前是占位**（UI 里有选项，但连接逻辑明确只支持 VNC）。
- **增强路线（可选）**：

  - 若要支持 RDP/SSH：需要新增后端代理/隧道能力 + 前端客户端方案（浏览器原生无法直接 RDP/SSH）。
  - 实际可行路线通常是：
    - SSH：后端开 WebSocket 终端（xterm.js）
    - RDP：使用网关（如 guacamole）或自研代理（成本较高）

---

### 2.4 模块 4：视频监控（M4）

- **现状**：

  - `modules/video.html`：静态表格；`<video>` 没有真实流地址绑定。
  - 后端没有“视频源管理、拉流、转码、鉴权”的接口。
- **结论**：**静态展示**。
- **真功能落地路线（建议步骤）**：

  1. 明确需求形态（二选一）：
     - A：只做**浏览器摄像头预览**（纯前端 `getUserMedia`，不涉及后端存储/流管理）。
     - B：做**RTSP/HTTP 摄像头接入**（需要后端/流媒体组件）。
  2. 如果选 A（最快落地）：
     - `modules/video.html` 增加 `navigator.mediaDevices.getUserMedia({video:true})`，把流绑定到 `<video>`。
  3. 如果选 B（真实监控平台）：
     - 增加表 `video_sources`（name/url/region/status/...）。
     - 新增 API：`GET/POST/DELETE /api/video/sources`。
     - 播放：推荐使用 HLS/WebRTC（浏览器不直接播 RTSP），通常需要引入第三方服务（成本与工作量明显上升）。

---

### 2.5 模块 5：硬件状态检测（M5）

- **现状**：

  - 后端：`GET /api/hardware/snapshot` 已实现。
  - `modules/hardware.html`：静态表格/图表，按钮不调用 API。
- **结论**：**半真功能**。
- **最小落地方案（建议步骤）**：

  1. 页面加载时调用 `api('/hardware/snapshot')`。
  2. 将返回数据映射到表格的“硬件名称/状态/温度/CPU/内存/检测时间”。
  3. 增加定时刷新（例如 3s/5s 轮询）。

---

### 2.6 模块 6：软件更新管理（M6）

- **现状**：

  - 后端：
    - `GET /api/updates/list`、`POST /api/updates/add`：可用（DB）。
    - `GET /api/updates/check`：当前为**硬编码 demo 返回**。
  - `modules/updates.html`：静态表格/图表。
- **结论**：当前属于 **“界面静态 + 后端部分可用 + check 为演示”**。
- **真功能落地路线（建议步骤）**：

  1. 先把模块页对接 list/add：
     - `GET /api/updates/list` 渲染列表。
     - 新增表单调用 `POST /api/updates/add`。
  2. 定义“检查更新”的真实来源（二选一）：
     - A：以 DB 中 `updates` 的最新 `version` 为准（最简单）。
     - B：对接外部更新源（HTTP 拉取最新版本信息）。
  3. 改造 `/api/updates/check`：从真实来源计算 `latest_version/has_update`。

---

### 2.7 模块 7：数据同步状态（M7）

- **现状**：

  - 后端：`GET/POST /api/sync/status` 已实现（写 `sync_status` 表）。
  - `modules/sync.html`：静态表格/图表，`syncNow()` 仅 toast。
- **结论**：**半真功能**。
- **最小落地方案（建议步骤）**：

  1. 页面加载调用 `GET /api/sync/status`，渲染当前状态（status/message/last_synced_at）。
  2. “立即同步”按钮调用 `POST /api/sync/status` 写入 `syncing` 状态，并刷新。
  3. 如果你们要实现真正“同步任务”（跨机器同步文件/数据）：需要另做同步引擎（目前只有状态表）。

---

### 2.8 模块 8：异常报警（M8）

- **现状**：

  - 后端：
    - `GET /api/alerts/list`（分页）
    - `POST /api/alerts/ack/:id`
    - `POST /api/alerts/new`
  - `modules/alerts.html`：静态表格；“新增/导出”均 toast。
- **结论**：**半真功能**。
- **最小落地方案（建议步骤）**：

  1. 用 `GET /api/alerts/list` 替换静态表格数据。
  2. “标记已读”按钮 -> `POST /api/alerts/ack/:id`。
  3. “新增报警”按钮 -> 弹窗表单 -> `POST /api/alerts/new`。

---

### 2.9 模块 9：性能分析报告（M9）

- **现状**：

  - 后端：`GET /api/report/perf` 已实现（汇总实时指标 + logs/alerts 计数）。
  - `modules/performance.html`：静态表格/图表；按钮 toast。
- **结论**：**半真功能**。
- **最小落地方案（建议步骤）**：

  1. 页面加载时调用 `GET /api/report/perf`。
  2. 将返回 JSON：
     - 渲染到“报告摘要/指标/日志数/告警数/告警分布”。
  3. 图表改为使用真实返回数据（例如 CPU/内存作为趋势需要你们先落历史，或先画“当前快照”柱状图）。

---

### 2.10 模块 10：维护操作日志（M10）

- **现状**：

  - 后端：`POST /api/logs/append`、`GET /api/logs/list` 已实现（DB）。
  - `modules/logs.html`：静态表格；`search/export/add` toast。
- **结论**：**半真功能**。
- **最小落地方案（建议步骤）**：

  1. `GET /api/logs/list` 渲染列表。
  2. “新增日志”按钮 -> 弹窗表单 -> `POST /api/logs/append`。
  3. 如果需要“导出”：前端导出 CSV（用当前列表数据即可）。

---

## 3. 把 10 大模块都变成真功能：推荐路线（按优先级）

### 阶段 1（1 天内可见效果）：把“后端已具备”的模块页全部接通

目标：不改或少改后端，把大部分模块从“静态”变成“动态”。

1. **接通列表类模块**：
   - 音频（list/delete/download）
   - 告警（list/ack/new）
   - 日志（list/append）
   - 更新（list/add）
   - 同步（get/set status）
2. **接通快照类模块**：
   - 硬件（hardware snapshot）
3. **接通监控实时**：
   - monitor 模块连 `WS /api/metrics/stream`

> 代码落点：
>
> - 前端主要改 `project/web/modules/*.html`
> - 调接口统一使用 `project/web/modules/common.js` 的 `api()`

### 阶段 2（1-3 天）：补齐“真正需要历史/任务引擎”的能力

1. **监控历史与筛选（monitor 查询条件）**
   - 新表 `metrics_history`
   - 后台 goroutine 周期采集落库
   - 新 API：`GET /api/metrics/history?start=&end=&...`
2. **性能报告趋势化（performance）**
   - 复用 `metrics_history` 或单独存 report
   - 让图表不再是固定演示数据
3. **同步任务真实化（sync）**
   - 如果只是状态展示：阶段 1 足够
   - 若要真实同步：定义同步对象（文件/配置/数据库），实现任务执行、进度、失败重试

### 阶段 3（工作量最大）：视频监控与 RDP/SSH

1. **视频监控**
   - 最快：仅浏览器摄像头预览（纯前端）
   - 真实摄像头/RTSP：需要引入流媒体能力（HLS/WebRTC/转码），后端要做源管理与鉴权
2. **RDP/SSH**
   - SSH：WebSocket + xterm.js（可落地）
   - RDP：推荐使用成熟网关（如 guacamole），否则自研成本高

---

## 4. 建议你接下来如何“逐一解决”

为了你后面逐个修，我建议你按这个顺序：

1. **先做“接线型改造”**（最容易、最像真系统）：
   - `modules/monitor.html`（实时）
   - `modules/remote.html`（已经真了）
   - `modules/alerts.html` + `modules/logs.html`
2. **再做“文件/DB型改造”**：
   - `modules/audio.html`
   - `modules/updates.html`
   - `modules/sync.html`
3. **最后做“需要新能力”的模块**：
   - 视频监控（选路线 A 或 B）
   - RDP/SSH

---

## 5. 附：关键代码索引（方便你定位）

- **后端路由与实现**：`project/internal/handlers/api.go`、`project/internal/handlers/auth.go`
- **DB 表结构**：`project/internal/db/db.go`（`Migrate()`）
- **前端模块页**：`project/web/modules/*.html`
- **前端统一 API helper**：`project/web/modules/common.js`
- **现有“真实动态演示页”**：`project/web/index.html` + `project/web/script.js`
- **VNC 真功能页**：`project/web/vnc.html`
