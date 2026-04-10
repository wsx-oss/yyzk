# CloudControl v4.0 优化 TODO

> **文档创建时间**：2026-04-10  
> **项目名称**：CloudControl — 企业级智能无人机管控平台  
> **技术栈**：Go 1.24 + Gin + MySQL(Aiven) + Leaflet + WebSocket + Chart.js  
> **前端架构**：`dashboard.html` 壳层 + iframe 加载 22 个模块页 + `ai-assistant.js` 浮窗 + `notification-bell.js` 铃铛  
> **当前版本**：v3.6.0  
> **本轮目标**：5 大类 23 项优化，覆盖地图、界面、性能、业务、AI、视觉全链路

---

## 总览

| 分类 | 编号 | 标题 | 优先级 |
|------|------|------|--------|
| 一、基础地图与界面 | 1 | 仿真地图 → 天地图 | P0 |
| | 2 | AI 助手图标+位置优化 | P1 |
| | 3 | 仿真模拟事件日志优化 | P1 |
| | 4 | 导出→展示（自动跳转模拟器任务建设） | P1 |
| 二、系统功能与性能 | 5 | CoT 思维链优化 | P1 |
| | 6 | 离线状态下取消相关异常通知 | P1 |
| | 7 | 并发任务·RPM 优化 | P2 |
| | 8 | 前端整体性能优化 | P1 |
| | 9 | 模拟器任务建设优化 | P0 |
| | 10 | 模拟机电量+图表优化 | P1 |
| 三、业务功能与视觉 | 11 | 轨迹弧度（曲线化）优化 | P2 |
| | 12 | 视频监控模块位置调整 | P2 |
| | 13 | Logo 优化 | P1 |
| | 14 | 登录界面背景优化 | P1 |
| | 15 | 项目命名 ★ | P0 |
| 四、AI 能力与配置 | 16 | LLM API / Deepseek 适配 | P0 |
| | 17 | PPT 首页 22U/背景图+无人机元素 | P2 |
| | 18 | RAG 检索增强生成优化 | P1 |
| | 19 | 用户默认头像使用项目 Logo | P2 |
| 五、前端细节与系统 | 20 | 前端全面圆角化 | P1 |
| | 21 | 毛玻璃/磨砂感视觉效果 | P1 |
| | 22 | 域名优化 | P2 |
| | 23 | 动态性优化 | P1 |

---

## 一、基础地图与界面元素优化（1-4 项）

### TODO-01 ★ 仿真地图 → 天地图

**现状**：
- `simulation.html` 地图使用 CartoDB 暗色瓦片（`basemaps.cartocdn.com/dark_all`），中心为成都 `[30.5728, 104.0668]`
- `gps.html` / `flight.html` / `noflyzone.html` 已接入天地图（`tdtImgLayer` + `tdtCiaLayer`），`.env` 已配 `TIANDITU_KEY`
- 天地图 Key 在各页面内各自硬编码，未统一管理
- 地图范围改为郑州大学 `[34.748, 113.655]`

**任务清单**：
- [ ] 替换 `simulation.html` 的 `L.tileLayer` 瓦片源为天地图影像底图（`img_w`）+ 标注图层（`cia_w`），复用 `gps.html` 中 `tdtImgLayer()` / `tdtCiaLayer()` 的 WMTS 模板
- [ ] 在 `common.js` 中统一导出 `TIANDITU_KEY` 常量和 `tdtImgLayer()` / `tdtCiaLayer()` 工厂函数，消除各页面重复定义
- [ ] 统一 `simulation.html` 默认中心点为郑州大学 `[34.748, 113.655]`
- [ ] 适配仿真页暗色主题：天地图卫星影像在暗背景下视觉协调，必要时对标注层添加 CSS `filter: invert(1) hue-rotate(180deg)` 模拟暗色
- [ ] 回归验证：无人机图标、轨迹线、航线标注、碰撞规避动画在新瓦片下正常渲染

**涉及文件**：`simulation.html` · `common.js` · `gps.html` · `flight.html` · `noflyzone.html` · `.env`

---

### TODO-02 AI 助手图标 + 位置优化

**现状**：
- `ai-assistant.js` 注入右下角 56×56px 圆形 FAB 按钮（`#ai-fab`），`bottom:28px; right:28px`
- 图标为 emoji 文字（🤖），面板 `#ai-panel` 为 400×600px 白色弹窗   logo

