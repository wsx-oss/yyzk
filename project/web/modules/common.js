// Common utilities for modules
const $ = (q) => document.querySelector(q);
const $$ = (q) => document.querySelectorAll(q);

// ---- Theme Sync (iframe ← parent dashboard) ----
(function initThemeSync() {
  function setChartDefaults(isDark) {
    if (typeof Chart === 'undefined') return;
    var textColor = isDark ? 'rgba(185,205,240,0.85)' : '#4e5969';
    var textColorSub = isDark ? 'rgba(145,175,225,0.72)' : '#86909c';
    var gridColor = isDark ? 'rgba(56,132,255,0.12)' : 'rgba(0,0,0,0.06)';
    var tooltipBg = isDark ? 'rgba(10,18,36,0.92)' : 'rgba(255,255,255,0.96)';
    var tooltipText = isDark ? '#e0ebff' : '#1d2129';
    var tooltipBorder = isDark ? 'rgba(56,132,255,0.25)' : 'rgba(0,0,0,0.08)';
    var defaults = Chart.defaults;
    defaults.color = textColor;
    defaults.borderColor = gridColor;
    defaults.font.family = "-apple-system, BlinkMacSystemFont, 'PingFang SC', 'Microsoft YaHei', sans-serif";
    defaults.animation.duration = 750;
    defaults.animation.easing = 'easeOutQuart';
    defaults.responsive = true;
    if (defaults.scale) {
      defaults.scale.grid = defaults.scale.grid || {};
      defaults.scale.grid.color = gridColor;
      defaults.scale.grid.drawBorder = false;
    }
    if (defaults.plugins) {
      if (defaults.plugins.legend && defaults.plugins.legend.labels) {
        defaults.plugins.legend.labels.color = textColor;
        defaults.plugins.legend.labels.usePointStyle = true;
        defaults.plugins.legend.labels.pointStyle = 'circle';
        defaults.plugins.legend.labels.padding = 16;
      }
      if (defaults.plugins.tooltip) {
        defaults.plugins.tooltip.backgroundColor = tooltipBg;
        defaults.plugins.tooltip.titleColor = tooltipText;
        defaults.plugins.tooltip.bodyColor = textColorSub;
        defaults.plugins.tooltip.borderColor = tooltipBorder;
        defaults.plugins.tooltip.borderWidth = 1;
        defaults.plugins.tooltip.cornerRadius = 10;
        defaults.plugins.tooltip.padding = 12;
        defaults.plugins.tooltip.boxPadding = 6;
        defaults.plugins.tooltip.titleFont = { weight: '600', size: 13 };
        defaults.plugins.tooltip.bodyFont = { size: 12 };
        defaults.plugins.tooltip.displayColors = true;
        defaults.plugins.tooltip.usePointStyle = true;
      }
    }
  }
  function applyTheme(isDark) {
    if (isDark) {
      document.documentElement.classList.add('dark');
    } else {
      document.documentElement.classList.remove('dark');
    }
    setChartDefaults(isDark);
  }
  // Read persisted theme on load
  applyTheme(localStorage.getItem('cc-theme') === 'dark');
  // Also set defaults after Chart.js loads
  window.addEventListener('load', function() { setChartDefaults(localStorage.getItem('cc-theme') === 'dark'); });
  // Listen for theme change messages from parent
  window.addEventListener('message', function(e) {
    if (e.data && e.data.type === 'cc-theme-change') {
      applyTheme(e.data.dark);
    }
  });
})();

// ---- Tianditu Map Utilities ----
let TIANDITU_KEY = '90aa13159977757fb5d9061bf4d8c22b';
 
const DEFAULT_CAMPUS_MAP_CENTER = Object.freeze({ lat: 34.810201, lng: 113.533285, zoom: 17 });
const DEFAULT_CAMPUS_ROUTE_START = Object.freeze({ name: '郑州大学主校区南门', lat: 34.793000, lng: 113.663600 });
const DEFAULT_CAMPUS_ROUTE_GOAL = Object.freeze({ name: '郑州大学主校区北门', lat: 34.802000, lng: 113.664000 });

function tdtImgLayer() {
  return L.tileLayer(
    `https://t{s}.tianditu.gov.cn/img_w/wmts?SERVICE=WMTS&REQUEST=GetTile&VERSION=1.0.0&LAYER=img&STYLE=default&TILEMATRIXSET=w&FORMAT=tiles&TILEMATRIX={z}&TILEROW={y}&TILECOL={x}&tk=${TIANDITU_KEY}`,
    { subdomains: ['0','1','2','3','4','5','6','7'], attribution: '© 天地图', maxZoom: 18 }
  );
}

