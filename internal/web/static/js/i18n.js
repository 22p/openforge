/* OpenForge i18n - lightweight translation layer (English / 简体中文). */
(function () {
  "use strict";

  const dict = {
    en: {
      "brand.sub": "Firmware Builder",
      "nav.builder": "Builder",
      "nav.stats": "Statistics",
      "nav.api": "API",
      "theme.toggle": "Toggle theme",

      "hero.title": "Build OpenWrt firmware in your browser",
      "hero.lead": "Pick a release and device, customize the package set, then download a ready-to-flash image. Powered by the Attended Sysupgrade API.",
      "hero.server": "Server",
      "hero.builder": "Builder",
      "hero.queue": "Queue",
      "hero.online": "online",
      "hero.offline": "unconfigured",
      "hero.docs": "Read the API",

      "step.release.title": "Release",
      "step.release.desc": "Choose the OpenWrt version to build.",
      "step.device.title": "Device",
      "step.device.desc": "Pick a target and your exact device model.",
      "step.packages.title": "Packages",
      "step.packages.desc": "Add or replace packages included in the image.",
      "step.advanced.title": "Advanced options",
      "step.advanced.desc": "Root filesystem, image format and first-boot script.",

      "field.branch": "Branch",
      "field.version": "Version",
      "field.target": "Target",
      "field.device": "Device",
      "field.device.placeholder": "Search your device model…",
      "field.device.empty": "No matching device.",
      "field.packages.placeholder": "Type a package name and press Enter",
      "field.packages.empty": "No matching package.",
      "field.packages.default": "Default packages",
      "field.diff": "Send the package list as-is (absolute list)",
      "field.diff.hint": "When enabled the list above replaces the default package set. Disable it to treat the list as extra packages on top of the defaults.",
      "field.rootfs": "Root filesystem size (MB)",
      "field.rootfs.hint": "Leave empty to use the ImageBuilder default. Maximum {n} MB.",
      "field.filesystem": "Image filesystem",
      "field.filesystem.auto": "ImageBuilder default",
      "field.defaults": "First-boot script (UCI defaults)",
      "field.defaults.hint": "Maximum {n} bytes. Executed on the device's first boot.",
      "field.repositories": "Custom repositories",
      "field.repositories.hint": "Only repositories whose URL prefix is on the server allow-list are accepted.",
      "field.repo.name": "Name",
      "field.repo.keys": "Signing keys (optional)",
      "field.repo.add": "Add repository",

      "summary.title": "Build summary",
      "summary.empty": "Select a device to begin.",
      "summary.release": "Release",
      "summary.target": "Target",
      "summary.profile": "Profile",
      "summary.packages": "Packages",
      "summary.packages.count": "{n} selected",
      "summary.packages.default": "default set",
      "summary.rootfs": "Rootfs",
      "summary.filesystem": "Filesystem",
      "summary.repos": "Repositories",
      "build.button": "Build firmware",
      "build.button.busy": "Building…",

      "progress.queued": "Queued",
      "progress.started": "Building image",
      "progress.done": "Completed",
      "progress.position": "Queue position {n}",
      "progress.hint": "A custom image usually takes a few minutes to build.",

      "result.title": "Your images are ready",
      "result.download": "Download",
      "result.copy": "Copy",
      "result.copied": "Copied",
      "result.rebuild": "Build another image",
      "result.sysupgrade": "Sysupgrade",
      "result.factory": "Factory",
      "result.manifest": "Installed packages",
      "result.hash": "Request hash",

      "error.title": "Build failed",
      "error.generic": "Something went wrong.",
      "error.backend": "The build backend is unavailable. Check the server configuration.",
      "error.load": "Failed to load data.",


      "footer.overview": "Server overview",
      "footer.stats": "Statistics",
      "footer.source": "Source code",

      "api.title": "API",
      "api.lead": "OpenForge speaks the Attended Sysupgrade API. Point owut, LuCI or the OpenWrt firmware selector at this server.",

      "stats.title": "Statistics",
      "stats.lead": "Build activity reported by the connected ASU backend.",
      "stats.queue": "Queue length",
      "stats.builds24h": "Builds (24h)",
      "stats.perDay": "Builds per day",
      "stats.byVersion": "Builds by version",
      "stats.topPackages": "Top packages",
      "stats.errors": "Recent build errors",
      "stats.empty": "No data available.",
    },

    zh: {
      "brand.sub": "固件编译",
      "nav.builder": "固件编译",
      "nav.stats": "统计",
      "nav.api": "API",
      "theme.toggle": "切换主题",

      "hero.title": "在浏览器中编译 OpenWrt 固件",
      "hero.lead": "选择版本与设备，定制软件包，然后下载可直接刷写的镜像。基于 Attended Sysupgrade API。",
      "hero.server": "服务端",
      "hero.builder": "编译后端",
      "hero.queue": "队列",
      "hero.online": "在线",
      "hero.offline": "未配置",
      "hero.docs": "查看 API",

      "step.release.title": "系统版本",
      "step.release.desc": "选择要编译的 OpenWrt 版本。",
      "step.device.title": "目标设备",
      "step.device.desc": "选择目标平台与具体设备型号。",
      "step.packages.title": "软件包",
      "step.packages.desc": "添加或替换镜像中包含的软件包。",
      "step.advanced.title": "高级选项",
      "step.advanced.desc": "根文件系统、镜像格式与首次启动脚本。",

      "field.branch": "分支",
      "field.version": "版本",
      "field.target": "目标平台",
      "field.device": "设备",
      "field.device.placeholder": "搜索设备型号…",
      "field.device.empty": "未找到匹配的设备。",
      "field.packages.placeholder": "输入软件包名称后回车",
      "field.packages.empty": "未找到匹配的软件包。",
      "field.packages.default": "默认软件包",
      "field.diff": "按当前列表编译（绝对列表）",
      "field.diff.hint": "启用后上述列表将替换默认软件包集合；关闭则将其视为默认包之外的附加软件包。",
      "field.rootfs": "根文件系统大小 (MB)",
      "field.rootfs.hint": "留空使用 ImageBuilder 默认值。最大 {n} MB。",
      "field.filesystem": "镜像文件系统",
      "field.filesystem.auto": "ImageBuilder 默认",
      "field.defaults": "首次启动脚本 (UCI defaults)",
      "field.defaults.hint": "最大 {n} 字节，设备首次启动时执行。",
      "field.repositories": "自定义软件源",
      "field.repositories.hint": "仅接受地址前缀位于服务端白名单中的软件源。",
      "field.repo.name": "名称",
      "field.repo.keys": "签名密钥（可选）",
      "field.repo.add": "添加软件源",

      "summary.title": "编译摘要",
      "summary.empty": "请先选择设备。",
      "summary.release": "版本",
      "summary.target": "平台",
      "summary.profile": "配置",
      "summary.packages": "软件包",
      "summary.packages.count": "已选 {n} 个",
      "summary.packages.default": "默认集合",
      "summary.rootfs": "根分区",
      "summary.filesystem": "文件系统",
      "summary.repos": "软件源",
      "build.button": "开始编译",
      "build.button.busy": "编译中…",

      "progress.queued": "排队中",
      "progress.started": "正在编译镜像",
      "progress.done": "已完成",
      "progress.position": "队列位置 {n}",
      "progress.hint": "自定义镜像通常需要几分钟完成编译。",

      "result.title": "固件已生成",
      "result.download": "下载",
      "result.copy": "复制",
      "result.copied": "已复制",
      "result.rebuild": "再编译一个镜像",
      "result.sysupgrade": "系统升级",
      "result.factory": "出厂镜像",
      "result.manifest": "已安装软件包",
      "result.hash": "请求哈希",

      "error.title": "编译失败",
      "error.generic": "出现错误。",
      "error.backend": "编译后端不可用，请检查服务端配置。",
      "error.load": "数据加载失败。",


      "footer.overview": "服务端概览",
      "footer.stats": "统计",
      "footer.source": "源代码",

      "api.title": "API",
      "api.lead": "OpenForge 实现 Attended Sysupgrade API。可将 owut、LuCI 或 OpenWrt 固件选择器指向本服务器。",

      "stats.title": "统计",
      "stats.lead": "来自所连接 ASU 后端的编译活动。",
      "stats.queue": "队列长度",
      "stats.builds24h": "24 小时编译数",
      "stats.perDay": "每日编译量",
      "stats.byVersion": "各版本编译量",
      "stats.topPackages": "热门软件包",
      "stats.errors": "最近编译错误",
      "stats.empty": "暂无数据。",
    },
  };

  const STORAGE_KEY = "openforge.lang";

  function detect() {
    const params = new URLSearchParams(location.search);
    const query = (params.get("lang") || "").toLowerCase();
    if (query.startsWith("zh")) return "zh";
    if (query.startsWith("en")) return "en";
    const saved = localStorage.getItem(STORAGE_KEY);
    if (saved && dict[saved]) return saved;
    const nav = (navigator.language || "en").toLowerCase();
    return nav.startsWith("zh") ? "zh" : "en";
  }

  const I18n = {
    lang: "en",

    t(key, vars) {
      const table = dict[this.lang] || dict.en;
      let value = table[key];
      if (value === undefined) value = (dict.en[key] !== undefined ? dict.en[key] : key);
      if (vars) {
        value = value.replace(/\{(\w+)\}/g, (m, name) =>
          Object.prototype.hasOwnProperty.call(vars, name) ? String(vars[name]) : m
        );
      }
      return value;
    },

    setLang(lang) {
      if (!dict[lang]) lang = "en";
      this.lang = lang;
      localStorage.setItem(STORAGE_KEY, lang);
      document.documentElement.lang = lang === "zh" ? "zh-CN" : "en";
      this.apply(document);
      document.dispatchEvent(new CustomEvent("langchange", { detail: { lang } }));
    },

    apply(root) {
      root = root || document;
      root.querySelectorAll("[data-i18n]").forEach((el) => {
        const vars = {};
        if (el.dataset.i18nMax) vars.n = el.dataset.i18nMax;
        el.textContent = this.t(el.getAttribute("data-i18n"), vars);
      });
      root.querySelectorAll("[data-i18n-placeholder]").forEach((el) => {
        el.setAttribute("placeholder", this.t(el.getAttribute("data-i18n-placeholder")));
      });
      root.querySelectorAll("[data-i18n-title]").forEach((el) => {
        el.setAttribute("title", this.t(el.getAttribute("data-i18n-title")));
        el.setAttribute("aria-label", this.t(el.getAttribute("data-i18n-title")));
      });
    },

    init() {
      this.lang = detect();
      document.documentElement.lang = this.lang === "zh" ? "zh-CN" : "en";
    },
  };

  I18n.init();
  window.I18n = I18n;
})();