**任务清单**：
- [ ] 替换 `#ai-fab` 图标为 SVG 矢量图标（推荐 Lucide `bot` / `sparkles`），提升清晰度和品质感
- [ ] 增大按钮至 60×60px，增加呼吸脉冲动画（`@keyframes pulse`）吸引首次注意
- [ ] 为 `#ai-panel` 弹窗加毛玻璃效果（`backdrop-filter: blur(16px); background: rgba(255,255,255,0.85)`），圆角增至 20px
- [ ] 小屏适配：面板宽度改为 `min(400px, calc(100vw - 56px))`，避免溢出
- [ ] 评估可拖拽能力：若 FAB 遮挡内容，允许用户长按拖动到屏幕任意位置（记住位置到 localStorage）

**涉及文件**：`ai-assistant.js`

---

### TODO-03 仿真模拟事件 日志优化

-  改为异常日志
**现状**：
- `simulation.html` 事件日志面板通过 WebSocket `/api/sim/stream` 实时接收所有事件（正常状态变更 + 异常事件）
- 正常/异常日志混排，重要异常被淹没
- 事件类型：状态变更(正常)、低电量/偏航/失联/温度异常/碰撞预警(异常)

**任务清单**：
- [ ] 新增日志筛选 Tab：`全部` | `异常`，默认选中「异常」，视觉上将"全部"/"正常"置灰或用删除线标记
- [ ] 后端 `/api/sim/stream` 增加可选 query 参数 `?filter=anomaly`，只推送异常类型事件
- [ ] 异常日志展示优化：
  - 按等级显示红色（critical: 失联/碰撞）、橙色（warning: 低电量/偏航）、黄色（info: 温度异常）badge
  - 每条日志增加实例名称高亮、异常类型图标、时间戳格式化
- [ ] 面板标题增加实时异常计数 badge（红色圆点 + 数字）
- [ ] 自动滚动：新日志到达时自动滚到底部，提供「暂停滚动」锁定按钮

**涉及文件**：`simulation.html` · `simulation.go`（可选 stream 过滤）

---

### TODO-04 导出 → 展示（自动跳转模拟器任务建设）

**现状**：
- `simulation.html` RL 面板的 `exportRLPolicy()` 导出后仅在当前页内 `#rlExportCard` 展示策略详情
- `dashboard.html` 提供全局 `window.navigateTo(page)` 实现 iframe 模块切换
- 无导出后跳转或策略应用到任务的联动逻辑

**任务清单**：
- [ ] 在 `#rlExportCard` 底部增加「应用策略到新批次」按钮
- [ ] 点击后自动切换到「批次管理」Tab 并打开创建批次弹窗，预填策略相关参数（策略类型、训练轮次、推荐任务模板）
- [ ] 导出成功后弹出 Toast：「策略已导出，可点击"应用到任务"创建仿真批次」
- [ ] 若从外部模块跳转进来，支持 URL hash 定位：`simulation.html#rl-export` 自动切换到 RL Tab 并展开导出卡片
- [ ] 与 TODO-09（模拟器任务建设优化）联动，确保策略导出结果可直接驱动任务创建

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

**现状**：
- `patrol.go` 定时巡检产生通知写入 `notifications` 表，`notification-bell.js` 展示
- 离线时（网络断开/LLM 不可达/DB 超时），巡检可能产生大量"连接失败"通知，形成通知风暴
- 无离线状态检测和降级机制

**任务清单**：
- [ ] **后端离线检测**：在 `patrol.go` 巡检逻辑前增加预检查：
  - MySQL 连接 ping 测试
  - LLM API 可达性测试（轻量 HEAD 请求）
  - 若检测到不可达，跳过本轮巡检或仅执行本地检查
- [ ] **通知去重/抑制**：对同一类型的网络/连接异常通知，在 N 分钟内最多写入 1 条（通知降频），避免通知洪泛
- [ ] **前端离线提示**：`notification-bell.js` 检测 WebSocket 断连后，在铃铛图标旁显示离线状态 badge（灰色断链图标），并暂停拉取通知的轮询
- [ ] **离线通知静默**：离线期间产生的通知标记为 `silent` 类型，恢复在线后批量折叠展示（如："离线期间产生 12 条通知"）
- [ ] **systemd/健康检查联动**：`/api/healthz` 增加连接状态返回字段，供前端判断后端健康度

**涉及文件**：`patrol.go` · `notification.go` · `notification-bell.js` · `main.go`（healthz）

---

### TODO-07 并发任务 · RPM 优化

- 增加RPM优化