function tdtCiaLayer() {
  return L.tileLayer(
    `https://t{s}.tianditu.gov.cn/cia_w/wmts?SERVICE=WMTS&REQUEST=GetTile&VERSION=1.0.0&LAYER=cia&STYLE=default&TILEMATRIXSET=w&FORMAT=tiles&TILEMATRIX={z}&TILEROW={y}&TILECOL={x}&tk=${TIANDITU_KEY}`,
    { subdomains: ['0','1','2','3','4','5','6','7'], maxZoom: 18 }
  );
}

// Fetch server-side config to override the default Tianditu key
function loadTdtConfig() {
  return fetch('/api/config').then(r => r.json()).then(cfg => {
    if (cfg.tianditu_key) TIANDITU_KEY = cfg.tianditu_key;
  }).catch(() => {});
}

// API helper with authentication
var _apiActiveCount = 0;
async function api(path, options = {}) {
  var _gl = document.getElementById('globalLoading');
  _apiActiveCount++;
  if (_gl && _apiActiveCount === 1) _gl.style.display = 'flex';
  try {
    const token = localStorage.getItem("token");
    const headers = options.headers || {};
    if (token) {
      headers["Authorization"] = `Bearer ${token}`;
    }
    if (
      options.body &&
      typeof options.body === "object" &&
      !(options.body instanceof FormData)
    ) {
      headers["Content-Type"] = "application/json";
      options.body = JSON.stringify(options.body);
    }
    const res = await fetch("/api" + path, { ...options, headers });
    if (res.status === 401) {
      localStorage.removeItem("token");
      window.top.location.href = "/app/login.html";
      throw new Error("Unauthorized");
    }
    return res;
  } finally {
    _apiActiveCount--;
    if (_gl && _apiActiveCount <= 0) { _gl.style.display = 'none'; _apiActiveCount = 0; }
  }
}

// Format date
function formatDate(dateStr) {
  if (!dateStr) return "-";
  const d = new Date(dateStr);
  if (isNaN(d.getTime()) || d.getFullYear() < 2000) return "-";
  return d.toLocaleString("zh-CN", {
    year: "numeric",
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
  });
}

// Format bytes
function formatBytes(bytes) {
  if (bytes === 0) return "0 B";
  const k = 1024;
  const sizes = ["B", "KB", "MB", "GB", "TB"];
  const i = Math.floor(Math.log(bytes) / Math.log(k));
  return parseFloat((bytes / Math.pow(k, i)).toFixed(2)) + " " + sizes[i];
}

// ---- Chart Gradient Utility ----
function createLinearGradient(ctx, colorStart, colorEnd, height) {
  height = height || 300;
  var grad = ctx.createLinearGradient(0, 0, 0, height);
  grad.addColorStop(0, colorStart);
  grad.addColorStop(1, colorEnd);
  return grad;
}

// Professional color palette (Geeker-Admin inspired)
var CC_CHART_COLORS = {
  blue:    { solid: '#165dff', light: 'rgba(22,93,255,0.15)',  gradient: ['rgba(22,93,255,0.35)', 'rgba(22,93,255,0.02)'] },
  cyan:    { solid: '#14c9c9', light: 'rgba(20,201,201,0.15)', gradient: ['rgba(20,201,201,0.35)', 'rgba(20,201,201,0.02)'] },
  green:   { solid: '#00b42a', light: 'rgba(0,180,42,0.15)',   gradient: ['rgba(0,180,42,0.35)',   'rgba(0,180,42,0.02)'] },
  orange:  { solid: '#ff7d00', light: 'rgba(255,125,0,0.15)',  gradient: ['rgba(255,125,0,0.35)',  'rgba(255,125,0,0.02)'] },
  red:     { solid: '#f53f3f', light: 'rgba(245,63,63,0.15)',  gradient: ['rgba(245,63,63,0.35)',  'rgba(245,63,63,0.02)'] },
  purple:  { solid: '#722ed1', light: 'rgba(114,46,209,0.15)', gradient: ['rgba(114,46,209,0.35)', 'rgba(114,46,209,0.02)'] },
  magenta: { solid: '#eb2f96', light: 'rgba(235,47,150,0.15)', gradient: ['rgba(235,47,150,0.35)', 'rgba(235,47,150,0.02)'] },
  gold:    { solid: '#f7ba1e', light: 'rgba(247,186,30,0.15)', gradient: ['rgba(247,186,30,0.35)', 'rgba(247,186,30,0.02)'] },
};
var CC_PIE_PALETTE = ['#165dff', '#14c9c9', '#00b42a', '#722ed1', '#f7ba1e', '#ff7d00', '#eb2f96', '#f53f3f'];
var CC_PIE_PALETTE_DARK = ['#4e9fff', '#36d6d6', '#34d399', '#9b7cee', '#fcd34d', '#fbbf24', '#f472b6', '#f87171'];

