// Common utilities for modules
const $ = (q) => document.querySelector(q);
const $$ = (q) => document.querySelectorAll(q);

// ---- Tianditu Map Utilities ----
let TIANDITU_KEY = '90aa13159977757fb5d9061bf4d8c22b';

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
async function api(path, options = {}) {
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
}

// Format date
function formatDate(dateStr) {
  if (!dateStr) return "-";
  const d = new Date(dateStr);
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

// Create chart (using Chart.js)
function createChart(canvas, config) {
  if (typeof Chart === "undefined") {
    console.warn("Chart.js not loaded");
    return null;
  }
  return new Chart(canvas, config);
}

// Show toast message (Arco-style notification at top)
function showToast(message, type = "info") {
  const colors = {
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