**现状**：
- `internal/taskpool/pool.go` 提供 IO(16 workers) + CPU(4 workers) 双池，`batcher.go` 提供 WriteBatcher / Throttler / StatsCache
- 当前限流为 IP 级别 500 次/分钟（`middleware/`），无针对 LLM API 的独立 RPM 控制
- LLM 调用集中在航线规划（`flight_plan.go`）、CoT 分析（`cot.go`）、AI 助手（`ai_assistant.go`）

**任务清单**：
- [ ] **LLM 请求速率控制**：在 `internal/llm/llm.go` 的 `Client` 中增加令牌桶限流器（`golang.org/x/time/rate`），按 `.env` 配置 `LLM_RPM`（默认 30 RPM）限制对大模型 API 的并发请求
- [ ] **RPM 可视化**：在 `concurrency.html` 并发监控面板增加 LLM RPM 监控卡片，展示：当前分钟请求数 / 上限 / 排队数 / 平均延迟
- [ ] **任务优先级细化**：航线规划（用户触发）优先级高于定时巡检（后台自动），确保用户操作不被后台任务阻塞
- [ ] **超时策略调优**：LLM 调用超时从默认值调整为可配置（`.env` `LLM_TIMEOUT_SEC`，默认 60s），超时后降级为直线规划/缓存结果
- [ ] **并发指标扩展**：`/api/taskpool/metrics` 增加 LLM 专属指标组（`llm_rpm_current` / `llm_rpm_limit` / `llm_avg_latency_ms`）

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

- 显示无人机的任务进度

**现状**：
- `simulation.html` 已支持批次创建（`BatchCreate`）、实例管理、任务模板（巡逻/巡检/配送/测绘/搜救）、异常注入、RL 训练
- 创建批次通过弹窗表单完成：选择任务类型 → 填写数量/区域/参数 → 提交
- 缺少从策略导出 → 任务创建 → 执行监控 → 结果评估的完整闭环流程

**任务清单**：
- [ ] **任务建设向导化**：将批次创建从单步弹窗升级为多步向导：
  1. 选择任务模板（巡逻/巡检/配送/测绘/搜救）+ 预览模板说明
  2. 配置任务参数（区域/数量/持续时间/异常注入策略）
  3. 关联 RL 策略（可选：选择已导出的训练策略应用到本批次）
  4. 确认与启动
- [ ] **任务模板预览增强**：每种任务模板增加可视化预览卡片，展示典型航线轨迹缩略图、预计时长、推荐参数
- [ ] **批量操作优化**：支持多选批次一键启动/停止/删除，增加批量操作确认弹窗
- [ ] **任务执行看板**：新增任务执行实时面板，汇总展示：
  - 当前运行批次数 / 总实例数 / 在线率
  - 任务完成进度环形图
  - 近 1 小时异常趋势折线图
- [ ] **任务结果导出**：支持将批次运行数据导出为 CSV/JSON，包含所有实例的遥测数据、异常事件、RL 奖励

**涉及文件**：`simulation.html`（前端向导 + 看板） · `simulation.go`（批次 API 增强）

---

### TODO-10 模拟机电量 + 图表优化

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
- [ ] **电量告警联动**：模拟机电量低于阈值时，在图表上实时标注告警点（红色三角标记），并链接到通知中心

**涉及文件**：`simulation.html`（统计面板图表） · `battery.html`（电池监控图表） · `simulation/instance.go`（数据源）

---

## 三、业务功能与视觉设计优化（11-15 项）

### TODO-11 轨迹弧度（非直线）优化

**现状**：
- `gps.html` 历史轨迹、`flight.html` 航线展示、`simulation.html` 飞行轨迹均使用 Leaflet `L.polyline` 直线连接各航点
- 真实无人机飞行轨迹并非严格直线，直线展示缺乏真实感

**任务清单**：
- [ ] **贝塞尔曲线替代直线**：将 `L.polyline` 替换为贝塞尔曲线（Leaflet 插件 `leaflet-curve` 或自定义 `L.Curve`），在相邻航点间生成平滑弧线
- [ ] **弧度算法**：根据两点距离和航向变化自动计算控制点偏移量：
  - 短距离（<500m）：小弧度
  - 长距离（>2km）：明显弧度
  - 急转弯航段：更大曲率
- [ ] **动画效果**：轨迹绘制增加渐进动画（从起点到终点逐段绘制），增强动态感
- [ ] **仿真地图轨迹适配**：`simulation.html` 实时轨迹也使用弧线绘制，确保大量无人机同时显示时性能无明显下降（Canvas 渲染模式）
- [ ] **可选开关**：提供"显示直线/曲线"切换按钮，方便对比查看

