# CloudControl v4.0 优化 TODO

> **文档创建时间**：2026-04-10
> **最后更新**：2026-04-11
> **项目名称**：CloudControl — 企业级智能无人机管控平台
> **技术栈**：Go 1.24 + Gin + MySQL(Aiven) + Leaflet + WebSocket + Chart.js
> **前端架构**：`dashboard.html` 壳层 + iframe 加载 22 个模块页 + `ai-assistant.js` 浮窗 + `notification-bell.js` 铃铛
> **当前版本**：v3.7.0
> **本轮目标**：5 大类 23 项优化，覆盖地图、界面、性能、业务、AI、视觉全链路
> **进度汇总**：已完成 10 项 ✅ | 部分完成 4 项 ⚠️ | 待开始 9 项 ⬜

---

## 总览

| 分类               | 编号 | 标题                                 | 优先级 | 状态 |
| ------------------ | ---- | ------------------------------------ | ------ | ---- |
| 一、基础地图与界面 | 1    | ✅ 仿真地图 → 天地图                | P0     | 已完成 |
|                    | 2    | ✅ AI 助手图标+位置优化              | P1     | 已完成 |
|                    | 3    | ✅ 仿真模拟事件日志优化            | P1     | 已完成 |
|                    | 4    | ✅ 导出→展示（自动跳转模拟器任务建设）| P1   | 已完成 |
| 二、系统功能与性能 | 5    | CoT 思维链优化                       | P1     | 待开始 |
|                    | 6    | ✅ 离线状态下取消相关异常通知       | P1     | 已完成 |
|                    | 7    | ✅ 并发任务·RPM 优化                  | P2     | 已完成 |
|                    | 8    | 前端整体性能优化                     | P1     | 待开始 |
|                    | 9    | ⚠️ 模拟器任务建设优化               | P0     | 部分完成 |
|                    | 10   | ⚠️ 模拟机电量+图表优化              | P1     | 部分完成 |
| 三、业务功能与视觉 | 11   | ✅ 轨迹弧度（曲线化）优化            | P2     | 已完成 |
|                    | 12   | 视频监控模块位置调整                 | P2     | 待开始 |
|                    | 13   | Logo 优化                            | P1     | 待开始 |
|                    | 14   | ✅ 登录界面背景优化                  | P1     | 已完成 |
|                    | 15   | 项目命名 ★                          | P0     | 待开始 |
| 四、AI 能力与配置  | 16   | LLM API / Deepseek 适配              | P0     | 待开始 |
|                    | 17   | PPT 首页 22U/背景图+无人机元素       | P2     | 待开始 |
|                    | 18   | ✅ RAG 检索增强生成优化              | P1     | 已完成 |
|                    | 19   | 用户默认头像使用项目 Logo            | P2     | 待开始 |
| 五、前端细节与系统 | 20   | 前端全面圆角化                       | P1     | 待开始 |
|                    | 21   | ⚠️ 毛玻璃/磨砂感视觉效果            | P1     | 部分完成 |
|                    | 22   | 域名优化                             | P2     | 待开始 |
|                    | 23   | ⚠️ 动态性优化                       | P1     | 部分完成 |

---

## 一、基础地图与界面元素优化（1-4 项）

### TODO-01 ★ 仿真地图 → 天地图

> **✅ 已完成**（v3.7.0）

**现状**：

- `simulation.html` 地图使用 CartoDB 暗色瓦片（`basemaps.cartocdn.com/dark_all`），中心为成都 `[30.5728, 104.0668]`
- `gps.html` / `flight.html` / `noflyzone.html` 已接入天地图（`tdtImgLayer` + `tdtCiaLayer`），`.env` 已配 `TIANDITU_KEY`
- 天地图 Key 在各页面内各自硬编码，未统一管理
- 地图范围改为郑州大学 `[34.748, 113.655]`

**任务清单**：

- [x] 替换 `simulation.html` 的 `L.tileLayer` 瓦片源为天地图影像底图（`img_w`）+ 标注图层（`cia_w`）
- [x] 在 `common.js` 中统一导出 `TIANDITU_KEY` 常量和 `tdtImgLayer()` / `tdtCiaLayer()` 工厂函数，消除各页面重复定义（同时提供 `loadTdtConfig()` 从后端获取 Key）
- [x] 统一 `simulation.html` 默认中心点为郑州大学附近 `[34.7930, 113.6636]`
- [x] 适配仿真页暗色主题：对标注层（`ciaLayer`）添加 `.tdt-cia-dark` CSS 类实现暗色适配
- [x] 使用 `L.canvas()` 渲染器确保大量无人机轨迹的性能
- [ ] 回归验证：无人机图标、轨迹线、航线标注、碰撞规避动画在新瓦片下正常渲染（待人工验证）

**涉及文件**：`simulation.html` · `common.js` · `gps.html` · `flight.html` · `noflyzone.html` · `.env`

---

### TODO-02 AI 助手图标 + 位置优化

> **✅ 已完成**（v3.7.0）

**现状**：

- `ai-assistant.js` 注入右下角 56×56px 圆形 FAB 按钮（`#ai-fab`），`bottom:28px; right:28px`
- 图标为 emoji 文字（🤖），面板 `#ai-panel` 为 400×600px 白色弹窗

**任务清单**：