// ---- Bar Chart Data Labels Plugin ----
var ccBarDataLabelsPlugin = {
  id: 'ccBarDataLabels',
  afterDatasetsDraw: function(chart) {
    if (chart.config.type !== 'bar') return;
    if (chart.options.plugins && chart.options.plugins.ccBarDataLabels && chart.options.plugins.ccBarDataLabels.display === false) return;
    var ctx = chart.ctx;
    var dk = document.documentElement.classList.contains('dark');
    var isHorizontal = chart.options.indexAxis === 'y';
    chart.data.datasets.forEach(function(dataset, i) {
      var meta = chart.getDatasetMeta(i);
      if (meta.hidden) return;
      meta.data.forEach(function(bar, index) {
        var value = dataset.data[index];
        if (value == null || value === 0) return;
        var displayVal = Number.isInteger(value) ? value : value.toFixed(1);
        // Append unit if configured
        var unit = (chart.options.plugins && chart.options.plugins.ccBarDataLabels && chart.options.plugins.ccBarDataLabels.unit) || '';
        ctx.save();
        ctx.fillStyle = dk ? 'rgba(224,235,255,0.88)' : '#4e5969';
        ctx.font = 'bold 11px -apple-system, BlinkMacSystemFont, "PingFang SC", "Microsoft YaHei", sans-serif';
        if (isHorizontal) {
          ctx.textAlign = 'left';
          ctx.textBaseline = 'middle';
          ctx.fillText(displayVal + unit, bar.x + 6, bar.y);
        } else {
          ctx.textAlign = 'center';
          ctx.textBaseline = 'bottom';
          ctx.fillText(displayVal + unit, bar.x, bar.y - 4);
        }
        ctx.restore();
      });
    });
  }
};

// Register the plugin globally so all Chart instances get it
if (typeof Chart !== 'undefined') {
  Chart.register(ccBarDataLabelsPlugin);
} else {
  window.addEventListener('load', function() {
    if (typeof Chart !== 'undefined') Chart.register(ccBarDataLabelsPlugin);
  });
}

// Create chart (using Chart.js) with enhanced defaults
function createChart(canvas, config) {
  if (typeof Chart === "undefined") {
    console.warn("Chart.js not loaded");
    return null;
  }
  var isDark = document.documentElement.classList.contains('dark');
  // Auto-enhance doughnut/pie charts
  if (config.type === 'doughnut' || config.type === 'pie') {
    var ds = config.data && config.data.datasets && config.data.datasets[0];
    if (ds) {
      if (!ds.borderWidth && ds.borderWidth !== 0) ds.borderWidth = 3;
      if (!ds.borderColor) ds.borderColor = isDark ? '#0e1525' : '#ffffff';
      if (!ds.hoverBorderColor) ds.hoverBorderColor = isDark ? '#0e1525' : '#ffffff';
      if (!ds.hoverOffset) ds.hoverOffset = 8;
    }
    var opts = config.options = config.options || {};
    if (!opts.cutout && config.type === 'doughnut') opts.cutout = '62%';
  }
  // Auto-enhance bar charts: rounded corners, legend, hover effect
  if (config.type === 'bar') {
    var ds = config.data && config.data.datasets && config.data.datasets[0];
    if (ds && !ds.borderRadius && ds.borderRadius !== 0) ds.borderRadius = 6;
    if (ds && !ds.hoverBackgroundColor) {
      ds.hoverBackgroundColor = ds.backgroundColor;
      ds.hoverBorderWidth = 2;
      ds.hoverBorderColor = isDark ? 'rgba(224,235,255,0.5)' : 'rgba(0,0,0,0.15)';
    }
    // Auto-show legend for multi-label bar charts (categorical bars with distinct colors)
    var opts = config.options = config.options || {};
    var plugins = opts.plugins = opts.plugins || {};
    // Add animation easing
    if (!opts.animation) opts.animation = {};
    if (!opts.animation.duration) opts.animation.duration = 900;
    if (!opts.animation.easing) opts.animation.easing = 'easeOutQuart';
  }
  return new Chart(canvas, config);
}