**涉及文件**：`gps.html` · `flight.html` · `simulation.html` · 可能新增 `web/libs/leaflet-curve.js`

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

**现状**：
- `login.html` 背景已有高级效果：暗色径向渐变 + 动态网格 + 3 个光晕球（`glow-orb`）浮动动画 + 粒子连线效果
- 视觉质感已较高，但缺少无人机/科技感相关的具象元素
- `register.html` 背景风格应与登录页保持一致

**任务清单**：
- [ ] **增加主题性背景元素**：
  - 添加无人机剪影 SVG 作为装饰元素（低透明度浮动，增强项目主题感）
  - 添加数据流/连接线粒子动画（模拟无人机通信链路）
  - 考虑添加模糊化的卫星地图纹理作为底层背景
- [ ] **优化光晕与配色**：调整 3 个 `glow-orb` 的颜色为项目主色调渐变（蓝→紫→青），增加光晕随鼠标移动微偏移效果
- [ ] **登录卡片毛玻璃化**：将登录表单卡片背景改为毛玻璃效果（`backdrop-filter: blur(20px); background: rgba(255,255,255,0.08)`），与 TODO-21 风格一致
- [ ] **注册页同步优化**：确保 `register.html` 背景效果与 `login.html` 完全一致（共用同一套 CSS 类）
- [ ] **响应式适配**：确保在 1366×768 笔记本屏幕上背景动画不掉帧、卡片不溢出

**涉及文件**：`login.html` · `register.html` · 可能新增 `web/assets/drone-silhouette.svg`

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

**现状**：
- `ai_assistant.go` 的 AI 助手已有知识源接入：功能介绍文档、业务数据、告警记录、日志数据、模拟机统计
- 当前知识注入方式为将数据拼接到 System Prompt 中（Prompt Stuffing），非真正的 RAG 架构
- 项目 `功能介绍/` 目录有 19 个文档文件可作为知识库

**任务清单**：
- [ ] **知识库向量化**：
  - 将 `功能介绍/` 下所有文档切分为 chunk（每 chunk 500-800 字符，重叠 100 字符）
  - 使用 embedding 模型（Deepseek/OpenAI embedding API）将 chunk 向量化
  - 向量存储选型：本地 SQLite/文件存储（轻量，无外部依赖）或 Qdrant/Chroma
- [ ] **检索增强流程**：
  - 用户提问 → embedding 向量化 → 相似度检索 Top-K chunk → 注入 context → LLM 生成回答
  - 在 `ai_assistant.go` 中实现 RAG pipeline
- [ ] **知识库管理接口**：
  - `POST /api/rag/index` — 手动触发知识库重建
  - `GET /api/rag/status` — 查看知识库状态（文档数/chunk 数/最后更新时间）
  - 启动时自动检查知识库是否需要更新
- [ ] **前端展示增强**：
  - AI 助手回答时，底部展示「参考来源」链接（引用了哪些文档 chunk）
  - 增加「知识库管理」入口（管理员可见），支持上传新文档
- [ ] **业务数据动态注入**：除静态文档外，将实时业务数据（告警统计、模拟机状态、电池概览）也编码为可检索的知识 chunk，每 5 分钟刷新

**涉及文件**：`ai_assistant.go` · `ai-assistant.js` · 新增 `internal/rag/` 包 · `功能介绍/` 文档 · `.env`（embedding 配置）

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

**现状**：
- 当前 UI 以纯白/浅灰实色背景为主（`#fff` / `#f8fafc` / `#f1f5f9`），无毛玻璃效果
- 仿真页（`simulation.html`）使用暗色实色背景（`#0f172a` / `#1e293b`）
- `login.html` 背景有光晕动效，但卡片为实色

**任务清单**：
- [ ] **定义毛玻璃样式规范**：
  ```css
  .glass {
    background: rgba(255, 255, 255, 0.6);
    backdrop-filter: blur(16px) saturate(180%);
    -webkit-backdrop-filter: blur(16px) saturate(180%);
    border: 1px solid rgba(255, 255, 255, 0.3);
  }
  .glass-dark {
    background: rgba(30, 41, 59, 0.7);
    backdrop-filter: blur(16px) saturate(180%);
    -webkit-backdrop-filter: blur(16px) saturate(180%);
    border: 1px solid rgba(255, 255, 255, 0.08);
  }
  ```
