/* OpenForge shared frontend utilities: theme, header, API client, toasts. */
(function () {
  "use strict";

  const OF = {};

  /* --- theme ------------------------------------------------------------- */
  const THEME_KEY = "openforge.theme";

  function preferredTheme() {
    const current = document.documentElement.getAttribute("data-theme");
    if (current === "light" || current === "dark") return current;
    const saved = localStorage.getItem(THEME_KEY);
    if (saved === "light" || saved === "dark") return saved;
    return window.matchMedia("(prefers-color-scheme: dark)").matches ? "dark" : "light";
  }

  function applyTheme(theme) {
    document.documentElement.setAttribute("data-theme", theme);
    document.querySelectorAll("[data-theme-toggle]").forEach((btn) => {
      btn.innerHTML = theme === "dark" ? iconSun() : iconMoon();
      btn.setAttribute("aria-pressed", theme === "dark" ? "true" : "false");
    });
  }

  function iconSun() {
    return '<svg width="17" height="17" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="12" r="4"/><path d="M12 2v2M12 20v2M4.9 4.9l1.4 1.4M17.7 17.7l1.4 1.4M2 12h2M20 12h2M4.9 19.1l1.4-1.4M17.7 6.3l1.4-1.4"/></svg>';
  }
  function iconMoon() {
    return '<svg width="17" height="17" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M21 12.8A9 9 0 1 1 11.2 3a7 7 0 0 0 9.8 9.8z"/></svg>';
  }

  OF.initTheme = function () {
    applyTheme(preferredTheme());
    document.querySelectorAll("[data-theme-toggle]").forEach((btn) => {
      btn.addEventListener("click", () => {
        const current = document.documentElement.getAttribute("data-theme");
        const next = current === "dark" ? "light" : "dark";
        localStorage.setItem(THEME_KEY, next);
        applyTheme(next);
      });
    });
  };

  /* --- language switcher ------------------------------------------------- */
  OF.initLang = function () {
    const wrap = document.querySelector("[data-lang-switch]");
    if (!wrap) return;
    const buttons = wrap.querySelectorAll("button[data-lang]");
    function sync() {
      buttons.forEach((b) =>
        b.setAttribute("aria-pressed", b.dataset.lang === window.I18n.lang ? "true" : "false")
      );
    }
    buttons.forEach((b) =>
      b.addEventListener("click", () => {
        window.I18n.setLang(b.dataset.lang);
        sync();
      })
    );
    document.addEventListener("langchange", sync);
    sync();
  };

  /* --- API client -------------------------------------------------------- */
  OF.api = async function (path, options) {
    const opts = Object.assign({ headers: {} }, options || {});
    if (opts.body && typeof opts.body !== "string") {
      opts.headers["Content-Type"] = "application/json";
      opts.body = JSON.stringify(opts.body);
    }
    const res = await fetch(path, opts);
    const text = await res.text();
    let data = null;
    if (text) {
      try {
        data = JSON.parse(text);
      } catch (e) {
        data = text;
      }
    }
    if (!res.ok) {
      const detail =
        data && typeof data === "object" && data.detail
          ? normalizeDetail(data.detail)
          : (typeof data === "string" && data) || res.statusText;
      const err = new Error(detail || "HTTP " + res.status);
      err.status = res.status;
      err.data = data;
      throw err;
    }
    return data;
  };

  function normalizeDetail(detail) {
    if (typeof detail === "string") return detail;
    if (Array.isArray(detail)) {
      return detail
        .map((d) => (typeof d === "string" ? d : d.msg || JSON.stringify(d)))
        .join("; ");
    }
    if (detail && typeof detail === "object") return detail.msg || JSON.stringify(detail);
    return String(detail);
  }

  /* --- toasts ------------------------------------------------------------ */
  OF.toast = function (message, type) {
    let wrap = document.querySelector(".toast-wrap");
    if (!wrap) {
      wrap = document.createElement("div");
      wrap.className = "toast-wrap";
      document.body.appendChild(wrap);
    }
    const el = document.createElement("div");
    el.className = "toast" + (type ? " " + type : "");
    el.textContent = message;
    wrap.appendChild(el);
    setTimeout(() => {
      el.style.transition = "opacity .3s ease";
      el.style.opacity = "0";
      setTimeout(() => el.remove(), 320);
    }, 4200);
  };

  /* --- misc -------------------------------------------------------------- */
  OF.copy = async function (text) {
    try {
      await navigator.clipboard.writeText(text);
      return true;
    } catch (e) {
      const ta = document.createElement("textarea");
      ta.value = text;
      ta.style.position = "fixed";
      ta.style.opacity = "0";
      document.body.appendChild(ta);
      ta.select();
      let ok = false;
      try {
        ok = document.execCommand("copy");
      } catch (_) {
        ok = false;
      }
      ta.remove();
      return ok;
    }
  };

  OF.el = function (tag, attrs, children) {
    const node = document.createElement(tag);
    if (attrs) {
      for (const k in attrs) {
        if (k === "class") node.className = attrs[k];
        else if (k === "text") node.textContent = attrs[k];
        else if (k === "html") node.innerHTML = attrs[k];
        else if (k.startsWith("on") && typeof attrs[k] === "function")
          node.addEventListener(k.slice(2), attrs[k]);
        else if (attrs[k] != null && attrs[k] !== false) node.setAttribute(k, attrs[k]);
      }
    }
    (children || []).forEach((c) => c && node.appendChild(c));
    return node;
  };

  OF.debounce = function (fn, wait) {
    let t;
    return function () {
      const args = arguments;
      clearTimeout(t);
      t = setTimeout(() => fn.apply(this, args), wait);
    };
  };

  OF.init = function () {
    OF.initTheme();
    OF.initLang();
    window.I18n.apply(document);
  };

  window.OF = OF;
  document.addEventListener("DOMContentLoaded", OF.init);
})();