- [x] 替换 `#ai-fab` 图标为 SVG 矢量图标（`SVG_SPARKLES` + `SVG_BOT_AVATAR`），提升清晰度和品质感
- [x] 增大按钮至 60×60px，增加呼吸脉冲动画（`@keyframes aiFabPulse`）
- [x] 为 `#ai-panel` 弹窗加毛玻璃效果（`backdrop-filter: blur(16px); background: rgba(255,255,255,0.85)`），圆角 20px
- [x] 小屏适配：面板宽度改为 `min(400px, calc(100vw - 56px))`
- [x] 可拖拽 FAB：`pointerdown/pointermove/pointerup` 实现拖拽，位置持久化到 `localStorage('ai_fab_pos')`

**涉及文件**：`ai-assistant.js`

---

### TODO-03 仿真模拟事件 日志优化

- 改为异常日志
  **现状**：
- `simulation.html` 事件日志面板通过 WebSocket `/api/sim/stream` 实时接收所有事件（正常状态变更 + 异常事件）
- 正常/异常日志混排，重要异常被淹没
- 事件类型：状态变更(正常)、低电量/偏航/失联/温度异常/碰撞预警(异常)

**任务清单**：

- [x] 新增日志筛选 Tab：`全部` | `异常`，默认选中「异常」，视觉上将“全部”/“正常”置灰或用删除线标记
- [x] 后端 `/api/sim/stream` 增加可选 query 参数 `?filter=anomaly`，只推送异常类型事件
- [x] 异常日志展示优化：
  - 按等级显示红色（critical: 失联/碰撞）、橙色（warning: 低电量/偏航）、黄色（info: 温度异常）badge
  - 每条日志增加实例名称高亮、异常类型图标、时间戳格式化
- [x] 面板标题增加实时异常计数 badge（红色圆点 + 数字）
- [x] 自动滚动：新日志到达时自动滚到底部，提供「暂停滚动」锁定按钮

**涉及文件**：`simulation.html` · `simulation.go`（可选 stream 过滤）

---

### TODO-04 导出 → 展示（自动跳转模拟器任务建设）

> **✅ 已完成**（v3.7.0）

**现状**：

- `simulation.html` RL 面板的 `exportRLPolicy()` 导出后仅在当前页内 `#rlExportCard` 展示策略详情
- `dashboard.html` 提供全局 `window.navigateTo(page)` 实现 iframe 模块切换
- 无导出后跳转或策略应用到任务的联动逻辑

**任务清单**：

- [x] 在 `#rlExportCard` 底部增加「🚀 应用策略到新批次」按钮（`applyPolicyToBatch()` 函数）
- [x] 导出成功后弹出 Toast：「策略已展示，可点击「应用策略到新批次」创建仿真批次」
- [x] 支持 URL hash 定位：`simulation.html#rl-export` 自动切换到 RL Tab 并展开导出卡片
- [x] `dashboard.html` 的 `navigateTo()` 支持 hash 片段（`page#fragment` 格式）
- [ ] 点击「应用策略」后自动切换到批次管理 Tab 并预填策略参数（按钮已有但跳转逻辑待完善）

**涉及文件**：`simulation.html`（`exportRLPolicy()` + Tab 切换） · `dashboard.html`（`navigateTo` hash 支持）

---

## 二、系统功能与性能优化（5-10 项）

### TODO-05 CoT（思维链）优化

- 改名称
  **现状**：
- `cot.html` 展示 CoT 推理记录，支持 4 种任务类型：`scheduling` / `fault_diagnosis` / `path_optimization` / `mission_planning`
- 后端 `cot.go` 调用 LLM 生成 `ReasoningStep` 链，含置信度评分
- 展示为纯列表 + 文本，缺少可视化推理路径

**任务清单**：

- [ ] **推理过程可视化**：将步骤列表升级为竖向时间线（Timeline UI），每步一张卡片：标题/输入/推理/输出，步骤间用连接线串联
- [ ] **实时流式展示**：发起分析时采用 SSE 或长轮询逐步展示推理过程（打字机效果），提升交互感知
- [ ] **结果联动跳转**：
  - 故障诊断 → 「跳转到告警页」/「跳转到电池页」按钮
  - 路径优化 → 「在地图上查看」按钮（跳转 flight.html）
  - 任务规划 → 「创建飞行任务」快捷按钮
- [ ] **搜索与筛选增强**：增加关键字搜索、时间范围选择器、置信度区间滑块
- [ ] **双链对比**：支持勾选两条历史推理链并排对比分析

**涉及文件**：`cot.html` · `cot.go` · `internal/cot/`

---

### TODO-06 离线状态下取消相关异常通知

> **✅ 已完成**（v3.8.0）

**现状**：

- `patrol.go` 定时巡检产生通知写入 `notifications` 表，`notification-bell.js` 展示
- 离线时（网络断开/LLM 不可达/DB 超时），巡检可能产生大量“连接失败”通知，形成通知风暴
- 无离线状态检测和降级机制

**任务清单**：

- [x] **通知去重/抑制（无人机失联冷却）**：`patrol.go` 中无人机下线通知已实现 5 分钟/台冷却期（`notified map[int]time.Time`），防止反复触发
- [x] **后端离线检测**：`patrol.go` 巡检前 `wrapWithHealthCheck` 预检DB ping + LLM HEAD，离线时跳过巡检
- [x] **前端离线提示**：`notification-bell.js` 检测 `navigator.onLine` + fetch 失败计数，显示离线 badge，暂停轮询
- [x] **离线通知静默**：离线期间巡检跳过不生成通知，恢复时自动批量折叠已读 + 插入恢复摘要
- [x] **systemd/健康检查联动**：`/api/healthz` 返回 `db_reachable`、`llm_reachable`、`patrol_online`、`uptime_s`

**涉及文件**：`patrol.go` · `notification.go` · `notification-bell.js` · `main.go`（healthz）

---