- [ ] **应用毛玻璃的优先目标**：
  - `dashboard.html` 顶部栏（`.top-bar`）→ 半透明 + 模糊
  - `dashboard.html` 侧边栏（`.sidebar`）→ 暗色毛玻璃
  - AI 助手面板（`#ai-panel`）→ 白色毛玻璃
  - 通知铃铛下拉面板 → 白色毛玻璃
  - 登录/注册卡片 → 暗色毛玻璃
  - 各模块弹窗（`.modal-box`）→ 白色毛玻璃
- [ ] **仿真页暗色毛玻璃**：`simulation.html` 的 `.card` 背景改为 `.glass-dark`
- [ ] **性能保障**：`backdrop-filter` 在低端 GPU 上可能卡顿，增加 `@media (prefers-reduced-motion)` 降级为纯色背景
- [ ] **浏览器兼容**：确认 `-webkit-backdrop-filter` 对 Safari 支持，Firefox 可能需要 `layout.css.backdrop-filter.enabled` 标记

**涉及文件**：`common.css`（全局 `.glass` 类） · `dashboard.html` · `ai-assistant.js` · `notification-bell.js` · `login.html` · `register.html` · `simulation.html`

---

### TODO-22 域名优化

**现状**：
- 当前项目部署通过 `:8080` 端口直接访问（`http://127.0.0.1:8080/`）
- MySQL 使用 Aiven 云数据库（`cloudcontrol-tolovelife-49cf.i.aivencloud.com:16898`）
- 无自定义域名、无 HTTPS、无反向代理配置

**任务清单**：
- [ ] **域名注册与配置**：
  - 注册与项目名（TODO-15）匹配的域名（如 `skypilot.dev` / `aerog.cc` 等）
  - DNS 解析配置：A 记录指向服务器 IP / CNAME 指向云平台地址
- [ ] **HTTPS 证书**：
  - 使用 Let's Encrypt 免费证书
  - 在 Gin 中启用 TLS 或通过 Nginx/Caddy 反向代理终止 SSL
- [ ] **反向代理配置**：
  - Nginx / Caddy 配置文件，代理 `https://域名` → `http://127.0.0.1:8080`
  - WebSocket 代理配置（`/api/*/stream`）
  - 静态资源缓存头配置
- [ ] **环境变量适配**：增加 `SC_BASE_URL` 环境变量，用于后端生成完整链接（通知跳转、备份下载等）
- [ ] **CORS 更新**：将 CORS 允许的 Origin 从 `*` 改为具体域名

**涉及文件**：`.env` · `main.go`（CORS/TLS） · 新增 `deploy/nginx.conf` 或 `deploy/Caddyfile` · DNS 服务商配置

---

### TODO-23 动态性优化

**现状**：
- 当前页面交互以静态刷新为主：点击按钮 → 请求 API → 重新渲染表格/卡片
- 部分模块有 WebSocket 实时推送（GPS/电池/监控/仿真），但 UI 更新方式为直接替换 DOM
- 缺乏过渡动画、微交互、状态变化反馈等动态体验

**任务清单**：
- [ ] **页面切换动画**：iframe 切换模块时增加淡入效果（`opacity 0→1 + translateY`，300ms），避免白屏闪烁
- [ ] **卡片/列表动画**：
  - 卡片首次加载时使用交错入场动画（stagger animation，每张卡片延迟 50ms）
  - 表格数据刷新时使用行级 fade 过渡，而非整体替换
  - 统计数字变化时使用计数动画（数字从旧值滚动到新值）
- [ ] **状态变化微交互**：
  - 模拟机状态变更时卡片/表格行短暂高亮闪烁（`@keyframes highlight`）
  - 通知到达时铃铛图标抖动动画
  - 告警触发时相关模块导航项脉冲提示
- [ ] **加载状态优化**：
  - 全局统一 Loading 骨架屏（skeleton），替代空白或旋转 spinner
  - 按钮点击后切换为 loading 状态（禁用 + 旋转图标），请求完成后恢复
- [ ] **实时数据动态更新**：
  - WebSocket 推送的数据更新使用平滑过渡（数值渐变、进度条缓动、图表数据点滑入）
  - Chart.js 图表更新使用 `animation: { duration: 500 }` 而非瞬时跳变
- [ ] **滚动与视差效果**：
  - 侧边栏滚动增加滚动阴影提示（上下滚动时顶/底部渐变阴影）
  - 统计看板长页面增加轻微视差滚动效果
- [ ] **暗色主题过渡**（可选）：为仿真页增加亮/暗主题切换按钮，切换时全局平滑过渡