// Show toast message (Arco-style notification at top)
function showToast(message, type = "info") {
  const dark = document.documentElement.classList.contains('dark');
  const colors = dark ? {
    success: { bg: 'rgba(52,211,153,0.12)', border: '#34d399', text: '#5eead4', icon: '✓' },
    error:   { bg: 'rgba(248,113,113,0.12)', border: '#f87171', text: '#fca5a5', icon: '✕' },
    warning: { bg: 'rgba(251,191,36,0.12)', border: '#fbbf24', text: '#fcd34d', icon: '!' },
    info:    { bg: 'rgba(60,140,255,0.12)', border: '#4e9fff', text: '#6db3ff', icon: 'ℹ' }
  } : {
    success: { bg: '#e8ffea', border: '#00b42a', text: '#00b42a', icon: '✓' },
    error:   { bg: '#ffece8', border: '#f53f3f', text: '#f53f3f', icon: '✕' },
    warning: { bg: '#fff7e8', border: '#ff7d00', text: '#ff7d00', icon: '!' },
    info:    { bg: '#e8f3ff', border: '#165dff', text: '#165dff', icon: 'ℹ' }
  };
  const c = colors[type] || colors.info;
  const toast = document.createElement("div");
  toast.innerHTML = `<span style="display:inline-flex;align-items:center;justify-content:center;width:20px;height:20px;border-radius:50%;background:${c.border};color:#fff;font-size:12px;font-weight:700;margin-right:8px;flex-shrink:0;">${c.icon}</span><span>${message}</span>`;
  toast.style.cssText = `
    position:fixed;top:24px;left:50%;transform:translateX(-50%);z-index:9999;
    background:${c.bg};color:${c.text};padding:10px 20px;border-radius:8px;
    border:1px solid ${c.border}20;
    box-shadow:0 4px 16px rgba(0,0,0,0.1);font-size:14px;font-weight:500;
    display:flex;align-items:center;white-space:nowrap;pointer-events:none;
    animation:toastFadeIn 0.3s ease-out;
  `;
  document.body.appendChild(toast);
  setTimeout(() => {
    toast.style.animation = "toastFadeOut 0.3s ease-out";
    setTimeout(() => toast.remove(), 300);
  }, 3000);
}

// Animate stat value count-up (call after data loads)
function animateStatValue(el, targetValue, duration = 800) {
  const start = 0;
  const startTime = performance.now();
  const isFloat = String(targetValue).includes('.');
  function tick(now) {
    const elapsed = now - startTime;
    const progress = Math.min(elapsed / duration, 1);
    const eased = 1 - Math.pow(1 - progress, 3); // ease-out cubic
    const current = start + (targetValue - start) * eased;
    el.textContent = isFloat ? current.toFixed(1) : Math.round(current);
    if (progress < 1) requestAnimationFrame(tick);
  }
  requestAnimationFrame(tick);
}

// ---- Auto-inject close button into all modals ----
(function initModalCloseButtons() {
  const CLOSE_SVG = '<svg width="14" height="14" viewBox="0 0 14 14" fill="none"><path d="M1 1l12 12M13 1L1 13" stroke="currentColor" stroke-width="2" stroke-linecap="round"/></svg>';

  function injectCloseBtn(overlay) {
    const box = overlay.querySelector('.modal-box') || overlay.querySelector('.modal');
    if (!box || box.querySelector('.modal-close-btn')) return;
    const btn = document.createElement('button');
    btn.className = 'modal-close-btn';
    btn.title = '关闭';
    btn.innerHTML = CLOSE_SVG;
    btn.addEventListener('click', function(e) {
      e.stopPropagation();
      overlay.classList.remove('active');
      overlay.classList.remove('show');
    });
    box.insertBefore(btn, box.firstChild);
  }

  // Inject into existing modals on DOM ready
  function scanAll() {
    document.querySelectorAll('.modal-overlay').forEach(injectCloseBtn);
  }
  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', scanAll);
  } else {
    scanAll();
  }

  // Watch for dynamically added modals
  var mo = new MutationObserver(function(mutations) {
    mutations.forEach(function(m) {
      m.addedNodes.forEach(function(n) {
        if (n.nodeType !== 1) return;
        if (n.classList && n.classList.contains('modal-overlay')) injectCloseBtn(n);
        if (n.querySelectorAll) n.querySelectorAll('.modal-overlay').forEach(injectCloseBtn);
      });
    });
  });
  mo.observe(document.body || document.documentElement, { childList: true, subtree: true });
})();

// Stagger animate child elements on load
function staggerAnimateIn(parentSelector, childSelector, delay = 60) {
  const parent = document.querySelector(parentSelector);
  if (!parent) return;
  const children = parent.querySelectorAll(childSelector);
  children.forEach((child, i) => {
    child.style.opacity = '0';
    child.style.transform = 'translateY(12px)';
    child.style.transition = `opacity 0.4s ease-out ${i * delay}ms, transform 0.4s ease-out ${i * delay}ms`;
    requestAnimationFrame(() => {
      child.style.opacity = '1';
      child.style.transform = 'translateY(0)';
    });
  });
}