### TODO-07 并发任务 · RPM 优化

- 增加RPM优化

> **✅ 已完成**（v3.8.0）

**现状**：

- `internal/taskpool/pool.go` 提供 IO(16 workers) + CPU(4 workers) 双池，`batcher.go` 提供 WriteBatcher / Throttler / StatsCache
- 当前限流为 IP 级别 500 次/分钟（`middleware/`），无针对 LLM API 的独立 RPM 控制
- LLM 调用集中在航线规划（`flight_plan.go`）、CoT 分析（`cot.go`）、AI 助手（`ai_assistant.go`）

**任务清单**：

- [x] **LLM 请求速率控制**：`llm.go` 增加 `golang.org/x/time/rate` 令牌桶限流器，按 `LLM_RPM`（默认 30）控制，每次调用前 Wait
- [x] **RPM 可视化**：`concurrency.html` 增加 LLM RPM 监控卡片（当前分钟/上限/排队/平均延迟 + 进度条）
- [x] **任务优先级细化**：航线规划使用 `PriorityHigh`，巡检使用 `PriorityNormal/Low`，用户操作优先
- [x] **超时策略调优**：LLM 超时支持 `LLM_TIMEOUT_SEC` 环境变量配置（默认 60s）
- [x] **并发指标扩展**：`/api/taskpool/metrics` 增加 `llm_rpm` 指标组（current/limit/failed/avg_latency），另有 `/api/taskpool/llm-rpm` 独立端点

**涉及文件**：`internal/llm/llm.go` · `internal/taskpool/pool.go` · `concurrency.html` · `taskpool_api.go` · `.env`

---

### TODO-08 前端整体性能优化

- 参考两个网页

**现状**：

- 前端为多 HTML 页面 + iframe 架构，每个模块页独立加载 CSS/JS/Chart.js/Leaflet 等外部依赖
- 外部 CDN 依赖：`chart.js`（~60KB）、`leaflet`（~40KB+CSS）、`unpkg/cdn.jsdelivr`
- 无资源预加载、无懒加载、无公共资源复用机制

**任务清单**：

- [ ] **CDN 资源本地化**：将 Chart.js、Leaflet 等高频外部依赖下载到 `web/libs/` 目录，由后端 embed 提供，消除 CDN 依赖和网络延迟
- [ ] **公共脚本合并**：将 `common.js` + `common.css` + 天地图工具函数合并为统一的公共 bundle，减少每个 iframe 重复请求
- [ ] **图片/SVG 优化**：Logo、图标等资源统一使用 SVG 矢量格式，减小体积
- [ ] **Lazy Loading**：对非首屏 Tab 内容实现懒加载（如 simulation.html 中 RL/Statistics 面板只在切换到对应 Tab 时才加载图表）
- [ ] **WebSocket 连接管理**：多个模块各自建立 WS 连接，考虑在 `dashboard.html` 壳层建立统一 WS Hub，通过 postMessage 分发给各 iframe
- [ ] **请求合并**：多个模块启动时各自请求 `/api/stats/cached`，改为 `common.js` 统一请求 + 缓存 + 分发
- [ ] **首屏加载指标**：增加 `performance.mark` 埋点，在控制台或仪表盘展示 FCP/LCP 指标

**涉及文件**：`web/` 全局 · `dashboard.html` · `common.js` · `common.css` · 各模块 HTML

---

### TODO-09 ★ 模拟器任务建设优化

> **⚠️ 部分完成**（v3.7.0）

**现状**：

- `simulation.html` 已支持批次创建（`BatchCreate`）、实例管理、任务模板（巡逻/巡检/配送/测绘/搜救）、异常注入、RL 训练
- 创建批次通过弹窗表单完成：选择任务类型 → 填写数量/区域/参数 → 提交
- 缺少从策略导出 → 任务创建 → 执行监控 → 结果评估的完整闭环流程

**任务清单**：

- [x] **任务进度显示**：地图弹窗中已显示无人机任务进度条（`任务进度: X%`，含颜色渐变），实时反映完成度
- [x] **策略应用入口**：RL 导出后可通过「🚀 应用策略到新批次」按钮（`applyPolicyToBatch()`）联动批次创建
- [ ] **任务建设向导化**：将批次创建从单步弹窗升级为多步向导（模板选择 → 参数配置 → 关联策略 → 确认启动）
- [ ] **任务模板预览增强**：每种任务模板增加可视化预览卡片，展示典型航线轨迹缩略图
- [ ] **批量操作优化**：支持多选批次一键启动/停止/删除
- [ ] **任务执行看板**：汇总展示当前运行批次数 / 总实例数 / 在线率 / 进度环形图 / 异常趋势
- [ ] **任务结果导出**：支持批次运行数据导出为 CSV/JSON

**涉及文件**：`simulation.html`（前端向导 + 看板） · `simulation.go`（批次 API 增强）

---

### TODO-10 模拟机电量 + 图表优化

> **⚠️ 部分完成**（v3.8.0）— 低电量异常阈值已修复

**现状**：

- `battery.html` 展示电池实时数据：电量/电压/温度/健康度，含 Chart.js 折线图趋势
- `simulation/instance.go` 使用 `float64` 精确追踪模拟机电量，含 idle 充电逻辑
- `simulation.html` 统计面板有电池风险数统计，但无专门的电量图表

**任务清单**：

- [ ] **模拟机电量独立面板**：在 `simulation.html` 统计 Tab 中增加电量专区：
  - 所有模拟机电量实时排行（横向条形图，低电量红色高亮）
  - 电量分布饼图（健康 >60% / 警告 20%-60% / 危险 <20%）
  - 平均电量趋势折线图（近 1 小时）
