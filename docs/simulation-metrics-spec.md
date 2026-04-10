# 仿真统计指标口径说明（Stage 8）

本文档定义仿真可视化看板与 AI 助手问答使用的统一统计口径。

## 1. 数据来源

- 实时引擎快照：`Engine.ListInstances()`、`Engine.GetAllTelemetry()`
- 历史遥测日志：`sim_telemetry_log`
- 历史事件日志：`sim_events`
- RL 训练日志：`rl_training_log`
- 统计 API：`GET /api/sim/stats`

## 2. 实时指标（live）

### 2.1 模拟机总数 `total_instances`

- 定义：当前仿真引擎实例总数
- 计算：`len(ListInstances())`

### 2.2 在线数 `running_instances`

- 定义：状态为 `running` 的实例数量
- 计算：`count(instance.state == running)`

### 2.3 异常实例数 `anomaly_instances`

- 定义：当前遥测中存在激活异常的实例数
- 计算：遍历 `telemetry.anomalies`，实例维度去重计数

### 2.4 电池风险数 `battery_risk_count`

- 定义：电量小于等于 20% 的实例数量
- 计算：`count(telemetry.battery.level <= 20)`

### 2.5 任务完成率 `task_completion_rate`

- 定义：实时运行实例的平均航线进度（连续值，0~1）
- 计算：`sum(telemetry.route_progress) / running_telemetry_count`
- 回退：当无运行遥测时，回退为 `completed_instances / total_instances`

### 2.6 平均航线进度 `avg_route_progress`

- 定义：与 `task_completion_rate` 同口径的显示字段，用于前端语义化展示
- 计算：同 `task_completion_rate`

### 2.7 已完成任务数 `completed_tasks`

- 定义：任务状态为 `已完成` 的实例数量
- 计算：`count(instance.task_status == 已完成)`

## 3. 分布指标（distribution）

### 3.1 任务类型分布 `mission`

- 定义：不同任务类型实例数量分布
- 维度：`patrol / inspection / delivery / survey / sar / unknown`

### 3.2 任务状态分布 `task_status`

- 定义：实例任务状态数量分布
- 维度：`未开始 / 执行中 / 已完成 / 已暂停 / 任务失败`

### 3.3 告警类型分布 `alert_type`

- 定义：事件日志中不同 `event_type` 的出现次数
- 来源：`sim_events`

## 4. 趋势图指标（charts）

### 4.1 运行趋势图 `running_trend`

- 时间粒度：分钟（`YYYY-MM-DD HH:MM`）
- 定义：每分钟内非待飞状态的唯一实例数
- 窗口：最近 30 个分钟点

### 4.2 异常统计图 `anomaly_trend`

- 时间粒度：分钟
- 定义：每分钟事件日志条数
- 来源：`sim_events`
- 窗口：最近 30 个分钟点

### 4.3 电池风险图 `battery_risk_trend`

- 时间粒度：分钟
- 定义：每分钟低电样本占比
- 公式：`risk_ratio = risk_samples / total_samples`
- 判定阈值：`battery_level <= 20`
- 窗口：最近 30 个分钟点

### 4.4 任务分类占比图

- 数据源：`distribution.mission`
- 图型：环形图

### 4.5 策略学习曲线 `rl_curve`

- 横轴：`episode`
- 指标1：平均奖励 `avg_reward`
- 指标2：探索率 `epsilon`
- 来源：`rl_training_log`

## 5. AI 助手问答映射建议

- “当前有多少异常模拟机” → `live.anomaly_instances`
- “哪类任务最多” → `distribution.mission` 最大值对应键
- “哪种策略更优” → `rl_curve.rewards` 近期上升趋势 + `rl/export-policy` 建议动作

## 6. 并发架构说明（Stage 5-6）

仿真遥测数据链路已全部接入统一并发框架：

| 数据链路 | 执行模式 | Pool 组名 | 说明 |
| --- | --- | --- | --- |
| 遥测 DB 写入 | IO Pool | `simulation` | `SimTelemetryPusher.OnTelemetry` 异步提交 GPS/Battery/Flight 写入 |
| LLM 航线规划 | CPU Pool | `route_planning` | `FlightPlanCreate` 将 LLM 调用提交到 CPU 池 |
| 监控告警 | Periodic | `monitoring` | `SchedulePeriodic` 阈值检测 + 离线检测 |
| AI 巡检 | Periodic | `patrol` | `StartPatrolInspection` 独立后台任务 |
| 统计缓存 | StatsCache | - | 后台定时刷新，`/api/stats/cached` 返回 |
| WS 推送 | Throttler | - | 按 topic 节流（gps 200ms / battery 500ms / sim 100ms） |
| DB 高频写 | WriteBatcher | - | GPS/Battery 批量合并写入 |

压测验证：`pool_test.go` 含 9 项测试，包括 5000 并发提交、混合 IO+CPU+Periodic 负载、benchmark。

## 7. 接口返回示例（简化）

```json
{
  "live": {
    "total_instances": 120,
    "running_instances": 96,
    "anomaly_instances": 8,
    "battery_risk_count": 11,
    "task_completion_rate": 0.42,
    "avg_route_progress": 0.42,
    "completed_tasks": 36
  },
  "distribution": {
    "mission": {"patrol": 35, "inspection": 28, "delivery": 20, "survey": 22, "sar": 15},
    "task_status": {"执行中": 80, "已完成": 50},
    "alert_type": {"low_battery": 41, "flight_deviation": 18}
  },
  "charts": {
    "running_trend": {"labels": ["2026-04-10 12:30"], "values": [95]},
    "anomaly_trend": {"labels": ["2026-04-10 12:30"], "values": [6]},
    "battery_risk_trend": {"labels": ["2026-04-10 12:30"], "values": [0.14]},
    "rl_curve": {"episodes": [100, 200], "rewards": [0.12, 0.21], "epsilons": [0.28, 0.22]}
  }
}
```
