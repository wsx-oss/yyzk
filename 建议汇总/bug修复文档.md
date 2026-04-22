# 云翼智控项目 Bug 修复提示词

你好，AI。请帮我修复 `云翼智控` 项目中的 6 个系统 Bug。
项目前端源码位于 `project/web/modules/`，后端位于 `project/internal/handlers/` 目录下。请严格按照以下问题描述与修改建议，修改对应的代码文件。

---

## 1. 航线规划 - 查看任务详情时存在空白区域
**问题描述**：
在航线规划中查看任务详情时，界面中间出现不必要的空白部分。每次打开任务详情都会导致空白部分增加。
![image.avif](https://user9136.cn.imgto.link/public/20260422/image.avif)

**需要你执行的操作**：
修改文件 `project/web/modules/flight.html`
1. 在 `viewDetail(id)` 方法中，动态创建无人机控制按钮容器 `ctrlDiv` 时，给它分配一个固定的ID（例如 `ctrlDiv.id = 'droneCtrlDiv';`）。
2. 在 `closeDetailModal()` 方法中，增加移除该元素的逻辑：
   ```javascript
   var droneCtrl = document.getElementById('droneCtrlDiv');
   if (droneCtrl) droneCtrl.remove();
   ```

---

## 2. 航线规划 - 搜索任务功能异常
**问题描述**：
输入正确的任务名无法搜索成功，但使用任务目标进行搜索可以成功。表现为任务名搜索无结果。
![image.avif](https://user9136.cn.imgto.link/public/20260422/image-1.avif)
![image.avif](https://user9136.cn.imgto.link/public/20260422/image-2.avif)

**需要你执行的操作**：
修改文件 `project/internal/handlers/flight.go`
1. 在 `FlightMissionsList` 函数中，由于 SQL 查询包含 `flight_missions f LEFT JOIN gps_devices g`，两者都有 `name` 和 `status` 字段，造成字段模糊歧义 (ambiguous column name)。
2. 将搜索条件的 SQL 拼接改为带有 `f.` 别名的形式：
   `name LIKE ?` 改为 `f.name LIKE ?`
   `status = ?` 改为 `f.status = ?`
   `target LIKE ?` 改为 `f.target LIKE ?`
   `created_at >= ?` 改为 `f.created_at >= ?`
   `created_at <= ?` 改为 `f.created_at <= ?`
3. 同步修改分页前的 COUNT 查询，也为表指定别名：
   `SELECT COUNT(*) FROM flight_missions f WHERE ` + wc

---

## 3. GPS 无人机位置搜索 - 缺少定位跳转逻辑
**问题描述**：
当前搜索功能仅能筛选对应的无人机，无法自动定位到无人机所在的地图位置。
建议：点击搜索后，增加逻辑跳转到当前无人机所在的地图位置。
![image.avif](https://user9136.cn.imgto.link/public/20260422/image-3.avif)

**需要你执行的操作**：
修改文件 `project/web/modules/gps.html`
在 `loadDevices(useInputFilters)` 函数中，执行到 `updateMapMarkers(items)` 之后，增加判断：
如果 `useInputFilters` 为 true 且 `items` 数组长度大于 0（有搜索结果），且第一项有 `latitude` 和 `longitude` 数据，则调用 `map.setView([items[0].latitude, items[0].longitude], 16);` 将地图视角移动到该无人机位置。

---

## 4. COT 智能决策 - 搜索任务类型不完整
**问题描述**：
COT 智能决策中，搜索任务类型时仅显示四个类型，缺少“飞行规划”和“应急分析”。
![image.avif](https://user9136.cn.imgto.link/public/20260422/image-4.avif)

**需要你执行的操作**：
修改文件 `project/web/modules/cot.html`
找到 `<select id="filterTaskType">` 元素，在其中补充如下两个选项：
```html
<option value="flight_planning">飞行规划</option>
<option value="emergency_analysis">应急分析</option>
```

---

## 5. 电池监控 - 搜索功能不可用
**问题描述**：
电池监控模块中，搜索功能完全不可用，包括：无人机设备搜索、电池状态搜索。
![image.avif](https://user9136.cn.imgto.link/public/20260422/image-5.avif)

**需要你执行的操作**：
修改文件 `project/web/modules/battery.html`
1. 修复设备下拉框的值：在 `loadDrones()` 方法中，下拉框绑定的原本是无人机的 `d.id`，但电池监控使用的是 `gps_devices` 的ID。将拼接 HTML 的 `value="${d.id}"` 改为 `value="${d.linked_gps_device_id || d.id}"`（包括 `#filterDevice` 和 `#reportDevice` 两个下拉框）。
2. 修复搜索逻辑：修改 `search()` 为 `function search() { currentPage = 1; loadLatest(); loadRecords(); loadStats(); }` （即加入对 `loadLatest()` 的调用）。
3. 修复实时状态的过滤：在 `loadLatest()` 函数内部，获取到 `data.items` 后，根据当前 `#filterDevice` 和 `#filterStatus` 的值对 `items` 数组使用 `filter` 进行前端过滤，然后再将过滤后的数据传给 `renderLatestTable()`。

---

## 6. 硬件状态检测 - 刷新逻辑问题
**问题描述**：
现状：搜索到相应硬件后，每隔5秒的刷新会导致全部硬件重新显示。
期望：改变刷新逻辑——当搜索选中某一个或某几个硬件后，仅刷新选中的硬件信息。
![image.avif](https://user9136.cn.imgto.link/public/20260422/image-6.avif)

**需要你执行的操作**：
修改文件 `project/web/modules/hardware.html`
在 `changeRefreshInterval()` 函数内的 `setInterval` 定时器中：
获取到实时接口的 `data.items` 后，读取页面当前的四个过滤输入值（`filterName`, `filterType`, `filterIP`, `filterStatus`），使用 `.filter()` 方法筛除不符合条件的硬件数据，最后再调用 `renderLiveData(过滤后的数组)`，从而保持搜索状态下的自动刷新不会展示不相干的硬件。