- [ ] **电量图表样式优化**：
  - 使用渐变填充（绿→黄→红）替代单色填充
  - 增加阈值参考线（20% 危险线、60% 警告线）
  - 图表增加 Tooltip 展示详细数据
- [ ] **battery.html 图表升级**：
  - 电压/温度趋势图增加双 Y 轴（左侧电压 V、右侧温度 ℃）
  - 健康度历史增加区间着色（优秀/良好/一般/差 对应绿/蓝/黄/红背景带）
  - 响应式优化：小屏时图表自动缩放、Legend 折叠
- [x] **低电量异常自动检测**：`instance.go` `tick()` 中新增 ≤20% 电量自动注入 `low_battery` 警告异常（`AlertWarning`），实例管理表「异常」列实时显示 `low_battery` 标签；≤15% 时自动升级为 `AlertCritical` 并触发强制返航，`updateAnomalies()` 在电量恢复 >20% 后自动清除
- [ ] **电量告警图表联动**：模拟机电量低于阈值时，在图表上实时标注告警点（红色三角标记），并链接到通知中心

**涉及文件**：`simulation.html`（统计面板图表） · `battery.html`（电池监控图表） · `simulation/instance.go`（数据源）

---

## 三、业务功能与视觉设计优化（11-15 项）

### TODO-11 轨迹弧度（非直线）优化

**现状**：

- `gps.html` 历史轨迹、`flight.html` 航线展示、`simulation.html` 飞行轨迹均使用 Leaflet `L.polyline` 直线连接各航点
- 真实无人机飞行轨迹并非严格直线，直线展示缺乏真实感

**任务清单**：

- [x] **贝塞尔曲线替代直线**：新增 `curve-utils.js` 工具库，提供 `smoothRoute`（三次贝塞尔）和 `smoothTrail`（Catmull-Rom 样条），在相邻航点间生成平滑弧线
- [x] **弧度算法**：根据两点距离和航向变化自动计算控制点偏移量：
  - 短距离（<100m）：小弧度(0.15)
  - 中距离（500-2000m）：中弧度(0.30)
  - 长距离（>2km）：大弧度(0.38)
  - 急转弯航段(>60°)：curveFactor×1.3，奇偶段交替弯曲方向形成 S 曲线
- [x] **动画效果**：`animatePolyline` 渐进绘制动画（从起点到终点逐段绘制），支持 Canvas 渲染
- [x] **仿真地图轨迹适配**：`simulation.html` 实时轨迹使用 Catmull-Rom 平滑、航线使用贝塞尔曲线，采用 `L.canvas()` 渲染器确保大量无人机下性能
- [x] **可选开关**：`simulation.html` 和 `gps.html` 提供「曲线轨迹」切换开关，`flight.html` 默认启用曲线（异步加载 + typeof 安全检查）

**完成备注**：已完成贝塞尔曲线替代直线、弧度算法、动画效果、仿真地图轨迹适配和可选开关功能，新增 `curve-utils.js` 工具库。

**涉及文件**：`gps.html` · `flight.html` · `simulation.html` · 新增 `web/modules/curve-utils.js`

> **✅ 已完成**（v3.7.0）：`curve-utils.js` 工具库已集成到三个页面，包含贝塞尔曲线、Catmull-Rom 样条、Canvas 渲染、曲线/直线切换开关。

---

### TODO-12 视频监控/视频展示（放上面）

**现状**：

- `video.html` 在侧边栏「无人机状态监控」分组中，排在「远程桌面控制」后面
- `dashboard.html` 中导航顺序为：视频监控 → 远程桌面 → 异常报警 → 电池监控 → 硬件状态
- 视频监控作为高频使用的监控入口，位置偏下不够直觉

**任务清单**：

- [ ] **侧边栏导航调整**：将 `video` 从「无人机状态监控」子组中提升位置：
  - 方案 A：移至「无人机状态监控」分组的第一项（当前已是第一项）
  - 方案 B：独立成为顶级导航项，放在「无人机管理」正下方
  - 方案 C：创建「实时监控」独立分组，包含视频监控 + GPS 位置
- [ ] **视频模块面板增强**：视频播放区放大到页面上半部分（60% 高度），下方为无人机选择卡片网格
- [ ] **画中画模式**（可选）：支持视频窗口悬浮在其他模块之上，切换到航线/电池等页面时视频持续播放
- [ ] **多路视频分屏**：支持 2×2 / 3×3 分屏同时查看多路视频流

**涉及文件**：`dashboard.html`（侧边栏顺序） · `video.html`（布局优化）

---

### TODO-13 Logo 设计

**现状**：

- `dashboard.html` 顶部栏显示文字 `CloudControl`，侧边栏 header 显示「系统菜单」
- `login.html` / `register.html` 登录页显示 `CloudControl` 文字 Logo
- 无独立的 Logo 图片/SVG 文件

**任务清单**：

- [ ] **设计项目 Logo**：
  - 元素建议：云（Cloud）+ 无人机剪影 + 控制/信号图形
  - 风格：扁平/线性/渐变，与项目蓝紫色调（`#0ea5e9` ~ `#6366f1`）一致
  - 输出：SVG 矢量格式，含方形图标版（用于 favicon/头像）和横版文字版（用于顶栏/登录页）
- [ ] **全局应用 Logo**：
  - `dashboard.html` 侧边栏 header → 替换「系统菜单」为 Logo 图标 + 项目名
  - `dashboard.html` 顶部栏 → `<h1>` 前添加 Logo 小图标
  - `login.html` / `register.html` → 卡片顶部添加 Logo
  - 浏览器 favicon → 使用方形图标版
