// Notification Bell Widget - 右上角消息通知小喇叭
(function () {
  if (document.getElementById('notif-bell-root')) return;

  // ===================== Styles =====================
  const style = document.createElement('style');
  style.textContent = `
    #notif-bell-root {
      position: relative;
      display: inline-flex;
      align-items: center;
    }
    #notif-bell-btn {
      position: relative;
      width: 36px; height: 36px;
      border: 1px solid var(--cc-glass-border, rgba(255,255,255,0.56));
      border-radius: 50%;
      background: var(--cc-glass-bg-soft, rgba(255,255,255,0.38));
      backdrop-filter: blur(18px) saturate(160%);
      -webkit-backdrop-filter: blur(18px) saturate(160%);
      color: var(--cc-text-2, #4e5969);
      cursor: pointer; font-size: 18px;
      display: flex; align-items: center; justify-content: center;
      transition: all 0.2s;
    }
    #notif-bell-btn:hover {
      border-color: var(--cc-primary, #165dff);
      color: var(--cc-primary, #165dff);
      background: rgba(22,93,255,0.12);
    }
    #notif-bell-btn .bell-badge {
      position: absolute; top: -2px; right: -4px;
      background: #ef4444; color: #fff; font-size: 10px;
      min-width: 18px; height: 18px; border-radius: 9px;
      display: flex; align-items: center; justify-content: center;
      font-weight: 700; padding: 0 4px; border: 2px solid var(--cc-bg-2, #fff);
    }
    #notif-bell-btn .bell-badge.hidden { display: none; }

    #notif-bell-btn.ring-anim {
      animation: bellRing 0.6s ease-in-out;
    }
    @keyframes bellRing {
      0% { transform: rotate(0); }
      15% { transform: rotate(14deg); }
      30% { transform: rotate(-14deg); }
      45% { transform: rotate(8deg); }
      60% { transform: rotate(-8deg); }
      75% { transform: rotate(3deg); }
      100% { transform: rotate(0); }
    }

    #notif-panel {
      position: absolute; top: 48px; right: 0; z-index: 10002;
      width: 400px; max-height: 520px; border-radius: 12px;
      background: #fff; box-shadow: 0 8px 40px rgba(0,0,0,0.18);
      display: none; flex-direction: column; overflow: hidden;
      animation: notifSlideDown 0.25s ease-out;
      font-family: "Noto Sans SC", "Source Han Sans SC", "Source Han Sans CN", "Source Han Sans", "思源黑体", sans-serif;
    }
    #notif-panel.open { display: flex; }

    @keyframes notifSlideDown {
      from { opacity: 0; transform: translateY(-10px); }
      to { opacity: 1; transform: translateY(0); }
    }

    .notif-header {
      padding: 14px 18px; display: flex; align-items: center;
      justify-content: space-between; border-bottom: 1px solid #f1f5f9;
      flex-shrink: 0;
    }
    .notif-header h3 { font-size: 15px; font-weight: 600; color: #1e293b; margin: 0; }
    .notif-header-actions { display: flex; gap: 8px; }
    .notif-header-actions button {
      background: #f1f5f9; border: 1px solid #e2e8f0; color: #475569;
      padding: 4px 10px; border-radius: 6px; font-size: 12px;
      cursor: pointer; transition: all 0.2s;
    }
    .notif-header-actions button:hover { background: #e2e8f0; color: #1e293b; }

    .notif-filters {
      padding: 8px 18px; display: flex; gap: 6px;
      border-bottom: 1px solid #f1f5f9; flex-shrink: 0; flex-wrap: wrap;
    }
    .notif-filters .nf-btn {
      padding: 4px 12px; border-radius: 12px; font-size: 12px;
      background: #f8fafc; color: #64748b; border: 1px solid #e2e8f0;
      cursor: pointer; transition: all 0.2s;
    }
    .notif-filters .nf-btn:hover { background: #e2e8f0; }
    .notif-filters .nf-btn.active { background: #0ea5e9; color: #fff; border-color: #0ea5e9; }

    .notif-list {
      flex: 1; overflow-y: auto; max-height: 380px;
    }
    .notif-list::-webkit-scrollbar { width: 4px; }
    .notif-list::-webkit-scrollbar-thumb { background: #cbd5e1; border-radius: 2px; }

    .notif-item {
      padding: 12px 18px; border-bottom: 1px solid #f8fafc;
      cursor: pointer; transition: background 0.15s;
      display: flex; gap: 10px; align-items: flex-start;
    }
    .notif-item:hover { background: #f8fafc; }
    .notif-item.unread { background: #eff6ff; }
    .notif-item.unread:hover { background: #dbeafe; }

    .notif-icon {
      width: 34px; height: 34px; border-radius: 50%;
      display: flex; align-items: center; justify-content: center;
      font-size: 16px; flex-shrink: 0;
    }
    .notif-icon.type-alert { background: #fef2f2; }
    .notif-icon.type-battery { background: #fefce8; }
    .notif-icon.type-drone { background: #f0fdf4; }
    .notif-icon.type-mission { background: #eff6ff; }
    .notif-icon.type-hardware { background: #faf5ff; }
    .notif-icon.type-log { background: #f8fafc; }
    .notif-icon.type-system { background: #f1f5f9; }

    .notif-content { flex: 1; min-width: 0; }
    .notif-title {
      font-size: 13px; font-weight: 600; color: #1e293b;
      margin-bottom: 2px; overflow: hidden; text-overflow: ellipsis;
      white-space: nowrap;
    }
    .notif-desc {
      font-size: 12px; color: #64748b; line-height: 1.4;
      overflow: hidden; text-overflow: ellipsis;
      display: -webkit-box; -webkit-line-clamp: 2; -webkit-box-orient: vertical;
    }
    .notif-time {
      font-size: 11px; color: #94a3b8; margin-top: 3px;
    }
    .notif-unread-dot {
      width: 8px; height: 8px; border-radius: 50%;
      background: #0ea5e9; flex-shrink: 0; margin-top: 4px;
    }
    .notif-unread-dot.hidden { display: none; }

    .notif-empty {
      padding: 40px 20px; text-align: center; color: #94a3b8; font-size: 13px;
    }
    .notif-empty-icon { font-size: 36px; margin-bottom: 8px; }

    #notif-offline-badge {
      display: none; align-items: center; gap: 4px;
      background: #ef4444; color: #fff; font-size: 10px; font-weight: 700;
      padding: 2px 8px; border-radius: 8px; white-space: nowrap;
      animation: offlinePulse 2s ease-in-out infinite;
    }
    #notif-offline-badge.show { display: inline-flex; }
    @keyframes offlinePulse { 0%,100% { opacity:1; } 50% { opacity:0.5; } }

    /* Dark mode */
    html.dark #notif-panel,
    [data-theme="dark"] #notif-panel {
      background: rgba(14,21,37,0.95);
      box-shadow: 0 8px 40px rgba(0,0,0,0.45), 0 0 0 1px rgba(56,132,255,0.12);
    }
    html.dark .notif-header,
    [data-theme="dark"] .notif-header { border-color: rgba(56,132,255,0.12); }
    html.dark .notif-header h3,
    [data-theme="dark"] .notif-header h3 { color: var(--cc-text-1); }
    html.dark .notif-header-actions button,
    [data-theme="dark"] .notif-header-actions button {
      background: var(--cc-bg-3); border-color: var(--cc-border); color: var(--cc-text-2);
    }
    html.dark .notif-header-actions button:hover,
    [data-theme="dark"] .notif-header-actions button:hover { background: rgba(40,100,255,0.12); color: var(--cc-text-1); }
    html.dark .notif-filters,
    [data-theme="dark"] .notif-filters { border-color: rgba(56,132,255,0.12); }
    html.dark .notif-filters .nf-btn,
    [data-theme="dark"] .notif-filters .nf-btn { background: var(--cc-bg-3); color: var(--cc-text-3); border-color: var(--cc-border); }
    html.dark .notif-filters .nf-btn:hover,
    [data-theme="dark"] .notif-filters .nf-btn:hover { background: rgba(40,100,255,0.1); }
    html.dark .notif-filters .nf-btn.active,
    [data-theme="dark"] .notif-filters .nf-btn.active { background: var(--cc-primary); color: #fff; border-color: var(--cc-primary); }
    html.dark .notif-item,
    [data-theme="dark"] .notif-item { border-color: rgba(56,132,255,0.06); }
    html.dark .notif-item:hover,
    [data-theme="dark"] .notif-item:hover { background: rgba(40,100,255,0.06); }
    html.dark .notif-item.unread,
    [data-theme="dark"] .notif-item.unread { background: rgba(40,100,255,0.1); }
    html.dark .notif-item.unread:hover,
    [data-theme="dark"] .notif-item.unread:hover { background: rgba(40,100,255,0.15); }
    html.dark .notif-title,
    [data-theme="dark"] .notif-title { color: var(--cc-text-1); }
    html.dark .notif-desc,
    [data-theme="dark"] .notif-desc { color: var(--cc-text-3); }
    html.dark .notif-time,
    [data-theme="dark"] .notif-time { color: var(--cc-text-4); }
    html.dark .notif-list::-webkit-scrollbar-thumb,
    [data-theme="dark"] .notif-list::-webkit-scrollbar-thumb { background: #475569; }
  `;
  document.head.appendChild(style);

  // ===================== HTML =====================
  const root = document.createElement('div');
  root.id = 'notif-bell-root';
  root.innerHTML = `
    <span id="notif-offline-badge">⚡ 离线</span>
    <button id="notif-bell-btn" title="消息通知">
      🔔
      <span class="bell-badge hidden" id="bellBadge">0</span>
    </button>
    <div id="notif-panel">
      <div class="notif-header">
        <h3>🔔 消息通知</h3>
        <div class="notif-header-actions">
          <button id="notifReadAllBtn">全部已读</button>
          <button id="notifClearOldBtn">清理旧消息</button>
        </div>
      </div>
      <div class="notif-filters" id="notifFilters">
        <span class="nf-btn active" data-type="">全部</span>
        <span class="nf-btn" data-type="alert">⚠️ 告警</span>
        <span class="nf-btn" data-type="battery">🔋 电池</span>
        <span class="nf-btn" data-type="drone">🚁 无人机</span>
        <span class="nf-btn" data-type="mission">✈️ 任务</span>
        <span class="nf-btn" data-type="hardware">🖥️ 硬件</span>
        <span class="nf-btn" data-type="log">📋 日志</span>
        <span class="nf-btn" data-type="system">⚙️ 系统</span>
      </div>
      <div class="notif-list" id="notifList"></div>
    </div>
  `;

  // ===================== Inject into top-bar =====================
  // This widget needs to be inserted into the parent dashboard's navbar-right area.
  // We wrap it in an <li> to match the navbar list structure.
  function injectBell() {
    // Look for the navbar-right <ul> in the dashboard
    const navRight = document.querySelector('.navbar-right');
    if (!navRight) {
      return false;
    }
    // Wrap root in an <li> to fit into the <ul> structure
    const li = document.createElement('li');
    li.appendChild(root);
    // Insert before the user dropdown (last <li>)
    const userDropdownLi = navRight.querySelector('#userDropdown');
    if (userDropdownLi && userDropdownLi.parentElement) {
      navRight.insertBefore(li, userDropdownLi.parentElement);
    } else {
      navRight.appendChild(li);
    }
    return true;
  }

  if (!injectBell()) {
    document.addEventListener('DOMContentLoaded', () => injectBell());
  }

  // ===================== Logic =====================
  const bellBtn = document.getElementById('notif-bell-btn');
  const bellBadge = document.getElementById('bellBadge');
  const panel = document.getElementById('notif-panel');
  const listEl = document.getElementById('notifList');
  const readAllBtn = document.getElementById('notifReadAllBtn');
  const clearOldBtn = document.getElementById('notifClearOldBtn');
  let isOpen = false;
  let currentFilter = '';

  function togglePanel() {
    isOpen = !isOpen;
    panel.classList.toggle('open', isOpen);
    if (isOpen) {
      loadNotifications();
    }
  }

  bellBtn.addEventListener('click', (e) => {
    e.stopPropagation();
    togglePanel();
  });

  // Close panel on outside click
  document.addEventListener('click', (e) => {
    if (isOpen && !root.contains(e.target)) {
      isOpen = false;
      panel.classList.remove('open');
    }
  });
  panel.addEventListener('click', (e) => e.stopPropagation());

  // Filter buttons
  document.querySelectorAll('#notifFilters .nf-btn').forEach(btn => {
    btn.addEventListener('click', () => {
      document.querySelectorAll('#notifFilters .nf-btn').forEach(b => b.classList.remove('active'));
      btn.classList.add('active');
      currentFilter = btn.dataset.type;
      loadNotifications();
    });
  });

  // Mark all read
  readAllBtn.addEventListener('click', () => {
    notifAPI('/notifications/read-all', 'POST')
      .then(() => {
        loadNotifications();
        updateBadge();
      });
  });

  // Clear old
  clearOldBtn.addEventListener('click', () => {
    notifAPI('/notifications/clear-old', 'POST')
      .then(() => loadNotifications());
  });

  // Load notifications
  function loadNotifications() {
    let url = '/notifications?limit=50';
    if (currentFilter) url += '&type=' + currentFilter;
    notifAPI(url, 'GET')
      .then(data => {
        renderList(data.items || []);
      })
      .catch(() => {
        listEl.innerHTML = '<div class="notif-empty"><div class="notif-empty-icon">❌</div>加载失败</div>';
      });
  }

  function renderList(items) {
    if (items.length === 0) {
      listEl.innerHTML = '<div class="notif-empty"><div class="notif-empty-icon">🔕</div>暂无通知</div>';
      return;
    }
    listEl.innerHTML = '';
    items.forEach(item => {
      const div = document.createElement('div');
      div.className = 'notif-item' + (item.is_read ? '' : ' unread');
      const icon = getTypeIcon(item.type);
      const timeStr = formatTimeAgo(item.created_at);
      div.innerHTML = `
        <div class="notif-icon type-${item.type}">${icon}</div>
        <div class="notif-content">
          <div class="notif-title">${escapeHtml(item.title)}</div>
          <div class="notif-desc">${escapeHtml(item.message)}</div>
          <div class="notif-time">${timeStr}${item.source ? ' · ' + escapeHtml(item.source) : ''}</div>
        </div>
        <div class="notif-unread-dot ${item.is_read ? 'hidden' : ''}"></div>
      `;
      div.addEventListener('click', () => {
        // Mark as read
        if (!item.is_read) {
          notifAPI('/notifications/' + item.id + '/read', 'POST')
            .then(() => {
              div.classList.remove('unread');
              div.querySelector('.notif-unread-dot').classList.add('hidden');
              updateBadge();
            });
        }
        // Navigate if link is present
        if (item.link) {
          const match = item.link.match(/modules\/(\w+)\.html/);
          if (match) {
            const page = match[1];
            // Try parent navigateTo (dashboard iframe setup)
            if (window.parent && window.parent.navigateTo) {
              window.parent.navigateTo(page);
            } else if (typeof navigateTo === 'function') {
              navigateTo(page);
            }
            // Close panel
            isOpen = false;
            panel.classList.remove('open');
          }
        }
      });
      listEl.appendChild(div);
    });
  }

  function getTypeIcon(type) {
    const icons = {
      alert: '⚠️', battery: '🔋', drone: '🚁', mission: '✈️',
      hardware: '🖥️', log: '📋', system: '⚙️'
    };
    return icons[type] || '📌';
  }

  function formatTimeAgo(dateStr) {
    if (!dateStr) return '';
    const date = new Date(dateStr.replace(' ', 'T') + (dateStr.includes('Z') ? '' : 'Z'));
    const now = new Date();
    const diff = Math.floor((now - date) / 1000);
    if (diff < 60) return '刚刚';
    if (diff < 3600) return Math.floor(diff / 60) + ' 分钟前';
    if (diff < 86400) return Math.floor(diff / 3600) + ' 小时前';
    if (diff < 604800) return Math.floor(diff / 86400) + ' 天前';
    return dateStr.substring(0, 16);
  }

  function escapeHtml(str) {
    if (!str) return '';
    const div = document.createElement('div');
    div.textContent = str;
    return div.innerHTML;
  }

  // Update badge count
  function updateBadge() {
    notifAPI('/notifications/unread-count', 'GET')
      .then(data => {
        const cnt = data.count || 0;
        bellBadge.textContent = cnt > 99 ? '99+' : cnt;
        bellBadge.classList.toggle('hidden', cnt === 0);
        if (cnt > 0) {
          bellBtn.classList.add('ring-anim');
          setTimeout(() => bellBtn.classList.remove('ring-anim'), 700);
        }
      })
      .catch(() => {});
  }

  // API helper
  async function notifAPI(path, method, body) {
    const headers = {};
    const token = localStorage.getItem('token');
    if (token) headers['Authorization'] = 'Bearer ' + token;
    const opts = { method, headers };
    if (body) {
      headers['Content-Type'] = 'application/json';
      opts.body = JSON.stringify(body);
    }
    const res = await fetch('/api' + path, opts);
    if (!res.ok) throw new Error('HTTP ' + res.status);
    return res.json();
  }

  // ===================== Offline detection =====================
  const offlineBadge = document.getElementById('notif-offline-badge');
  let isSystemOnline = navigator.onLine;
  let pollTimer = null;
  let consecutiveFailures = 0;

  function setOnlineState(online) {
    if (isSystemOnline === online) return;
    isSystemOnline = online;
    offlineBadge.classList.toggle('show', !online);
    if (online) {
      consecutiveFailures = 0;
      updateBadge();
      if (isOpen) loadNotifications();
      startPolling();
    } else {
      stopPolling();
    }
  }

  window.addEventListener('online', () => setOnlineState(true));
  window.addEventListener('offline', () => setOnlineState(false));

  // Wrap updateBadge to detect fetch failures
  const origUpdateBadge = updateBadge;
  updateBadge = function() {
    notifAPI('/notifications/unread-count', 'GET')
      .then(data => {
        consecutiveFailures = 0;
        if (!isSystemOnline) setOnlineState(true);
        const cnt = data.count || 0;
        bellBadge.textContent = cnt > 99 ? '99+' : cnt;
        bellBadge.classList.toggle('hidden', cnt === 0);
        if (cnt > 0) {
          bellBtn.classList.add('ring-anim');
          setTimeout(() => bellBtn.classList.remove('ring-anim'), 700);
        }
      })
      .catch(() => {
        consecutiveFailures++;
        if (consecutiveFailures >= 3) setOnlineState(false);
      });
  };

  function startPolling() {
    stopPolling();
    pollTimer = setInterval(updateBadge, 60000);
  }
  function stopPolling() {
    if (pollTimer) { clearInterval(pollTimer); pollTimer = null; }
  }

  // Initial load + start polling
  updateBadge();
  startPolling();
})();
