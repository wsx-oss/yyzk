// AI Assistant Widget - 右下角 AI 小助手浮窗
(function () {
  if (document.getElementById('ai-assistant-root')) return;

  const TOKEN = localStorage.getItem('token');
  const SESSION_ID = 'session_' + (localStorage.getItem('username') || 'default');

  // ===================== Styles =====================
  // SVG icons used in the widget
  const SVG_SPARKLES = `<svg xmlns="http://www.w3.org/2000/svg" width="28" height="28" viewBox="0 0 36 36" fill="none" stroke="currentColor" stroke-width="1.2" stroke-linecap="round" stroke-linejoin="round"><rect x="15" y="13" width="6" height="10" rx="2" fill="currentColor" opacity="0.85"/><line x1="18" y1="15" x2="8" y2="8" stroke="currentColor" stroke-width="1.8"/><line x1="18" y1="15" x2="28" y2="8" stroke="currentColor" stroke-width="1.8"/><line x1="18" y1="21" x2="8" y2="28" stroke="currentColor" stroke-width="1.8"/><line x1="18" y1="21" x2="28" y2="28" stroke="currentColor" stroke-width="1.8"/><circle cx="8" cy="8" r="4" fill="currentColor" opacity="0.25" stroke="currentColor"/><circle cx="28" cy="8" r="4" fill="currentColor" opacity="0.25" stroke="currentColor"/><circle cx="8" cy="28" r="4" fill="currentColor" opacity="0.25" stroke="currentColor"/><circle cx="28" cy="28" r="4" fill="currentColor" opacity="0.25" stroke="currentColor"/><circle cx="8" cy="8" r="1.5" fill="currentColor" opacity="0.9"/><circle cx="28" cy="8" r="1.5" fill="currentColor" opacity="0.9"/><circle cx="8" cy="28" r="1.5" fill="currentColor" opacity="0.9"/><circle cx="28" cy="28" r="1.5" fill="currentColor" opacity="0.9"/><polygon points="18,11 16,14 20,14" fill="currentColor" opacity="0.8"/></svg>`;
  const SVG_BOT_AVATAR = `<svg xmlns="http://www.w3.org/2000/svg" width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M12 8V4H8"/><rect x="2" y="8" width="20" height="12" rx="2"/><path d="M6 12h.01"/><path d="M18 12h.01"/><path d="M9 16h6"/></svg>`;
  const USER_AVATAR = '<img src="/app/logo.jpg" alt="默认头像" style="width:100%;height:100%;object-fit:cover;border-radius:50%;" />';

  const style = document.createElement('style');
  style.textContent = `
    #ai-fab {
      position: fixed; bottom: 28px; right: 28px; z-index: 10000;
      width: 60px; height: 60px; border-radius: 50%;
      background: linear-gradient(135deg, #0ea5e9, #6366f1);
      color: #fff; border: none; cursor: pointer;
      box-shadow: 0 4px 20px rgba(14,165,233,0.4);
      display: flex; align-items: center; justify-content: center;
      font-size: 26px; transition: transform 0.3s, box-shadow 0.3s;
      animation: aiFabPulse 2.5s ease-in-out infinite;
      touch-action: none; user-select: none;
    }
    #ai-fab:hover { transform: scale(1.1); box-shadow: 0 6px 28px rgba(14,165,233,0.55); animation: none; }
    #ai-fab.dragging { animation: none; cursor: grabbing; }
    #ai-fab .fab-badge {
      position: absolute; top: -2px; right: -2px;
      background: #ef4444; color: #fff; font-size: 11px;
      min-width: 18px; height: 18px; border-radius: 9px;
      display: flex; align-items: center; justify-content: center;
      font-weight: 600; padding: 0 4px; display: none;
    }
    @keyframes aiFabPulse {
      0%, 100% { box-shadow: 0 4px 20px rgba(14,165,233,0.4); }
      50% { box-shadow: 0 4px 30px rgba(99,102,241,0.65), 0 0 0 8px rgba(99,102,241,0.12); }
    }

    #ai-panel {
      position: fixed; bottom: 100px; right: 28px; z-index: 10001;
      width: min(400px, calc(100vw - 56px)); max-height: 600px; border-radius: 20px;
      background: rgba(255,255,255,0.85);
      backdrop-filter: blur(16px); -webkit-backdrop-filter: blur(16px);
      box-shadow: 0 8px 40px rgba(0,0,0,0.18), 0 0 0 1px rgba(255,255,255,0.3);
      display: none; flex-direction: column; overflow: hidden;
      animation: aiSlideUp 0.3s ease-out;
      font-family: system-ui, "Segoe UI", "Microsoft YaHei", sans-serif;
    }
    #ai-panel.open { display: flex; }

    @keyframes aiSlideUp {
      from { opacity: 0; transform: translateY(20px); }
      to { opacity: 1; transform: translateY(0); }
    }

    .ai-header {
      background: linear-gradient(135deg, #0ea5e9, #6366f1);
      color: #fff; padding: 16px 20px; display: flex;
      align-items: center; justify-content: space-between;
      flex-shrink: 0;
    }
    .ai-header-left { display: flex; align-items: center; gap: 10px; }
    .ai-header-left .ai-avatar {
      width: 36px; height: 36px; border-radius: 50%;
      background: rgba(255,255,255,0.2); display: flex;
      align-items: center; justify-content: center; font-size: 20px;
      color: #fff;
    }
    .ai-header-left .ai-title { font-size: 15px; font-weight: 600; }
    .ai-header-left .ai-subtitle { font-size: 11px; opacity: 0.8; }
    .ai-header-actions { display: flex; gap: 8px; }
    .ai-header-actions button {
      background: rgba(255,255,255,0.2); border: none; color: #fff;
      width: 30px; height: 30px; border-radius: 50%; cursor: pointer;
      font-size: 14px; display: flex; align-items: center; justify-content: center;
      transition: background 0.2s;
    }
    .ai-header-actions button:hover { background: rgba(255,255,255,0.35); }

    .ai-messages {
      flex: 1; overflow-y: auto; padding: 16px;
      min-height: 300px; max-height: 400px;
      background: #f8fafc;
    }
    .ai-messages::-webkit-scrollbar { width: 4px; }
    .ai-messages::-webkit-scrollbar-thumb { background: #cbd5e1; border-radius: 2px; }

    .ai-msg {
      display: flex; gap: 8px; margin-bottom: 14px;
      animation: aiFadeIn 0.3s ease-out;
    }
    @keyframes aiFadeIn {
      from { opacity: 0; transform: translateY(8px); }
      to { opacity: 1; transform: translateY(0); }
    }
    .ai-msg.user { flex-direction: row-reverse; }
    .ai-msg-avatar {
      width: 32px; height: 32px; border-radius: 50%;
      flex-shrink: 0; display: flex; align-items: center;
      justify-content: center; font-size: 15px;
    }
    .ai-msg.assistant .ai-msg-avatar { background: linear-gradient(135deg, #0ea5e9, #6366f1); color: #fff; }
    .ai-msg.user .ai-msg-avatar { background: #e2e8f0; color: #475569; }
    .ai-msg.user .ai-msg-avatar { overflow: hidden; border: 1px solid #cbd5e1; }
    .ai-msg-bubble {
      max-width: 80%; padding: 10px 14px; border-radius: 12px;
      font-size: 13px; line-height: 1.6; word-break: break-word;
    }
    .ai-msg.assistant .ai-msg-bubble {
      background: #fff; color: #1e293b;
      border: 1px solid #e2e8f0; border-top-left-radius: 4px;
    }
    .ai-msg.user .ai-msg-bubble {
      background: #0ea5e9; color: #fff;
      border-top-right-radius: 4px;
    }
    .ai-msg-bubble .nav-link {
      display: inline-block; margin: 4px 2px; padding: 2px 10px;
      background: #eff6ff; color: #2563eb; border-radius: 12px;
      font-size: 12px; cursor: pointer; text-decoration: none;
      border: 1px solid #bfdbfe; transition: all 0.2s;
    }
    .ai-msg-bubble .nav-link:hover { background: #dbeafe; }

    .ai-suggestions {
      padding: 8px 16px; display: flex; flex-wrap: wrap; gap: 6px;
      border-top: 1px solid #f1f5f9; background: #fff; flex-shrink: 0;
    }
    .ai-suggestions .sug-btn {
      padding: 5px 12px; border-radius: 14px; font-size: 12px;
      background: #f1f5f9; color: #475569; border: 1px solid #e2e8f0;
      cursor: pointer; transition: all 0.2s; white-space: nowrap;
    }
    .ai-suggestions .sug-btn:hover { background: #e2e8f0; color: #1e293b; }

    .ai-quick-cmds {
      padding: 6px 16px; display: flex; gap: 6px; overflow-x: auto;
      border-top: 1px solid #f1f5f9; background: #fff; flex-shrink: 0;
    }
    .ai-quick-cmds::-webkit-scrollbar { height: 0; }
    .ai-quick-cmds .qcmd {
      padding: 4px 10px; border-radius: 12px; font-size: 11px;
      background: #eff6ff; color: #2563eb; border: 1px solid #bfdbfe;
      cursor: pointer; transition: all 0.2s; white-space: nowrap;
      flex-shrink: 0;
    }
    .ai-quick-cmds .qcmd:hover { background: #dbeafe; }

    .ai-input-area {
      padding: 12px 16px; border-top: 1px solid #e2e8f0;
      display: flex; gap: 8px; background: #fff; flex-shrink: 0;
    }
    .ai-input-area input {
      flex: 1; padding: 10px 14px; border: 1px solid #e2e8f0;
      border-radius: 20px; font-size: 13px; outline: none;
      transition: border-color 0.2s;
    }
    .ai-input-area input:focus { border-color: #0ea5e9; box-shadow: 0 0 0 3px rgba(14,165,233,0.1); }
    .ai-input-area button {
      width: 40px; height: 40px; border-radius: 50%;
      background: #0ea5e9; color: #fff; border: none;
      cursor: pointer; font-size: 16px; display: flex;
      align-items: center; justify-content: center; transition: background 0.2s;
      flex-shrink: 0;
    }
    .ai-input-area button:hover { background: #0284c7; }
    .ai-input-area button:disabled { background: #94a3b8; cursor: not-allowed; }

    .ai-typing { display: flex; gap: 4px; align-items: center; padding: 4px 0; }
    .ai-typing span {
      width: 6px; height: 6px; border-radius: 50%; background: #94a3b8;
      animation: aiTypingDot 1.2s infinite;
    }
    .ai-typing span:nth-child(2) { animation-delay: 0.2s; }
    .ai-typing span:nth-child(3) { animation-delay: 0.4s; }
    @keyframes aiTypingDot {
      0%, 60%, 100% { opacity: 0.3; transform: scale(0.8); }
      30% { opacity: 1; transform: scale(1); }
    }
  `;
  document.head.appendChild(style);

  // ===================== HTML =====================
  const root = document.createElement('div');
  root.id = 'ai-assistant-root';
  root.innerHTML = `
    <button id="ai-fab" title="AI 助手">
      ${SVG_SPARKLES}
      <span class="fab-badge" id="aiFabBadge"></span>
    </button>
    <div id="ai-panel">
      <div class="ai-header">
        <div class="ai-header-left">
          <div class="ai-avatar">${SVG_BOT_AVATAR}</div>
          <div>
            <div class="ai-title">小云 AI 助手</div>
            <div class="ai-subtitle" id="aiSubtitle">云翼智控 智能助手</div>
          </div>
        </div>
        <div class="ai-header-actions">
          <button id="aiClearBtn" title="清空会话">🗑️</button>
          <button id="aiCloseBtn" title="关闭">✕</button>
        </div>
      </div>
      <div class="ai-messages" id="aiMessages"></div>
      <div class="ai-suggestions" id="aiSuggestions"></div>
      <div class="ai-quick-cmds">
        <span class="qcmd" data-cmd="/status">📊 系统状态</span>
        <span class="qcmd" data-cmd="/drones">🚁 无人机</span>
        <span class="qcmd" data-cmd="/alerts">⚠️ 告警</span>
        <span class="qcmd" data-cmd="/battery">🔋 电池</span>
        <span class="qcmd" data-cmd="/tasks">✈️ 任务</span>
        <span class="qcmd" data-cmd="/help">❓ 帮助</span>
      </div>
      <div class="ai-input-area">
        <input type="text" id="aiInput" placeholder="输入问题或指令..." autocomplete="off" />
        <button id="aiSendBtn">➤</button>
      </div>
    </div>
  `;
  document.body.appendChild(root);

  // ===================== Logic =====================
  const fab = document.getElementById('ai-fab');
  const panel = document.getElementById('ai-panel');
  const messagesEl = document.getElementById('aiMessages');
  const input = document.getElementById('aiInput');
  const sendBtn = document.getElementById('aiSendBtn');
  const closeBtn = document.getElementById('aiCloseBtn');
  const clearBtn = document.getElementById('aiClearBtn');
  const suggestionsEl = document.getElementById('aiSuggestions');
  const subtitleEl = document.getElementById('aiSubtitle');
  let isOpen = false;
  let isSending = false;

  function positionPanelNearFab() {
    if (!isOpen) return;
    const gap = 12;
    const padding = 8;
    const fabRect = fab.getBoundingClientRect();
    const panelRect = panel.getBoundingClientRect();

    let left = fabRect.left - panelRect.width - gap;
    let top = fabRect.top - panelRect.height - gap;

    // If left space is not enough, place panel on the right side of FAB
    if (left < padding) {
      left = fabRect.right + gap;
    }

    if (top < padding) {
      top = fabRect.bottom + gap;
    }
    if (top + panelRect.height > window.innerHeight - padding) {
      top = Math.max(padding, window.innerHeight - panelRect.height - padding);
    }
    if (left + panelRect.width > window.innerWidth - padding) {
      left = window.innerWidth - panelRect.width - padding;
    }
    if (left < padding) {
      left = padding;
    }

    panel.style.right = 'auto';
    panel.style.bottom = 'auto';
    panel.style.left = left + 'px';
    panel.style.top = top + 'px';
  }

  function togglePanel() {
    isOpen = !isOpen;
    panel.classList.toggle('open', isOpen);
    if (isOpen) {
      requestAnimationFrame(positionPanelNearFab);
      loadRAGStatus();
      input.focus();
      if (messagesEl.children.length === 0) {
        loadHistory();
      }
    }
  }

  function loadRAGStatus() {
    if (!subtitleEl) return;
    aiAPI('/ai/rag/stats', 'GET')
      .then(data => {
        if (data && data.enabled) {
          subtitleEl.textContent = `云翼智控 智能助手 · RAG已启用 (${data.chunk_count || 0})`;
        } else {
          subtitleEl.textContent = '云翼智控 智能助手 · RAG未启用';
        }
      })
      .catch(() => {
        subtitleEl.textContent = '云翼智控 智能助手';
      });
  }

  // ---- Draggable FAB ----
  let _fabDragState = null;
  function initFabPosition() {
    try {
      const saved = JSON.parse(localStorage.getItem('ai_fab_pos'));
      if (saved && typeof saved.x === 'number' && typeof saved.y === 'number') {
        fab.style.right = 'auto';
        fab.style.bottom = 'auto';
        fab.style.left = Math.min(saved.x, window.innerWidth - 60) + 'px';
        fab.style.top = Math.min(saved.y, window.innerHeight - 60) + 'px';
      }
    } catch(e) {}
  }
  initFabPosition();

  fab.addEventListener('pointerdown', (e) => {
    _fabDragState = { sx: e.clientX, sy: e.clientY, moved: false, startLeft: fab.offsetLeft, startTop: fab.offsetTop };
    fab.setPointerCapture(e.pointerId);
  });
  fab.addEventListener('pointermove', (e) => {
    if (!_fabDragState) return;
    const dx = e.clientX - _fabDragState.sx;
    const dy = e.clientY - _fabDragState.sy;
    if (Math.abs(dx) > 4 || Math.abs(dy) > 4) _fabDragState.moved = true;
    if (_fabDragState.moved) {
      fab.classList.add('dragging');
      fab.style.right = 'auto';
      fab.style.bottom = 'auto';
      fab.style.left = Math.max(0, Math.min(window.innerWidth - 60, _fabDragState.startLeft + dx)) + 'px';
      fab.style.top = Math.max(0, Math.min(window.innerHeight - 60, _fabDragState.startTop + dy)) + 'px';
      positionPanelNearFab();
    }
  });
  fab.addEventListener('pointerup', (e) => {
    if (!_fabDragState) return;
    fab.classList.remove('dragging');
    if (_fabDragState.moved) {
      localStorage.setItem('ai_fab_pos', JSON.stringify({ x: fab.offsetLeft, y: fab.offsetTop }));
    } else {
      togglePanel();
    }
    _fabDragState = null;
  });

  window.addEventListener('resize', () => {
    positionPanelNearFab();
  });

  closeBtn.addEventListener('click', togglePanel);

  // Send message
  function sendMessage(text) {
    if (!text || isSending) return;
    text = text.trim();
    if (!text) return;
    input.value = '';
    appendMessage('user', text);
    showTyping();
    isSending = true;
    sendBtn.disabled = true;

    aiAPI('/ai/chat', 'POST', { message: text, session_id: SESSION_ID })
      .then(data => {
        hideTyping();
        if (data.reply) {
          appendMessage('assistant', data.reply);
        }
      })
      .catch(err => {
        hideTyping();
        appendMessage('assistant', '❌ 请求失败：' + err.message);
      })
      .finally(() => {
        isSending = false;
        sendBtn.disabled = false;
        input.focus();
      });
  }

  sendBtn.addEventListener('click', () => sendMessage(input.value));
  input.addEventListener('keydown', e => {
    if (e.key === 'Enter' && !e.shiftKey) {
      e.preventDefault();
      sendMessage(input.value);
    }
  });

  // Quick commands
  document.querySelectorAll('.qcmd').forEach(btn => {
    btn.addEventListener('click', () => sendMessage(btn.dataset.cmd));
  });

  // Clear chat
  clearBtn.addEventListener('click', () => {
    if (!confirm('确定要清空会话历史吗？')) return;
    aiAPI('/ai/clear', 'POST', { session_id: SESSION_ID })
      .then(() => {
        messagesEl.innerHTML = '';
        showWelcome();
      });
  });

  // Append a message to the chat
  function appendMessage(role, content) {
    const div = document.createElement('div');
    div.className = 'ai-msg ' + role;
    const avatar = role === 'assistant' ? SVG_BOT_AVATAR : USER_AVATAR;
    // Process [NAV:xxx] tags into clickable links
    const processedContent = processNavLinks(escapeHtml(content));
    div.innerHTML = `
      <div class="ai-msg-avatar">${avatar}</div>
      <div class="ai-msg-bubble">${processedContent}</div>
    `;
    messagesEl.appendChild(div);
    messagesEl.scrollTop = messagesEl.scrollHeight;

    // Bind nav link clicks
    div.querySelectorAll('.nav-link').forEach(link => {
      link.addEventListener('click', () => {
        const page = link.dataset.nav;
        if (page && window.parent && window.parent.navigateTo) {
          window.parent.navigateTo(page);
        } else if (page && typeof navigateTo === 'function') {
          navigateTo(page);
        }
      });
    });
  }

  function processNavLinks(text) {
    // Convert **text** to bold
    text = text.replace(/\*\*(.+?)\*\*/g, '<strong>$1</strong>');
    // Convert \n to <br>
    text = text.replace(/\n/g, '<br>');
    // Convert [NAV:xxx] to clickable links
    const navLabels = {
      drones: '🚁 无人机管理', flight: '✈️ 航线规划', noflyzone: '🚫 禁飞区',
      gps: '📍 GPS定位', video: '📹 视频监控', remote: '🖥️ 远程桌面',
      alerts: '⚠️ 异常报警', battery: '🔋 电池监控', hardware: '💻 硬件状态',
      monitor: '📊 系统监控', audio: '🎤 语音交互', performance: '📈 性能分析',
      updates: '📦 软件更新', logs: '📋 操作日志', sync: '🔄 数据同步',
      cot: '🧠 CoT决策',
    };
    text = text.replace(/\[NAV:(\w+)\]/g, (_, page) => {
      const label = navLabels[page] || page;
      return `<span class="nav-link" data-nav="${page}">${label} →</span>`;
    });
    return text;
  }

  function escapeHtml(str) {
    const div = document.createElement('div');
    div.textContent = str;
    return div.innerHTML;
  }

  function showTyping() {
    const div = document.createElement('div');
    div.className = 'ai-msg assistant';
    div.id = 'aiTypingIndicator';
    div.innerHTML = `
      <div class="ai-msg-avatar">${SVG_BOT_AVATAR}</div>
      <div class="ai-msg-bubble"><div class="ai-typing"><span></span><span></span><span></span></div></div>
    `;
    messagesEl.appendChild(div);
    messagesEl.scrollTop = messagesEl.scrollHeight;
  }

  function hideTyping() {
    const el = document.getElementById('aiTypingIndicator');
    if (el) el.remove();
  }

  function showWelcome() {
    appendMessage('assistant',
      '👋 你好！我是 **小云**，云翼智控 平台的 AI 助手。\n\n' +
      '我可以帮你：\n' +
      '• 📊 查询系统状态和数据\n' +
      '• ⚠️ 分析告警和异常\n' +
      '• 🔋 解读电池状态\n' +
      '• ✈️ 了解飞行任务\n' +
      '• 🗺️ 快速跳转到各模块页面\n\n' +
      '试试下方的快捷指令，或直接问我任何问题！'
    );
  }

  // Load chat history
  function loadHistory() {
    aiAPI('/ai/history?session_id=' + encodeURIComponent(SESSION_ID), 'GET')
      .then(data => {
        messagesEl.innerHTML = '';
        if (data.items && data.items.length > 0) {
          data.items.forEach(m => appendMessage(m.role, m.content));
        } else {
          showWelcome();
        }
      })
      .catch(() => showWelcome());

    // Load suggestions
    loadSuggestions();
  }

  function loadSuggestions() {
    aiAPI('/ai/suggest', 'GET')
      .then(data => {
        suggestionsEl.innerHTML = '';
        if (data.suggestions && data.suggestions.length > 0) {
          data.suggestions.forEach(s => {
            const btn = document.createElement('span');
            btn.className = 'sug-btn';
            btn.textContent = s.text;
            btn.addEventListener('click', () => {
              if (s.nav) {
                if (window.parent && window.parent.navigateTo) {
                  window.parent.navigateTo(s.nav);
                } else if (typeof navigateTo === 'function') {
                  navigateTo(s.nav);
                }
              } else {
                sendMessage(s.action || s.text);
              }
            });
            suggestionsEl.appendChild(btn);
          });
        }
      })
      .catch(() => {});
  }

  // API helper
  async function aiAPI(path, method, body) {
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

  // Periodically refresh suggestions when panel is open
  setInterval(() => {
    if (isOpen) loadSuggestions();
  }, 30000);
})();