- [ ] **Logo 动效**（可选）：登录页 Logo 增加微动效（呼吸/渐现/旋转无人机螺旋桨）

**涉及文件**：`dashboard.html` · `login.html` · `register.html` · 新增 `web/assets/logo.svg` · `web/assets/favicon.svg`

---

### TODO-14 登录界面背景优化

> **✅ 已完成**（v3.7.0）

**现状**：

- `login.html` 背景已有高级效果：暗色径向渐变 + 动态网格 + 3 个光晕球（`glow-orb`）浮动动画 + 粒子连线效果
- 视觉质感已较高，但缺少无人机/科技感相关的具象元素
- `register.html` 背景风格应与登录页保持一致

**任务清单**：

- [x] **增加主题性背景元素**：`login.html` 已添加 3 个浮动无人机剪影 SVG（`.floating-drone.fd1/fd2/fd3`），低透明度（0.12），含独立浮动动画
- [x] **优化光晕与配色**：3 个 `glow-orb` 已使用项目主色（`#6366f1` 紫 / `#0ea5e9` 蓝 / `#22c55e` 绿），含 `orbFloat` 动画
- [x] **登录卡片毛玻璃化**：`login.html` 的 `.login-card` 已使用 `backdrop-filter: blur(20px); background: rgba(15,23,42,0.85)`；卡片顶部含无人机 SVG Logo
- [x] **注册页同步优化**：`register.html` 已实现相同风格（2 个光晕球 + 2 个浮动无人机 + `backdrop-filter: blur(20px)` 卡片）

**涉及文件**：`login.html` · `register.html`

---

### TODO-15 ★ 项目取名

**现状**：

- 当前项目名为 `CloudControl`，在代码、文档、页面标题中广泛使用
- 用户标注星号为最高优先级需求

**任务清单**：

- [ ] **确定正式项目名称**：与团队讨论并敲定项目正式中英文名称
  - 候选方向参考：
    - `SkyPilot` — 天空领航者
    - `AeroGuard` — 航空卫士
    - `DroneForge` — 无人机锻造
    - `CloudHawk` — 云鹰
    - `WingCommand` — 翼控
    - `AirMatrix` — 空域矩阵
    - `PhoenixUAV` — 凤翼无人机平台
  - 命名原则：简洁、科技感、与无人机/智能管控相关、英文可搜索、无商标冲突
- [ ] **全局替换项目名**：
  - `dashboard.html` 顶栏 `<h1>` 和 `<title>`
  - `login.html` / `register.html` 标题和卡片文字
  - 各模块页面 `<title>` 中 `CloudControl` 后缀
  - `README.md` 项目标题与描述
  - `.env` 注释、日志输出中的项目引用
- [ ] **域名/子域名同步**：新名称需考虑域名可用性（TODO-22 联动）

**涉及文件**：全项目范围（HTML/Go/MD/env）

---

## 四、AI 能力与地图配置优化（16-19 项）

### TODO-16 ★ LLM API / Deepseek 适配

**现状**：

- `internal/llm/llm.go` 封装了 OpenAI 兼容 API 调用，当前配置：`LLM_BASE_URL=https://openapi.monica.im/v1`，`LLM_MODEL=gpt-4.1`
- API Key 通过 `.env` 的 `LLM_API_KEY` 配置
- LLM 被航线规划（`flight_plan.go`）、CoT 分析（`cot.go`）、AI 助手（`ai_assistant.go`）三处调用

**任务清单**：

- [ ] **Deepseek 模型接入**：
  - `.env` 中支持配置 Deepseek API（`LLM_BASE_URL=https://api.deepseek.com/v1`，`LLM_MODEL=deepseek-chat` / `deepseek-reasoner`）
  - 验证 `llm.go` 的 OpenAI 兼容调用格式是否适配 Deepseek API（请求/响应格式差异）
  - 处理 Deepseek 特有的 `reasoning_content` 字段（思考过程），可展示在 CoT 分析中
- [ ] **多模型切换支持**：
  - 在 `.env` 中支持配置多组 LLM（`LLM_PRIMARY_*` / `LLM_FALLBACK_*`）
  - 主模型调用失败时自动 fallback 到备用模型
  - AI 助手面板增加「当前模型」展示标签
- [ ] **System Prompt 优化**：针对不同调用场景定制 System Prompt：
  - 航线规划：强调 JSON 结构化输出、坐标精度、禁飞区规避
  - CoT 分析：强调逐步推理、不跳步、置信度自评
  - AI 助手：强调项目知识、中文回答、操作指引
- [ ] **Token 用量监控**：在 `llm.go` 中记录每次调用的 token 消耗（prompt + completion），增加 `/api/llm/usage` 统计接口
- [ ] **前端模型信息展示**：AI 助手面板 header 区域显示当前模型名称和状态（在线/离线/切换中）

**涉及文件**：`internal/llm/llm.go` · `ai_assistant.go` · `cot.go` · `flight_plan.go` · `.env` · `ai-assistant.js`

---

### TODO-17 PPT 首页 22U / 背景图 + 无人机优化

- PPT的制作

**现状**：

- 项目已有 `Cloudcontrol.pptx` 演示文件（约 15MB）
- 项目包含 22 大功能模块（22U = 22 个 Unit），需要在 PPT 首页有效传达系统规模
- 当前 PPT 首页需要适配 22U 场景展示

**任务清单**：

