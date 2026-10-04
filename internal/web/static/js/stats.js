/* OpenForge statistics page. */
(function () {
  "use strict";

  const t = (k) => window.I18n.t(k);
  let perDay = null;

  async function init() {
    try {
      const summary = await OF.api("/api/v1/stats/summary").catch(() => null);
      if (summary) {
        setText("stat-queue", summary.queue_length != null ? summary.queue_length : "—");
        setText("stat-builds24h", summary.builds_24h != null ? summary.builds_24h : "—");
      }
    } catch (e) {}

    OF.api("/api/v1/builds-per-day")
      .then((data) => {
        perDay = data;
        if (data && data.datasets && data.datasets.length) {
          drawLineChart(document.getElementById("chart-per-day"), data.labels, data.datasets);
          renderLegend(data.datasets);
        }
      })
      .catch(() => {});

    OF.api("/api/v1/builds-by-version")
      .then((data) => renderVersionBars(data))
      .catch(() => {});

    OF.api("/api/v1/top-packages?n=20")
      .then((data) => renderPackageBars(data))
      .catch(() => {});

    fetch("/api/v1/build-errors")
      .then((r) => r.text())
      .then((text) => setText("build-errors", text || t("stats.empty")))
      .catch(() => setText("build-errors", t("stats.empty")));

    window.addEventListener("resize", OF.debounce(redraw, 150));
    document.addEventListener("langchange", () => {
      if (perDay) {
        renderLegend(perDay.datasets);
        drawLineChart(document.getElementById("chart-per-day"), perDay.labels, perDay.datasets);
      }
    });
    new MutationObserver(redraw).observe(document.documentElement, {
      attributes: true,
      attributeFilter: ["data-theme"],
    });
  }

  function redraw() {
    if (perDay) drawLineChart(document.getElementById("chart-per-day"), perDay.labels, perDay.datasets);
  }

  function setText(id, value) {
    const el = document.getElementById(id);
    if (el) el.textContent = value;
  }

  function renderLegend(datasets) {
    const wrap = document.getElementById("legend-per-day");
    wrap.innerHTML = "";
    datasets.forEach((dataset, i) => {
      const color = seriesColor(i);
      const dot = OF.el("span", {
        style: `width:9px;height:9px;border-radius:999px;display:inline-block;background:${color}`,
      });
      wrap.appendChild(OF.el("span", { class: "row", style: "gap:7px;font-size:13px" }, [
        dot,
        OF.el("span", { text: dataset.label }),
      ]));
    });
  }

  function seriesColor(index) {
    const vars = ["--accent", "--success", "--danger", "--warning"];
    return cssVar(vars[index % vars.length]) || "#2f6fec";
  }

  function cssVar(name) {
    return getComputedStyle(document.documentElement).getPropertyValue(name).trim();
  }

  function renderVersionBars(data) {
    const wrap = document.getElementById("bars-by-version");
    wrap.innerHTML = "";
    if (!data || !data.datasets || !data.datasets.length) {
      wrap.appendChild(OF.el("p", { class: "muted", text: t("stats.empty") }));
      return;
    }
    const rows = data.datasets
      .map((d) => ({ label: d.label, total: (d.data || []).reduce((a, b) => a + (b || 0), 0) }))
      .filter((r) => r.total > 0)
      .sort((a, b) => b.total - a.total);
    if (!rows.length) {
      wrap.appendChild(OF.el("p", { class: "muted", text: t("stats.empty") }));
      return;
    }
    renderBars(wrap, rows);
  }

  function renderPackageBars(data) {
    const wrap = document.getElementById("bars-top-packages");
    wrap.innerHTML = "";
    if (!data || !data.packages || !data.packages.length) {
      wrap.appendChild(OF.el("p", { class: "muted", text: t("stats.empty") }));
      return;
    }
    renderBars(
      wrap,
      data.packages.map((p) => ({ label: p.name, total: p.count }))
    );
  }

  function renderBars(wrap, rows) {
    const max = Math.max(1, ...rows.map((r) => r.total));
    rows.forEach((row) => {
      const fill = OF.el("div", { class: "bar-fill", style: `width:${(row.total / max) * 100}%` });
      wrap.appendChild(
        OF.el("div", { class: "bar-row" }, [
          OF.el("span", { class: "mono", style: "overflow:hidden;text-overflow:ellipsis", text: row.label, title: row.label }),
          OF.el("div", { class: "bar-track" }, [fill]),
          OF.el("span", { class: "bar-count", text: String(row.total) }),
        ])
      );
    });
  }

  /* --- canvas line chart -------------------------------------------------- */
  function drawLineChart(canvas, labels, datasets) {
    if (!canvas || !datasets) return;
    const dpr = window.devicePixelRatio || 1;
    const w = Math.max(320, canvas.clientWidth || 600);
    const h = canvas.clientHeight || 220;
    canvas.width = Math.round(w * dpr);
    canvas.height = Math.round(h * dpr);
    const ctx = canvas.getContext("2d");
    ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
    ctx.clearRect(0, 0, w, h);

    const pad = { l: 38, r: 12, t: 12, b: 26 };
    const plotW = w - pad.l - pad.r;
    const plotH = h - pad.t - pad.b;
    const border = cssVar("--border") || "#e4e7ec";
    const faint = cssVar("--text-faint") || "#8a93a0";

    const all = datasets.flatMap((d) => d.data || []).map((v) => v || 0);
    const maxVal = Math.max(1, ...all);
    const niceMax = niceCeil(maxVal);

    ctx.strokeStyle = border;
    ctx.fillStyle = faint;
    ctx.lineWidth = 1;
    ctx.font = "11px ui-monospace, monospace";

    const gridLines = 4;
    for (let i = 0; i <= gridLines; i++) {
      const y = pad.t + (plotH * i) / gridLines;
      ctx.beginPath();
      ctx.moveTo(pad.l, y);
      ctx.lineTo(pad.l + plotW, y);
      ctx.globalAlpha = 0.6;
      ctx.stroke();
      ctx.globalAlpha = 1;
      const value = Math.round(niceMax - (niceMax * i) / gridLines);
      ctx.textAlign = "right";
      ctx.textBaseline = "middle";
      ctx.fillText(String(value), pad.l - 8, y);
    }

    const n = labels ? labels.length : 0;
    const xFor = (i) => (n <= 1 ? pad.l + plotW / 2 : pad.l + (plotW * i) / (n - 1));
    const yFor = (v) => pad.t + plotH - (plotH * (v || 0)) / niceMax;

    datasets.forEach((dataset, di) => {
      const color = seriesColor(di);
      ctx.strokeStyle = color;
      ctx.lineWidth = 2;
      ctx.beginPath();
      (dataset.data || []).forEach((v, i) => {
        const x = xFor(i);
        const y = yFor(v);
        if (i === 0) ctx.moveTo(x, y);
        else ctx.lineTo(x, y);
      });
      ctx.stroke();
    });

    // x labels (a handful)
    ctx.fillStyle = faint;
    ctx.textAlign = "center";
    ctx.textBaseline = "top";
    const step = Math.max(1, Math.ceil(n / 7));
    for (let i = 0; i < n; i += step) {
      const label = (labels[i] || "").slice(5, 10);
      ctx.fillText(label, xFor(i), pad.t + plotH + 7);
    }
  }

  function niceCeil(v) {
    if (v <= 5) return 5;
    const mag = Math.pow(10, Math.floor(Math.log10(v)));
    const norm = v / mag;
    let nice;
    if (norm <= 1) nice = 1;
    else if (norm <= 2) nice = 2;
    else if (norm <= 5) nice = 5;
    else nice = 10;
    return nice * mag;
  }

  document.addEventListener("DOMContentLoaded", init);
})();