**涉及文件**：`common.css`（全局动画类） · `common.js`（动画工具函数） · `dashboard.html`（iframe 切换动画） · 各模块 HTML · `ai-assistant.js` · `notification-bell.js`

---

## 推荐实施顺序

> 按依赖关系和影响范围排列，建议从基础设施到上层视觉逐步推进。

### 第一批（基础设施 + 核心功能）— 建议最先完成
| 顺序 | TODO | 理由 |
|------|------|------|
| 1 | **#15 项目取名** ★ | 所有 UI/文档/域名的前提 |
| 2 | **#13 Logo 优化** | 依赖项目名确定 |
| 3 | **#16 LLM / Deepseek 适配** ★ | AI 能力基础，影响 CoT/助手/规划 |
| 4 | **#01 仿真地图→天地图** ★ | 地图统一，影响多模块 |
| 5 | **#09 模拟器任务建设优化** ★ | 核心业务流程闭环 |

### 第二批（功能增强 + 性能优化）
| 顺序 | TODO | 理由 |
|------|------|------|
| 6 | #05 CoT 思维链优化 | 依赖 #16 LLM 基础 |
| 7 | #06 离线通知屏蔽 | 通知稳定性保障 |
| 8 | #07 并发 RPM 优化 | 依赖 #16 LLM 限流需求 |
| 9 | #18 RAG 优化 | 依赖 #16 LLM + embedding |
| 10 | #03 仿真日志优化 | 依赖 #09 仿真流程清晰 |
| 11 | #04 导出→展示跳转 | 依赖 #09 任务建设完善 |
| 12 | #10 电量+图表优化 | 独立可并行 |
| 13 | #08 前端性能优化 | 基础设施，不影响功能 |

### 第三批（视觉升级 + 体验打磨）
| 顺序 | TODO | 理由 |
|------|------|------|
| 14 | #20 前端全面圆角 | CSS 变量基础 |
| 15 | #21 毛玻璃效果 | 依赖 #20 圆角规范 |
| 16 | #14 登录背景优化 | 依赖 #13 Logo + #21 毛玻璃 |
| 17 | #23 动态性优化 | 依赖 #20/#21 视觉基础 |
| 18 | #11 轨迹弧度化 | 独立可并行 |
| 19 | #12 视频模块位置 | 独立可并行 |
| 20 | #02 AI 助手图标优化 | 依赖 #21 毛玻璃 |
| 21 | #19 默认头像→Logo | 依赖 #13 Logo 完成 |
| 22 | #22 域名优化 | 依赖 #15 项目名 |
| 23 | #17 PPT 首页优化 | 依赖 #13/#15，最后做 |

---

## 附录：涉及文件全景索引

| 文件/目录 | 涉及 TODO 编号 |
|-----------|---------------|
| `project/web/modules/simulation.html` | #01 #03 #04 #09 #10 #11 |
| `project/web/modules/common.css` | #20 #21 #23 |
| `project/web/modules/common.js` | #01 #08 #23 |
| `project/web/modules/ai-assistant.js` | #02 #19 #21 #23 |
| `project/web/modules/notification-bell.js` | #06 #21 #23 |
| `project/web/modules/cot.html` | #05 |
| `project/web/modules/battery.html` | #10 |
| `project/web/modules/gps.html` | #01 #11 |
| `project/web/modules/flight.html` | #01 #11 |
| `project/web/modules/video.html` | #12 |
| `project/web/modules/concurrency.html` | #07 |
| `project/web/dashboard.html` | #04 #08 #12 #13 #15 #19 #20 #21 #23 |
| `project/web/login.html` | #13 #14 #15 #21 |
| `project/web/register.html` | #13 #14 #15 #21 |
| `project/internal/llm/llm.go` | #07 #16 |
| `project/internal/handlers/cot.go` | #05 #16 |
| `project/internal/handlers/ai_assistant.go` | #16 #18 |
| `project/internal/handlers/simulation.go` | #03 #09 |
| `project/internal/handlers/notification.go` | #06 |
| `project/internal/handlers/patrol.go` | #06 |
| `project/internal/taskpool/pool.go` | #07 |
| `project/.env` | #01 #07 #16 #22 |
| `README.md` | #15 |
| `Cloudcontrol.pptx` | #17 |
| `功能介绍/` | #18 |

---

> **维护说明**：本文档随开发进度更新，完成的任务项标记 `[x]`。每完成一批后回顾下一批的依赖关系是否满足。