- [ ] **PPT 首页重设计**：
  - 首页布局：项目 Logo + 项目名（TODO-15） + 副标题"22U 企业级智能无人机管控平台"
  - 背景图：使用科技感暗色背景（与 `login.html` 风格一致），叠加低透明度城市鸟瞰/卫星图
  - 添加无人机 3D 渲染图或矢量插图作为主视觉元素
- [ ] **22U 功能矩阵展示**：
  - 用 4×6 或 5×5 网格图标矩阵展示 22 个功能模块
  - 每个模块一个图标 + 短标题，按分类着色（蓝色=飞行类、绿色=监控类、紫色=AI 类、橙色=运维类）
- [ ] **技术栈展示**：底部横条展示核心技术栈：Go / Gin / MySQL / WebSocket / Leaflet / LLM / RL
- [ ] **团队信息**：预留团队名称、成员、指导教师信息位置
- [ ] **导出为图片**：将 PPT 首页导出为 PNG/SVG，可复用于 README.md 和登录页

**涉及文件**：`Cloudcontrol.pptx` · 可能新增设计素材

---

### TODO-18 RAG 检索增强生成优化

> **✅ 已完成**（v3.7.0，BM25 关键词检索方案）

**现状**：

- `ai_assistant.go` 的 AI 助手已有知识源接入：功能介绍文档、业务数据、告警记录、日志数据、模拟机统计
- 当前知识注入方式为将数据拼接到 System Prompt 中（Prompt Stuffing），非真正的 RAG 架构
- 项目 `功能介绍/` 目录有 19 个文档文件可作为知识库

**任务清单**：

- [x] **知识库构建**：`internal/rag/rag.go` 实现 BM25 关键词检索引擎，自动加载 `knowledge_base/`（20 个 .md 文档），切分为 ~500 字符 chunk，构建倒排索引
- [x] **检索增强流程**：用户提问 → BM25 检索 Top-K chunk → 注入 context → LLM 生成回答，已集成到 `ai_assistant.go` 的 `RAGRetrieveContext()`
- [x] **知识库管理接口**：
  - `GET /api/ai/rag/search?q=xxx` — 直接查询知识库
  - `GET /api/ai/rag/stats` — 查看 chunk 数量与状态
- [x] **前端展示增强**：AI 助手面板副标题显示知识库 chunk 数（`loadRAGStatus()`），每次开启面板刷新
- [x] **System Prompt 指引**：AI 助手 System Prompt 明确要求「当提供了【知识库参考】内容时，优先参考知识库」
- [ ] **升级为向量检索**（可选提升）：当前为 BM25 关键词匹配，可升级为 embedding 向量相似度检索（需接入 Deepseek/OpenAI embedding API）
- [ ] **业务数据动态注入**：将实时告警统计、模拟机状态编码为可检索知识 chunk，每 5 分钟刷新

**涉及文件**：`ai_assistant.go` · `ai-assistant.js` · `internal/rag/rag.go` · `knowledge_base/`（20 个文档） · `handlers/rag_endpoints.go` · `handlers/rag_integration.go`

---

### TODO-19 用户默认头像使用项目 Logo

**现状**：

- `dashboard.html` 顶部栏 `.user-info` 区域仅显示用户名文字 + 退出按钮，无头像图片
- `login.html` / `register.html` 无用户头像相关逻辑
- 用户表（`users`）无 `avatar` 字段

**任务清单**：

- [ ] **头像 UI 组件**：在 `dashboard.html` 顶部栏用户名左侧添加圆形头像容器（32×32px），默认显示项目 Logo 方形版（TODO-13 产出）
- [ ] **头像 CSS**：`border-radius: 50%; object-fit: cover; border: 2px solid rgba(255,255,255,0.3)`，悬停时放大/亮框效果
- [ ] **后端支持**（可选扩展）：
  - `users` 表新增 `avatar_url` 字段（默认为空）
  - `GET /api/auth/profile` 返回头像 URL
  - 上传自定义头像接口 `POST /api/auth/avatar`（存储到 `data/avatars/`）
- [ ] **降级策略**：若用户未设置自定义头像，始终显示项目 Logo；若 Logo 未加载则显示用户名首字母圆形色块（类似 Google 风格）
- [ ] **AI 助手面板同步**：`ai-assistant.js` 聊天消息区域的用户消息头像也使用同一头像

**涉及文件**：`dashboard.html` · `ai-assistant.js` · `auth.go`（可选） · `web/assets/logo.svg`

---

## 五、前端与系统细节优化（20-23 项）

### TODO-20 前端全面圆角化

**现状**：

- `common.css` 当前圆角值不统一：`.module-header` 8px、`.btn` 6px、`.card` 8px、`.stat-box` 8px、`.status-badge` 12px、`.progress-bar` 4px
- 各模块页面内联 CSS 中圆角值更加分散（4px~16px 不等）
- 整体视觉缺乏统一的圆润感

**任务清单**：

- [ ] **定义圆角设计规范**（CSS 变量）：
  ```css
  :root {
    --radius-sm: 8px;    /* 小元素：badge、按钮、输入框 */
    --radius-md: 12px;   /* 中元素：卡片、弹窗、面板 */
    --radius-lg: 16px;   /* 大元素：模块容器、主面板 */
    --radius-xl: 20px;   /* 特殊：AI 面板、登录卡片 */
    --radius-full: 9999px; /* 圆形：头像、圆点状态 */
  }
  ```
