// Common utilities for modules
const $ = (q) => document.querySelector(q);
const $$ = (q) => document.querySelectorAll(q);

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

// Show toast message (centered at top)
function showToast(message, type = "info") {
  const toast = document.createElement("div");
  toast.className = `toast toast-${type}`;
  toast.textContent = message;
  toast.style.cssText = `
    position: fixed; top: 60px; left: 50%; transform: translateX(-50%); z-index: 9999;
    background: ${type === "success" ? "#10b981" : type === "error" ? "#ef4444" : "#0ea5e9"};
    color: white; padding: 12px 24px; border-radius: 8px;
    box-shadow: 0 6px 20px rgba(0,0,0,0.2); font-size: 14px; font-weight: 500;
    white-space: nowrap; pointer-events: none;
    animation: toastFadeIn 0.3s ease-out;
  `;
  document.body.appendChild(toast);
  setTimeout(() => {
    toast.style.animation = "toastFadeOut 0.3s ease-out";
    setTimeout(() => toast.remove(), 300);
  }, 3000);
}
