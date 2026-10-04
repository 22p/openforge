/* OpenForge firmware selector and build client (ASU compatible). */
(function () {
  "use strict";

  const cfg = {
    allowDefaults: document.body.dataset.allowDefaults === "true",
    backendConfigured: document.body.dataset.backendConfigured === "true",
  };

  const state = {
    branches: [],
    branch: null,
    version: null,
    target: null,
    arch: null,
    profile: null,
    profileTitle: null,
    devicePackages: [],
    defaultPackages: [],
    defaultSet: [],
    packages: [],
    packageIndex: null,
    packageIndexPath: null,
    devices: [],
    building: false,
    pollTimer: null,
    versionSeq: 0,
    targetSeq: 0,
  };

  const $ = (id) => document.getElementById(id);

  /* --- boot --------------------------------------------------------------- */
  async function init() {
    wireStaticEvents();
    updateBuilderBadge();
    try {
      const [branches, overview] = await Promise.all([
        OF.api("/json/v1/branches.json"),
        OF.api("/json/v1/overview.json"),
      ]);
      state.branches = (branches || []).filter((b) => b.enabled);
      if (!state.branches.length) {
        throw new Error(window.I18n.t("error.generic"));
      }
      renderBranchChips();
      selectBranch(state.branches[0].name);
      loadQueue();
    } catch (err) {
      OF.toast(err.message || window.I18n.t("error.load"), "error");
    }
  }

  function updateBuilderBadge() {
    const badge = $("badge-builder");
    if (!badge) return;
    const label = $("builder-state");
    const key = cfg.backendConfigured ? "hero.online" : "hero.offline";
    label.setAttribute("data-i18n", key);
    label.textContent = window.I18n.t(key);
    if (cfg.backendConfigured) {
      badge.querySelector(".dot").style.background = "var(--success)";
    }
  }

  async function loadQueue() {
    if (!cfg.backendConfigured) return;
    try {
      const stats = await OF.api("/api/v1/stats");
      if (stats && typeof stats.queue_length === "number") {
        $("queue-value").textContent = stats.queue_length;
        $("badge-queue").classList.remove("hidden");
      }
    } catch (e) {
      /* Statistics are optional. */
    }
  }

  /* --- release selection -------------------------------------------------- */
  function renderBranchChips() {
    const wrap = $("branch-chips");
    wrap.innerHTML = "";
    state.branches.forEach((branch) => {
      const chip = OF.el("button", {
        class: "chip",
        type: "button",
        "aria-pressed": "false",
        text: branch.name === "SNAPSHOT" ? "SNAPSHOT" : branch.name,
      });
      chip.addEventListener("click", () => selectBranch(branch.name));
      chip.dataset.branch = branch.name;
      wrap.appendChild(chip);
    });
  }

  function selectBranch(name) {
    const branch = state.branches.find((b) => b.name === name);
    if (!branch) return;
    state.branch = branch;
    document.querySelectorAll("#branch-chips .chip").forEach((c) =>
      c.setAttribute("aria-pressed", c.dataset.branch === name ? "true" : "false")
    );

    const select = $("version-select");
    select.innerHTML = "";
    (branch.versions || []).forEach((version) => {
      select.appendChild(OF.el("option", { value: version, text: version }));
    });
    selectBranchVersion(branch.versions && branch.versions[0]);
  }

  function selectBranchVersion(version) {
    if (!version) return;
    $("version-select").value = version;
    onVersionChange(version);
  }

  async function onVersionChange(version) {
    state.version = version;
    state.target = null;
    state.profile = null;
    const seq = ++state.versionSeq;
    resetDevice();
    setStepEnabled("step-device", false);
    setStepEnabled("step-packages", false);
    updateSummary();

    const path = branchPath(state.branch, version);
    try {
      let targets = state.branch.targets;
      const isNewest = state.branch.versions[0] === version;
      if (!isNewest || !targets || !Object.keys(targets).length) {
        targets = await OF.api(`/json/v1/${path}/.targets.json`);
      }
      if (seq !== state.versionSeq) return; // A newer version was selected.
      populateTargets(targets || {});
      setStepEnabled("step-device", true);
    } catch (err) {
      if (seq === state.versionSeq) OF.toast(err.message || window.I18n.t("error.load"), "error");
    }
  }

  function populateTargets(targets) {
    const select = $("target-select");
    select.innerHTML = "";
    const entries = Object.entries(targets).sort((a, b) => a[0].localeCompare(b[0]));
    entries.forEach(([target, arch]) => {
      select.appendChild(OF.el("option", { value: target, text: `${target}  ·  ${arch}`, "data-arch": arch }));
    });
    state.targetsMap = targets;
    if (entries.length) {
      select.value = entries[0][0];
      onTargetChange(entries[0][0]);
    }
  }

  /* --- device selection --------------------------------------------------- */
  function resetDevice() {
    $("device-input").value = "";
    $("device-list").hidden = true;
    hideCombo($("device-list"));
    state.devices = [];
    state.defaultPackages = [];
    state.defaultSet = [];
    state.packages = [];
    state.devicePackages = [];
    state.packageIndex = null;
    state.packageIndexPath = null;
    renderTags();
    $("default-count").textContent = "—";
  }

  async function onTargetChange(target) {
    state.target = target;
    state.arch = state.targetsMap ? state.targetsMap[target] : null;
    state.profile = null;
    state.profileTitle = null;
    const seq = ++state.targetSeq;
    setStepEnabled("step-packages", false);
    hideCombo($("device-list"));
    $("device-input").value = "";
    updateSummary();

    const path = branchPath(state.branch, state.version);
    try {
      const raw = await OF.api(`/json/v1/${path}/targets/${target}/profiles.json`);
      if (seq !== state.targetSeq) return; // A newer target was selected.
      state.defaultPackages = raw.default_packages || [];
      state.devices = buildDevices(raw.profiles || {});
      state.packageIndex = null;
      state.packageIndexPath = null;

      // A target with a single profile (x86, armsr, …) can be selected
      // immediately, otherwise refresh any in-flight device search.
      if (state.devices.length === 1) {
        selectDevice(state.devices[0]);
      } else if (document.activeElement === $("device-input") || $("device-input").value) {
        renderDeviceList($("device-input").value);
      }
    } catch (err) {
      if (seq === state.targetSeq) {
        OF.toast(err.message || window.I18n.t("error.load"), "error");
      }
    }
  }

  function buildDevices(profiles) {
    const devices = [];
    for (const [profileId, entry] of Object.entries(profiles)) {
      const title = (entry.titles && entry.titles[0] && entry.titles[0].title) || profileId;
      const names = entry.supported_devices || [];
      devices.push({
        profile: profileId,
        title: title,
        aliases: names,
        devicePackages: entry.device_packages || [],
        search: (title + " " + profileId + " " + names.join(" ")).toLowerCase(),
      });
    }
    devices.sort((a, b) => a.title.localeCompare(b.title));
    return devices;
  }

  function renderDeviceList(query) {
    const list = $("device-list");
    const q = (query || "").trim().toLowerCase();
    let matches = state.devices;
    if (q) matches = state.devices.filter((d) => d.search.includes(q));
    const limited = matches.slice(0, 60);

    list.innerHTML = "";
    if (!limited.length) {
      list.appendChild(
        OF.el("div", { class: "combo-empty", text: window.I18n.t("field.device.empty") })
      );
    }
    limited.forEach((device) => {
      const sub = device.aliases.slice(0, 2).join(", ") || device.profile;
      const opt = OF.el("div", { class: "combo-option", role: "option" }, [
        OF.el("span", { text: device.title }),
        OF.el("span", { class: "opt-sub", text: sub }),
      ]);
      opt.addEventListener("mousedown", (e) => {
        e.preventDefault();
        selectDevice(device);
      });
      list.appendChild(opt);
    });
    list.hidden = false;
  }

  function selectDevice(device) {
    state.profile = device.profile;
    state.profileTitle = device.title;
    state.devicePackages = device.devicePackages || [];
    $("device-input").value = device.title;
    hideCombo($("device-list"));

    const defaults = dedupe([...state.defaultPackages, ...state.devicePackages]);
    state.defaultSet = defaults;
    state.packages = defaults.slice();
    $("default-count").textContent = String(defaults.length);
    renderTags();
    setStepEnabled("step-packages", true);
    updateSummary();
  }

  function hideCombo(list) {
    list.hidden = true;
  }

  /* --- packages ----------------------------------------------------------- */
  function renderTags() {
    const wrap = $("tags");
    const input = $("package-input");
    wrap.querySelectorAll(".tag").forEach((t) => t.remove());
    const defaultSet = new Set(state.defaultSet);
    state.packages.forEach((name, index) => {
      const remove = OF.el("button", {
        type: "button",
        text: "×",
        "aria-label": "remove " + name,
      });
      remove.addEventListener("click", () => {
        state.packages.splice(index, 1);
        renderTags();
        updateSummary();
      });
      const tag = OF.el("span", { class: "tag" + (defaultSet.has(name) ? "" : " added") }, [
        OF.el("span", { text: name }),
        remove,
      ]);
      wrap.insertBefore(tag, input);
    });
  }

  function addPackage(name) {
    name = (name || "").trim().replace(/,$/, "");
    if (!name) return;
    if (!state.packages.includes(name)) {
      state.packages.push(name);
      renderTags();
      updateSummary();
    }
  }

  async function ensurePackageIndex() {
    const path = branchPath(state.branch, state.version);
    if (state.packageIndex && state.packageIndexPath === path) return state.packageIndex;
    const index = {};
    try {
      const [base, arch] = await Promise.all([
        OF.api(`/json/v1/${path}/targets/${state.target}/index.json`).catch(() => null),
        state.arch
          ? OF.api(`/json/v1/${path}/packages/${state.arch}-index.json`).catch(() => null)
          : Promise.resolve(null),
      ]);
      if (base && base.packages) Object.assign(index, base.packages);
      else if (base) Object.assign(index, base);
      if (arch && typeof arch === "object") Object.assign(index, arch);
    } catch (e) {
      /* Suggestions are best-effort. */
    }
    state.packageIndex = index;
    state.packageIndexPath = path;
    return index;
  }

  async function renderPackageList(query) {
    const list = $("package-list");
    const index = await ensurePackageIndex();
    const q = (query || "").trim().toLowerCase();
    const selected = new Set(state.packages);
    let names = Object.keys(index);

    let matches;
    if (!q) {
      matches = names.slice(0, 40);
    } else {
      matches = names
        .filter((n) => n.toLowerCase().includes(q))
        .sort((a, b) => {
          const ai = a.toLowerCase().indexOf(q);
          const bi = b.toLowerCase().indexOf(q);
          return ai - bi || a.localeCompare(b);
        })
        .slice(0, 60);
    }

    list.innerHTML = "";
    if (!matches.length) {
      list.appendChild(
        OF.el("div", { class: "combo-empty", text: q ? window.I18n.t("field.packages.empty") : "" })
      );
    }
    matches.forEach((name) => {
      if (selected.has(name)) return;
      const version = index[name];
      const opt = OF.el("div", { class: "combo-option", role: "option" }, [
        OF.el("span", { class: "mono", text: name }),
        OF.el("span", { class: "opt-sub", text: version || "" }),
      ]);
      opt.addEventListener("mousedown", (e) => {
        e.preventDefault();
        addPackage(name);
        $("package-input").value = "";
        renderPackageList("");
      });
      list.appendChild(opt);
    });
    list.hidden = false;
  }

  /* --- advanced options --------------------------------------------------- */
  function toggleAdvanced() {
    const body = $("advanced-body");
    const btn = $("advanced-toggle");
    const open = body.classList.toggle("hidden") === false;
    btn.textContent = open ? "−" : "+";
  }

  function addRepoRow(name, url) {
    const row = OF.el("div", { class: "field-row", "data-repo-row": "" }, [
      OF.el("input", { class: "input", "data-repo-name": "", placeholder: window.I18n.t("field.repo.name") }),
      OF.el("input", {
        class: "input input-mono",
        "data-repo-url": "",
        placeholder: "https://…",
      }),
    ]);
    const remove = OF.el("button", { class: "btn btn-sm btn-ghost", type: "button", text: "×" });
    remove.addEventListener("click", () => row.remove());
    row.appendChild(remove);
    if (name) row.querySelector("[data-repo-name]").value = name;
    if (url) row.querySelector("[data-repo-url]").value = url;
    $("repos").appendChild(row);
  }

  function collectRepositories() {
    const repositories = {};
    document.querySelectorAll("[data-repo-row]").forEach((row) => {
      const name = row.querySelector("[data-repo-name]").value.trim();
      const url = row.querySelector("[data-repo-url]").value.trim();
      if (name && url) repositories[name] = url;
    });
    const keysRaw = $("repo-keys") ? $("repo-keys").value : "";
    const repository_keys = keysRaw
      .split(/\n\s*\n/)
      .map((k) => k.trim())
      .filter(Boolean);
    return { repositories, repository_keys };
  }

  /* --- summary ------------------------------------------------------------ */
  function updateSummary() {
    const summary = $("summary");
    const empty = $("summary-empty");
    const buildBtn = $("build-btn");

    if (!state.profile) {
      summary.classList.add("hidden");
      empty.classList.remove("hidden");
      buildBtn.disabled = true;
      return;
    }
    summary.classList.remove("hidden");
    empty.classList.add("hidden");
    buildBtn.disabled = state.building;

    const rows = [];
    rows.push([window.I18n.t("summary.release"), state.branch.name + " · " + state.version]);
    rows.push([window.I18n.t("summary.target"), state.target]);
    rows.push([window.I18n.t("summary.profile"), state.profileTitle || state.profile]);

    const diff = $("diff-packages").checked;
    const pkgLabel = diff
      ? window.I18n.t("summary.packages.count", { n: state.packages.length })
      : window.I18n.t("summary.packages.default");
    rows.push([window.I18n.t("summary.packages"), pkgLabel]);

    const rootfs = $("rootfs-size").value.trim();
    if (rootfs) rows.push([window.I18n.t("summary.rootfs"), rootfs + " MB"]);
    const fs = $("filesystem").value;
    if (fs) rows.push([window.I18n.t("summary.filesystem"), fs]);
    const { repositories } = collectRepositories();
    const repoCount = Object.keys(repositories).length;
    if (repoCount) rows.push([window.I18n.t("summary.repos"), String(repoCount)]);

    summary.innerHTML = "";
    rows.forEach(([k, v]) => {
      summary.appendChild(
        OF.el("div", { class: "summary-item" }, [
          OF.el("span", { class: "k", text: k }),
          OF.el("span", { class: "v", text: v }),
        ])
      );
    });
  }

  /* --- build -------------------------------------------------------------- */
  async function startBuild() {
    if (!state.profile || state.building) return;
    if (!cfg.backendConfigured) {
      OF.toast(window.I18n.t("error.backend"), "error");
      return;
    }
    clearResult();
    state.building = true;
    $("build-btn").disabled = true;
    $("build-btn").querySelector("span").textContent = window.I18n.t("build.button.busy");
    setProgress(0, true);
    showStatus(window.I18n.t("progress.queued"), true);

    const diff = $("diff-packages").checked;
    const { repositories, repository_keys } = collectRepositories();

    const payload = {
      version: state.version,
      target: state.target,
      profile: state.profile,
      packages: state.packages.slice(),
      diff_packages: diff,
      client: "openforge-web/1",
    };
    const rootfs = parseInt($("rootfs-size").value, 10);
    if (rootfs > 0) payload.rootfs_size_mb = rootfs;
    if ($("filesystem").value) payload.filesystem = $("filesystem").value;
    if (cfg.allowDefaults && $("defaults") && $("defaults").value) {
      payload.defaults = $("defaults").value;
    }
    if (Object.keys(repositories).length) {
      payload.repositories = repositories;
      payload.repositories_mode = "append";
      if (repository_keys.length) payload.repository_keys = repository_keys;
    }

    try {
      const data = await OF.api("/api/v1/build", { method: "POST", body: payload });
      handleBuildResponse(data);
    } catch (err) {
      failBuild(err.message || window.I18n.t("error.generic"), err.data);
    }
  }

  function handleBuildResponse(data) {
    if (!data) {
      failBuild(window.I18n.t("error.generic"));
      return;
    }
    const status = data.status || 200;
    if (status === 200 && (data.detail === "done" || data.bin_dir)) {
      finishBuild(data);
    } else if (status === 202) {
      const position = data.queue_position || 0;
      showStatus(
        (data.detail === "queued" ? window.I18n.t("progress.queued") : window.I18n.t("progress.started")) +
          (position ? " · " + window.I18n.t("progress.position", { n: position }) : ""),
        true
      );
      setProgress(estimate(data), data.detail === "queued");
      poll(data.request_hash);
    } else if (status >= 400 || data.detail === "failed") {
      failBuild(data.detail || data.stderr || window.I18n.t("error.generic"), data);
    } else {
      finishBuild(data);
    }
  }

  function estimate(data) {
    const map = {
      init: 12,
      container_setup: 20,
      validate_revision: 32,
      validate_manifest: 48,
      building_image: 72,
      done: 100,
    };
    return map[data.imagebuilder_status] != null ? map[data.imagebuilder_status] : 30;
  }

  function poll(hash) {
    clearTimeout(state.pollTimer);
    state.pollTimer = setTimeout(() => pollOnce(hash), 3000);
  }

  async function pollOnce(hash) {
    if (!state.building) return;
    try {
      const data = await OF.api(`/api/v1/build/${hash}`);
      const status = data.status || 200;
      if (status === 200 && (data.detail === "done" || data.bin_dir)) {
        finishBuild(data);
        return;
      }
      if (status >= 400 || data.detail === "failed") {
        failBuild(data.detail || data.stderr || window.I18n.t("error.generic"), data);
        return;
      }
      const position = data.queue_position || 0;
      showStatus(
        (data.detail === "queued" ? window.I18n.t("progress.queued") : window.I18n.t("progress.started")) +
          (position ? " · " + window.I18n.t("progress.position", { n: position }) : ""),
        true
      );
      setProgress(estimate(data), data.detail === "queued");
      state.pollTimer = setTimeout(() => pollOnce(hash), 4000);
    } catch (err) {
      // Transient error: keep polling unless the job is gone.
      if (err.status === 404) {
        failBuild(err.message, err.data);
        return;
      }
      state.pollTimer = setTimeout(() => pollOnce(hash), 5000);
    }
  }

  function finishBuild(data) {
    state.building = false;
    clearTimeout(state.pollTimer);
    setProgress(100, false);
    hideStatus();
    resetBuildButton();
    renderResult(data);
    OF.toast(window.I18n.t("progress.done"), "success");
  }

  function failBuild(message, data) {
    state.building = false;
    clearTimeout(state.pollTimer);
    hideStatus();
    resetBuildButton();
    const area = $("result-area");
    area.classList.remove("hidden");
    area.innerHTML = "";
    area.appendChild(OF.el("div", { class: "alert alert-error" }, [OF.el("span", { text: message })]));
    if (data && (data.stderr || (data.stdout && data.detail === "failed"))) {
      const pre = OF.el("pre", {
        class: "mono",
        style: "max-height:240px;overflow:auto;white-space:pre-wrap;margin-top:10px;font-size:12px",
        text: (data.stderr || "") + (data.stdout ? "\n" + data.stdout : ""),
      });
      const details = OF.el("details", { style: "margin-top:10px" }, [
        OF.el("summary", { text: window.I18n.t("error.title") }),
        pre,
      ]);
      area.appendChild(details);
    }
  }

  function renderResult(data) {
    const area = $("result-area");
    area.classList.remove("hidden");
    area.innerHTML = "";

    const header = OF.el("div", { class: "row-between" }, [
      OF.el("h3", { text: window.I18n.t("result.title") }),
      OF.el("span", { class: "badge badge-success" }, [
        OF.el("span", { class: "dot" }),
        OF.el("span", { text: window.I18n.t("progress.done") }),
      ]),
    ]);
    area.appendChild(header);

    if (data.request_hash) {
      area.appendChild(
        OF.el("div", { class: "summary-item", style: "margin-top:10px" }, [
          OF.el("span", { class: "k", text: window.I18n.t("result.hash") }),
          OF.el("span", { class: "v mono", text: data.request_hash.slice(0, 16) + "…" }),
        ])
      );
    }

    const images = data.images || [];
    const binDir = data.bin_dir || data.request_hash || "";
    const preferred = images.filter((i) =>
      ["sysupgrade", "factory", "combined", "combined-efi", "sdcard"].includes(i.type)
    );
    const list = preferred.length ? preferred : images;
    const container = OF.el("div", { style: "margin-top:12px" });
    list.forEach((image) => container.appendChild(renderArtifact(image, binDir)));
    if (!list.length) {
      container.appendChild(
        OF.el("p", { class: "muted", text: window.I18n.t("stats.empty") })
      );
    }
    area.appendChild(container);

    if (data.manifest) {
      const manifest = data.manifest;
      let names = Array.isArray(manifest) ? manifest : Object.keys(manifest);
      const details = OF.el("details", { style: "margin-top:12px" }, [
        OF.el("summary", {
          text: window.I18n.t("result.manifest") + " (" + names.length + ")",
        }),
        OF.el("div", {
          class: "mono",
          style: "margin-top:8px;max-height:200px;overflow:auto;font-size:12px;color:var(--text-muted)",
          text: names.sort().join(", "),
        }),
      ]);
      area.appendChild(details);
    }

    const again = OF.el("button", {
      class: "btn btn-block",
      type: "button",
      text: window.I18n.t("result.rebuild"),
      style: "margin-top:14px",
    });
    again.addEventListener("click", () => {
      clearResult();
      window.scrollTo({ top: 0, behavior: "smooth" });
    });
    area.appendChild(again);
  }

  function renderArtifact(image, binDir) {
    const badgeClass =
      image.type === "sysupgrade" ? "badge-accent" : image.type === "factory" ? "badge-success" : "";
    const badgeText =
      image.type === "sysupgrade"
        ? window.I18n.t("result.sysupgrade")
        : image.type === "factory"
        ? window.I18n.t("result.factory")
        : image.type;

    const meta = [image.filesystem, image.sha256 ? image.sha256.slice(0, 16) + "…" : ""].filter(Boolean);
    const info = OF.el("div", { class: "grow" }, [
      OF.el("div", { class: "a-name", text: image.name }),
      OF.el("div", { class: "a-meta" }, [
        badgeText ? OF.el("span", { class: "badge " + badgeClass, text: badgeText }) : null,
        ...meta.map((m) => OF.el("span", { class: "mono", text: m })),
      ]),
    ]);

    const link = OF.el("a", {
      class: "btn btn-sm btn-primary",
      href: `/store/${binDir}/${image.name}`,
      text: window.I18n.t("result.download"),
    });
    link.setAttribute("download", "");

    const copyBtn = OF.el("button", {
      class: "btn btn-sm btn-ghost",
      type: "button",
      text: window.I18n.t("result.copy"),
      title: image.sha256 || "",
    });
    copyBtn.addEventListener("click", async () => {
      if (await OF.copy(image.sha256 || "")) {
        copyBtn.textContent = window.I18n.t("result.copied");
        setTimeout(() => (copyBtn.textContent = window.I18n.t("result.copy")), 1500);
      }
    });

    return OF.el("div", { class: "artifact" }, [info, OF.el("div", { class: "a-actions row" }, [copyBtn, link])]);
  }

  /* --- status helpers ----------------------------------------------------- */
  function showStatus(text, spinner) {
    const el = $("build-status");
    el.classList.remove("hidden");
    el.querySelector(".spinner").style.display = spinner ? "" : "none";
    $("build-status-text").textContent = text;
  }

  function hideStatus() {
    $("build-status").classList.add("hidden");
  }

  function setProgress(percent, indeterminate) {
    $("progress-wrap").classList.remove("hidden");
    const bar = $("progress-bar");
    if (indeterminate) {
      bar.classList.add("indeterminate");
      bar.style.width = "";
    } else {
      bar.classList.remove("indeterminate");
      bar.style.width = Math.max(2, Math.min(100, percent)) + "%";
    }
  }

  function resetBuildButton() {
    const btn = $("build-btn");
    btn.disabled = false;
    btn.querySelector("span").textContent = window.I18n.t("build.button");
    updateSummary();
  }

  function clearResult() {
    $("result-area").classList.add("hidden");
    $("result-area").innerHTML = "";
    $("progress-wrap").classList.add("hidden");
    $("progress-bar").style.width = "0%";
    hideStatus();
  }

  function setStepEnabled(id, enabled) {
    const el = $(id);
    if (el) el.classList.toggle("is-disabled", !enabled);
  }

  /* --- helpers ------------------------------------------------------------ */
  function branchPath(branch, version) {
    return (branch.path || "releases/{version}").replace("{version}", version);
  }

  function dedupe(list) {
    const seen = new Set();
    const out = [];
    list.forEach((item) => {
      if (item && !seen.has(item)) {
        seen.add(item);
        out.push(item);
      }
    });
    return out;
  }

  /* --- events ------------------------------------------------------------- */
  function wireStaticEvents() {
    $("version-select").addEventListener("change", (e) => onVersionChange(e.target.value));
    $("target-select").addEventListener("change", (e) => onTargetChange(e.target.value));
    $("advanced-toggle").addEventListener("click", toggleAdvanced);
    $("add-repo").addEventListener("click", () => addRepoRow());
    $("build-btn").addEventListener("click", startBuild);

    const deviceInput = $("device-input");
    deviceInput.addEventListener("focus", () => renderDeviceList(deviceInput.value));
    deviceInput.addEventListener("input", OF.debounce(() => renderDeviceList(deviceInput.value), 120));
    deviceInput.addEventListener("blur", () => setTimeout(() => hideCombo($("device-list")), 150));

    const pkgInput = $("package-input");
    pkgInput.addEventListener("focus", () => renderPackageList(pkgInput.value));
    pkgInput.addEventListener("input", OF.debounce(() => renderPackageList(pkgInput.value), 150));
    pkgInput.addEventListener("blur", () => setTimeout(() => hideCombo($("package-list")), 150));
    pkgInput.addEventListener("keydown", (e) => {
      if (e.key === "Enter" || e.key === ",") {
        e.preventDefault();
        addPackage(pkgInput.value);
        pkgInput.value = "";
        renderPackageList("");
      } else if (e.key === "Backspace" && !pkgInput.value && state.packages.length) {
        state.packages.pop();
        renderTags();
        updateSummary();
      }
    });

    ["rootfs-size", "filesystem"].forEach((id) =>
      $(id).addEventListener("change", updateSummary)
    );
    $("diff-packages").addEventListener("change", updateSummary);

    document.addEventListener("langchange", () => {
      renderTags();
      updateSummary();
      if (!state.profile) return;
      // Refresh placeholder-ish dynamic text.
      if ($("build-status") && !$("build-status").classList.contains("hidden")) {
        // leave progress text as-is
      }
    });
  }

  document.addEventListener("DOMContentLoaded", init);
})();