- [ ] **全局样式更新**：在 `common.css` 中统一修改所有圆角值为 CSS 变量引用
- [ ] **各模块同步**：遍历所有 22 个模块 HTML，将内联 `border-radius` 值替换为与 `common.css` 一致的规范值
- [ ] **弹窗圆角统一**：所有 `.modal-box` 统一为 `var(--radius-lg)` 即 16px
- [ ] **表格圆角处理**：`<table>` 外层包裹容器统一圆角 + `overflow: hidden`，避免表格直角穿透
- [ ] **仿真页适配**：`simulation.html` 使用暗色独立主题，需同步更新其 `:root` 变量

**涉及文件**：`common.css`（核心） · 全部 22 个模块 HTML · `dashboard.html` · `login.html` · `register.html` · `simulation.html`

---

### TODO-21 毛玻璃 / 磨砂感视觉效果

> **⚠️ 部分完成**（v3.7.0）

**现状**：

- 当前 UI 以纯白/浅灰实色背景为主（`#fff` / `#f8fafc` / `#f1f5f9`），无毛玻璃效果
- 仿真页（`simulation.html`）使用暗色实色背景（`#0f172a` / `#1e293b`）
- `login.html` 背景有光晕动效，但卡片为实色

**任务清单**：

- [x] **AI 助手面板毛玻璃**：`#ai-panel` 已应用 `backdrop-filter: blur(16px); background: rgba(255,255,255,0.85)`，圆角 20px
- [x] **登录/注册卡片毛玻璃**：`login.html` `.login-card` 和 `register.html` `.register-card` 均已使用 `backdrop-filter: blur(20px); background: rgba(15,23,42,0.85)`
- [ ] **定义全局毛玻璃样式规范**：在 `common.css` 中添加 `.glass` / `.glass-dark` 工具类
- [ ] **dashboard.html 顶部栏**（`.top-bar`）→ 半透明毛玻璃（目前为实色 `#0ea5e9`）
- [ ] **dashboard.html 侧边栏**（`.sidebar`）→ 暗色毛玻璃（目前为实色 `#1e3a8a`）
- [ ] **通知铃铛下拉面板** → 白色毛玻璃
- [ ] **各模块弹窗**（`.modal-box`）→ 白色毛玻璃
- [ ] **仿真页暗色毛玻璃**：`simulation.html` 的 `.card` 背景改为 `.glass-dark`
- [ ] **性能降级**：增加 `@media (prefers-reduced-motion)` 降级为纯色背景

**涉及文件**：`common.css`（全局 `.glass` 类） · `dashboard.html` · `ai-assistant.js` · `notification-bell.js` · `login.html` · `register.html` · `simulation.html`

---

### TODO-22 域名优化

**现状**：

- 当前项目部署通过 `:8080` 端口直接访问（`http://127.0.0.1:8080/`）
- MySQL 使用 Aiven 云数据库（`cloudcontrol-tolovelife-49cf.i.aivencloud.com:16898`）
- 无自定义域名、无 HTTPS、无反向代理配置

**任务清单**：

- [ ] **域名注册与配置**：注册与项目名（TODO-15）匹配的域名，DNS A 记录指向服务器 IP
- [ ] **HTTPS 证书**：使用 Let's Encrypt 免费证书，通过 Nginx/Caddy 反向代理终止 SSL
- [ ] **反向代理配置**：代理 `https://域名` → `http://127.0.0.1:8080`，含 WebSocket 代理（`/api/*/stream`）
- [ ] **环境变量适配**：增加 `SC_BASE_URL` 用于后端生成完整链接
- [ ] **CORS 更新**：将 CORS 允许的 Origin 从 `*` 改为具体域名

**涉及文件**：`.env` · `main.go`（CORS/TLS） · 新增 `deploy/nginx.conf` 或 `deploy/Caddyfile`

---

### TODO-23 动态性优化

> **⚠️ 部分完成**（v3.7.0）

**现状**：

- 各模块页面切换为 `iframe` 直接替换，无过渡动画
- Chart.js 图表已有内置动画（`animation: { duration: 600 }`），无人机状态变化更新为直接替换 DOM
- 按钮/交互元素已有 `transition: all 0.2s`，整体动态感较弱

**任务清单**：

- [x] **AI FAB 呼吸脉冲**：`#ai-fab` 已实现 `@keyframes aiFabPulse` 呼吸光晕动画（2.5s 无限循环），拖拽或悬停时暂停
- [x] **AI 消息淡入**：AI 助手消息气泡增加 `@keyframes aiFadeIn` 淡入动画（0.3s ease-out）
- [x] **Toast 通知动画**：`common.css` 已定义 `@keyframes toastFadeIn` / `toastFadeOut`
- [ ] **iframe 模块切换动画**：`dashboard.html` 切换模块时，`#contentFrame` 增加淡入过渡（300ms）
- [ ] **卡片加载骨架屏**：各模块首次加载时显示骨架屏占位（浅灰色闪烁）
- [ ] **Chart.js 动画增强**：告警计数变化时触发数字跳动动效
- [ ] **无人机状态动效**：电量低于 20% 时 badge 增加 `@keyframes blink` 闪烁警告
- [ ] **通知铃铛抖动**：新通知到达时触发 `@keyframes bellRing` 抖动
- [ ] **全局页面进入动效**：`common.css` 增加 `.fade-in` / `.slide-in-up` 工具类
- [ ] **交互反馈增强**：按钮点击增加涟漪效果（ripple）
- [ ] **暗色主题过渡**（可选）：为仿真页增加亮/暗主题切换按钮，切换时全局平滑过渡

**涉及文件**：`common.css`（全局动画类） · `common.js`（动画工具函数） · `dashboard.html`（iframe 切换动画） · 各模块 HTML · `ai-assistant.js` · `notification-bell.js`

---

## 推荐实施顺序

> 按依赖关系和影响范围排列，建议从基础设施到上层视觉逐步推进。

### 第一批（基础设施 + 核心功能）— 建议最先完成

| 顺序 | TODO                                 | 理由                            | 状态 |
| ---- | ------------------------------------ | ------------------------------- | ---- |
| 1    | **#15 项目取名** ★            | 所有 UI/文档/域名的前提         | ⬜ 待开始 |
| 2    | **#13 Logo 优化**              | 依赖项目名确定                  | ⬜ 待开始 |
| 3    | **#16 LLM / Deepseek 适配** ★ | AI 能力基础，影响 CoT/助手/规划 | ⬜ 待开始 |
| 4    | ✅ **#01 仿真地图→天地图** ★  | 地图统一，影响多模块            | 已完成 |
| 5    | ⚠️ **#09 模拟器任务建设优化** ★ | 核心业务流程闭环              | 部分完成 |

### 第二批（功能增强 + 性能优化）

| 顺序 | TODO                   | 理由                     | 状态 |
| ---- | ---------------------- | ------------------------ | ---- |
| 6    | #05 CoT 思维链优化     | 依赖 #16 LLM 基础        | ⬜ 待开始 |
| 7    | ⚠️ #06 离线通知屏蔽   | 通知稳定性保障           | 部分完成 |
| 8    | #07 并发 RPM 优化      | 依赖 #16 LLM 限流需求    | ⬜ 待开始 |
| 9    | ✅ #18 RAG 优化        | BM25 方案已完成          | 已完成 |
| 10   | #03 仿真日志优化       | 依赖 #09 仿真流程清晰    | ⬜ 待开始 |
| 11   | ✅ #04 导出→展示跳转   | 依赖 #09 任务建设完善    | 已完成 |
| 12   | ⚠️ #10 电量+图表优化   | 低电量异常阈值已修复     | 部分完成 |
| 13   | #08 前端性能优化       | 基础设施，不影响功能     | ⬜ 待开始 |

### 第三批（视觉升级 + 体验打磨）

| 顺序 | TODO                    | 理由                       | 状态 |
| ---- | ----------------------- | -------------------------- | ---- |
| 14   | #20 前端全面圆角        | CSS 变量基础               | ⬜ 待开始 |
| 15   | ⚠️ #21 毛玻璃效果      | 登录/AI 面板已完成         | 部分完成 |
| 16   | ✅ #14 登录背景优化     | 已完成（v3.7.0）           | 已完成 |
| 17   | ⚠️ #23 动态性优化      | FAB/Toast 已完成           | 部分完成 |
| 18   | ✅ #11 轨迹弧度化       | 已完成（v3.7.0）           | 已完成 |
| 19   | #12 视频模块位置        | 独立可并行                 | ⬜ 待开始 |
| 20   | ✅ #02 AI 助手图标优化  | 已完成（v3.7.0）           | 已完成 |
| 21   | #19 默认头像→Logo       | 依赖 #13 Logo 完成         | ⬜ 待开始 |
| 22   | #22 域名优化            | 依赖 #15 项目名            | ⬜ 待开始 |
| 23   | #17 PPT 首页优化        | 依赖 #13/#15，最后做       | ⬜ 待开始 |

---

## 附录：涉及文件全景索引

| 文件/目录                                     | 涉及 TODO 编号                      |
| --------------------------------------------- | ----------------------------------- |
| `project/web/modules/simulation.html`       | #01 #03 #04 #09 #10 #11             |
| `project/web/modules/common.css`            | #20 #21 #23                         |
| `project/web/modules/common.js`             | #01 #08 #23                         |
| `project/web/modules/ai-assistant.js`       | #02 #19 #21 #23                     |
| `project/web/modules/notification-bell.js`  | #06 #21 #23                         |
| `project/web/modules/cot.html`              | #05                                 |
| `project/web/modules/battery.html`          | #10                                 |
| `project/web/modules/gps.html`              | #01 #11                             |
| `project/web/modules/flight.html`           | #01 #11                             |
| `project/web/modules/video.html`            | #12                                 |
| `project/web/modules/concurrency.html`      | #07                                 |
| `project/web/dashboard.html`                | #04 #08 #12 #13 #15 #19 #20 #21 #23 |
| `project/web/login.html`                    | #13 #14 #15 #21                     |
| `project/web/register.html`                 | #13 #14 #15 #21                     |
| `project/internal/llm/llm.go`               | #07 #16                             |
| `project/internal/handlers/cot.go`          | #05 #16                             |
| `project/internal/handlers/ai_assistant.go` | #16 #18                             |
| `project/internal/simulation/instance.go`   | #10 ⚠️（低电量阈值修复）            |
| `project/internal/handlers/simulation.go`   | #03 #09                             |
| `project/internal/handlers/notification.go` | #06                                 |
| `project/internal/handlers/patrol.go`       | #06                                 |
| `project/internal/taskpool/pool.go`         | #07                                 |
| `project/internal/rag/rag.go`               | #18 ✅                              |
| `project/knowledge_base/`（20 个 .md 文档） | #18 ✅                              |
| `project/.env`                              | #01 #07 #16 #22                     |
| `README.md`                                 | #15                                 |
| `Cloudcontrol.pptx`                         | #17                                 |

---

> **维护说明**：本文档随开发进度更新，完成的任务项标记 `[x]`，已完成的模块标注 `> ✅ 已完成`。每完成一批后回顾下一批的依赖关系是否满足。
> **当前优先级**（待开始中最高）：#15 项目取名 → #13 Logo → #16 LLM/Deepseek → #09 模拟器向导 → #05 CoT → #03 仿真日志
