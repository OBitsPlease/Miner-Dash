const state = {
  token: sessionStorage.getItem("minerdash-token") || "",
  view: "workers",
  focusedRigID: "",
  expandedRigIDs: new Set(),
  focusedRigTab: "overview",
  historyRangeDays: 1,
  historyDate: localDateValue(new Date()),
  walletCoinFilter: "all",
  flightSheetCoinFilter: "all",
  expandedSurfaceIDs: new Set(),
  data: {}
};
const $ = selector => document.querySelector(selector);
const $$ = selector => [...document.querySelectorAll(selector)];
const login = $("#login");
const application = $("#application");
const navigationToggle = $("#nav-toggle");
const applicationUpdatePollInterval = 6 * 60 * 60 * 1000;

function setMobileNavigation(open) {
  $("aside")?.classList.toggle("menu-open", open);
  navigationToggle.setAttribute("aria-expanded", String(open));
  navigationToggle.setAttribute("aria-label", open ? "Close navigation" : "Open navigation");
}

const navigationGroupViews = {
  farms: ["farms"],
  mining: ["gpu-mining", "cpu-mining", "flight-sheets", "wallets", "mining-software", "pools", "custom-miners"],
  operations: ["schedules", "alerts", "activity"],
  discover: ["power-prices", "new-coins"],
  system: ["rig-os", "users", "miners", "overclocks"]
};
const navigationCloseTimers = new WeakMap();

function positionNavigationFlyout(group) {
  const flyout = group.querySelector(".nav-flyout");
  const trigger = group.querySelector(".nav-group-trigger");
  const bar = group.closest("aside");
  if (!flyout || !trigger || !bar) return;
  const triggerBounds = trigger.getBoundingClientRect();
  const width = flyout.offsetWidth;
  const left = Math.max(12, Math.min(window.innerWidth - width - 12, triggerBounds.left + triggerBounds.width / 2 - width / 2));
  flyout.style.setProperty("--nav-left", `${left}px`);
  const top = window.innerWidth <= 900 ? triggerBounds.bottom : bar.getBoundingClientRect().bottom;
  flyout.style.setProperty("--nav-top", `${top}px`);
}

function updateNavigationOrigin(group, event) {
  const flyout = group.querySelector(".nav-flyout");
  const trigger = group.querySelector(".nav-group-trigger");
  if (!flyout || !trigger) return;
  const bounds = flyout.getBoundingClientRect();
  const triggerBounds = trigger.getBoundingClientRect();
  const pointerX = Number.isFinite(event?.clientX) ? event.clientX : triggerBounds.left + triggerBounds.width / 2;
  const pointerY = Number.isFinite(event?.clientY) ? event.clientY : triggerBounds.bottom;
  flyout.style.setProperty("--nav-origin-x", `${Math.max(0, Math.min(bounds.width, pointerX - bounds.left))}px`);
  flyout.style.setProperty("--nav-origin-y", `${Math.max(0, Math.min(bounds.height, pointerY - bounds.top))}px`);
}

function closeNavigationGroup(group, event) {
  const timer = navigationCloseTimers.get(group);
  if (timer) clearTimeout(timer);
  navigationCloseTimers.delete(group);
  updateNavigationOrigin(group, event);
  group.classList.remove("open");
  group.querySelector(".nav-group-trigger")?.setAttribute("aria-expanded", "false");
}

function closeNavigationGroups(except, event) {
  $$(".nav-group.open").forEach(group => {
    if (group !== except) closeNavigationGroup(group, event);
  });
}

function openNavigationGroup(group, event) {
  const timer = navigationCloseTimers.get(group);
  if (timer) clearTimeout(timer);
  navigationCloseTimers.delete(group);
  closeNavigationGroups(group, event);
  positionNavigationFlyout(group);
  updateNavigationOrigin(group, event);
  group.classList.add("open");
  group.querySelector(".nav-group-trigger")?.setAttribute("aria-expanded", "true");
}

function scheduleNavigationClose(group, event) {
  const timer = navigationCloseTimers.get(group);
  if (timer) clearTimeout(timer);
  const pointer = event && Number.isFinite(event.clientX) ? {clientX: event.clientX, clientY: event.clientY} : undefined;
  navigationCloseTimers.set(group, setTimeout(() => closeNavigationGroup(group, pointer), 140));
}

function updateNavigationState() {
  $$(".nav-group").forEach(group => {
    const views = navigationGroupViews[group.dataset.navGroup] || [];
    group.classList.toggle("selected", views.includes(state.view));
  });
}

function renderNavigationFarms() {
  const root = $("#nav-farm-list");
  if (!root) return;
  const rigs = state.data.rigs || [];
  const farms = state.data.farms || [];
  if (!farms.length) {
    root.innerHTML = `<div class="nav-farm-empty"><strong>No farms yet</strong><span>Open All farms to create one.</span></div>`;
    return;
  }
  root.innerHTML = farms.map(farm => {
    const farmRigs = rigs.filter(rig => rig.farm_id === farm.id);
    const online = farmRigs.filter(rig => rig.online).length;
    const power = farmRigs.reduce((total, rig) => total + estimatedRigPower(rig), 0);
    const health = farmRigs.length && online === farmRigs.length ? "online" : online ? "attention" : "offline";
    return `<button class="nav-farm-card" data-farm-id="${escape(farm.id)}">
      <span class="nav-farm-card-heading"><strong>${escape(farm.name)}</strong><i class="${health}">${online ? "Online" : "Offline"}</i></span>
      <small>${escape(farm.description || "No description")}</small>
      <span class="nav-farm-metrics"><span><small>Rigs</small><b>${online} / ${farmRigs.length}</b></span><span><small>Power</small><b>${power.toFixed(0)} W</b></span></span>
    </button>`;
  }).join("");
}

function setupGroupedNavigation() {
  $$(".nav-group").forEach(group => {
    const trigger = group.querySelector(".nav-group-trigger");
    group.addEventListener("pointerenter", event => {
      if (event.pointerType === "mouse") openNavigationGroup(group, event);
    });
    group.addEventListener("pointermove", event => {
      if (group.classList.contains("open") && event.pointerType === "mouse") updateNavigationOrigin(group, event);
    });
    group.addEventListener("pointerleave", event => {
      if (event.pointerType === "mouse") scheduleNavigationClose(group, event);
    });
    group.addEventListener("focusin", event => openNavigationGroup(group, event));
    group.addEventListener("focusout", event => {
      if (!group.contains(event.relatedTarget)) scheduleNavigationClose(group, event);
    });
    trigger.addEventListener("click", event => {
      if (event.detail > 0 && window.matchMedia("(hover: hover)").matches) openNavigationGroup(group, event);
      else if (group.classList.contains("open")) closeNavigationGroup(group, event);
      else openNavigationGroup(group, event);
    });
  });
  $("#nav-farm-list")?.addEventListener("click", async event => {
    const shortcut = event.target.closest(".nav-farm-card");
    if (!shortcut) return;
    await navigateToView("farms");
    requestAnimationFrame(() => {
      const card = document.querySelector(`.farm-card[data-id="${CSS.escape(shortcut.dataset.farmId)}"]`);
      if (!card) return;
      card.classList.add("expanded");
      card.scrollIntoView({behavior: "smooth", block: "start"});
    });
  });
  document.addEventListener("pointerdown", event => {
    if (!event.target.closest("#primary-navigation")) closeNavigationGroups();
  });
  document.addEventListener("keydown", event => {
    if (event.key === "Escape") {
      closeNavigationGroups();
      document.querySelector(".nav-group.selected .nav-group-trigger")?.focus();
    }
  });
  window.addEventListener("resize", () => closeNavigationGroups());
}

const definitions = {
  farms: {
    title: "Farms", description: "Organize workers by location or purpose",
    endpoint: "farms", columns: [["name", "Name"], ["description", "Description"], ["electricity_rate_usd_per_kwh", "Power rate ($/kWh)"]],
    fields: [["name", "Name", "text", true], ["description", "Description", "textarea"], ["electricity_rate_usd_per_kwh", "Electricity rate ($/kWh)", "number", true]]
  },
  wallets: {
    title: "Wallets", description: "Public payout addresses used by flight sheets",
    endpoint: "wallets", columns: [["name", "Name"], ["coin", "Coin"], ["address", "Public address"]],
    fields: [["name", "Name", "text", true], ["coin", "Coin symbol", "text", true], ["address", "Public payout address", "text", true, "wide"]]
  },
  pools: {
    title: "Mining Pools", description: "Manage approved Stratum endpoints for your flight sheets",
    endpoint: "pools", columns: [["name", "Name"], ["url", "Stratum URL"], ["dashboard_url", "Worker page"], ["stats_pool_id", "Stats pool ID"], ["password", "Password"]],
    fields: [["name", "Name", "text", true], ["url", "Stratum URL", "text", true, "wide"], ["dashboard_url", "Worker page URL template — {WALLET}, {WORKER}, {COIN}", "url", false, "wide"], ["stats_url", "Pool statistics API base URL", "url", false, "wide"], ["stats_pool_id", "Pool statistics ID", "text"], ["password", "Worker password", "text"]]
  },
  miners: {
    title: "Miners", description: "Auditable miner definitions mapped to installed agent profiles",
    endpoint: "miners", columns: [["name", "Name"], ["version", "Version"], ["algorithm", "Algorithm"], ["profile", "Agent profile"]],
    fields: [["name", "Name", "text", true], ["version", "Version", "text"], ["algorithm", "Algorithm", "text", true], ["profile", "Preinstalled agent profile", "text"], ["managed_binary", "Controller-managed binary", "checkbox"], ["binary_name", "Installed binary name", "text"], ["binary_file", "Upload Linux executable", "file", false, "wide"], ["source_url", "Source URL", "text", false, "wide"], ["source_commit", "Reviewed source commit", "text", false, "wide"], ["default_arguments", "Default arguments, one per line", "textarea", false, "wide"], ["extra_arguments", "Flight sheet arguments, one per line. Placeholders: {POOL}, {WALLET}, {PASSWORD}, {WORKER}, {COIN}", "textarea", false, "wide"]]
  },
  "flight-sheets": {
    title: "Flight Sheets", description: "Reusable wallet, pool, miner, and algorithm assignments",
    endpoint: "flight-sheets", columns: [["name", "Name"], ["device_type", "Type"], ["coin", "Coin"], ["algorithm", "Algorithm"], ["wallet_id", "Wallet"], ["pool_id", "Pool"], ["miner_id", "Miner"], ["secondary_coin", "Second coin"]],
    fields: [["name", "Name", "text", true], ["device_type", "Primary workload", "device-select", true], ["coin", "Primary coin", "text"], ["algorithm", "Primary algorithm", "text", true], ["wallet_id", "Primary wallet", "wallet-select", true], ["pool_id", "Primary pool", "pool-select", true], ["miner_id", "Miner", "miner-select", true], ["secondary_device_type", "Second workload (SRBMiner only)", "device-select"], ["secondary_coin", "Second coin", "text"], ["secondary_algorithm", "Second algorithm", "text"], ["secondary_wallet_id", "Second wallet", "wallet-select"], ["secondary_pool_id", "Second pool", "pool-select"]],
    detailedFields: [["wallet_template", "Wallet format — {WALLET} uses the selected wallet", "text", false, "wide"], ["worker_name", "Fixed worker name (blank uses rig name)", "text"], ["pool_url_override", "Temporary primary pool URL override", "text", false, "wide"], ["backup_pool_urls", "Backup pool URLs, one per line", "textarea", false, "wide"], ["pool_password_override", "Temporary pool password override", "text"], ["extra_arguments", "Extra miner arguments, one per line", "textarea", false, "wide"]]
  },
  overclocks: {
    title: "Overclock Profiles", description: "Reusable, vendor-specific GPU tuning limits",
    endpoint: "overclock-profiles", columns: [["name", "Name"], ["vendor", "Vendor"], ["settings", "Default settings"]],
    fields: [["name", "Name", "text", true], ["vendor", "Vendor", "vendor-select", true], ["selector", "GPU index (* for all)", "text", true], ["power_limit_w", "Power limit W", "number"], ["fan_percent", "Fan %", "number"], ["core_clock_mhz", "Locked core clock MHz", "number"], ["core_offset_mhz", "Core offset MHz", "number"], ["memory_clock_mhz", "Locked memory clock MHz", "number"], ["memory_offset_mhz", "Memory offset MHz", "number"]]
  },
  alerts: {
    title: "Alerts", description: "Temperature, hashrate, miner, and worker health events",
    endpoint: "alerts", columns: [["started_at", "Started"], ["severity", "Severity"], ["rig_id", "Worker"], ["type", "Type"], ["message", "Message"], ["active", "Status"]], readonly: true
  },
  schedules: {
    title: "Schedules", description: "Apply flight sheets and run worker actions on local controller time",
    endpoint: "schedules", columns: [["name", "Name"], ["enabled", "Enabled"], ["days", "Days"], ["time", "Time"], ["action", "Action"], ["rig_ids", "Workers"]],
    fields: [["name", "Name", "text", true], ["enabled", "Enabled", "checkbox"], ["time", "Local time", "time", true], ["action", "Action", "action-select", true], ["days", "Days", "days-select", true, "wide"], ["rig_ids", "Workers", "rig-multiselect", true, "wide"], ["flight_sheet_id", "Flight sheet (for apply action)", "flight-select", false, "wide"]]
  },
  activity: {
    title: "Activity", description: "Controller configuration and security events",
    endpoint: "activity", columns: [["at", "Time"], ["action", "Action"], ["resource", "Resource"], ["resource_id", "Identifier"]], readonly: true
  },
  users: {
    title: "Users", description: "Local administrators, operators, and read-only viewers",
    endpoint: "users", columns: [["username", "Username"], ["role", "Role"], ["disabled", "Disabled"], ["created_at", "Created"]],
    fields: [["username", "Username", "text", true], ["password", "Password (12+ characters; blank keeps current)", "password"], ["role", "Role", "role-select", true], ["disabled", "Disabled", "checkbox"]]
  },
  "rig-os": {
    title: "Rig OS & USB", description: "Build, download, and update the Linux mining rig operating system",
    endpoint: "rig-os", readonly: true
  }
};

const escape = value => String(value ?? "").replace(/[&<>"']/g, character => ({"&":"&amp;","<":"&lt;",">":"&gt;","\"":"&quot;","'":"&#39;"}[character]));
const value = (number, suffix = "") => Number.isFinite(number) ? `${number.toFixed(1)}${suffix}` : "—";
const bytes = number => number ? `${(number / 1073741824).toFixed(1)} GiB` : "0 GiB";
const hashratePrefixes = ["", "K", "M", "G", "T", "P", "E"];

function formatDriverEfficiency(efficiency, unit) {
  if (!Number(efficiency)) return "unavailable";
  return `${Number(efficiency).toLocaleString(undefined, {maximumFractionDigits: 2})} ${unit || "hash"}/W`;
}

function minerReleaseForRig(rig, sheets) {
  const sheet = sheets[rig.desired?.flight_sheet_id];
  const miner = (state.data.miners || []).find(item => item.id === sheet?.miner_id);
  if (!miner?.catalog_id) return null;
  const release = (state.data["rig-os"]?.miners || []).find(item => item.id === miner.catalog_id);
  return release ? {...release, assignedMiner: miner} : null;
}

function normalizeHashrate(number, unit = "H/s") {
  const numeric = Number(number || 0);
  const match = String(unit || "H/s").trim().match(/^([KMGTPE]?)(H|SOL)\/?S$/i);
  if (!match) return {value: numeric, unit: String(unit || "H/s"), scalable: false};
  const prefixIndex = hashratePrefixes.findIndex(prefix => prefix.toLowerCase() === match[1].toLowerCase());
  return {
    value: numeric * (1000 ** Math.max(0, prefixIndex)),
    unit: match[2].toLowerCase() === "sol" ? "Sol/s" : "H/s",
    scalable: true
  };
}

function formatHashrate(number, unit = "H/s") {
  const normalized = normalizeHashrate(number, unit);
  if (!normalized.scalable) {
    return `${normalized.value.toLocaleString(undefined, {maximumFractionDigits: 2})} ${normalized.unit}`;
  }
  let scaled = normalized.value;
  let prefixIndex = 0;
  while (Math.abs(scaled) >= 1000 && prefixIndex < hashratePrefixes.length - 1) {
    scaled /= 1000;
    prefixIndex++;
  }
  return `${scaled.toLocaleString(undefined, {maximumFractionDigits: 2})} ${hashratePrefixes[prefixIndex]}${normalized.unit}`;
}
const coinLogos = {
  BTC: "/coins/btc.svg", ETH: "/coins/eth.svg", ETC: "/coins/etc.svg",
  RVN: "/coins/rvn.svg", XMR: "/coins/xmr.svg", QRL: "/coins/qrl.svg",
  ZCL: "/coins/zclassic-zcl-logo.png", VTC: "/coins/vertcoin-vtc-logo.png", YEC: "/coins/ycash-yec-logo.png"
};

function localDateValue(date) {
  const offset = date.getTimezoneOffset() * 60000;
  return new Date(date.getTime() - offset).toISOString().slice(0, 10);
}

function historyWindow(dateValue, days) {
  const selectedDay = new Date(`${dateValue}T00:00:00`);
  const since = new Date(selectedDay);
  since.setDate(since.getDate() - (days - 1));
  const until = new Date(selectedDay);
  until.setDate(until.getDate() + 1);
  const now = new Date();
  if (until > now) until.setTime(now.getTime());
  return {
    since,
    until,
    label: days === 1 ? "1 day" : days === 7 ? "7 days" : "1 month",
    days
  };
}

function activeCPUThreadCount(rigs) {
  return rigs.reduce((total, rig) => {
    if (!rig.metrics?.miner?.running) return total;
    return total + (rig.metrics.miner.devices || []).filter(device => device.kind === "CPU thread").length;
  }, 0);
}

function gpuSensorPower(metrics) {
  return (metrics?.gpus || []).reduce((total, gpu) => total + Number(gpu.power_w || 0), 0);
}

function estimatedRigPower(rig) {
  if (!rig.online) return 0;
  if ((rig.metrics?.gpus || []).length) {
    return gpuSensorPower(rig.metrics) + Number(rig.desired?.power_offset_w || 0);
  }
  return rig.metrics?.miner?.running ? Number(rig.desired?.estimated_power_w || 0) : 0;
}

function farmForRig(rig) {
  return (state.data.farms || []).find(farm => farm.id === rig.farm_id);
}

function dailyPowerCost(powerW, rate) {
  return Number(powerW || 0) / 1000 * 24 * Number(rate || 0);
}

function formatDailyPowerCost(powerW, rate) {
  return `$${dailyPowerCost(powerW, rate).toFixed(2)} / 24h`;
}

function coinBadge(symbol, size = "") {
  const coin = String(symbol || "?").toUpperCase();
  const asset = state.data["coin-assets"]?.[coin];
  const logo = asset?.logo || coinLogos[coin];
  return logo
    ? `<span class="coin-badge ${size}"><span aria-hidden="true">${escape(coin.slice(0, 2))}</span><img src="${logo}" alt="${escape(asset?.name || coin)}"></span>`
    : `<span class="coin-badge fallback ${size}">${escape(coin.slice(0, 2))}</span>`;
}

async function api(path, options = {}) {
  const response = await fetch(path, {...options, headers: {"Authorization": `Bearer ${state.token}`, "Content-Type": "application/json", ...(options.headers || {})}});
  if (!response.ok) {
    const error = new Error((await response.text()).trim() || response.statusText);
    error.status = response.status;
    throw error;
  }
  return response.status === 204 ? null : response.json();
}

function showExpiredSession() {
  sessionStorage.removeItem("minerdash-token");
  state.token = "";
  application.classList.add("hidden");
  login.classList.remove("hidden");
  $("#login-error").textContent = "Session expired after the controller restarted. Sign in again.";
}

function renderSystemUpdate() {
  const status = state.data["system-update"] || {};
  const button = $("#system-update-control");
  const runningVersion = $("#running-version");
  const available = Boolean(status.update_available);
  if (status.current_version) {
    runningVersion.textContent = `v${status.current_version}`;
    runningVersion.title = `Running Miner Dash ${status.current_version}`;
  }
  button.classList.toggle("available", available);
  button.setAttribute("aria-label", available
    ? `Update Miner Dash to ${status.latest_version}`
    : "Check for Miner Dash updates");
  button.title = available
    ? `Miner Dash ${status.latest_version} is available. Click to update.`
    : status.error || `Miner Dash ${status.current_version || ""} is up to date.`;
}

async function installSystemUpdate() {
  if (!confirm("Install the verified Miner Dash update now? The controller will restart briefly. Mining rigs continue their current work while it restarts.")) return;
  const button = $("#system-update-control");
  button.disabled = true;
  button.classList.add("installing");
  button.setAttribute("aria-label", "Downloading Miner Dash update");
  try {
    const result = await api("/api/v1/system-update", {method: "POST"});
    button.setAttribute("aria-label", `Installing Miner Dash ${result.version}`);
    showNotice("Update verified. Miner Dash is restarting and will reload automatically.");
    waitForControllerUpdate();
  } catch (error) {
    button.disabled = false;
    button.classList.remove("installing");
    renderSystemUpdate();
    showNotice(error.message);
  }
}

async function refreshSystemUpdate({notify = false} = {}) {
  try {
    state.data["system-update"] = await api("/api/v1/system-update");
    renderSystemUpdate();
    if (notify && !state.data["system-update"].update_available) {
      showNotice(`Miner Dash ${state.data["system-update"].current_version} is up to date.`);
    }
  } catch (error) {
    state.data["system-update"] = {error: error.message};
    renderSystemUpdate();
    if (notify) showNotice(`Update check failed: ${error.message}`);
  }
}

async function waitForControllerUpdate() {
  for (let attempt = 0; attempt < 90; attempt++) {
    await new Promise(resolve => setTimeout(resolve, 1000));
    try {
      const response = await fetch("/healthz", {cache: "no-store"});
      if (response.ok) {
        const health = await response.json();
        const expected = state.data["system-update"]?.latest_version;
        if (!expected || health.version === expected) {
          location.reload();
          return;
        }
      }
    } catch {
      // A short connection failure is expected while the Windows service restarts.
    }
  }
  showNotice("The update is taking longer than expected. Refresh after the controller service returns.");
}

async function loadAll({preserveWorkerUI = false, silentAuthFailure = false} = {}) {
  const workerUI = preserveWorkerUI && ["workers", "gpu-mining", "cpu-mining", "farms"].includes(state.view) ? captureWorkerUI() : null;
  try {
    const endpoints = ["rigs", "farms", "wallets", "pools", "miners", "flight-sheets", "overclock-profiles", "schedules", "alerts", "activity", "rig-os"];
    const values = await Promise.all(endpoints.map(endpoint => api(`/api/v1/${endpoint}`)));
    endpoints.forEach((endpoint, index) => state.data[endpoint] = values[index]);
    const minerUpdates = (state.data["rig-os"]?.miners || []).filter(miner => miner.update_available).length;
    $$(".miner-update-count").forEach(updateBadge => {
      updateBadge.textContent = minerUpdates;
      updateBadge.classList.toggle("hidden", minerUpdates === 0);
    });
    try {
      state.data["coin-prices"] = await api("/api/v1/coin-prices");
      state.coinPriceError = "";
    } catch (marketError) {
      state.data["coin-prices"] ||= {};
      state.coinPriceError = marketError.message;
    }
    try {
      state.data["coin-assets"] = await api("/api/v1/coin-assets");
    } catch (assetError) {
      state.data["coin-assets"] ||= {};
      console.warn(assetError.message);
    }
    try {
      state.data["community-miners"] = await api("/api/v1/community-miners");
    } catch (communityError) {
      state.data["community-miners"] = {miners: [], error: communityError.message};
      console.warn(communityError.message);
    }
    try {
      state.data["system-update"] = await api("/api/v1/system-update");
    } catch (updateError) {
      state.data["system-update"] = {error: updateError.message};
      console.warn(updateError.message);
    }
    login.classList.add("hidden");
    application.classList.remove("hidden");
    $("#last-update").textContent = `Updated ${new Date().toLocaleTimeString()}`;
    render();
    renderSystemUpdate();
    if (workerUI) restoreWorkerUI(workerUI);
    if (state.coinPriceError) showNotice(`Coin prices unavailable: ${state.coinPriceError}`);
  } catch (error) {
    if (error.status === 401) {
      sessionStorage.removeItem("minerdash-token");
      state.token = "";
    }
    login.classList.remove("hidden");
    application.classList.add("hidden");
    $("#login-error").textContent = silentAuthFailure ? "" : (error.status === 401 ? "Session expired. Please sign in again." : error.message);
  }
}

let liveRefreshInFlight = false;

async function refreshLiveRigs() {
  if (liveRefreshInFlight) return;
  liveRefreshInFlight = true;
  const scrollX = window.scrollX;
  const scrollY = window.scrollY;
  const focusedCard = document.querySelector(".rig-card.focused");
  const focusedScrollTop = focusedCard?.scrollTop || 0;
  try {
    state.data.rigs = await api("/api/v1/rigs");
    $("#last-update").textContent = `Updated ${new Date().toLocaleTimeString()}`;
    refreshRigCards();
  } catch (error) {
    if (error.status === 401) {
      sessionStorage.removeItem("minerdash-token");
      state.token = "";
      login.classList.remove("hidden");
      application.classList.add("hidden");
      $("#login-error").textContent = "Session expired. Please sign in again.";
    } else {
      showNotice(`Live update failed: ${error.message}`);
    }
  } finally {
    const liveFocusedCard = document.querySelector(".rig-card.focused");
    if (liveFocusedCard) liveFocusedCard.scrollTop = focusedScrollTop;
    window.scrollTo(scrollX, scrollY);
    liveRefreshInFlight = false;
  }
}

function refreshRigCards() {
  const container = workerViewContainer();
  const sheets = Object.fromEntries((state.data["flight-sheets"] || []).map(sheet => [sheet.id, sheet]));
  const rigs = state.view === "gpu-mining" || state.view === "cpu-mining"
    ? rigsForMiningView(state.view)
    : state.data.rigs || [];
  const existingCards = [...container.querySelectorAll(".rig-card[data-rig-id]")];
  const existingIDs = existingCards.map(card => card.dataset.rigId).sort();
  const nextIDs = rigs.map(rig => rig.id).sort();
  const topologyChanged = existingIDs.length !== nextIDs.length ||
    existingIDs.some((id, index) => id !== nextIDs[index]) ||
    rigs.some(rig => {
      const card = existingCards.find(item => item.dataset.rigId === rig.id);
      return card?.dataset.rigKind && card.dataset.rigKind !== rigKind(rig);
    });
  if (topologyChanged) {
    const snapshot = captureWorkerUI();
    if (state.view === "workers") renderWorkers();
    else if (state.view === "farms") renderFarms($("#resource-content"), state.data.farms || []);
    else renderMining();
    restoreWorkerUI(snapshot);
    return;
  }

  rigs.forEach(rig => {
    const current = container.querySelector(`.rig-card[data-rig-id="${CSS.escape(rig.id)}"]`);
    if (!current) return;
    const replacement = renderRig(rig, sheets).querySelector(".rig-card");
    if (current.classList.contains("focused")) updateFocusedRigCard(current, replacement);
    else current.replaceWith(replacement);
  });
  if (state.view === "workers") renderOverviewStats(rigs, sheets);
  if (state.view === "gpu-mining" || state.view === "cpu-mining") updateMiningSwitcher();
}

function updateFocusedRigCard(card, replacement) {
  card.classList.toggle("offline", replacement.classList.contains("offline"));
  card.dataset.rigKind = replacement.dataset.rigKind;
  [
    ".rig-title-line", ".rig-meta", ".gpu-health-strip", ".rig-workload-summary", ".status", ".rig-metrics", ".gpu-list",
    ".coin-price-card", ".system-detail-grid", ".miner-row", ".pool-health", ".smart-tune-status"
  ].forEach(selector => {
    const current = card.querySelector(selector);
    const next = replacement.querySelector(selector);
    if (!current || !next) return;
    current.className = next.className;
    current.innerHTML = next.innerHTML;
  });
  const minerScreen = card.querySelector(".miner-screen");
  const nextMinerScreen = replacement.querySelector(".miner-screen");
  minerScreen.disabled = nextMinerScreen.disabled;
  minerScreen.title = nextMinerScreen.title;
  fillWorkerSelect(card.querySelector(".focused-worker-select"), miningViewForRig(
    state.data.rigs.find(rig => rig.id === card.dataset.rigId)
  ), card.dataset.rigId);
}

function rigKind(rig) {
  if (!rig.metrics?.collected_at) return "pending";
  return (rig.metrics.gpus || []).length ? "gpu" : "cpu";
}

function captureWorkerUI() {
  const container = workerViewContainer();
  const active = document.activeElement;
  const activeCard = active?.closest?.(".rig-card");
  return {
    scrollX: window.scrollX,
    scrollY: window.scrollY,
    cards: Object.fromEntries([...container.querySelectorAll(".rig-card[data-rig-id]")].map(card => [card.dataset.rigId, {
      open: card.querySelector("details")?.open || false,
      cardScrollTop: card.scrollTop,
      controls: Object.fromEntries([...card.querySelectorAll(".assignment-form input, .assignment-form select")]
        .filter(control => control.classList.length)
        .map(control => [control.classList[0], {value: control.value, checked: control.checked}])),
      message: card.querySelector(".message")?.textContent || "",
      log: card.querySelector(".log-output")?.textContent || "",
      minerScreenMessage: card.querySelector(".miner-screen-message")?.textContent || "",
      minerScreenVisible: !card.querySelector(".miner-screen-panel")?.classList.contains("hidden"),
      history: card.querySelector(".history-chart")?.innerHTML || "",
      historyLoaded: card.querySelector(".history-chart")?.dataset.loaded === "true",
      historyDownloadEnabled: !card.querySelector(".history-download")?.disabled,
      historyVisible: !card.querySelector(".history-chart")?.classList.contains("hidden")
    }])),
    focus: activeCard && active?.classList?.length ? {
      rigID: activeCard.dataset.rigId,
      className: active.classList[0],
      start: active.selectionStart,
      end: active.selectionEnd
    } : null
  };
}

function restoreWorkerUI(snapshot) {
  const container = workerViewContainer();
  Object.entries(snapshot.cards).forEach(([rigID, saved]) => {
    const card = container.querySelector(`.rig-card[data-rig-id="${CSS.escape(rigID)}"]`);
    if (!card) return;
    const details = card.querySelector("details");
    if (details) details.open = saved.open;
    card.scrollTop = saved.cardScrollTop || 0;
    Object.entries(saved.controls).forEach(([className, controlState]) => {
      const control = card.querySelector(`.${CSS.escape(className)}`);
      if (!control) return;
      control.value = controlState.value;
      if (control.type === "checkbox") control.checked = controlState.checked;
    });
    const message = card.querySelector(".message");
    if (message) message.textContent = saved.message;
    const minerScreenMessage = card.querySelector(".miner-screen-message");
    if (minerScreenMessage) minerScreenMessage.textContent = saved.minerScreenMessage;
    const log = card.querySelector(".log-output");
    if (log) {
      log.textContent = saved.log;
      card.querySelector(".miner-screen-panel")?.classList.toggle("hidden", !saved.minerScreenVisible);
    }
    const history = card.querySelector(".history-chart");
    if (history) {
      history.innerHTML = saved.history;
      history.dataset.loaded = saved.historyLoaded ? "true" : "";
      history.classList.toggle("hidden", !saved.historyVisible);
      const download = card.querySelector(".history-download");
      if (download) download.disabled = !saved.historyDownloadEnabled;
    }
  });
  if (snapshot.focus) {
    const control = container.querySelector(`.rig-card[data-rig-id="${CSS.escape(snapshot.focus.rigID)}"] .${CSS.escape(snapshot.focus.className)}`);
    control?.focus({preventScroll: true});
    if (control?.setSelectionRange && snapshot.focus.start !== null) {
      control.setSelectionRange(snapshot.focus.start, snapshot.focus.end);
    }
  }
  requestAnimationFrame(() => window.scrollTo(snapshot.scrollX, snapshot.scrollY));
}

function workerViewContainer() {
  if (state.view === "workers") return $("#workers-view");
  if (state.view === "farms") return $("#resource-content");
  return $("#mining-view");
}

function rigsForMiningView(view) {
  const gpu = view === "gpu-mining";
  return [...(state.data.rigs || [])]
    .filter(rig => gpu
      ? (rig.metrics?.gpus || []).length > 0
      : rig.metrics?.collected_at && !(rig.metrics?.gpus || []).length)
    .sort((a, b) => a.name.localeCompare(b.name));
}

function miningViewForRig(rig) {
  return (rig.metrics?.gpus || []).length > 0 ? "gpu-mining" : "cpu-mining";
}

function fillWorkerSelect(select, view, selectedRigID = "") {
  select.replaceChildren(new Option("Select worker", ""));
  rigsForMiningView(view).forEach(rig => select.add(new Option(`${rig.name} · ${rig.ip_address || "IP pending"}`, rig.id)));
  select.value = selectedRigID;
}

function updateMiningSwitcher() {
  const mining = state.view === "gpu-mining" || state.view === "cpu-mining";
  $("#mining-switcher").classList.toggle("hidden", !mining);
  if (!mining) return;
  $("#mining-workspace-select").value = state.view;
  fillWorkerSelect($("#mining-worker-select"), state.view, state.focusedRigID);
}

function openRigFromSwitcher(rigID) {
  const rig = (state.data.rigs || []).find(item => item.id === rigID);
  if (!rig) return;
  state.view = miningViewForRig(rig);
  state.focusedRigID = rig.id;
  state.focusedRigTab = "overview";
  render();
  const card = document.querySelector(`.rig-card.focused[data-rig-id="${CSS.escape(rig.id)}"]`);
  if (card) loadRigPoolPerformance(card, rig);
}

function render() {
  const workers = state.view === "workers";
  const rigOS = state.view === "rig-os";
  const feature = ["mining-software", "power-prices", "new-coins"].includes(state.view);
  const customMiners = state.view === "custom-miners";
  const mining = state.view === "gpu-mining" || state.view === "cpu-mining";
  const farms = state.view === "farms";
  if (!workers && !mining && !farms) closeFocusedRig();
  $("#workers-view").classList.toggle("hidden", !workers);
  $("#resource-view").classList.toggle("hidden", workers || rigOS || feature || customMiners || mining);
  $("#rig-os-view").classList.toggle("hidden", !rigOS);
  $("#feature-view").classList.toggle("hidden", !feature);
  $("#custom-miners-view").classList.toggle("hidden", !customMiners);
  $("#mining-view").classList.toggle("hidden", !mining);
  $$(".nav").forEach(button => button.classList.toggle("active", button.dataset.view === state.view));
  updateNavigationState();
  renderNavigationFarms();
  updateMiningSwitcher();
  if (workers) renderWorkers();
  else if (mining) renderMining();
  else if (rigOS) renderRigOS();
  else if (feature) renderFeaturePage();
  else if (customMiners) renderCustomMinerLab();
  else renderResources();
}

function renderMining() {
  const deviceType = state.view === "cpu-mining" ? "CPU" : "GPU";
  const allSheets = state.data["flight-sheets"] || [];
  const sheets = allSheets.filter(sheet => sheet.device_type === deviceType || sheet.secondary_device_type === deviceType);
  const sheetMap = Object.fromEntries(allSheets.map(sheet => [sheet.id, sheet]));
  const rigs = rigsForMiningView(state.view);
  const wallets = Object.fromEntries((state.data.wallets || []).map(item => [item.id, item]));
  const pools = Object.fromEntries((state.data.pools || []).map(item => [item.id, item]));
  const miners = Object.fromEntries((state.data.miners || []).map(item => [item.id, item]));
  const assigned = {};
  (state.data.rigs || []).forEach(rig => {
    const sheetID = rig.desired?.flight_sheet_id;
    if (sheetID) (assigned[sheetID] ||= []).push(rig.name);
  });
  $("#page-title").textContent = `${deviceType} Mining`;
  $("#page-subtitle").textContent = `Wallets, pools, software, and ${deviceType} flight sheets in one place`;
  const cards = sheets.length ? sheets.map(sheet => {
    const wallet = wallets[sheet.wallet_id] || {};
    const pool = pools[sheet.pool_id] || {};
    const miner = miners[sheet.miner_id] || {};
    const minerReady = (miner.managed_binary && miner.binary_sha256) || (!miner.managed_binary && miner.profile);
    return `<article class="mining-sheet-card expandable-row" data-id="${escape(sheet.id)}">
      <div class="sheet-heading">${coinBadge(sheet.coin)}<span class="device-badge ${sheet.device_type?.toLowerCase()}">${escape(sheet.device_type || deviceType)}</span><div><h3>${escape(sheet.name)}</h3><small>${escape(sheet.coin)} · ${escape(sheet.algorithm || miner.algorithm)}</small></div></div>
      <div class="expandable-details">
        <dl><dt>Wallet</dt><dd title="${escape(wallet.address)}">${escape(shortValue(wallet.address))}</dd><dt>Pool</dt><dd>${escape(pool.url)}</dd><dt>Miner</dt><dd>${escape(miner.name)} ${escape(miner.version || "")}</dd><dt>Rigs</dt><dd>${escape((assigned[sheet.id] || []).join(", ") || "Not assigned yet")}</dd></dl>
        ${sheet.secondary_coin ? `<div class="dual-workload">${coinBadge(sheet.secondary_coin, "small")}<strong>${escape(sheet.secondary_device_type)}: ${escape(sheet.secondary_coin)}</strong><span>${escape(sheet.secondary_algorithm)}</span></div>` : ""}
        ${minerReady ? "" : `<div class="package-warning">This advanced miner definition does not have a runnable package.</div>`}
      </div>
      <div class="row-actions"><button class="secondary edit-sheet" data-id="${escape(sheet.id)}">Edit</button><button class="danger remove-sheet" data-id="${escape(sheet.id)}">Delete</button></div>
    </article>`;
  }).join("") : `<div class="empty simple-empty"><strong>No ${deviceType} mining setups yet</strong><span>Use the guided setup below. You will enter the wallet, pool, miner, and rigs in one form.</span></div>`;
  const usedWallets = [...new Set(sheets.map(sheet => sheet.wallet_id))].map(id => wallets[id]).filter(Boolean);
  const usedPools = [...new Set(sheets.map(sheet => sheet.pool_id))].map(id => pools[id]).filter(Boolean);
  const usedMiners = [...new Set(sheets.map(sheet => sheet.miner_id))].map(id => miners[id]).filter(Boolean);
  $("#mining-content").innerHTML = `
    <div class="mining-hero ${deviceType.toLowerCase()}">
      <div><span class="eyebrow">${deviceType} WORKLOADS</span><h2>${deviceType} mining made simple</h2><p>Create the complete flight sheet without leaving this page.</p></div>
      <button id="new-mining-setup">+ New ${deviceType} mining setup</button>
    </div>
    <div class="section-heading"><div><h2>${deviceType} rigs</h2><p>Live status, hardware, tuning, and controls</p></div><span class="section-count">${rigs.length} rig${rigs.length === 1 ? "" : "s"}</span></div>
    <div id="mining-rig-grid" class="rig-grid mining-rig-grid"></div>
    <div class="section-heading"><div><h2>${deviceType} flight sheets</h2><p>Complete configurations ready to apply to rigs</p></div></div>
    <div class="mining-sheet-grid">${cards}</div>
    <div class="included-resources">
      ${resourceSummary("Wallets used here", usedWallets, item => `${item.coin} · ${shortValue(item.address)}`)}
      ${resourceSummary("Pools used here", usedPools, item => item.url)}
      ${resourceSummary("Mining software", usedMiners, item => `${item.name} ${item.version || ""}`)}
    </div>`;
  const rigGrid = $("#mining-rig-grid");
  if (rigs.length) rigs.forEach(rig => rigGrid.append(renderRig(rig, sheetMap)));
  else rigGrid.innerHTML = `<div class="empty simple-empty">No ${deviceType} rigs have reported hardware yet.</div>`;
  bindExpandableRows($("#mining-content"), ".mining-sheet-card", `${deviceType.toLowerCase()}-mining-sheet`);
  $("#new-mining-setup").addEventListener("click", () => openQuickSheet(deviceType));
  $$(".edit-sheet").forEach(button => button.addEventListener("click", () => {
    state.view = "flight-sheets";
    openResourceDialog((state.data["flight-sheets"] || []).find(sheet => sheet.id === button.dataset.id));
  }));
  $$(".remove-sheet").forEach(button => button.addEventListener("click", async () => {
    if (!confirm("Delete this mining setup? Saved wallets and pools will remain available.")) return;
    try {
      await api(`/api/v1/flight-sheets/${button.dataset.id}`, {method: "DELETE"});
      await loadAll();
    } catch (error) { showNotice(error.message); }
  }));
}

function resourceSummary(title, records, describe) {
  const rows = records.length ? records.map(record => `<li><strong>${escape(record.name)}</strong><span>${escape(describe(record))}</span></li>`).join("") : "<li><span>Created automatically when you add a setup.</span></li>";
  return `<details><summary><span>${escape(title)}</span><b>${records.length}</b></summary><ul>${rows}</ul></details>`;
}

function shortValue(value = "") {
  return value.length > 24 ? `${value.slice(0, 12)}…${value.slice(-8)}` : value || "—";
}

function bindExpandableRows(root, selector, namespace) {
  root.querySelectorAll(selector).forEach((row, index) => {
    const id = row.dataset.id || String(index);
    const key = `${namespace}:${id}`;
    const setExpanded = expanded => {
      row.classList.toggle("expanded", expanded);
      row.setAttribute("aria-expanded", String(expanded));
      row.title = expanded ? "Collapse details" : "Show details";
      if (expanded) state.expandedSurfaceIDs.add(key);
      else state.expandedSurfaceIDs.delete(key);
    };
    row.tabIndex = 0;
    row.setAttribute("role", "button");
    setExpanded(state.expandedSurfaceIDs.has(key));
    const toggle = () => {
      const expanding = !row.classList.contains("expanded");
      root.querySelectorAll(selector).forEach(other => {
        if (other === row) return;
        other.classList.remove("expanded");
        other.setAttribute("aria-expanded", "false");
        state.expandedSurfaceIDs.delete(`${namespace}:${other.dataset.id || "0"}`);
      });
      setExpanded(expanding);
    };
    row.addEventListener("click", event => {
      if (event.target.closest("button, a, input, select, textarea, label")) return;
      toggle();
    });
    row.addEventListener("keydown", event => {
      if (event.key !== "Enter" && event.key !== " ") return;
      if (event.target.closest("button, a, input, select, textarea")) return;
      event.preventDefault();
      toggle();
    });
  });
}

function openQuickSheet(deviceType) {
  const form = $("#quick-sheet-form");
  form.reset();
  form.elements.custom_wallet_template.value = "{WALLET}.{WORKER}";
  $("#quick-device-type").value = deviceType;
  $("#quick-sheet-title").textContent = `New ${deviceType} mining setup`;
  $("#quick-sheet-error").textContent = "";
  const catalog = (state.data["rig-os"]?.miners || []).filter(entry =>
    (entry.image_included || entry.automatic_install || entry.package_ready) &&
    (deviceType === "CPU" ? entry.hardware.includes("CPU") : entry.hardware.some(hardware => hardware !== "CPU"))
  );
  const minerSelect = form.elements.miner_choice;
  minerSelect.replaceChildren(new Option("Choose mining software…", ""));
  catalog.forEach(entry => {
    const status = entry.package_ready || entry.image_included ? "ready" : "installs automatically";
    minerSelect.add(new Option(`${entry.name} — ${status}`, `catalog:${entry.id}`));
  });
  (state.data["community-miners"]?.miners || []).filter(entry =>
    deviceType === "CPU" ? entry.hardware.includes("CPU") : entry.hardware.some(hardware => hardware !== "CPU")
  ).forEach(entry => minerSelect.add(new Option(`${entry.name} ${entry.version} — Miner Dash registry`, `community:${entry.id}`)));
  minerSelect.add(new Option("Custom Miner — install from HTTPS link", "custom:new"));
  form.elements.wallet_choice.replaceChildren(new Option("Enter a new wallet…", ""));
  (state.data.wallets || []).forEach(wallet => form.elements.wallet_choice.add(new Option(`${wallet.coin} — ${shortValue(wallet.address)}`, wallet.id)));
  form.elements.pool_choice.replaceChildren(new Option("Enter a new pool…", ""));
  (state.data.pools || []).forEach(pool => form.elements.pool_choice.add(new Option(pool.name || pool.url, pool.id)));
  $("#wallet-options").replaceChildren(...(state.data.wallets || []).map(wallet => new Option(wallet.address)));
  $("#pool-options").replaceChildren(...(state.data.pools || []).map(pool => new Option(pool.url)));
  $("#custom-miner-fields").classList.add("hidden");
  $("#toggle-dual-workload").classList.add("hidden");
  $("#secondary-workload").classList.add("hidden");
  setSecondaryRequired(false);
  updateCoinPreview(form.elements.coin);
  const rigSelect = form.elements.rig_ids;
  rigSelect.replaceChildren();
  (state.data.rigs || []).filter(rig => deviceType === "CPU" || (rig.metrics?.gpus || []).length).forEach(rig => {
    rigSelect.add(new Option(rig.name, rig.id));
  });
  updateQuickMinerHelp();
  $("#quick-sheet-dialog").showModal();
}

function updateQuickMinerHelp() {
  const choice = $("#quick-sheet-form").elements.miner_choice.value;
  const help = $("#quick-miner-help");
  if (!choice) {
    $("#custom-miner-fields").classList.add("hidden");
    $("#toggle-dual-workload").classList.add("hidden");
    $("#secondary-workload").classList.add("hidden");
    ["custom_name", "custom_binary_name", "custom_url", "custom_wallet_template"].forEach(name => {
      $("#quick-sheet-form").elements[name].required = false;
    });
    setSecondaryRequired(false);
    help.textContent = "Choose the software you want to run. MinerDash installs supported official releases automatically when you save this setup.";
    return;
  }
  const [kind, id] = choice.split(":");
  const custom = kind === "custom";
  $("#custom-miner-fields").classList.toggle("hidden", !custom);
  ["custom_name", "custom_binary_name", "custom_url", "custom_wallet_template"].forEach(name => {
    $("#quick-sheet-form").elements[name].required = custom;
  });
  const dualAvailable = kind === "catalog" && id === "srbminer-multi";
  $("#toggle-dual-workload").classList.toggle("hidden", !dualAvailable);
  if (!dualAvailable) {
    $("#secondary-workload").classList.add("hidden");
    setSecondaryRequired(false);
  }
  if (custom) {
    help.textContent = "MinerDash will fetch the public HTTPS package, extract only the named executable, calculate its SHA-256, and push that pinned file to selected rigs.";
    return;
  }
  const entry = kind === "catalog" ? (state.data["rig-os"]?.miners || []).find(item => item.id === id) : null;
  help.textContent = entry?.package_ready || entry?.image_included
    ? `${entry.name} is ready on Rig OS.`
    : entry?.automatic_install ? `${entry.name} will be downloaded from its official release repository, verified, and sent to the selected rigs.` : "Custom miner configuration selected.";
}

function openMinerPackageDialog(minerID, algorithm = "") {
  const miner = (state.data.miners || []).find(item => item.id === minerID);
  if (!miner) {
    showNotice("Mining software definition was not found.");
    return;
  }
  const form = $("#miner-package-form");
  form.reset();
  form.elements.miner_id.value = miner.id;
  form.elements.version.value = miner.version === "package-required" ? "" : miner.version || "";
  form.elements.algorithm.value = algorithm || miner.algorithm || "";
  $("#miner-package-title").textContent = `Upload ${miner.name}`;
  $("#miner-package-source").innerHTML = miner.source_url
    ? `Official source: <a href="${escape(miner.source_url)}" target="_blank" rel="noreferrer">${escape(miner.source_url)}</a>`
    : "Use only a package you obtained from the miner's official project.";
  $("#miner-package-error").textContent = "";
  $("#miner-package-dialog").showModal();
}

async function saveMinerPackage(event) {
  event.preventDefault();
  const form = event.target;
  const miner = (state.data.miners || []).find(item => item.id === form.elements.miner_id.value);
  const binary = form.elements.binary_file.files[0];
  if (!miner || !binary) return;
  const updated = {...miner, version: form.elements.version.value.trim(), algorithm: form.elements.algorithm.value.trim()};
  delete updated.binary_sha256;
  try {
    const saved = await api(`/api/v1/miners/${miner.id}`, {method: "PUT", body: JSON.stringify(updated)});
    const response = await fetch(`/api/v1/miners/${saved.id}/binary`, {method: "PUT", headers: {"Authorization": "Bearer " + state.token, "Content-Type": "application/octet-stream"}, body: binary});
    if (!response.ok) throw new Error((await response.text()).trim() || response.statusText);
    $("#miner-package-dialog").close();
    await loadAll();
    showNotice(`${miner.name} ${updated.version} is verified and ready.`);
  } catch (error) { $("#miner-package-error").textContent = error.message; }
}

async function saveQuickSheet(event) {
  event.preventDefault();
  const form = event.target;
  const submit = form.querySelector('button[type="submit"]');
  const originalLabel = submit.textContent;
  const formData = new FormData(form);
  const [choiceType, choiceID] = String(formData.get("miner_choice")).split(":");
  const payload = {
    name: formData.get("name"),
    device_type: formData.get("device_type"),
    coin: formData.get("coin"),
    wallet_address: formData.get("wallet_address"),
    pool_url: formData.get("pool_url"),
    pool_password: formData.get("pool_password"),
    algorithm: formData.get("algorithm"),
    rig_ids: formData.getAll("rig_ids")
  };
  if (choiceType === "catalog") payload.catalog_id = choiceID;
  else if (choiceType === "miner") payload.miner_id = choiceID;
  else if (choiceType === "custom") payload.wallet_template = String(formData.get("custom_wallet_template") || "").trim();
  if (!$("#secondary-workload").classList.contains("hidden")) {
    Object.assign(payload, {
      secondary_coin: formData.get("secondary_coin"),
      secondary_device_type: formData.get("secondary_device_type"),
      secondary_wallet_address: formData.get("secondary_wallet_address"),
      secondary_pool_url: formData.get("secondary_pool_url"),
      secondary_pool_password: formData.get("secondary_pool_password"),
      secondary_algorithm: formData.get("secondary_algorithm")
    });
  }
  submit.disabled = true;
  submit.textContent = "Installing miner and applying setup…";
  try {
    if (choiceType === "community") {
      const installed = await api(`/api/v1/community-miners/${encodeURIComponent(choiceID)}`, {method: "POST"});
      payload.miner_id = installed.id;
    } else if (choiceType === "custom") {
      const imported = await api("/api/v1/miners/import-url", {
        method: "POST",
        body: JSON.stringify({
          name: formData.get("custom_name"),
          version: formData.get("custom_version"),
          algorithm: formData.get("algorithm"),
          url: formData.get("custom_url"),
          binary_name: formData.get("custom_binary_name"),
          default_arguments: String(formData.get("custom_arguments") || "").split("\n").map(item => item.trim()).filter(Boolean)
        })
      });
      payload.miner_id = imported.id;
    }
    const result = await api("/api/v1/quick-flight-sheet", {method: "POST", body: JSON.stringify(payload)});
    $("#quick-sheet-dialog").close();
    await loadAll();
    const applied = result.assigned ? ` Applied to ${result.assigned} rig${result.assigned === 1 ? "" : "s"}.` : "";
    showNotice(result.miner_ready ? `Mining setup created.${applied} The selected rigs will install and start it automatically.` : `Mining setup created.${applied}`);
  } catch (error) {
    $("#quick-sheet-error").textContent = error.message;
  } finally {
    submit.disabled = false;
    submit.textContent = originalLabel;
  }
}

function minerCatalogMarkup(status) {
  const catalogMiners = (status.miners || []).map(miner => `
    <article class="miner-catalog-card expandable-row" data-id="${escape(miner.id)}">
      <div><h3>${escape(miner.name)}</h3><span class="license">${escape(miner.license)}</span></div>
      <p>${escape(miner.description)}</p>
      <small>${escape((miner.hardware || []).join(" · "))}</small>
      <p class="license-note">${escape(miner.redistribution)}</p>
      <div class="catalog-status">
        ${miner.package_ready ? `<span class="${miner.update_available ? "pending" : "ready"}">${miner.image_included ? "Included in Rig OS" : "Installed"} · ${escape(miner.installed_version)}</span>` : `<span class="pending">${miner.image_included ? "Included in Rig OS" : miner.automatic_install ? "Automatic official download" : "Advanced manual package"}</span>`}
        ${miner.latest_version ? `<span class="${miner.update_available ? "update-available" : "latest-release"}">Official latest · ${escape(miner.latest_version)}</span>` : ""}
        ${miner.release_check_error ? `<small>Release check unavailable</small>` : ""}
      </div>
      <div class="row-actions">
        <a class="button-link secondary" href="${escape(miner.latest_release_url || miner.source_url)}" target="_blank" rel="noreferrer">Official release</a>
        ${miner.automatic_install ? `<button class="install-catalog-miner" data-catalog-id="${escape(miner.id)}" data-miner-name="${escape(miner.name)}" data-installed-version="${escape(miner.installed_version || "")}" data-latest-version="${escape(miner.latest_version || "")}"${miner.package_ready && !miner.update_available ? " disabled" : ""}>${miner.update_available ? "Update" : miner.package_ready ? "Up to date" : "Install"}</button>` : miner.installed_miner_id ? `<button class="secondary open-miner" data-miner-id="${escape(miner.installed_miner_id)}">Manage package</button>` : `<button class="install-catalog-miner" data-catalog-id="${escape(miner.id)}">Add advanced definition</button>`}
      </div>
    </article>`).join("");
  const customMiner = `
    <article class="miner-catalog-card custom-miner-card expandable-row" data-id="custom-miner">
      <div><h3>Custom miner</h3><span class="license">User supplied</span></div>
      <p>Run mining software that is not in the Miner Dash catalog, including your own executable.</p>
      <small>CPU · AMD · NVIDIA · Intel GPU</small>
      <p class="license-note">Upload a reviewed Linux executable and define its name, algorithm, launch arguments, wallet template, pool, and password. Guided flight-sheet setup also accepts a direct HTTPS package URL.</p>
      <div class="catalog-status"><span class="pending">Manual configuration</span></div>
      <div class="row-actions"><button id="add-custom-miner">Build custom miner</button></div>
    </article>`;
  return `${catalogMiners}${customMiner}`;
}

function renderRigOS() {
  const status = state.data["rig-os"] || {miners: []};
  $("#page-title").textContent = "Rig OS & USB";
  $("#page-subtitle").textContent = "Create, pair, and boot a complete Miner Dash rig";
  const image = status.image_available
    ? `<div class="os-image-ready"><span class="status online">Image ready</span><strong>${escape(status.image_filename)}</strong><small>${bytes(status.image_size_bytes)} · SHA-256 <code>${escape(status.image_sha256)}</code></small><button id="download-rig-os">Download verified image</button></div>`
    : status.release_image_url
      ? `<div class="os-image-missing"><span class="status pending">Online image</span><strong>Rig OS ${escape(status.release_version)} is available</strong><small>Miner Dash can download and verify it before writing a USB.</small><button id="prepare-rig-os">Download and verify image</button></div>`
      : `<div class="os-image-missing"><span class="status">Image missing</span><strong>This controller was installed without Rig OS</strong><small>Install the current complete Miner Dash release, or publish a verified image on this controller.</small></div>`;
  $("#rig-os-content").innerHTML = `
    <div class="os-hero">
      <div><h2>MinerDash Rig OS</h2><p>A persistent Ubuntu 24.04 image containing the MinerDash agent, GPU tooling, offline enrollment support, and operator-approved miner packages.</p></div>
      ${image}
    </div>
    <section class="rig-setup-workflow">
      <div class="section-heading"><div><span class="eyebrow">RECOMMENDED</span><h2>Set up a rig with a USB drive</h2><p>Create the USB on this Windows controller, boot the target rig from it, then choose whether to keep running from USB or replace the rig's internal operating system.</p></div></div>
      <div class="os-steps">
        <article><strong>1</strong><h3>Create a paired USB</h3><p>Select a removable drive, this controller's reachable LAN address, and a new rig name. The built-in writer handles the rest.</p></article>
        <article><strong>2</strong><h3>Boot the target rig</h3><p>Move the USB to the rig and choose it in the BIOS or boot menu. The rig enrolls with this controller automatically.</p></article>
        <article><strong>3</strong><h3>Run or replace</h3><p>Keep running from USB, or choose the console's internal-drive installation to erase that drive and completely replace its previous OS.</p></article>
      </div>
      <div id="usb-setup" class="usb-setup"><div class="empty simple-empty">Checking removable USB drives…</div></div>
    </section>
    <div class="ssh-pilot existing-linux-option">
      <div><span class="eyebrow">ADVANCED · KEEPS THE CURRENT OS</span><h2>Add Miner Dash to an existing Linux installation</h2><p>The SSH kit installs the Miner Dash agent alongside the current Linux system. It does not erase the disk, remove the existing OS, or automatically stop existing mining services. To completely replace another mining OS, use the USB workflow above and install Rig OS to the internal drive.</p>
        <div id="manual-enrollment-details" class="manual-enrollment-details hidden"></div>
      </div>
      <div class="row-actions"><button id="show-enrollment-details" class="secondary">Show manual enrollment credentials</button><a class="button-link" href="/downloads/minerdash-ssh-kit.zip" download>Download SSH install kit</a><a class="button-link secondary" href="/downloads/minerdash-ssh-kit.zip.sha256" download>SHA-256</a></div>
    </div>`;
  $("#download-rig-os")?.addEventListener("click", downloadRigOS);
  $("#prepare-rig-os")?.addEventListener("click", prepareRigOS);
  $("#show-enrollment-details")?.addEventListener("click", loadEnrollmentDetails);
  loadUSBStatus();
}

async function loadEnrollmentDetails() {
  const button = $("#show-enrollment-details");
  const root = $("#manual-enrollment-details");
  button.disabled = true;
  try {
    const records = await api("/api/v1/enrollment-token");
    const details = records?.[0];
    if (!details?.token || !details?.fingerprint) throw new Error("Enrollment credentials are unavailable.");
    root.innerHTML = `
      <div><span>Enrollment token</span><code>${escape(details.token)}</code></div>
      <div><span>TLS certificate fingerprint</span><code>${escape(details.fingerprint)}</code></div>
      <small>Use these only for a manual Linux agent installation. The built-in USB workflow pairs the rig automatically.</small>`;
    root.classList.remove("hidden");
    button.textContent = "Enrollment credentials shown";
  } catch (error) {
    button.disabled = false;
    if (error.status === 401) {
      showExpiredSession();
      return;
    }
    showNotice(error.message);
  }
}

function renderFeaturePage() {
  const root = $("#feature-content");
  if (state.view === "mining-software") {
    $("#page-title").textContent = "Mining Software";
    $("#page-subtitle").textContent = "Install and manage verified mining software";
    root.innerHTML = `
      <div class="feature-hero"><span class="eyebrow">SOFTWARE CATALOG</span><h2>Choose mining software for your rigs</h2><p>Automatic packages are downloaded from allow-listed official release repositories. Miner Dash hashes each executable, sends it only to assigned rigs, and verifies it again before launch.</p></div>
      <div class="miner-catalog-grid">${minerCatalogMarkup(state.data["rig-os"] || {miners: []})}</div>`;
    bindExpandableRows(root, ".miner-catalog-card", "mining-software");
    $$(".install-catalog-miner").forEach(button => button.addEventListener("click", () => installCatalogMiner(button)));
    $$(".open-miner").forEach(button => button.addEventListener("click", () => openMinerPackageDialog(button.dataset.minerId)));
    $("#add-custom-miner")?.addEventListener("click", () => {
      state.view = "custom-miners";
      render();
    });
    return;
  }
  if (state.view === "power-prices") {
    $("#page-title").textContent = "Power & Prices";
    $("#page-subtitle").textContent = "Compare mining returns using your electricity cost";
    root.innerHTML = `
      <div class="feature-hero"><span class="eyebrow">PROFITABILITY WORKSPACE</span><h2>Find the best use of your power</h2><p>This workspace will combine live coin prices, network conditions, measured rig performance, and your electricity rate to estimate net mining returns.</p></div>
      <div class="feature-roadmap">
        <article><h3>Enter power cost</h3><p>Set electricity pricing by farm, including cost per kWh.</p></article>
        <article><h3>Compare net profit</h3><p>Rank supported proof-of-work coins using power cost instead of revenue alone.</p></article>
        <article><h3>Send a flight sheet</h3><p>Choose a profitable result and create or assign its flight sheet to selected rigs.</p></article>
      </div>`;
    return;
  }
  $("#page-title").textContent = "New Coin";
  $("#page-subtitle").textContent = "Discover newly launched proof-of-work networks";
  root.innerHTML = `
    <div class="feature-hero"><span class="eyebrow">EARLY DISCOVERY</span><h2>Find new proof-of-work coins sooner</h2><p>This workspace will surface newly launched mineable networks with transparent source, algorithm, pool, wallet, exchange, and risk information.</p></div>
    <div class="feature-roadmap">
      <article><h3>Discover launches</h3><p>Track new proof-of-work projects and their launch timelines.</p></article>
      <article><h3>Review mining readiness</h3><p>Check algorithm, miner support, pools, wallets, network activity, and known risks.</p></article>
      <article><h3>Quick-add flight sheet</h3><p>Create a reviewed flight sheet and send it to selected test rigs without repetitive setup.</p></article>
    </div>`;
}

async function prepareRigOS() {
  try {
    await api("/api/v1/rig-os/image-download", {method: "POST"});
    showNotice("Rig OS download started. This page will show its progress.");
    await loadUSBStatus();
  } catch (error) { showNotice(error.message); }
}

async function loadUSBStatus() {
  if (state.view !== "rig-os") return;
  try {
    const status = await api("/api/v1/rig-os/usb");
    renderUSBSetup(status);
    if (status.operation?.state === "running") {
      clearTimeout(state.usbPollTimer);
      state.usbPollTimer = setTimeout(loadUSBStatus, 1500);
    } else if (status.operation?.state === "complete" && status.operation.action === "download" && !state.data["rig-os"]?.image_available) {
      state.data["rig-os"] = await api("/api/v1/rig-os");
      renderFeaturePage();
    }
  } catch (error) {
    if (error.status === 401) {
      showExpiredSession();
      return;
    }
    $("#usb-setup").innerHTML = `<div class="os-image-missing"><strong>USB tools unavailable</strong><small>${escape(error.message)}</small></div>`;
  }
}

function renderUSBSetup(status) {
  const root = $("#usb-setup");
  if (!root) return;
  const previousDevice = root.querySelector("#usb-device")?.value || "";
  const previousConfirmation = root.querySelector("#usb-confirmation")?.value || "";
  const previousRigName = root.querySelector("#usb-rig-name")?.value || "";
  const operation = status.operation || {state: "idle"};
  const progress = operation.bytes_total > 0
    ? Math.min(100, Math.round((operation.bytes_completed / operation.bytes_total) * 100))
    : 0;
  const devices = status.devices || [];
  const controllerURLs = status.controller_urls || [];
  const controllerURL = controllerURLs[0] || "";
  const controllerOptions = controllerURLs.map((url, index) => `<option value="${escape(url)}"${index === 0 ? " selected" : ""}>${escape(url)}</option>`).join("");
  const options = devices.map(device =>
    `<option value="${escape(device.id)}" data-disk-number="${device.disk_number}">Disk ${device.disk_number} · ${escape(device.name || "USB drive")} · ${bytes(device.size_bytes)}</option>`
  ).join("");
  root.innerHTML = `
    <div class="usb-method-grid">
      <article class="usb-method-card">
        <span class="status online">Built in · recommended</span>
        <h2>Step 1: Create the Rig OS USB</h2>
        <p>No flashing app is required. Miner Dash downloads the image if needed, verifies it, writes it directly to the selected USB drive, verifies the completed drive, and adds one-use pairing information.</p>
        ${status.supported ? `
          <label>USB drive to erase
            <select id="usb-device"><option value="">Select a USB drive…</option>${options}</select>
          </label>
          ${controllerURL ? `<div class="automatic-controller-address">
            <span>Controller address added automatically</span>
            <small>Miner Dash selected the LAN address used by this controller PC's default network route.</small>
            <label class="show-controller-address"><input id="show-controller-address" type="checkbox"> Show or change controller IP</label>
            <div id="controller-address-details" class="hidden">
              <strong id="selected-controller-address">${escape(controllerURL)}</strong>
              <select id="controller-address-alternatives" aria-label="Controller IP address">${controllerOptions}</select>
              <small>For PCs with multiple network ports, choose the address on the same network as the new rig.</small>
            </div>
            <input id="usb-controller" type="hidden" value="${escape(controllerURL)}">
          </div>` : `<div class="usb-warning">Miner Dash could not find a usable LAN address for this controller. Connect this PC to the rig's network and refresh.</div>`}
          <label>Name for the new rig
            <input id="usb-rig-name" autocomplete="off" maxlength="63" placeholder="Example: garage-rig-01">
          </label>
          <div id="usb-device-warning" class="usb-warning">Only removable USB disks are listed. The selected disk will be completely erased.</div>
          <label>Confirm the USB erase
            <input id="usb-confirmation" autocomplete="off" placeholder="Select a drive to see the required phrase">
          </label>
          <div class="row-actions">
            <button id="refresh-usb" class="secondary">Refresh drives</button>
            <button id="write-rig-usb" class="danger"${operation.state === "running" ? " disabled" : ""}>Create paired Rig OS USB</button>
            <button id="restore-usb" class="secondary"${operation.state === "running" ? " disabled" : ""}>Restore USB for normal storage</button>
          </div>` : `<div class="usb-warning">Direct USB writing is available when the controller is installed on Windows.</div>`}
      </article>
      <details class="usb-method-card usb-advanced-method">
        <summary>
          <span class="usb-disclosure-icon" aria-hidden="true"></span>
          <span class="usb-disclosure-title"><span class="status">Advanced · external app</span><strong>Manual alternative: use balenaEtcher</strong></span>
          <span class="usb-disclosure-action"><span class="when-closed">Click to expand</span><span class="when-open">Click to collapse</span></span>
        </summary>
        <div class="usb-advanced-content">
          <p>balenaEtcher is not included in Miner Dash or Rig OS. It is a separate desktop application for manually writing the downloaded <code>.img.xz</code> image.</p>
          <p>After Etcher finishes, leave the USB connected, select it in Step 1 above, enter the rig name, then click <strong>Pair manually flashed USB</strong>.</p>
          <div class="row-actions">
            ${state.data["rig-os"]?.release_image_url ? `<a class="button-link" href="${escape(state.data["rig-os"].release_image_url)}">Download Rig OS</a>` : ""}
            ${state.data["rig-os"]?.release_checksum_url ? `<a class="button-link secondary" href="${escape(state.data["rig-os"].release_checksum_url)}">Checksums</a>` : ""}
            <a class="button-link secondary" href="https://etcher.balena.io/" target="_blank" rel="noreferrer">Open balenaEtcher</a>
            <button id="pair-rig-usb" class="secondary"${operation.state === "running" ? " disabled" : ""}>Pair manually flashed USB</button>
          </div>
          <p class="license-note">When the USB is no longer needed, return here and use Restore USB to create one full-size exFAT partition.</p>
        </div>
      </details>
    </div>
    ${operation.state !== "idle" ? `<div class="usb-operation ${escape(operation.state)}"><strong>${escape(operation.action || "USB operation")} · ${escape(operation.state)}</strong><span>${escape(operation.message || "")}</span>${operation.state === "running" && operation.bytes_total > 0 ? `<progress max="100" value="${progress}"></progress><small>${progress}% · ${bytes(operation.bytes_completed)} of ${bytes(operation.bytes_total)}</small>` : ""}</div>` : ""}
    ${status.error ? `<div class="usb-warning">${escape(status.error)}</div>` : ""}`;
  const selector = $("#usb-device");
  if (selector && [...selector.options].some(option => option.value === previousDevice)) selector.value = previousDevice;
  const rigName = $("#usb-rig-name");
  if (rigName) rigName.value = previousRigName;
  const showControllerAddress = $("#show-controller-address");
  const controllerAddressDetails = $("#controller-address-details");
  showControllerAddress?.addEventListener("change", () => {
    controllerAddressDetails?.classList.toggle("hidden", !showControllerAddress.checked);
  });
  $("#controller-address-alternatives")?.addEventListener("change", event => {
    $("#usb-controller").value = event.target.value;
    $("#selected-controller-address").textContent = event.target.value;
  });
  const confirmation = $("#usb-confirmation");
  if (confirmation) confirmation.value = previousConfirmation;
  const updateConfirmationHint = () => {
    const option = selector?.selectedOptions[0];
    confirmation.placeholder = option?.value ? `Type ERASE USB ${option.dataset.diskNumber}` : "Select a drive to see the required phrase";
  };
  selector?.addEventListener("change", updateConfirmationHint);
  updateConfirmationHint();
  $("#refresh-usb")?.addEventListener("click", loadUSBStatus);
  $("#write-rig-usb")?.addEventListener("click", () => startUSBAction("flash"));
  $("#pair-rig-usb")?.addEventListener("click", () => startUSBAction("pair"));
  $("#restore-usb")?.addEventListener("click", () => startUSBAction("restore"));
}

async function startUSBAction(action) {
  const selector = $("#usb-device");
  const confirmation = $("#usb-confirmation");
  const option = selector?.selectedOptions[0];
  if (!option?.value) {
    showNotice("Select a removable USB drive first.");
    return;
  }
  if (action !== "pair") {
    const required = `ERASE USB ${option.dataset.diskNumber}`;
    if (confirmation.value !== required) {
      showNotice(`Type ${required} exactly to confirm that this USB drive may be erased.`);
      return;
    }
  }
  const controllerURL = $("#usb-controller")?.value || "";
  const rigName = $("#usb-rig-name")?.value.trim() || "";
  if (action !== "restore" && (!controllerURL || !rigName)) {
    showNotice(controllerURL ? "Enter a unique rig name." : "No usable controller LAN address was found.");
    return;
  }
  if (action !== "pair") {
    const verb = action === "flash" ? "write Miner Dash Rig OS to" : "restore for normal storage";
    if (!confirm(`Permanently erase Disk ${option.dataset.diskNumber} and ${verb} this USB drive?`)) return;
  }
  try {
    await api(`/api/v1/rig-os/usb/${action}`, {
      method: "POST",
      body: JSON.stringify({
        device_id: option.value,
        confirmation: confirmation.value,
        controller_url: controllerURL,
        rig_name: rigName
      })
    });
    showNotice(action === "flash"
      ? "USB preparation and automatic pairing started. Do not remove the drive."
      : action === "pair"
        ? "Pairing information is being written. Do not remove the drive."
        : "USB restore started. Do not remove the drive.");
    await loadUSBStatus();
  } catch (error) { showNotice(error.message); }
}

function renderCustomMinerLab() {
  $("#page-title").textContent = "Custom Miner Lab";
  $("#page-subtitle").textContent = "Build reusable Miner Dash-native miner integrations";
  const customMiners = (state.data.miners || []).filter(miner => !miner.catalog_id);
  const community = state.data["community-miners"] || {miners: []};
  const communityCards = (community.miners || []).map(miner => `
    <article class="custom-miner-record community-miner-record expandable-row" data-id="community-${escape(miner.id)}">
      <div><span class="status online">Approved</span><h3>${escape(miner.name)}</h3><p>${escape(miner.version)} · ${escape(miner.recommended_algorithm)}</p></div>
      <dl>
        <div><dt>Hardware</dt><dd>${escape((miner.hardware || []).join(" · ") || "Not specified")}</dd></div>
        <div><dt>License</dt><dd>${escape(miner.license || "See project")}</dd></div>
        <div><dt>Executable hash</dt><dd><code>${escape(miner.executable_sha256.slice(0, 16))}…</code></dd></div>
        ${miner.package_sha256 ? `<div><dt>Archive hash</dt><dd><code>${escape(miner.package_sha256.slice(0, 16))}…</code></dd></div>` : ""}
      </dl>
      <div class="row-actions"><a class="button-link secondary" href="${escape(miner.source_url)}" target="_blank" rel="noreferrer">Source</a><button class="install-community-miner" data-community-id="${escape(miner.id)}">Install</button></div>
    </article>`).join("");
  const cards = customMiners.length ? customMiners.map(miner => {
    const ready = Boolean(miner.binary_sha256 && (miner.package_format !== "tar.gz" || miner.package_sha256));
    const packageHash = miner.package_format === "tar.gz" ? miner.package_sha256 : miner.binary_sha256;
    return `
    <article class="custom-miner-record expandable-row" data-id="${escape(miner.id)}">
      <div><span class="status ${ready ? "online" : ""}">${ready ? "Ready" : "Package needed"}</span><h3>${escape(miner.name)}</h3><p>${escape(miner.version || "custom")} · ${escape(miner.algorithm || "algorithm not set")}</p></div>
      <dl>
        <div><dt>Executable</dt><dd>${escape(miner.entry_point || miner.binary_name || "Not set")}</dd></div>
        <div><dt>Package hash</dt><dd>${packageHash ? `<code>${escape(packageHash.slice(0, 16))}…</code>` : "Not uploaded"}</dd></div>
        <div><dt>Arguments</dt><dd>${escape((miner.default_arguments || []).join(" ") || "None")}</dd></div>
      </dl>
      <div class="row-actions"><button class="secondary custom-miner-manage" data-miner-id="${escape(miner.id)}">Manage package</button></div>
    </article>`;
  }).join("") : `<div class="empty simple-empty"><strong>No custom miners yet</strong><span>Create one from an official HTTPS package or your own Linux executable.</span></div>`;
  $("#custom-miners-content").innerHTML = `
    <section class="custom-lab-hero">
      <div><span class="eyebrow">DEVELOPER TOOLS</span><h2>Bring your own mining software</h2><p>Create a reusable, platform-native miner package. Miner Dash handles configuration placeholders, integrity checks, deployment, process control, and rollback.</p></div>
      <button id="custom-lab-build">Build custom miner</button>
    </section>
    <section class="custom-lab-actions">
      <article><span>1</span><h3>Prepare</h3><p>Use a Linux executable, or a .tar.gz archive containing a self-contained executable.</p></article>
      <article><span>2</span><h3>Configure</h3><p>Set the executable name, algorithm, version, and command-line arguments with Miner Dash placeholders.</p></article>
      <article><span>3</span><h3>Verify</h3><p>Miner Dash extracts only the selected executable, calculates its SHA-256, and never runs installer scripts.</p></article>
      <article><span>4</span><h3>Use</h3><p>Select the reusable miner from Flight Sheets, or create a one-time custom setup there.</p></article>
    </section>
    <section class="custom-lab-reference">
      <div><h2>Configuration placeholders</h2><p>Enter one argument per line so spaces in wallet names and values remain safe.</p></div>
      <div class="placeholder-grid">
        <code>{POOL}</code><span>Complete pool URL</span>
        <code>{POOL_ENDPOINT}</code><span>Pool host and port</span>
        <code>{WALLET}</code><span>Resolved wallet or wallet/worker template</span>
        <code>{WORKER}</code><span>Rig worker name</span>
        <code>{PASSWORD}</code><span>Pool password</span>
        <code>{COIN}</code><span>Coin ticker</span>
        <code>{ALGORITHM}</code><span>Flight-sheet algorithm</span>
      </div>
      <div class="custom-package-note"><strong>Multi-file packages supported</strong><span>Select Complete .tar.gz bundle in the builder. Miner Dash preserves bundled files, launches the selected entry point with its configured environment, and rolls the complete directory back if the updated miner fails to start.</span></div>
    </section>
    <div class="section-heading"><div><h2>Miner Dash community miners</h2><p>Approved definitions are refreshed automatically from the official Miner Dash custom-miner registry.</p></div><a class="button-link secondary" href="${escape(community.repository_url || "https://github.com/OBitsPlease/MinerDash-Custom-Miners")}" target="_blank" rel="noreferrer">Developer repository</a></div>
    ${community.error ? `<div class="package-warning">Registry unavailable: ${escape(community.error)}</div>` : ""}
    <div class="custom-miner-records">${communityCards || `<div class="empty simple-empty"><strong>No approved community miners published yet</strong><span>New approved entries will appear here and in Flight Sheets automatically.</span></div>`}</div>
    <div class="section-heading"><div><h2>Your custom miners</h2><p>Verified definitions available to all flight sheets.</p></div></div>
    <div class="custom-miner-records">${cards}</div>`;
  $("#custom-lab-build").addEventListener("click", openCustomMinerBuilder);
  $$(".custom-miner-manage").forEach(button => button.addEventListener("click", () => openMinerPackageDialog(button.dataset.minerId)));
  $$(".install-community-miner").forEach(button => button.addEventListener("click", async () => {
    button.disabled = true;
    try {
      const miner = await api(`/api/v1/community-miners/${encodeURIComponent(button.dataset.communityId)}`, {method: "POST"});
      await loadAll();
      showNotice(`${miner.name} is verified and available in Flight Sheets.`);
    } catch (error) {
      button.disabled = false;
      showNotice(error.message);
    }
  }));
}

function openCustomMinerBuilder() {
  const form = $("#custom-miner-builder-form");
  form.reset();
  updateCustomMinerBuilderMode();
  $("#custom-miner-builder-error").textContent = "";
  $("#custom-miner-builder-dialog").showModal();
}

function updateCustomMinerBuilderMode() {
  const form = $("#custom-miner-builder-form");
  const bundled = form.elements.package_format.value === "tar.gz";
  form.querySelectorAll(".bundle-field").forEach(field => field.classList.toggle("hidden", !bundled));
  form.elements.entry_point.required = bundled;
}

async function saveCustomMinerBuilder(event) {
  event.preventDefault();
  const form = event.target;
  const data = new FormData(form);
  const sourceURL = String(data.get("url") || "").trim();
  const binary = data.get("binary_file");
  const hasBinary = binary instanceof File && binary.size > 0;
  if (Boolean(sourceURL) === hasBinary) {
    $("#custom-miner-builder-error").textContent = "Choose either one HTTPS installation URL or one local package.";
    return;
  }
  const packageFormat = String(data.get("package_format") || "binary");
  const environment = {};
  for (const line of String(data.get("environment") || "").split("\n").map(item => item.trim()).filter(Boolean)) {
    const separator = line.indexOf("=");
    if (separator < 1) {
      $("#custom-miner-builder-error").textContent = `Environment entry "${line}" must use NAME=value.`;
      return;
    }
    environment[line.slice(0, separator).trim()] = line.slice(separator + 1);
  }
  const payload = {
    name: String(data.get("name") || "").trim(),
    version: String(data.get("version") || "").trim(),
    algorithm: String(data.get("algorithm") || "").trim(),
    binary_name: String(data.get("binary_name") || "").trim(),
    package_format: packageFormat,
    entry_point: packageFormat === "tar.gz" ? String(data.get("entry_point") || "").trim() : "",
    environment: packageFormat === "tar.gz" ? environment : {},
    stats_type: packageFormat === "tar.gz" ? String(data.get("stats_type") || "") : "",
    stats_url: packageFormat === "tar.gz" ? String(data.get("stats_url") || "").trim() : "",
    default_arguments: String(data.get("default_arguments") || "").split("\n").map(item => item.trim()).filter(Boolean)
  };
  const submit = form.querySelector('button[type="submit"]');
  submit.disabled = true;
  try {
    let saved;
    if (sourceURL) {
      saved = await api("/api/v1/miners/import-url", {
        method: "POST",
        body: JSON.stringify({...payload, url: sourceURL})
      });
    } else {
      saved = await api("/api/v1/miners", {
        method: "POST",
        body: JSON.stringify({...payload, version: payload.version || "custom", managed_binary: true})
      });
      try {
        const response = await fetch(`/api/v1/miners/${saved.id}/binary`, {
          method: "PUT",
          headers: {"Authorization": "Bearer " + state.token, "Content-Type": "application/octet-stream"},
          body: binary
        });
        if (!response.ok) throw new Error((await response.text()).trim() || response.statusText);
      } catch (error) {
        await api(`/api/v1/miners/${saved.id}`, {method: "DELETE"});
        throw error;
      }
    }
    $("#custom-miner-builder-dialog").close();
    await loadAll();
    showNotice(`${saved.name} is verified, reusable, and ready for flight sheets.`);
  } catch (error) {
    $("#custom-miner-builder-error").textContent = error.message;
  } finally {
    submit.disabled = false;
  }
}

async function installCatalogMiner(button) {
  const catalogID = button.dataset.catalogId;
  const name = button.dataset.minerName || catalogID;
  const installed = button.dataset.installedVersion;
  const latest = button.dataset.latestVersion;
  if (installed && !confirm(`Update ${name} from ${installed} to ${latest || "the latest official release"}? Assigned rigs will download the verified binary and restart onto it.`)) return;
  button.disabled = true;
  const original = button.textContent;
  button.textContent = installed ? "Updating…" : "Installing…";
  try {
    await api(`/api/v1/rig-os/miners/${encodeURIComponent(catalogID)}`, {method: "POST"});
    [state.data.miners, state.data["rig-os"]] = await Promise.all([api("/api/v1/miners"), api("/api/v1/rig-os")]);
    renderRigOS();
    showNotice(`${name} ${latest || "latest"} is verified and queued for assigned rigs.`);
  } catch (error) {
    button.disabled = false;
    button.textContent = original;
    showNotice(error.message);
  }
}

function openMinerUpdateDialog(rig, release) {
    const dialog = $("#miner-update-dialog");
    const releaseMonitored = Boolean(release.latest_version);
    dialog.dataset.catalogId = release.id;
    dialog.dataset.minerName = release.name;
    dialog.dataset.installedVersion = release.installed_version || "";
    dialog.dataset.latestVersion = release.latest_version || "";
    dialog.querySelector(".miner-update-version").textContent = releaseMonitored
      ? `${release.name} ${release.installed_version || "not installed"} → ${release.latest_version}`
      : `${release.name} ${release.installed_version || release.assignedMiner?.version || "version unknown"}`;
    dialog.querySelector(".miner-update-rig").textContent = !releaseMonitored
      ? `${rig.name} uses this miner. Automatic release monitoring is not configured for its official publisher yet.`
      : release.update_available
      ? `${rig.name} uses this miner. Click Update once and Miner Dash will download, verify, install, and roll it out automatically to every rig assigned to this miner.`
      : `${rig.name} is using the latest monitored release.`;
    dialog.querySelector(".miner-release-notes").textContent = releaseMonitored
      ? release.latest_release_notes || "The publisher did not provide release notes."
      : "Miner Dash needs the miner's official release page and verified Linux package details before automatic updates can be enabled safely.";
    configureExternalLink(dialog.querySelector(".miner-release-link"), release.latest_release_url, "Official release page unavailable");
    dialog.querySelector(".miner-update-error").textContent = "";
    const update = $("#apply-miner-update");
    update.disabled = !release.update_available;
    update.textContent = release.update_available ? "Update" : releaseMonitored ? "Miner is up to date" : "Update unavailable";
    dialog.showModal();
  }

async function applyMinerUpdateFromDialog() {
    const dialog = $("#miner-update-dialog");
    const button = $("#apply-miner-update");
    const name = dialog.dataset.minerName;
    const installed = dialog.dataset.installedVersion;
    const latest = dialog.dataset.latestVersion;
    if (!confirm(`Update ${name} from ${installed} to ${latest}? Assigned rigs will download the verified binary and restart onto it.`)) return;
    button.disabled = true;
    button.textContent = "Updating…";
    try {
      await api(`/api/v1/rig-os/miners/${encodeURIComponent(dialog.dataset.catalogId)}`, {method: "POST"});
      dialog.close();
      await loadAll({preserveWorkerUI: true});
      showNotice(`${name} ${latest} is verified and queued for assigned rigs.`);
    } catch (error) {
      button.disabled = false;
      button.textContent = "Update";
      dialog.querySelector(".miner-update-error").textContent = error.message;
  }
}

async function downloadRigOS() {
  try {
    const ticket = await api("/api/v1/rig-os/image-ticket", {method: "POST"});
    const link = document.createElement("a");
    link.href = ticket.url;
    link.download = "minerdash-rig-os.img.xz";
    link.click();
  } catch (error) { showNotice(error.message); }
}

function renderWorkers() {
  $("#page-title").textContent = "Farm overview";
  $("#page-subtitle").textContent = "Live hashrate, power, coins, temperatures, and rig controls";
  const rigs = state.data.rigs || [];
  const sheets = Object.fromEntries((state.data["flight-sheets"] || []).map(sheet => [sheet.id, sheet]));
  renderOverviewStats(rigs, sheets);
  const groups = $("#rig-groups");
  groups.replaceChildren();
  if (!rigs.length) {
    groups.innerHTML = `<div class="empty">No workers enrolled. Each installed rig will appear here as its own named card.</div>`;
    return;
  }
  const sorted = [...rigs].sort((a, b) => a.name.localeCompare(b.name));
  const sections = [
    ["GPU rigs", "Rigs reporting one or more GPUs", sorted.filter(rig => (rig.metrics?.gpus || []).length)],
    ["CPU-only rigs", "Rigs reporting CPU hardware without a GPU", sorted.filter(rig => rig.metrics?.collected_at && !(rig.metrics?.gpus || []).length)],
    ["Awaiting hardware report", "New or offline rigs that have not reported hardware yet", sorted.filter(rig => !rig.metrics?.collected_at)]
  ];
  sections.forEach(([title, description, records]) => {
    if (!records.length) return;
    const section = document.createElement("section");
    section.className = "rig-group";
    section.innerHTML = `<div class="rig-group-heading"><div><h2>${title}</h2><p>${description}</p></div><span>${records.length} rig${records.length === 1 ? "" : "s"}</span></div><div class="rig-grid"></div>`;
    const grid = section.querySelector(".rig-grid");
    records.forEach(rig => grid.append(renderRig(rig, sheets)));
    groups.append(section);
  });
}

function renderOverviewStats(rigs, sheets) {
  const gpus = rigs.flatMap(rig => rig.metrics?.gpus || []);
  const hashrates = new Map();
  const coins = new Set();
  rigs.forEach(rig => {
    const sheet = sheets[rig.desired?.flight_sheet_id];
    if (sheet?.coin) coins.add(sheet.coin);
    if (sheet?.secondary_coin) coins.add(sheet.secondary_coin);
    const miner = rig.metrics?.miner;
    if (!miner?.running || !miner.hashrate) return;
    const normalized = normalizeHashrate(miner.hashrate, miner.hashrate_unit);
    const key = `${sheet?.coin || "Mining"}|${sheet?.algorithm || miner.profile || "Hashrate"}|${normalized.unit}`;
    hashrates.set(key, (hashrates.get(key) || 0) + normalized.value);
  });
  const totalPower = rigs.reduce((total, rig) => total + estimatedRigPower(rig), 0);
  const totalDailyCost = rigs.reduce((total, rig) => total + dailyPowerCost(estimatedRigPower(rig), farmForRig(rig)?.electricity_rate_usd_per_kwh), 0);
  const baseCards = [
    ["Workers", `${rigs.filter(rig => rig.online).length} / ${rigs.length}`, "online / enrolled"],
    ["GPU devices", String(gpus.length), [...new Set(gpus.map(gpu => gpu.vendor))].join(" + ") || "No devices"],
    ["CPU mining threads", String(activeCPUThreadCount(rigs)), "active miner threads reported"],
    ["Consumption", `${totalPower.toFixed(0)} W`, `calibrated wall estimate · $${totalDailyCost.toFixed(2)} / 24h`],
    ["Coins", String(coins.size), [...coins].join(" + ") || "None assigned"]
  ];
  const hashCards = [...hashrates].map(([key, total]) => {
    const [coin, algorithm, unit] = key.split("|");
    return [coin, formatHashrate(total, unit), algorithm, coin];
  });
  $("#overview-stats").innerHTML = [...baseCards, ...hashCards].map(([label, amount, note, coin]) =>
    `<article>${coin ? coinBadge(coin, "small") : ""}<span>${escape(label)}</span><strong>${escape(amount)}</strong><small>${escape(note)}</small></article>`
  ).join("");
}

function renderRig(rig, sheets) {
  const fragment = $("#rig-template").content.cloneNode(true);
  const card = fragment.querySelector(".rig-card");
  card.dataset.rigId = rig.id;
  card.tabIndex = 0;
  card.classList.toggle("offline", !rig.online);
  card.classList.toggle("expanded", state.expandedRigIDs.has(rig.id));
  if (state.focusedRigID === rig.id) {
    state.expandedRigIDs.add(rig.id);
    card.classList.add("expanded", "focused");
    document.body.classList.add("rig-focus-active");
  }
  const m = rig.metrics || {};
  const sheet = sheets[rig.desired?.flight_sheet_id];
  fragment.querySelector("h3").textContent = rig.name;
  card.title = card.classList.contains("expanded")
    ? `Open ${rig.name} in full view`
    : `Show ${rig.name} details`;
  const advanceView = () => {
    if (!card.classList.contains("expanded")) {
      state.expandedRigIDs.add(rig.id);
      card.classList.add("expanded");
      card.title = `Open ${rig.name} in full view`;
      return;
    }
    setFocusedRig(card, true);
    loadRigPoolPerformance(card, rig);
  };
  card.addEventListener("click", event => {
    if (!card.classList.contains("focused") && !event.target.closest("input, button, a, select, textarea, summary")) {
      advanceView();
    }
  });
  card.addEventListener("keydown", event => {
    if (!card.classList.contains("focused") && event.target === card && (event.key === "Enter" || event.key === " ")) {
      event.preventDefault();
      advanceView();
    }
  });
  const isGPURig = (m.gpus || []).length > 0;
  const hardwarePending = !m.collected_at;
  card.dataset.rigKind = hardwarePending ? "pending" : isGPURig ? "gpu" : "cpu";
  const kind = fragment.querySelector(".rig-kind");
  kind.textContent = hardwarePending ? "Pending" : isGPURig ? `${m.gpus.length} GPU${m.gpus.length === 1 ? "" : "s"}` : "CPU";
  kind.classList.toggle("cpu", !isGPURig && !hardwarePending);
  kind.classList.toggle("pending", hardwarePending);
  const farm = (state.data.farms || []).find(item => item.id === rig.farm_id);
  fragment.querySelector(".rig-meta").textContent = `${m.system?.hostname || "unknown host"} · ${rig.ip_address || "IP pending"} · ${farm?.name || "No farm"} · ${rig.tags?.join(", ") || "No tags"} · rev ${m.applied_revision || 0}/${rig.desired?.revision || 0}`;
  const gpuHealthStrip = fragment.querySelector(".gpu-health-strip");
  (m.gpus || []).forEach(gpu => {
    const temperature = Number(gpu.temperature_c || 0);
    const indicator = document.createElement("span");
    const temperatureState = rig.online ? thermalClass(temperature) : "temperature-hot";
    indicator.className = `gpu-health-indicator ${temperatureState}`;
    indicator.title = rig.online
      ? `GPU ${gpu.index}: ${temperature ? value(temperature, "°C") : "temperature unavailable"}`
      : `GPU ${gpu.index}: rig offline`;
    indicator.innerHTML = `${fanIcon()}<span>GPU ${gpu.index}</span>`;
    gpuHealthStrip.append(indicator);
  });
  gpuHealthStrip.classList.toggle("hidden", !(m.gpus || []).length);
  const workloadSummary = fragment.querySelector(".rig-workload-summary");
  if (sheet?.coin) {
    const hashrate = m.miner?.hashrate
      ? formatHashrate(m.miner.hashrate, m.miner.hashrate_unit)
      : "—";
    workloadSummary.innerHTML = `${coinBadge(sheet.coin, "small")}<span><strong>${escape(sheet.coin)}</strong><small>${escape(hashrate)}</small></span>`;
    workloadSummary.classList.remove("hidden");
  }
  const status = fragment.querySelector(".status");
  status.textContent = rig.online ? "Online" : "Offline";
  status.classList.toggle("online", rig.online);
  const minerRelease = minerReleaseForRig(rig, sheets);
  const minerUpdateIndicator = fragment.querySelector(".miner-update-indicator");
  minerUpdateIndicator.classList.toggle("hidden", !minerRelease);
  minerUpdateIndicator.classList.toggle("available", Boolean(minerRelease?.update_available));
  minerUpdateIndicator.title = minerRelease?.update_available
    ? `${minerRelease.name} ${minerRelease.latest_version} is available`
    : minerRelease?.latest_version
      ? `${minerRelease.name} is up to date`
      : `${minerRelease?.name || "Miner"} update monitoring is not configured`;
  minerUpdateIndicator.addEventListener("click", event => {
    event.preventDefault();
    if (minerRelease) openMinerUpdateDialog(rig, minerRelease);
  });
  const focusedWorkspaceSelect = fragment.querySelector(".focused-workspace-select");
  const focusedWorkerSelect = fragment.querySelector(".focused-worker-select");
  const rigMiningView = miningViewForRig(rig);
  focusedWorkspaceSelect.value = rigMiningView;
  fillWorkerSelect(focusedWorkerSelect, rigMiningView, rig.id);
  focusedWorkspaceSelect.addEventListener("change", () => {
    state.view = focusedWorkspaceSelect.value;
    state.focusedRigID = "";
    state.focusedRigTab = "overview";
    closeFocusedRig();
    render();
  });
  focusedWorkerSelect.addEventListener("change", () => {
    if (focusedWorkerSelect.value) openRigFromSwitcher(focusedWorkerSelect.value);
  });
  const hottestTemperature = Math.max(Number(m.cpu_temperature_c || 0), ...(m.gpus || []).map(gpu => Number(gpu.temperature_c || 0)));
  const totalPower = estimatedRigPower(rig);
  const electricityRate = Number(farmForRig(rig)?.electricity_rate_usd_per_kwh || 0);
  const metrics = [
    ["LAN IP", rig.ip_address || "—"],
    ["Load", value(m.load_1)],
    ["Temperature", hottestTemperature ? value(hottestTemperature, "°C") : "—"],
    ["Est. wall power", totalPower ? value(totalPower, " W") : "—", totalPower ? `${formatDailyPowerCost(totalPower, electricityRate)} @ $${electricityRate.toFixed(3)}/kWh` : ""],
    ["Uptime", formatUptime(m.uptime_seconds)]
  ];
  const metricRoot = fragment.querySelector(".rig-metrics");
  metrics.forEach(([label, content, note]) => {
    const item = document.createElement("div");
    item.className = "metric";
    item.innerHTML = `<span>${escape(label)}</span><strong>${escape(content)}</strong>${note ? `<small>${escape(note)}</small>` : ""}`;
    metricRoot.append(item);
  });
  const market = state.data["coin-prices"]?.[sheet?.coin];
  const coinPriceCard = fragment.querySelector(".coin-price-card");
  if (sheet?.coin) {
    coinPriceCard.innerHTML = `${coinBadge(sheet.coin)}
      <div><small>Current ${escape(sheet.coin)} price</small><strong>${market ? formatUSD(market.usd) : "Unavailable"}</strong><span>${escape(market?.name || sheet.coin)} · CoinGecko</span></div>`;
  } else {
    coinPriceCard.classList.add("hidden");
  }
  const gpuRoot = fragment.querySelector(".gpu-list");
  (m.gpus || []).forEach(gpu => {
    const row = document.createElement("div");
    const temperature = Number(gpu.temperature_c || 0);
    row.className = `gpu ${thermalClass(temperature)}`;
    const deviceHashrate = (m.miner?.devices || []).find(device => device.index === gpu.index);
    row.title = `GPU ${gpu.index} · ${gpu.vendor || ""} ${gpu.name || ""}`;
    row.innerHTML = `
      <div class="gpu-tile-heading"><span class="gpu-tile-title">${fanIcon()}<strong>GPU ${gpu.index}</strong></span><b>${value(temperature, "°C")}</b></div>
      <span class="gpu-model">${escape(compactGPUName(gpu))}</span>
      <button type="button" class="gpu-gear secondary" title="Adjust GPU ${gpu.index} overclocks" aria-label="Adjust GPU ${gpu.index} overclocks">&#9881;</button>
      <div class="gpu-tile-stats">
        <span><small>Hash</small><strong>${deviceHashrate ? escape(formatHashrate(deviceHashrate.hashrate, deviceHashrate.unit)) : "—"}</strong></span>
        <span><small>Load</small><strong>${value(gpu.utilization_percent, "%")}</strong></span>
        <span><small>Power</small><strong>${value(gpu.power_w, " W")}</strong><em>${formatDailyPowerCost(gpu.power_w, electricityRate)}</em></span>
        <span><small>Fan</small><strong>${value(gpu.fan_percent, "%")}</strong></span>
      </div>
      <small class="gpu-clocks">${value(gpu.core_clock_mhz, " core")} · ${value(gpu.memory_clock_mhz, " mem")}</small>
      <dl class="gpu-software">
        <div><dt>Driver</dt><dd>${escape(gpu.driver || "Unavailable")}</dd></div>
        <div><dt>VBIOS</dt><dd>${escape(gpu.firmware || "Unavailable")}</dd></div>
        ${gpu.pci_address ? `<div><dt>PCI</dt><dd>${escape(gpu.pci_address)}</dd></div>` : ""}
      </dl>`;
    gpuRoot.append(row);
    row.querySelector(".gpu-gear").addEventListener("click", event => {
      event.preventDefault();
      openGPUTuning(rig, gpu.index);
    });
  });
  const cpuThreads = (m.miner?.devices || []).filter(device => device.kind === "CPU thread");
  const threadGrid = document.createElement("div");
  threadGrid.className = "cpu-thread-grid";
  cpuThreads.forEach(device => {
    const row = document.createElement("div");
    const temperature = Number(device.temperature_c || m.cpu_temperature_c || 0);
    row.className = `cpu-thread-tile ${thermalClass(temperature)}`;
    row.title = `${device.name || `CPU thread ${device.index}`} · ${temperature ? value(temperature, "°C") : "temperature unavailable"}`;
    row.innerHTML = `<span class="cpu-thread-label">${fanIcon()}<strong>T${device.index}</strong></span><span class="cpu-thread-hash">${escape(formatHashrate(device.hashrate, device.unit))}</span>${temperature ? `<small>${value(temperature, "°C")}</small>` : ""}`;
    threadGrid.append(row);
  });
  if (cpuThreads.length) gpuRoot.append(threadGrid);
  const powerModel = isGPURig
    ? `${value(gpuSensorPower(m), " W")} GPU sensors + ${value(Number(rig.desired?.power_offset_w || 0), " W")} system`
    : `${value(Number(rig.desired?.estimated_power_w || 0), " W")} fixed mining estimate`;
  fragment.querySelector(".system-detail-grid").innerHTML = [
    ["CPU", m.system?.cpu_model || "Not reported"],
    ["Memory", `${bytes(m.memory_total_bytes)} total`],
    ["Operating system", m.system?.os || "Not reported"],
    ["Kernel", m.system?.kernel || "Not reported"],
    ["Agent", m.system?.agent || "Not reported"],
    ["Network", `${m.system?.hostname || "Unknown host"} · ${rig.ip_address || "IP pending"}`],
    ["Power model", `${powerModel} · ${formatDailyPowerCost(totalPower, electricityRate)} @ $${electricityRate.toFixed(3)}/kWh`]
  ].map(([label, content]) => `<div><span>${escape(label)}</span><strong>${escape(content)}</strong></div>`).join("");
  const miner = fragment.querySelector(".miner-row");
  const minerDefinition = (state.data.miners || []).find(item => item.id === sheet?.miner_id);
  const pool = (state.data.pools || []).find(item => item.id === sheet?.pool_id);
  miner.innerHTML = m.miner?.running
    ? `<div class="miner-identity">${coinBadge(sheet?.coin, "small")}<span><small>Flight sheet</small><strong>${escape(sheet?.coin || "Mining")} · ${escape(sheet?.algorithm || "")}</strong></span></div>
       <div class="miner-detail"><small>Miner</small><strong>${escape(minerDefinition?.name || m.miner.profile || "Unknown")}</strong></div>
       <div class="miner-detail miner-hash"><small>Hashrate</small><strong>${m.miner.hashrate ? escape(formatHashrate(m.miner.hashrate, m.miner.hashrate_unit)) : "Starting…"}</strong></div>
       <div class="miner-detail"><small>Shares</small><strong>${Number(m.miner.accepted_shares || 0).toLocaleString()} / ${Number(m.miner.rejected_shares || 0).toLocaleString()}</strong></div>`
    : `<div class="miner-identity stopped"><span><small>Mining</small><strong>Miner stopped</strong></span></div>`;
  const accepted = Number(m.miner?.accepted_shares || 0);
  const rejected = Number(m.miner?.rejected_shares || 0);
  const totalShares = accepted + rejected;
  const rejectionRate = totalShares ? rejected * 100 / totalShares : 0;
  const telemetryAge = Math.max(0, (Date.now() - new Date(rig.last_heartbeat).getTime()) / 1000);
  let poolHealth = "healthy";
  let poolHealthLabel = "Healthy";
  if (!rig.online || !m.miner?.running) {
    poolHealth = "offline";
    poolHealthLabel = rig.online ? "Miner stopped" : "Rig offline";
  } else if (m.miner.last_error || rejectionRate >= 5) {
    poolHealth = "critical";
    poolHealthLabel = "Needs attention";
  } else if (!totalShares || rejectionRate >= 1 || telemetryAge > 30) {
    poolHealth = "warning";
    poolHealthLabel = !totalShares ? "Waiting for shares" : "Check connection";
  }
  const poolHealthRoot = fragment.querySelector(".pool-health");
  poolHealthRoot.classList.add(poolHealth);
  poolHealthRoot.innerHTML = `
    <div class="pool-health-heading"><strong>Pool health</strong><span>${escape(poolHealthLabel)}</span></div>
    <div class="pool-health-grid">
      <div><small>Pool</small><strong title="${escape(pool?.url || "")}">${escape(pool?.name || "Not assigned")}</strong></div>
      <div><small>Connection</small><strong>${m.miner?.running && rig.online ? "Connected" : "Disconnected"}</strong></div>
      <div><small>Hashrate</small><strong>${m.miner?.hashrate ? escape(formatHashrate(m.miner.hashrate, m.miner.hashrate_unit)) : "—"}</strong></div>
      <div><small>Accepted</small><strong>${accepted.toLocaleString()}</strong></div>
      <div><small>Rejected</small><strong>${rejected.toLocaleString()}</strong></div>
      <div><small>Reject rate</small><strong>${totalShares ? `${rejectionRate.toFixed(2)}%` : "—"}</strong></div>
      <div><small>Telemetry</small><strong>${escape(formatAge(telemetryAge))}</strong></div>
    </div>
    <p>Universal miner telemetry. Pool luck, block history, and network status require a compatible pool API.</p>`;
  fragment.querySelector(".pool-performance").innerHTML = `<div class="pool-performance-loading">Open this rig to load pool performance.</div>`;
  const minerScreenButton = fragment.querySelector(".miner-screen");
  minerScreenButton.disabled = !m.miner?.running;
  minerScreenButton.title = m.miner?.running
    ? `Show the actual MinerDash-managed ${m.miner.profile || "miner"} output`
    : "The miner is not running";
  fragment.querySelector(".close-rig-focus").addEventListener("click", event => {
    event.preventDefault();
    setFocusedRig(card, false);
  });
  fragment.querySelectorAll(".rig-tab").forEach(button => button.addEventListener("click", event => {
    event.preventDefault();
    state.focusedRigTab = button.dataset.rigTab;
    selectRigTab(card, state.focusedRigTab);
    if (state.focusedRigTab === "mining") loadRigPoolPerformance(card, rig);
    if (state.focusedRigTab === "stats" && !card.querySelector(".history-chart").dataset.loaded) {
      card.querySelector(".history-button").click();
    }
  }));
  if (card.classList.contains("focused")) selectRigTab(card, state.focusedRigTab);
  fragment.querySelector(".close-miner-screen").addEventListener("click", event => {
    event.preventDefault();
    card.querySelector(".miner-screen-panel").classList.add("hidden");
    card.querySelector(".miner-screen-message").textContent = "";
  });
  const wallet = (state.data.wallets || []).find(item => item.id === sheet?.wallet_id);
  const poolDashboardURL = expandPoolDashboardURL(pool?.dashboard_url, wallet?.address, rig.name, sheet?.coin);
  configureExternalLink(
    fragment.querySelector(".pool-link"),
    poolDashboardURL,
    poolDashboardURL ? `Open ${rig.name} on ${pool?.name || "the pool"}` : "Add a Worker page URL template to this flight sheet's pool"
  );
  setOptions(fragment.querySelector(".farm-select"), state.data.farms, rig.farm_id, "Unassigned");
  fragment.querySelector(".worker-name").value = rig.name;
  setOptions(fragment.querySelector(".flight-select"), state.data["flight-sheets"], rig.desired?.flight_sheet_id, "No flight sheet");
  setOptions(fragment.querySelector(".oc-select"), state.data["overclock-profiles"], rig.desired?.overclock_profile_id, "Stock settings");
  fragment.querySelector(".tags").value = (rig.tags || []).join(", ");
  fragment.querySelector(".estimated-power").value = rig.desired?.estimated_power_w || "";
  fragment.querySelector(".power-offset").value = rig.desired?.power_offset_w || "";
  fragment.querySelector(".max-temp").value = rig.desired?.watchdog?.max_temperature_c || "";
  fragment.querySelector(".min-hash").value = rig.desired?.watchdog?.min_hashrate || "";
  fragment.querySelector(".restart-delay").value = rig.desired?.watchdog?.restart_after_seconds || 60;
  fragment.querySelector(".reboot-failures").value = rig.desired?.watchdog?.reboot_after_failures || 3;
  fragment.querySelector(".fan-target").value = rig.desired?.autofan?.target_temperature_c || "";
  fragment.querySelector(".fan-min").value = rig.desired?.autofan?.minimum_fan_percent || "";
  fragment.querySelector(".fan-max").value = rig.desired?.autofan?.maximum_fan_percent || "";
  const driverPanel = fragment.querySelector(".driver-panel");
  const driverInventory = fragment.querySelector(".driver-inventory");
  const gpuVendors = [...new Set((m.gpus || []).map(gpu => String(gpu.vendor || "").toUpperCase()).filter(vendor => vendor === "NVIDIA" || vendor === "AMD"))];
  driverPanel.classList.toggle("hidden", gpuVendors.length === 0);
  driverInventory.innerHTML = gpuVendors.map(vendor => {
    const cards = m.gpus.filter(gpu => String(gpu.vendor || "").toUpperCase() === vendor);
    const versions = [...new Set(cards.map(gpu => gpu.driver).filter(Boolean))];
    const evidence = (rig.driver_efficiency || []).filter(item => item.vendor === vendor);
    const protectedCurrent = evidence.find(item => item.status === "current-best" && item.compared_drivers > 1);
    const betterTested = evidence.find(item => item.status === "better-tested");
    const established = evidence.find(item => item.current_samples >= 60);
    const evidenceText = protectedCurrent
      ? `Performance guard: keep ${protectedCurrent.current_driver}; it is your best locally tested driver for ${protectedCurrent.coin}.`
      : betterTested
        ? `${betterTested.best_driver} tested ${betterTested.improvement_percent.toFixed(1)}% more efficient for ${betterTested.coin} on ${compactGPUName({vendor: betterTested.vendor, name: betterTested.model})}.`
        : established
          ? `Local baseline: ${formatDriverEfficiency(established.current_efficiency, established.hashrate_unit)} from ${established.current_samples} samples.`
          : `Learning locally; at least 60 one-minute samples per driver are required for a recommendation.`;
    return `<div class="driver-vendor-row">
      <div><strong>${escape(vendor)}</strong><span>${cards.length} GPU${cards.length === 1 ? "" : "s"} · Driver ${escape(versions.join(", ") || "Unavailable")}</span><small>${escape(evidenceText)}</small></div>
      <button type="button" class="driver-update secondary" data-vendor="${escape(vendor)}"${m.driver_update_supported && !protectedCurrent ? "" : " disabled"}>${protectedCurrent ? "Current driver protected" : "Update stable driver"}</button>
    </div>`;
  }).join("");
  driverInventory.querySelectorAll(".driver-update").forEach(button => button.addEventListener("click", async event => {
    event.preventDefault();
    const vendor = button.dataset.vendor;
    if (!confirm(`Update the stable ${vendor} driver on ${rig.name}? Mining will stop and remain stopped until you reboot the rig.`)) return;
    const root = event.target.closest(".rig-card");
    try {
      await api(`/api/v1/rigs/${rig.id}/commands`, {
        method: "POST",
        body: JSON.stringify({action: "gpu-driver-update", driver_update: {vendor, channel: "stable"}})
      });
      root.querySelector(".message").textContent = `${vendor} stable driver update queued. Monitor Commands, then reboot this rig when it completes.`;
      button.disabled = true;
    } catch (error) {
      root.querySelector(".message").textContent = error.message;
    }
  }));
  const tuningButton = fragment.querySelector(".gpu-tuning");
  tuningButton.classList.toggle("hidden", !(m.gpus || []).length);
  tuningButton.addEventListener("click", event => {
    event.preventDefault();
    openGPUTuning(rig);
  });
  const smartTuneButton = fragment.querySelector(".smart-tune");
  const stopSmartTuneButton = fragment.querySelector(".stop-smart-tune");
  const smartTuneStatus = fragment.querySelector(".smart-tune-status");
  const tune = m.smart_tune;
  smartTuneButton.classList.toggle("hidden", !isGPURig || Boolean(tune?.active));
  smartTuneButton.disabled = !m.miner?.running || !rig.desired?.overclock_profile_id || !m.smart_tune_supported;
  smartTuneButton.title = smartTuneButton.disabled
    ? (m.smart_tune_supported
      ? "A running GPU miner and assigned GPU tuning profile are required"
      : "Upgrade this rig to the latest MinerDash agent first")
    : "Safely benchmark each GPU without exceeding 115 W";
  stopSmartTuneButton.classList.toggle("hidden", !tune?.active);
  if (tune) {
    const progress = tune.total_gpus ? `${tune.completed_gpus}/${tune.total_gpus}` : "";
    smartTuneStatus.classList.remove("hidden");
    smartTuneStatus.classList.toggle("complete", tune.phase === "complete");
    smartTuneStatus.classList.toggle("failed", tune.phase === "failed");
    smartTuneStatus.innerHTML = `<strong>${tune.active ? "Smart Tune running" : tune.phase === "complete" ? "Smart Tune complete" : "Smart Tune stopped"} ${escape(progress)}</strong><span>${escape(tune.message || "")}</span>`;
  }
  smartTuneButton.addEventListener("click", async event => {
    event.preventDefault();
    const root = event.target.closest(".rig-card");
    try {
      await api(`/api/v1/rigs/${rig.id}/commands`, {
        method: "POST",
        body: JSON.stringify({action: "smart-tune", smart_tune: {mode: "balanced", max_power_w: 115, max_temperature_c: 78}})
      });
      root.querySelector(".message").textContent = "Smart Tune queued. Each GPU will be tested independently for about 5-10 minutes total.";
      await loadAll({preserveWorkerUI: true});
    } catch (error) { root.querySelector(".message").textContent = error.message; }
  });
  stopSmartTuneButton.addEventListener("click", async event => {
    event.preventDefault();
    const root = event.target.closest(".rig-card");
    try {
      await api(`/api/v1/rigs/${rig.id}/commands`, {method: "POST", body: JSON.stringify({action: "stop-smart-tune"})});
      root.querySelector(".message").textContent = "Smart Tune stop requested. Original limits will be restored.";
    } catch (error) { root.querySelector(".message").textContent = error.message; }
  });
  fragment.querySelector(".save-assignment").addEventListener("click", async event => {
    event.preventDefault();
    const root = event.target.closest(".rig-card");
    const watchdogEnabled = Boolean(root.querySelector(".max-temp").value || root.querySelector(".min-hash").value);
    const autofanEnabled = Boolean(root.querySelector(".fan-target").value);
    const body = {
      name: root.querySelector(".worker-name").value.trim(),
      farm_id: root.querySelector(".farm-select").value,
      flight_sheet_id: root.querySelector(".flight-select").value,
      overclock_profile_id: root.querySelector(".oc-select").value,
      tags: root.querySelector(".tags").value.split(",").map(item => item.trim()).filter(Boolean),
      estimated_power_w: Number(root.querySelector(".estimated-power").value || 0),
      power_offset_w: Number(root.querySelector(".power-offset").value || 0),
      watchdog: watchdogEnabled ? {enabled: true, max_temperature_c: Number(root.querySelector(".max-temp").value || 0), min_hashrate: Number(root.querySelector(".min-hash").value || 0), restart_after_seconds: Number(root.querySelector(".restart-delay").value || 60), reboot_after_failures: Number(root.querySelector(".reboot-failures").value || 0)} : {enabled: false},
      autofan: autofanEnabled ? {enabled: true, target_temperature_c: Number(root.querySelector(".fan-target").value), minimum_fan_percent: Number(root.querySelector(".fan-min").value || 35), maximum_fan_percent: Number(root.querySelector(".fan-max").value || 100)} : {enabled: false}
    };
    await perform(root, () => api(`/api/v1/rigs/${rig.id}/assignment`, {method: "PUT", body: JSON.stringify(body)}), "Configuration queued");
  });
  fragment.querySelectorAll("[data-action]").forEach(button => button.addEventListener("click", async event => {
    const root = event.target.closest(".rig-card");
    const isMinerScreen = button.dataset.action === "logs";
    const message = root.querySelector(isMinerScreen ? ".miner-screen-message" : ".message");
    if (button.dataset.action === "stop" && !confirm(`Stop the miner on ${rig.name}?`)) return;
    try {
      const command = await api(`/api/v1/rigs/${rig.id}/commands`, {method: "POST", body: JSON.stringify({action: button.dataset.action})});
      message.textContent = isMinerScreen ? "Loading current miner output…" : `${button.dataset.action} command queued`;
      if (isMinerScreen) {
        await showCommandResult(rig.id, command.id, root);
        root.querySelector(".miner-screen-panel").scrollIntoView({behavior: "smooth", block: "nearest"});
      }
    } catch (error) { message.textContent = error.message; }
  }));
  const historyRoot = fragment.querySelector(".history-chart");
  const historyDate = fragment.querySelector(".history-date");
  const historyNext = fragment.querySelector(".history-next");
  const historyDownload = fragment.querySelector(".history-download");
  historyDownload.onclick = () => downloadHistoryCharts(
    historyRoot,
    rig,
    historyWindow(state.historyDate, state.historyRangeDays)
  );
  historyDate.value = state.historyDate;
  historyDate.max = localDateValue(new Date());
  card.querySelectorAll(".history-range").forEach(button => {
    button.classList.toggle("active", Number(button.dataset.days) === state.historyRangeDays);
  });
  const refreshHistoryControls = () => {
    historyNext.disabled = state.historyDate >= localDateValue(new Date());
    historyDate.value = state.historyDate;
    card.querySelectorAll(".history-range").forEach(button => {
      button.classList.toggle("active", Number(button.dataset.days) === state.historyRangeDays);
    });
  };
  const loadHistory = async () => {
    const range = historyWindow(state.historyDate, state.historyRangeDays);
    historyRoot.innerHTML = `<div class="empty simple-empty">Loading ${escape(range.label)} of telemetry…</div>`;
    historyDownload.disabled = true;
    try {
      const query = new URLSearchParams({
        since: range.since.toISOString(),
        until: range.until.toISOString(),
        max_points: "2000"
      });
      const history = await api(`/api/v1/rigs/${rig.id}/history?${query}`);
      renderHistory(historyRoot, history, rig, sheet, range);
      historyRoot.dataset.loaded = "true";
      historyDownload.disabled = !history.length;
    } catch (error) {
      historyRoot.innerHTML = `<div class="empty simple-empty">${escape(error.message)}</div>`;
    }
  };
  fragment.querySelector(".history-button").addEventListener("click", loadHistory);
  fragment.querySelector(".history-previous").addEventListener("click", () => {
    const date = new Date(`${state.historyDate}T12:00:00`);
    date.setDate(date.getDate() - state.historyRangeDays);
    state.historyDate = localDateValue(date);
    refreshHistoryControls();
    loadHistory();
  });
  historyNext.addEventListener("click", () => {
    const date = new Date(`${state.historyDate}T12:00:00`);
    date.setDate(date.getDate() + state.historyRangeDays);
    const today = localDateValue(new Date());
    state.historyDate = localDateValue(date) > today ? today : localDateValue(date);
    refreshHistoryControls();
    loadHistory();
  });
  historyDate.addEventListener("change", () => {
    if (!historyDate.value) return;
    state.historyDate = historyDate.value;
    refreshHistoryControls();
    loadHistory();
  });
  card.querySelectorAll(".history-range").forEach(button => button.addEventListener("click", () => {
    state.historyRangeDays = Number(button.dataset.days);
    refreshHistoryControls();
    loadHistory();
  }));
  refreshHistoryControls();
  fragment.querySelector(".remove-worker").addEventListener("click", async event => {
    if (!confirm(`Remove ${rig.name} and its local history from MinerDash?`)) return;
    try {
      await api(`/api/v1/rigs/${rig.id}`, {method: "DELETE"});
      await loadAll();
    } catch (error) { event.target.closest(".rig-card").querySelector(".message").textContent = error.message; }
  });
  return fragment;
}

async function loadRigPoolPerformance(card, rig) {
  const root = card.querySelector(".pool-performance");
  if (!root || root.dataset.loaded === "true" || root.dataset.loading === "true") return;
  root.dataset.loading = "true";
  root.innerHTML = `<div class="pool-performance-loading">Loading pool blocks and payments…</div>`;
  try {
    const result = await api(`/api/v1/rigs/${rig.id}/pool-stats`);
    root.dataset.loaded = "true";
    if (!result.available) {
      root.innerHTML = `<div class="pool-performance-unavailable"><strong>Pool performance unavailable</strong><span>${escape(result.message)}</span></div>`;
      return;
    }
    const periods = [["24h", "24 hours"], ["7d", "7 days"], ["all", "All time"]];
    root.innerHTML = `
      <div class="pool-performance-heading">
        <div>${coinBadge(result.coin, "small")}<span><small>POOL PERFORMANCE</small><strong>${escape(result.coin)} earnings</strong></span></div>
        <span>${escape(result.scope === "worker" ? "Worker attributed" : "Wallet totals")}</span>
      </div>
      <div class="pool-performance-grid">
        <div class="pool-performance-label"></div>
        ${periods.map(([, label]) => `<strong>${escape(label)}</strong>`).join("")}
        <span>Blocks found</span>
        ${periods.map(([key]) => `<b>${Number(result.periods[key]?.blocks || 0).toLocaleString()}</b>`).join("")}
        <span>Coins paid</span>
        ${periods.map(([key]) => `<b>${formatCoinAmount(result.periods[key]?.paid_coins)} ${escape(result.coin)}</b>`).join("")}
        <span>Coins mined</span>
        ${periods.map(([key]) => `<b>${formatCoinAmount(result.periods[key]?.mined_coins)} ${escape(result.coin)}</b>`).join("")}
        <span>Mined value</span>
        ${periods.map(([key]) => `<b>${formatUSD(result.periods[key]?.usd_value || 0)}</b>`).join("")}
      </div>
      <p>Current ${escape(result.coin)} price: ${formatUSD(result.price_usd)}. USD values use the current CoinGecko price, not historical daily prices.</p>`;
  } catch (error) {
    root.innerHTML = `<div class="pool-performance-unavailable"><strong>Pool performance error</strong><span>${escape(error.message)}</span></div>`;
  } finally {
    root.dataset.loading = "";
  }
}

function formatCoinAmount(amount) {
  return Number(amount || 0).toLocaleString(undefined, {maximumFractionDigits: 8});
}

function thermalClass(temperature) {
  return !temperature ? "temperature-unknown" : temperature >= 80 ? "temperature-hot" : temperature >= 65 ? "temperature-warm" : "temperature-cool";
}

function fanIcon() {
  return `<span class="gpu-fan" aria-hidden="true"><i></i><i></i><i></i><i></i><b></b></span>`;
}

function setFocusedRig(card, focused) {
  state.focusedRigID = focused ? card.dataset.rigId : "";
  if (focused) state.focusedRigTab = "overview";
  if (focused) {
    state.expandedRigIDs.add(card.dataset.rigId);
    card.classList.add("expanded");
  }
  card.classList.toggle("focused", focused);
  document.body.classList.toggle("rig-focus-active", focused);
  if (focused) {
    selectRigTab(card, state.focusedRigTab);
    card.scrollTop = 0;
  } else {
    card.querySelectorAll("[data-rig-panel]").forEach(panel => panel.classList.remove("hidden"));
  }
}

function selectRigTab(card, tab) {
  card.dataset.activeTab = tab;
  card.querySelectorAll(".rig-tab").forEach(button => {
    const active = button.dataset.rigTab === tab;
    button.classList.toggle("active", active);
    button.setAttribute("aria-selected", String(active));
  });
  card.querySelectorAll("[data-rig-panel]").forEach(panel => {
    panel.classList.toggle("hidden", panel.dataset.rigPanel !== tab);
  });
}

function closeFocusedRig() {
  state.focusedRigID = "";
  document.body.classList.remove("rig-focus-active");
  const card = document.querySelector(".rig-card.focused");
  card?.classList.remove("focused");
  card?.querySelectorAll("[data-rig-panel]").forEach(panel => panel.classList.remove("hidden"));
}

function configureExternalLink(link, href, title) {
  link.title = title;
  link.classList.toggle("disabled", !href);
  link.setAttribute("aria-disabled", href ? "false" : "true");
  if (href) link.href = href;
  else link.removeAttribute("href");
}

function expandPoolDashboardURL(template, wallet, worker, coin) {
  if (!template || !wallet) return "";
  try {
    const expanded = template
      .replaceAll("{WALLET}", encodeURIComponent(wallet))
      .replaceAll("{WORKER}", encodeURIComponent(worker || ""))
      .replaceAll("{COIN}", encodeURIComponent(coin || ""));
    const parsed = new URL(expanded);
    return ["http:", "https:"].includes(parsed.protocol) ? parsed.href : "";
  } catch {
    return "";
  }
}

function compactGPUName(gpu) {
  const vendor = String(gpu.vendor || "").trim();
  const name = String(gpu.name || "Unknown GPU").trim();
  return vendor && name.toLowerCase().startsWith(vendor.toLowerCase()) ? name : `${vendor} ${name}`.trim();
}

function openGPUTuning(rig, focusedGPUIndex = null) {
  const vendors = [...new Set((rig.metrics?.gpus || []).map(gpu => gpu.vendor))];
  if (vendors.length !== 1) {
    showNotice("Per-rig tuning currently requires all GPUs in the rig to use the same vendor.");
    return;
  }
  const profile = (state.data["overclock-profiles"] || []).find(item => item.id === rig.desired?.overclock_profile_id);
  const settings = Object.fromEntries((profile?.settings || []).map(setting => [String(setting.selector), setting]));
  $("#gpu-tuning-form").elements.rig_id.value = rig.id;
  $("#gpu-tuning-rows").innerHTML = (rig.metrics?.gpus || []).map(gpu => {
    const setting = settings[String(gpu.index)] || {};
    return `<fieldset data-index="${gpu.index}">
      <legend>GPU ${gpu.index} · ${escape(gpu.vendor)} ${escape(gpu.name)}</legend>
      <div class="form-grid">
        <label>Power limit W<input name="power_limit_w" type="number" min="0" value="${setting.power_limit_w || ""}" placeholder="${gpu.power_w ? Math.ceil(gpu.power_w) : ""}"></label>
        <label>Fan %<input name="fan_percent" type="number" min="0" max="100" value="${setting.fan_percent || ""}"></label>
        <label>Locked core MHz<input name="core_clock_mhz" type="number" min="0" value="${setting.core_clock_mhz || ""}"></label>
        <label>Core offset MHz<input name="core_offset_mhz" type="number" value="${setting.core_offset_mhz || ""}"></label>
        <label>Locked memory MHz<input name="memory_clock_mhz" type="number" min="0" value="${setting.memory_clock_mhz || ""}"></label>
        <label>Memory offset MHz<input name="memory_offset_mhz" type="number" value="${setting.memory_offset_mhz || ""}"></label>
      </div>
    </fieldset>`;
  }).join("");
  const presets = rig.overclock_presets || [];
  $("#gpu-preset-rows").innerHTML = presets.length ? presets.map((preset, index) => `
    <article class="gpu-preset-card">
      <div class="gpu-preset-heading"><div><strong>GPU ${preset.gpu_index} · ${escape(compactGPUName({vendor: preset.vendor, name: preset.model}))}</strong><span>${escape(preset.coin)} · ${escape(displayAlgorithm(preset.algorithm))} · driver ${escape(preset.driver)}</span></div><b>${formatDriverEfficiency(preset.efficiency, preset.hashrate_unit)}</b></div>
      <div class="gpu-preset-values">${formatTuningPreset(preset.tuning)}</div>
      <div class="gpu-preset-footer"><small>${Number(preset.samples).toLocaleString()} local one-minute samples${preset.miner ? ` · ${escape(preset.miner)}` : ""}</small><button type="button" class="import-gpu-preset" data-preset-index="${index}">Import and apply</button></div>
    </article>`).join("") : `<div class="empty simple-empty">No measured presets yet. A preset appears after at least 60 valid one-minute samples with an overclock profile applied to this GPU model and coin.</div>`;
  $("#gpu-preset-rows").querySelectorAll(".import-gpu-preset").forEach(button => button.addEventListener("click", async event => {
    event.preventDefault();
    const preset = presets[Number(button.dataset.presetIndex)];
    if (!preset || !confirm(`Apply this measured ${preset.coin} preset to GPU ${preset.gpu_index} on ${rig.name} now?`)) return;
    const settings = tuningSettingsFromDialog();
    const target = settings.find(setting => Number(setting.selector) === Number(preset.gpu_index));
    if (!target) return;
    Object.assign(target, preset.tuning, {selector: String(preset.gpu_index)});
    await applyGPUTuningSettings(rig, settings, `Imported measured preset for GPU ${preset.gpu_index} on ${rig.name}.`);
  }));
  selectGPUTuningPane("manual");
  $("#gpu-tuning-error").textContent = "";
  $("#gpu-tuning-dialog").showModal();
  if (focusedGPUIndex !== null) {
    $("#gpu-tuning-rows").querySelector(`[data-index="${focusedGPUIndex}"]`)?.scrollIntoView({block: "nearest"});
  }
}

function tuningSettingsFromDialog() {
  return [...$("#gpu-tuning-rows").querySelectorAll("fieldset")].map(row => {
    const setting = {selector: row.dataset.index};
    ["power_limit_w", "fan_percent", "core_clock_mhz", "core_offset_mhz", "memory_clock_mhz", "memory_offset_mhz"].forEach(name => {
      const raw = row.querySelector(`[name="${name}"]`).value;
      if (raw !== "") setting[name] = Number(raw);
    });
    return setting;
  });
}

function formatTuningPreset(setting) {
  const values = [
    ["Core", setting.core_clock_mhz ? `${setting.core_clock_mhz} MHz lock` : setting.core_offset_mhz ? `${setting.core_offset_mhz > 0 ? "+" : ""}${setting.core_offset_mhz} MHz` : "stock"],
    ["Memory", setting.memory_clock_mhz ? `${setting.memory_clock_mhz} MHz lock` : setting.memory_offset_mhz ? `${setting.memory_offset_mhz > 0 ? "+" : ""}${setting.memory_offset_mhz} MHz` : "stock"],
    ["Power", setting.power_limit_w ? `${setting.power_limit_w} W` : "stock"],
    ["Fan", setting.fan_percent ? `${setting.fan_percent}%` : "auto"]
  ];
  return values.map(([label, amount]) => `<span><small>${label}</small><strong>${escape(amount)}</strong></span>`).join("");
}

function selectGPUTuningPane(pane) {
  $$(".gpu-tuning-tab").forEach(button => button.classList.toggle("active", button.dataset.tuningPane === pane));
  $$("[data-tuning-panel]").forEach(panel => panel.classList.toggle("hidden", panel.dataset.tuningPanel !== pane));
  $("#gpu-tuning-form").querySelector('button[type="submit"]').classList.toggle("hidden", pane !== "manual");
}

async function applyGPUTuningSettings(rig, settings, notice) {
  const vendor = rig.metrics.gpus[0].vendor;
  const existing = (state.data["overclock-profiles"] || []).find(item => item.id === rig.desired?.overclock_profile_id && item.name === `${rig.name} GPU tuning`);
  const profilePayload = {name: `${rig.name} GPU tuning`, vendor, settings};
  try {
    const profile = await api(existing ? `/api/v1/overclock-profiles/${existing.id}` : "/api/v1/overclock-profiles", {
      method: existing ? "PUT" : "POST", body: JSON.stringify({...profilePayload, ...(existing ? {id: existing.id} : {})})
    });
    await api(`/api/v1/rigs/${rig.id}/assignment`, {
      method: "PUT",
      body: JSON.stringify({
        name: rig.name, farm_id: rig.farm_id, tags: rig.tags,
        flight_sheet_id: rig.desired?.flight_sheet_id,
        overclock_profile_id: profile.id,
        estimated_power_w: Number(rig.desired?.estimated_power_w || 0),
        power_offset_w: Number(rig.desired?.power_offset_w || 0),
        watchdog: rig.desired?.watchdog || {},
        autofan: rig.desired?.autofan || {}
      })
    });
    $("#gpu-tuning-dialog").close();
    await loadAll();
    showNotice(notice);
  } catch (error) {
    $("#gpu-tuning-error").textContent = error.message;
  }
}

async function saveGPUTuning(event) {
  event.preventDefault();
  const rigID = event.target.elements.rig_id.value;
  const rig = (state.data.rigs || []).find(item => item.id === rigID);
  if (!rig) return;
  await applyGPUTuningSettings(rig, tuningSettingsFromDialog(), `Per-GPU tuning queued for ${rig.name}.`);
}

function renderHistory(root, history, rig, sheet, range) {
  root.classList.remove("hidden");
  if (!history.length) {
    root.innerHTML = `<div class="empty simple-empty">No telemetry was recorded during this ${escape(range.label)} period.</div>`;
    return;
  }
  if ((rig.metrics?.gpus || []).length) {
    renderGPUHistory(root, history, rig, sheet, range);
    return;
  }
  const temperatures = history.map(sample => Math.max(0, ...(sample.gpus || []).map(gpu => gpu.temperature_c || 0)));
  const cpu = history.map(sample => sample.cpu_usage_percent || 0);
  const points = values => values.map((item, index) => `${history.length === 1 ? 0 : index * 100 / (history.length - 1)},${100 - Math.min(100, item)}`).join(" ");
  const estimate = Number(rig.desired?.estimated_power_w || 0);
  const powerChart = estimate > 0 ? renderMetricChart({
    title: "Estimated Total Power",
    unit: "W",
    total: true,
    series: [{label: "Estimate", values: history.map(sample => sample.miner?.running ? estimate : undefined)}]
  }, history, range) : "";
  root.innerHTML = `<div class="chart-legend"><span class="cpu-line">CPU %</span><span class="temp-line">Temperature °C</span><span>${escape(range.label)} · ${history.length.toLocaleString()} samples</span></div><svg viewBox="0 0 100 100" preserveAspectRatio="none" role="img" aria-label="${escape(range.label)} CPU and temperature history"><polyline class="cpu-series" points="${points(cpu)}"></polyline><polyline class="temp-series" points="${points(temperatures)}"></polyline></svg>${powerChart}`;
}

const gpuChartColors = ["#40a9df", "#e78bbd", "#73d36b", "#f0a33b", "#a78bfa", "#56d6c9", "#ef6b73", "#d1c759", "#8ab4f8", "#c58af9"];

function renderGPUHistory(root, history, rig, sheet, range) {
  const indices = [...new Set(history.flatMap(sample => (sample.gpus || []).map(gpu => gpu.index)))].sort((a, b) => a - b);
  const sampleGPU = (sample, index) => (sample.gpus || []).find(gpu => gpu.index === index);
  const sampleHashrate = (sample, index) => (sample.miner?.devices || []).find(device =>
    device.index === index && String(device.kind || "").toUpperCase() !== "CPU THREAD"
  );
  const algorithm = displayAlgorithm(sheet?.algorithm || rig.metrics?.miner?.profile || "Hashrate");
  const hashrateUnit = history.flatMap(sample => sample.miner?.devices || []).find(device => device.unit)?.unit ||
    rig.metrics?.miner?.hashrate_unit || "H/s";
  const charts = [
    {
      title: "Activity", unit: "%", ceiling: 100,
      series: indices.map(index => ({label: `GPU ${index}`, values: history.map(sample => sampleGPU(sample, index)?.utilization_percent)}))
    },
    {
      title: "Temperature", unit: "°C", ceiling: 100,
      series: indices.map(index => ({label: `GPU ${index}`, values: history.map(sample => sampleGPU(sample, index)?.temperature_c)}))
    },
    {
      title: "Fan", unit: "%", ceiling: 100,
      series: indices.map(index => ({label: `GPU ${index}`, values: history.map(sample => sampleGPU(sample, index)?.fan_percent)}))
    },
    {
      title: "GPU Power", unit: "W",
      series: indices.map(index => ({label: `GPU ${index}`, values: history.map(sample => sampleGPU(sample, index)?.power_w)}))
    },
    {
      title: algorithm, unit: hashrateUnit, formatValue: value => formatHashrate(value, hashrateUnit),
      series: indices.map(index => ({label: `GPU ${index}`, values: history.map(sample => sampleHashrate(sample, index)?.hashrate)}))
    },
    {
      title: "Estimated Total Power", unit: "W", total: true,
      series: [{
        label: "Total",
        values: history.map(sample => sample.gpus?.length
          ? sample.gpus.reduce((sum, gpu) => sum + Number(gpu.power_w || 0), 0) + Number(rig.desired?.power_offset_w || 0)
          : undefined)
      }]
    }
  ];
  root.innerHTML = `<div class="gpu-history-stack">${charts.map(chart => renderMetricChart(chart, history, range)).join("")}</div>`;
}

function displayAlgorithm(algorithm) {
  const normalized = String(algorithm || "Hashrate")
    .replace(/equihash/gi, "Equihash ")
    .replace(/[,_]/g, "/")
    .replace(/\s+/g, " ")
    .trim();
  return (/^\d+\/\d+$/.test(normalized) ? `Equihash ${normalized}` : normalized).toUpperCase();
}

function renderMetricChart(chart, history, range) {
  const isSample = item => typeof item === "number" && Number.isFinite(item);
  const numericValues = chart.series.flatMap(series => series.values).filter(isSample);
  const maximum = chart.ceiling || Math.max(1, ...numericValues) * 1.08;
  const width = 1000;
  const height = chart.total ? 100 : 145;
  const plotTop = 14;
  const plotBottom = height - 22;
  const plotHeight = plotBottom - plotTop;
  const x = index => history.length === 1 ? 0 : index * width / (history.length - 1);
  const y = value => plotBottom - Math.max(0, Math.min(maximum, value)) * plotHeight / maximum;
  const lines = chart.series.map((series, seriesIndex) => {
    const segments = [];
    let points = [];
    series.values.forEach((value, index) => {
      if (isSample(value)) {
        points.push(`${x(index)},${y(value)}`);
      } else if (points.length) {
        segments.push(points);
        points = [];
      }
    });
    if (points.length) segments.push(points);
    return segments.map(segment =>
      `<polyline class="gpu-series-${seriesIndex % gpuChartColors.length}" points="${segment.join(" ")}"></polyline>`
    ).join("");
  }).join("");
  const latest = chart.series.map(series => {
    const value = [...series.values].reverse().find(isSample);
    return value === undefined ? null : {
      label: series.label,
      value: chart.formatValue ? chart.formatValue(value) : `${value.toFixed(chart.unit === "W" ? 1 : 0)}${chart.unit}`
    };
  }).filter(Boolean);
  const legend = latest.map((item, index) =>
    `<span><i class="gpu-key-${index % gpuChartColors.length}"></i>${escape(item.label)} <strong>${escape(item.value)}</strong></span>`
  ).join("");
  const timeOptions = range.days === 1
    ? {hour: "2-digit", minute: "2-digit"}
    : {month: "short", day: "numeric", hour: "2-digit"};
  const firstTime = new Date(history[0].collected_at).toLocaleString([], timeOptions);
  const lastTime = new Date(history[history.length - 1].collected_at).toLocaleString([], timeOptions);
  const summary = chart.total ? latest[0]?.value : `${latest.length} GPU${latest.length === 1 ? "" : "s"}`;
  return `<section class="gpu-history-chart ${chart.total ? "total-power-chart" : ""}">
    <div class="gpu-chart-heading"><div><span>${escape(chart.title)}</span><small>${escape(range.label)} · ${history.length.toLocaleString()} samples</small></div><strong>${escape(summary || "—")}</strong></div>
    <div class="gpu-chart-legend">${legend}</div>
    <svg viewBox="0 0 ${width} ${height}" preserveAspectRatio="none" role="img" aria-label="${escape(chart.title)} history">
      <line x1="0" y1="${plotTop}" x2="${width}" y2="${plotTop}"></line>
      <line x1="0" y1="${(plotTop + plotBottom) / 2}" x2="${width}" y2="${(plotTop + plotBottom) / 2}"></line>
      <line x1="0" y1="${plotBottom}" x2="${width}" y2="${plotBottom}"></line>
      ${lines}
    </svg>
    <div class="gpu-chart-times"><span>${escape(firstTime)}</span><span>${escape(lastTime)}</span></div>
  </section>`;
  bindExpandableRows($("#custom-miners-content"), ".custom-miner-record", "custom-miner");
}

function downloadHistoryCharts(root, rig, range) {
  const charts = [...root.querySelectorAll(".gpu-history-chart")];
  const sourceSVGs = charts.length
    ? charts.map(chart => ({title: chart.querySelector(".gpu-chart-heading span")?.textContent || "Chart", svg: chart.querySelector("svg")}))
    : [{title: "Performance", svg: root.querySelector("svg")}];
  if (!sourceSVGs[0]?.svg) return;
  const width = 1060;
  let y = 70;
  const sections = sourceSVGs.map(item => {
    const viewBox = item.svg.viewBox.baseVal;
    const height = Math.max(100, viewBox.height);
    const xScale = 1000 / viewBox.width;
    const section = `<text x="30" y="${y}" fill="#f1f4f6" font-size="18" font-family="Arial" font-weight="700">${escape(item.title)}</text>
      <g transform="translate(30 ${y + 16}) scale(${xScale} 1)">${item.svg.innerHTML}</g>`;
    y += height + 70;
    return section;
  }).join("");
  const seriesStyles = gpuChartColors.map((color, index) => `.gpu-series-${index}{stroke:${color}}`).join("");
  const svgDocument = `<svg xmlns="http://www.w3.org/2000/svg" width="${width}" height="${y}" viewBox="0 0 ${width} ${y}">
    <style>line{stroke:#34404a;stroke-width:1}polyline{fill:none;stroke-width:2;stroke-linecap:round;stroke-linejoin:round}${seriesStyles}</style>
    <rect width="100%" height="100%" fill="#20262c"/>
    <text x="30" y="32" fill="#ffffff" font-size="22" font-family="Arial" font-weight="700">${escape(rig.name)} · ${escape(range.label)} telemetry</text>
    ${sections}
  </svg>`;
  const url = URL.createObjectURL(new Blob([svgDocument], {type: "image/svg+xml"}));
  const link = documentCreate("a", {
    href: url,
    download: `${rig.name.replace(/[^a-z0-9_-]+/gi, "-")}-${state.historyDate}-${range.days}d.svg`
  });
  document.body.appendChild(link);
  link.click();
  link.remove();
  setTimeout(() => URL.revokeObjectURL(url), 1000);
}

function documentCreate(tagName, properties) {
  const element = document.createElement(tagName);
  Object.assign(element, properties);
  return element;
}

async function showCommandResult(rigID, commandID, root) {
  for (let attempt = 0; attempt < 10; attempt++) {
    await new Promise(resolve => setTimeout(resolve, 1000));
    const commands = await api(`/api/v1/rigs/${rigID}/commands`);
    const command = commands.find(item => item.id === commandID);
    if (command?.status === "complete" || command?.status === "failed") {
      const liveRoot = root.isConnected ? root : document.querySelector(`.rig-card[data-rig-id="${CSS.escape(rigID)}"]`);
      if (!liveRoot) return;
      const output = liveRoot.querySelector(".log-output");
      output.textContent = cleanMinerOutput(command.output || command.error || "No log output returned");
      liveRoot.querySelector(".miner-screen-panel").classList.remove("hidden");
      liveRoot.querySelector(".miner-screen-message").textContent = command.status === "failed" ? "Unable to load miner output" : "Current MinerDash-managed miner output";
      return;
    }
  }
  const liveRoot = root.isConnected ? root : document.querySelector(`.rig-card[data-rig-id="${CSS.escape(rigID)}"]`);
  if (liveRoot) liveRoot.querySelector(".miner-screen-message").textContent = "Miner screen request is still waiting for the worker";
}

function cleanMinerOutput(output) {
  return String(output || "").replace(/\x1b\[[0-?]*[ -/]*[@-~]/g, "");
}

async function perform(root, operation, success) {
  const message = root.querySelector(".message");
  try {
    await operation();
    message.textContent = success;
    await loadAll();
  } catch (error) { message.textContent = error.message; }
}

function renderResources() {
  const definition = definitions[state.view];
  $("#page-title").textContent = definition.title;
  $("#page-subtitle").textContent = definition.description;
  $("#create-resource").classList.toggle("hidden", Boolean(definition.readonly));
  $("#create-resource").textContent = state.view === "miners" ? "Add custom miner (advanced)" : "Add new";
  const records = state.data[definition.endpoint] || [];
  const content = $("#resource-content");
  const recoveryPanel = state.view === "users" ? `<section class="recovery-panel"><h3>Offline password recovery</h3><p>Save this controller recovery code somewhere secure. It can reset any local account from the login screen.</p><div class="recovery-actions"><button id="show-recovery-code" class="secondary">Show recovery code</button><code id="recovery-code-value" class="hidden"></code></div></section>` : "";
  content.classList.toggle("table-card", !["farms", "wallets", "flight-sheets"].includes(state.view));
  content.classList.toggle("farm-dashboard", state.view === "farms");
  content.classList.toggle("wallet-grid", state.view === "wallets");
  content.classList.toggle("flight-sheet-grid", state.view === "flight-sheets");
  if (state.view === "farms") {
    renderFarms(content, records);
    return;
  }
  if (state.view === "wallets") {
    renderWallets(content, records);
    return;
  }
  if (state.view === "flight-sheets") {
    renderFlightSheets(content, records);
    return;
  }
  if (!records.length) {
    content.innerHTML = `${recoveryPanel}<div class="empty">No ${escape(definition.title.toLowerCase())} yet.${definition.readonly ? "" : " Add the first one to begin."}</div>`;
    bindRecoveryCodeButton();
    return;
  }
  const lookup = buildLookup();
  content.innerHTML = `${recoveryPanel}<div class="resource-list">${records.map((record, index) => {
    const id = record.id || `${state.view}-${index}`;
    const [primaryKey, primaryLabel] = definition.columns[0];
    const secondary = definition.columns[1];
    return `<article class="resource-row expandable-row" data-id="${escape(id)}">
      <div class="resource-row-heading"><span>${escape(primaryLabel)}</span><strong>${formatCell(primaryKey, record[primaryKey], lookup)}</strong>${secondary ? `<small>${escape(secondary[1])}: ${formatCell(secondary[0], record[secondary[0]], lookup)}</small>` : ""}</div>
      <dl class="expandable-details">${definition.columns.map(([key, label]) => `<div><dt>${escape(label)}</dt><dd>${formatCell(key, record[key], lookup)}</dd></div>`).join("")}</dl>
      ${definition.readonly ? "" : `<div class="row-actions"><button class="secondary edit-resource" data-id="${escape(record.id)}">Edit</button><button class="danger delete-resource" data-id="${escape(record.id)}">Delete</button></div>`}
    </article>`;
  }).join("")}</div>`;
  bindRecoveryCodeButton();
  bindExpandableRows(content, ".resource-row", `resource-${state.view}`);
  $$(".edit-resource").forEach(button => button.addEventListener("click", () => openResourceDialog(records.find(record => record.id === button.dataset.id))));
  $$(".delete-resource").forEach(button => button.addEventListener("click", async () => {
    const record = records.find(item => item.id === button.dataset.id);
    if (!confirm(`Delete ${record.name}?`)) return;
    try {
      await api(`/api/v1/${definition.endpoint}/${record.id}`, {method: "DELETE"});
      await loadAll();
    } catch (error) { showNotice(error.message); }
  }));
}

function renderWallets(content, wallets) {
  if (!wallets.length) {
    content.innerHTML = `<div class="empty simple-empty"><strong>No wallets saved</strong><span>Add a public payout address to use it in mining setups.</span></div>`;
    return;
  }
  const prices = state.data["coin-prices"] || {};
  const coins = [...new Set(wallets.map(wallet => wallet.coin))].sort();
  if (state.walletCoinFilter !== "all" && !coins.includes(state.walletCoinFilter)) {
    state.walletCoinFilter = "all";
  }
  const visibleWallets = state.walletCoinFilter === "all"
    ? wallets
    : wallets.filter(wallet => wallet.coin === state.walletCoinFilter);
  const filterButton = (coin, label, count) => `<button type="button" class="wallet-coin-filter ${state.walletCoinFilter === coin ? "active" : ""}" data-coin="${escape(coin)}">
    ${coin === "all" ? `<span class="wallet-all-icon">ALL</span>` : coinBadge(coin, "small")}
    <span>${escape(label)}</span><b>${count}</b>
  </button>`;
  content.innerHTML = `
    <div class="wallet-filter-bar">
      ${filterButton("all", "All wallets", wallets.length)}
      ${coins.map(coin => filterButton(coin, coin, wallets.filter(wallet => wallet.coin === coin).length)).join("")}
    </div>
    <div class="wallet-list">
      <div class="wallet-list-header"><span>Coin</span><span>Name</span><span>Address</span><span>Price</span><span></span></div>
      ${visibleWallets.map(wallet => {
    const market = prices[wallet.coin];
    return `<article class="wallet-row expandable-row" data-id="${escape(wallet.id)}">
      <div class="wallet-coin">${coinBadge(wallet.coin, "small")}<strong>${escape(wallet.coin)}</strong></div>
      <div class="wallet-name"><strong>${escape(wallet.name)}</strong><small>${escape(market?.name || wallet.coin)}</small></div>
      <div class="wallet-row-details expandable-details"><code title="${escape(wallet.address)}">${escape(wallet.address)}</code>
        <div class="wallet-row-price"><strong>${market ? formatUSD(market.usd) : "Unavailable"}</strong><small>Current USD</small></div>
      </div>
      <div class="row-actions"><button class="secondary edit-wallet" data-id="${escape(wallet.id)}">Edit</button><button class="danger delete-wallet" data-id="${escape(wallet.id)}">Delete</button></div>
    </article>`;
  }).join("")}
    </div>`;
  content.querySelectorAll(".wallet-coin-filter").forEach(button => button.addEventListener("click", () => {
    state.walletCoinFilter = button.dataset.coin;
    renderWallets(content, wallets);
  }));
  bindExpandableRows(content, ".wallet-row", "wallet");
  content.querySelectorAll(".edit-wallet").forEach(button => button.addEventListener("click", () => {
    openResourceDialog(wallets.find(wallet => wallet.id === button.dataset.id));
  }));
  content.querySelectorAll(".delete-wallet").forEach(button => button.addEventListener("click", async () => {
    const wallet = wallets.find(item => item.id === button.dataset.id);
    if (!confirm(`Delete ${wallet.name}?`)) return;
    try {
      await api(`/api/v1/wallets/${wallet.id}`, {method: "DELETE"});
      await loadAll();
    } catch (error) { showNotice(error.message); }
  }));
}

function renderFlightSheets(content, sheets) {
  if (!sheets.length) {
    content.innerHTML = `<div class="empty simple-empty"><strong>No flight sheets saved</strong><span>Add a flight sheet to connect a coin, wallet, pool, and miner.</span></div>`;
    return;
  }
  const wallets = Object.fromEntries((state.data.wallets || []).map(wallet => [wallet.id, wallet]));
  const pools = Object.fromEntries((state.data.pools || []).map(pool => [pool.id, pool]));
  const miners = Object.fromEntries((state.data.miners || []).map(miner => [miner.id, miner]));
  const sheetCoins = sheet => [...new Set([
    sheet.coin || wallets[sheet.wallet_id]?.coin,
    sheet.secondary_coin || wallets[sheet.secondary_wallet_id]?.coin
  ].filter(Boolean))];
  const coins = [...new Set(sheets.flatMap(sheetCoins))].sort();
  if (state.flightSheetCoinFilter !== "all" && !coins.includes(state.flightSheetCoinFilter)) {
    state.flightSheetCoinFilter = "all";
  }
  const visibleSheets = state.flightSheetCoinFilter === "all"
    ? sheets
    : sheets.filter(sheet => sheetCoins(sheet).includes(state.flightSheetCoinFilter));
  const filterButton = (coin, label, count) => `<button type="button" class="flight-sheet-coin-filter ${state.flightSheetCoinFilter === coin ? "active" : ""}" data-coin="${escape(coin)}">
    ${coin === "all" ? `<span class="wallet-all-icon">ALL</span>` : coinBadge(coin, "small")}
    <span>${escape(label)}</span><b>${count}</b>
  </button>`;
  const linkedResource = (label, resource, fallback, detail = "") => `<div class="flight-sheet-resource" data-label="${escape(label)}"><strong>${escape(resource?.name || fallback || "Not configured")}</strong>${detail ? `<small title="${escape(detail)}">${escape(detail)}</small>` : ""}</div>`;
  content.innerHTML = `
    <div class="flight-sheet-filter-bar">
      ${filterButton("all", "All sheets", sheets.length)}
      ${coins.map(coin => filterButton(coin, coin, sheets.filter(sheet => sheetCoins(sheet).includes(coin)).length)).join("")}
    </div>
    <div class="flight-sheet-list">
      <div class="flight-sheet-list-header"><span>Coin</span><span>Flight sheet</span><span>Wallet</span><span>Pool</span><span>Miner / algorithm</span><span></span></div>
      ${visibleSheets.map(sheet => {
    const primaryCoin = sheet.coin || wallets[sheet.wallet_id]?.coin || "—";
    const secondaryCoin = sheet.secondary_coin || wallets[sheet.secondary_wallet_id]?.coin;
    const wallet = wallets[sheet.wallet_id];
    const pool = pools[sheet.pool_id];
    const miner = miners[sheet.miner_id];
    const minerReady = Boolean(miner?.profile || (miner?.managed_binary && miner?.binary_sha256));
    return `<article class="flight-sheet-row expandable-row" data-id="${escape(sheet.id)}">
      <div class="flight-sheet-coins">${coinBadge(primaryCoin, "small")}<strong>${escape(primaryCoin)}</strong>${secondaryCoin ? `<span>+</span>${coinBadge(secondaryCoin, "small")}<strong>${escape(secondaryCoin)}</strong>` : ""}</div>
      <div class="flight-sheet-name"><strong>${escape(sheet.name)}</strong><small><span class="device-badge ${sheet.device_type?.toLowerCase()}">${escape(sheet.device_type || "Mining")}</span> workload</small></div>
      <div class="flight-sheet-details expandable-details">
        ${linkedResource("Wallet", wallet, sheet.wallet_id, wallet?.address)}
        ${linkedResource("Pool", pool, sheet.pool_id, sheet.pool_url_override || pool?.url)}
        <div class="flight-sheet-resource" data-label="Miner / algorithm"><strong>${escape(miner?.name || sheet.miner_id || "Not configured")} <span class="sheet-readiness ${minerReady ? "ready" : "setup"}">${minerReady ? "Ready" : "Setup required"}</span></strong><small>${escape([sheet.algorithm, sheet.secondary_algorithm].filter(Boolean).join(" + "))}</small></div>
      </div>
      <div class="row-actions"><button class="secondary edit-flight-sheet" data-id="${escape(sheet.id)}">Edit</button></div>
    </article>`;
  }).join("")}
    </div>`;
  content.querySelectorAll(".flight-sheet-coin-filter").forEach(button => button.addEventListener("click", () => {
    state.flightSheetCoinFilter = button.dataset.coin;
    renderFlightSheets(content, sheets);
  }));
  bindExpandableRows(content, ".flight-sheet-row", "flight-sheet");
  content.querySelectorAll(".edit-flight-sheet").forEach(button => button.addEventListener("click", () => {
    openResourceDialog(sheets.find(sheet => sheet.id === button.dataset.id));
  }));
}

function formatUSD(amount) {
  const value = Number(amount);
  if (!Number.isFinite(value)) return "Unavailable";
  const digits = value >= 1 ? 2 : value >= .01 ? 4 : 6;
  return value.toLocaleString(undefined, {style: "currency", currency: "USD", maximumFractionDigits: digits});
}

function bindRecoveryCodeButton() {
  const button = $("#show-recovery-code");
  if (!button) return;
  button.addEventListener("click", async () => {
    try {
      const result = await api("/api/v1/recovery-code");
      const output = $("#recovery-code-value");
      output.textContent = result.recovery_code;
      output.classList.remove("hidden");
      button.textContent = "Recovery code shown";
      button.disabled = true;
    } catch (error) {
      showNotice(error.message);
    }
  });
}

function renderFarms(content, farms) {
  const rigs = state.data.rigs || [];
  const sheets = Object.fromEntries((state.data["flight-sheets"] || []).map(sheet => [sheet.id, sheet]));
  const groups = farms.map(farm => ({farm, rigs: rigs.filter(rig => rig.farm_id === farm.id)}));
  const unassigned = rigs.filter(rig => !rig.farm_id);
  if (unassigned.length) groups.push({farm: {id: "", name: "Unassigned rigs", description: "Rigs that have not been assigned to a farm"}, rigs: unassigned});
  content.replaceChildren();
  if (!groups.length) {
    content.innerHTML = `<div class="empty simple-empty"><strong>No farms yet</strong><span>Add a farm, then assign rigs from each rig card.</span></div>`;
    return;
  }
  groups.forEach(({farm, rigs: farmRigs}) => {
    const gpus = farmRigs.flatMap(rig => rig.metrics?.gpus || []);
    const online = farmRigs.filter(rig => rig.online).length;
    const cpuThreads = activeCPUThreadCount(farmRigs);
    const power = farmRigs.reduce((total, rig) => total + estimatedRigPower(rig), 0);
    const electricityRate = Number(farm.electricity_rate_usd_per_kwh || 0);
    const hashrates = new Map();
    farmRigs.forEach(rig => {
      const miner = rig.metrics?.miner;
      if (!miner?.running || !miner.hashrate) return;
      const sheet = sheets[rig.desired?.flight_sheet_id];
      const normalized = normalizeHashrate(miner.hashrate, miner.hashrate_unit);
      const key = `${sheet?.coin || "Mining"}|${sheet?.algorithm || miner.profile || "Hashrate"}|${normalized.unit}`;
      hashrates.set(key, (hashrates.get(key) || 0) + normalized.value);
    });
    const card = document.createElement("article");
    card.className = "farm-card expandable-row";
    card.dataset.id = farm.id || "unassigned";
    card.innerHTML = `
      <div class="farm-card-heading">
        <div><h2>${escape(farm.name)}</h2><p>${escape(farm.description || "No description")} · ${farmRigs.length} rig${farmRigs.length === 1 ? "" : "s"} · ${online} online</p></div>
        ${farm.id ? `<div class="row-actions"><button class="secondary edit-farm" data-id="${escape(farm.id)}">Edit</button><button class="danger delete-farm" data-id="${escape(farm.id)}">Delete</button></div>` : ""}
      </div>
      <div class="farm-card-details expandable-details">
        <div class="farm-summary-grid">
          <div><span>Online</span><strong>${online} / ${farmRigs.length}</strong></div>
          <div><span>GPU devices</span><strong>${gpus.length}</strong></div>
          <div><span>CPU mining threads</span><strong>${cpuThreads}</strong></div>
          <div><span>Est. wall power</span><strong>${power.toFixed(0)} W</strong><small>${formatDailyPowerCost(power, electricityRate)} @ $${electricityRate.toFixed(3)}/kWh</small></div>
        </div>
        <div class="farm-hashrates">${hashrates.size ? [...hashrates].map(([key, total]) => {
          const [coin, algorithm, unit] = key.split("|");
          return `<span>${coinBadge(coin, "small")}<strong>${escape(coin)}</strong> ${escape(formatHashrate(total, unit))}<small>${escape(algorithm)}</small></span>`;
        }).join("") : `<span class="farm-no-hashrate">No active hashrate reported</span>`}</div>
        <div class="farm-rig-heading"><h3>Rigs</h3><span>${farmRigs.length} assigned</span></div>
        <div class="rig-grid"></div>
      </div>`;
    const grid = card.querySelector(".rig-grid");
    if (farmRigs.length) {
      [...farmRigs].sort((a, b) => a.name.localeCompare(b.name)).forEach(rig => grid.append(renderRig(rig, sheets)));
    } else {
      grid.innerHTML = `<div class="empty simple-empty">No rigs are assigned to this farm yet.</div>`;
    }
    content.append(card);
  });
  bindExpandableRows(content, ".farm-card", "farm");
  $$(".edit-farm").forEach(button => button.addEventListener("click", () => openResourceDialog(farms.find(farm => farm.id === button.dataset.id))));
  $$(".delete-farm").forEach(button => button.addEventListener("click", async () => {
    const farm = farms.find(item => item.id === button.dataset.id);
    if (!confirm(`Delete ${farm.name}?`)) return;
    try {
      await api(`/api/v1/farms/${farm.id}`, {method: "DELETE"});
      await loadAll();
    } catch (error) { showNotice(error.message); }
  }));
}

function buildLookup() {
  const result = {};
  [["wallet_id", "wallets"], ["pool_id", "pools"], ["miner_id", "miners"]].forEach(([key, collection]) => result[key] = Object.fromEntries((state.data[collection] || []).map(item => [item.id, item.name])));
  result.secondary_wallet_id = result.wallet_id;
  result.secondary_pool_id = result.pool_id;
  result.rig_id = Object.fromEntries((state.data.rigs || []).map(item => [item.id, item.name]));
  return result;
}

function formatCell(key, value, lookup) {
  if (lookup[key]) return escape(lookup[key][value] || value);
  if (key === "password") return value ? "Configured" : "Not set";
  if (key === "at" || key === "started_at") return escape(new Date(value).toLocaleString());
  if (key === "created_at") return escape(new Date(value).toLocaleString());
  if (key === "active") return value ? "Active" : "Resolved";
  if (key === "disabled") return value ? "Yes" : "No";
  if (key === "enabled") return value ? "Yes" : "No";
  if (key === "days") return escape((value || []).map(day => ["Sun","Mon","Tue","Wed","Thu","Fri","Sat"][day]).join(", "));
  if (key === "rig_ids") return escape((value || []).map(id => lookup.rig_id[id] || id).join(", "));
  if (key === "settings") {
    const setting = value?.[0] || {};
    return escape(`${setting.power_limit_w || "—"} W · ${setting.fan_percent || "auto"}% fan`);
  }
  if (key === "address" || key === "resource_id" || key === "token" || key === "fingerprint") return `<code>${escape(value)}</code>`;
  return escape(value || "—");
}

function openResourceDialog(record = {}) {
  const definition = definitions[state.view];
  $("#dialog-title").textContent = `${record.id ? "Edit" : "Add"} ${definition.title.replace(/s$/, "")}`;
  $("#dialog-help").textContent = definition.description;
  $("#form-error").textContent = "";
  const fields = $("#resource-fields");
  const detailedFields = $("#resource-detailed-fields");
  const editorTabs = $("#flight-sheet-editor-tabs");
  fields.replaceChildren();
  detailedFields.replaceChildren();
  const appendFields = (target, fieldDefinitions) => (fieldDefinitions || []).forEach(([name, label, type, required, width]) => {
    const wrapper = document.createElement("label");
    if (width) wrapper.className = width;
    wrapper.textContent = label;
    let input;
    if (type === "textarea") input = document.createElement("textarea");
    else if (type.endsWith("-select")) input = document.createElement("select");
    else { input = document.createElement("input"); input.type = type; }
    input.name = name;
    input.required = Boolean(required);
    if (name === "electricity_rate_usd_per_kwh") {
      input.min = "0";
      input.max = "100";
      input.step = "0.001";
      input.placeholder = "0.150";
    }
    populateSpecialSelect(input, type);
    const current = formValue(record, name);
    if (type === "checkbox") input.checked = Boolean(current);
    else if (Array.isArray(current) && input.multiple) [...input.options].forEach(option => option.selected = current.map(String).includes(option.value));
    else if (current !== undefined) input.value = current;
    wrapper.append(input);
    target.append(wrapper);
  });
  appendFields(fields, definition.fields);
  appendFields(detailedFields, definition.detailedFields);
  const isFlightSheet = state.view === "flight-sheets";
  if (isFlightSheet) {
    const guidance = document.createElement("p");
    guidance.className = "field-help wide";
    guidance.textContent = "Choose a saved wallet or pool, or select Add new to save one without leaving this flight sheet. Values entered here stay in place when you open Detailed miner config.";
    fields.prepend(guidance);
    configureFlightSheetResourceChoosers(fields);
    configureFlightSheetMinerChooser(fields);
    const detailedGuidance = document.createElement("p");
    detailedGuidance.className = "field-help wide";
    detailedGuidance.textContent = "Normally leave pool override blank. {WALLET} automatically resolves to the saved wallet selected in Basic setup. Add .{WORKER} only when the pool requires a worker suffix.";
    detailedFields.prepend(detailedGuidance);
  }
  editorTabs.classList.toggle("hidden", !isFlightSheet);
  detailedFields.classList.add("hidden");
  fields.classList.remove("hidden");
  editorTabs.querySelectorAll(".editor-tab").forEach(button => button.classList.toggle("active", button.dataset.editorPane === "basic"));
  $("#delete-dialog-resource").classList.toggle("hidden", !isFlightSheet || !record.id);
  $("#resource-form").dataset.id = record.id || "";
  $("#resource-dialog").showModal();
}

function configureFlightSheetResourceChoosers(fields) {
  const configurations = [
    {select: "wallet_id", marker: "__new_wallet__", label: "Add new wallet…", prefix: "new_wallet", kind: "wallet", coin: "coin"},
    {select: "pool_id", marker: "__new_pool__", label: "Add new pool…", prefix: "new_pool", kind: "pool"},
    {select: "secondary_wallet_id", marker: "__new_secondary_wallet__", label: "Add new wallet…", prefix: "new_secondary_wallet", kind: "wallet", coin: "secondary_coin"},
    {select: "secondary_pool_id", marker: "__new_secondary_pool__", label: "Add new pool…", prefix: "new_secondary_pool", kind: "pool"}
  ];
  configurations.forEach(configuration => {
    const select = fields.querySelector(`[name="${configuration.select}"]`);
    if (!select) return;
    select.add(new Option(configuration.label, configuration.marker));
    const panel = document.createElement("div");
    panel.className = "form-grid wide inline-flight-resource hidden";
    if (configuration.kind === "wallet") {
      panel.innerHTML = `
        <label>New wallet name<input name="${configuration.prefix}_name" placeholder="Example: Main ${configuration.coin === "coin" ? "wallet" : "second wallet"}"></label>
        <label>Public wallet address<input name="${configuration.prefix}_address" placeholder="Wallet address"></label>`;
    } else {
      panel.innerHTML = `
        <label>New pool name<input name="${configuration.prefix}_name" placeholder="Example: Pool name"></label>
        <label>Pool password<input name="${configuration.prefix}_password" placeholder="Usually x"></label>
        <label class="wide">Stratum pool URL<input name="${configuration.prefix}_url" placeholder="stratum+tcp://pool.example:3333"></label>`;
    }
    const summary = document.createElement("span");
    summary.className = "field-help";
    select.parentElement.append(summary);
    select.parentElement.insertAdjacentElement("afterend", panel);
    const update = event => {
      const adding = select.value === configuration.marker;
      panel.classList.toggle("hidden", !adding);
      panel.querySelectorAll("input").forEach(input => {
        input.required = adding && (configuration.kind === "wallet" || !input.name.endsWith("_password"));
      });
      const records = state.data[configuration.kind === "wallet" ? "wallets" : "pools"] || [];
      const selected = records.find(item => item.id === select.value);
      if (selected) {
        summary.textContent = configuration.kind === "wallet"
          ? `${selected.coin} · ${selected.address}`
          : selected.url;
        if (event && configuration.kind === "wallet") {
          const coinInput = fields.querySelector(`[name="${configuration.coin}"]`);
          if (coinInput) coinInput.value = selected.coin || "";
        }
      } else {
        summary.textContent = adding ? `This ${configuration.kind} will be saved and selected.` : "";
      }
    };
    select.addEventListener("change", update);
    update();
  });
}

function configureFlightSheetMinerChooser(fields) {
  const minerSelect = fields.querySelector('[name="miner_id"]');
  if (!minerSelect) return;
  const customFields = document.createElement("div");
  customFields.className = "form-grid wide custom-flight-miner-fields hidden";
  customFields.innerHTML = `
    <h3 class="wide">Custom miner</h3>
    <label>Miner name<input name="custom_name" placeholder="My custom miner"></label>
    <label>Version<input name="custom_version" placeholder="optional"></label>
    <label>Executable name<input name="custom_binary_name" placeholder="miner"></label>
    <label class="wide">Installation URL<input name="custom_url" type="url" placeholder="https://github.com/project/releases/download/version/miner.tar.gz"></label>
    <label class="wide">Wallet and worker template<input name="custom_wallet_template" value="{WALLET}.{WORKER}" placeholder="{WALLET}.{WORKER}"></label>
    <label class="wide">Extra config arguments<textarea name="custom_arguments" placeholder="One argument per line&#10;--pool&#10;{POOL}&#10;--wallet&#10;{WALLET}.{WORKER}"></textarea></label>
    <p class="field-help wide">Miner Dash downloads a direct Linux executable or .tar.gz package, extracts only the named executable, and pins its SHA-256. It never runs installer scripts.</p>`;
  fields.append(customFields);
  const updateCustomFields = () => {
    const custom = minerSelect.value === "__custom__";
    customFields.classList.toggle("hidden", !custom);
    ["custom_name", "custom_binary_name", "custom_url", "custom_wallet_template"].forEach(name => {
      customFields.querySelector(`[name="${name}"]`).required = custom;
    });
  };
  minerSelect.addEventListener("change", updateCustomFields);
  updateCustomFields();
}

function formValue(record, name) {
  if (name === "extra_arguments" || name === "default_arguments" || name === "backup_pool_urls") return (record[name] || []).join("\n");
  if (["selector", "power_limit_w", "fan_percent", "core_clock_mhz", "core_offset_mhz", "memory_clock_mhz", "memory_offset_mhz"].includes(name)) return record.settings?.[0]?.[name] ?? (name === "selector" ? "*" : "");
  return record[name];
}

function populateSpecialSelect(input, type) {
  const maps = {"wallet-select": "wallets", "pool-select": "pools", "miner-select": "miners"};
  maps["flight-select"] = "flight-sheets";
  if (type === "miner-select" && state.view === "flight-sheets") {
    input.add(new Option("Choose…", ""));
    const installed = state.data.miners || [];
    const installedCatalogIDs = new Set(installed.map(miner => miner.catalog_id).filter(Boolean));
    const installedGroup = document.createElement("optgroup");
    installedGroup.label = "Installed miners";
    installed.forEach(miner => installedGroup.append(new Option(`${miner.name}${miner.version ? ` ${miner.version}` : ""}`, miner.id)));
    if (installedGroup.children.length) input.append(installedGroup);
    const automaticGroup = document.createElement("optgroup");
    automaticGroup.label = "Available from official source";
    (state.data["rig-os"]?.miners || []).filter(miner => miner.automatic_install && !installedCatalogIDs.has(miner.id)).forEach(miner => {
      automaticGroup.append(new Option(`${miner.name}${miner.latest_version ? ` ${miner.latest_version}` : ""}`, `catalog:${miner.id}`));
    });
    if (automaticGroup.children.length) input.append(automaticGroup);
    const communityGroup = document.createElement("optgroup");
    communityGroup.label = "Miner Dash community miners";
    (state.data["community-miners"]?.miners || []).forEach(miner => {
      communityGroup.append(new Option(`${miner.name} ${miner.version}`, `community:${miner.id}`));
    });
    if (communityGroup.children.length) input.append(communityGroup);
    const unavailableGroup = document.createElement("optgroup");
    unavailableGroup.label = "Manual package required";
    (state.data["rig-os"]?.miners || []).filter(miner => !miner.automatic_install && !miner.image_included && !installedCatalogIDs.has(miner.id)).forEach(miner => {
      const option = new Option(`${miner.name} — package required`, "");
      option.disabled = true;
      unavailableGroup.append(option);
    });
    if (unavailableGroup.children.length) input.append(unavailableGroup);
    input.add(new Option("Custom miner — configure here", "__custom__"));
  } else if (maps[type]) {
    setOptions(input, state.data[maps[type]], "", "Choose…");
  }
  if (type === "vendor-select") {
    [["", "Choose…"], ["NVIDIA", "NVIDIA"], ["AMD", "AMD"]].forEach(([value, label]) => input.add(new Option(label, value)));
  }
  if (type === "device-select") {
    [["", "Choose…"], ["GPU", "GPU mining"], ["CPU", "CPU mining"]].forEach(([value, label]) => input.add(new Option(label, value)));
  }
  if (type === "action-select") {
    [["", "Choose…"], ["apply-flight-sheet", "Apply flight sheet"], ["start", "Start miner"], ["stop", "Stop miner"], ["restart", "Restart miner"], ["reboot", "Reboot worker"], ["shutdown", "Shut down worker"]].forEach(([value, label]) => input.add(new Option(label, value)));
  }
  if (type === "role-select") {
    [["", "Choose…"], ["admin", "Administrator"], ["operator", "Operator"], ["viewer", "Read-only viewer"]].forEach(([value, label]) => input.add(new Option(label, value)));
  }
  if (type === "days-select") {
    input.multiple = true;
    ["Sunday","Monday","Tuesday","Wednesday","Thursday","Friday","Saturday"].forEach((label, day) => input.add(new Option(label, String(day))));
  }
  if (type === "rig-multiselect") {
    input.multiple = true;
    (state.data.rigs || []).forEach(rig => input.add(new Option(rig.name, rig.id)));
  }
}

async function saveResource(event) {
  event.preventDefault();
  const definition = definitions[state.view];
  const formData = new FormData(event.target);
  const data = Object.fromEntries(formData.entries());
  if (state.view === "farms") data.electricity_rate_usd_per_kwh = Number(data.electricity_rate_usd_per_kwh);
  if (data.extra_arguments !== undefined) data.extra_arguments = data.extra_arguments.split("\n").map(item => item.trim()).filter(Boolean);
  if (data.default_arguments !== undefined) data.default_arguments = data.default_arguments.split("\n").map(item => item.trim()).filter(Boolean);
  if (data.backup_pool_urls !== undefined) data.backup_pool_urls = data.backup_pool_urls.split("\n").map(item => item.trim()).filter(Boolean);
  if (state.view === "overclocks") {
    const setting = {selector: data.selector || "*"};
    ["power_limit_w", "fan_percent", "core_clock_mhz", "core_offset_mhz", "memory_clock_mhz", "memory_offset_mhz"].forEach(key => {
      if (data[key] !== "") setting[key] = Number(data[key]);
      delete data[key];
    });
    delete data.selector;
    data.settings = [setting];
  }
  if (state.view === "schedules") {
    data.enabled = event.target.elements.enabled.checked;
    data.days = formData.getAll("days").map(Number);
    data.rig_ids = formData.getAll("rig_ids");
  }
  if (state.view === "users") data.disabled = event.target.elements.disabled.checked;
  let binaryFile;
  if (state.view === "miners") {
    data.managed_binary = event.target.elements.managed_binary.checked;
    binaryFile = event.target.elements.binary_file.files[0];
    delete data.binary_file;
    delete data.binary_sha256;
  }
  const id = event.target.dataset.id;
  let createdCustomMinerID = "";
  const createdResources = [];
  let saveCommitted = false;
  try {
    if (state.view === "flight-sheets") {
      await resolveInlineFlightSheetResources(data, createdResources);
    }
    if (state.view === "flight-sheets" && String(data.miner_id).startsWith("catalog:")) {
      const catalogID = String(data.miner_id).slice("catalog:".length);
      const miner = await api(`/api/v1/rig-os/miners/${encodeURIComponent(catalogID)}`, {method: "POST"});
      data.miner_id = miner.id;
    } else if (state.view === "flight-sheets" && String(data.miner_id).startsWith("community:")) {
      const communityID = String(data.miner_id).slice("community:".length);
      const miner = await api(`/api/v1/community-miners/${encodeURIComponent(communityID)}`, {method: "POST"});
      data.miner_id = miner.id;
    } else if (state.view === "flight-sheets" && data.miner_id === "__custom__") {
      const miner = await api("/api/v1/miners/import-url", {
        method: "POST",
        body: JSON.stringify({
          name: String(data.custom_name || "").trim(),
          version: String(data.custom_version || "").trim(),
          algorithm: String(data.algorithm || "").trim(),
          url: String(data.custom_url || "").trim(),
          binary_name: String(data.custom_binary_name || "").trim(),
          default_arguments: String(data.custom_arguments || "").split("\n").map(item => item.trim()).filter(Boolean)
        })
      });
      createdCustomMinerID = miner.id;
      data.miner_id = miner.id;
      data.wallet_template = String(data.custom_wallet_template || "").trim();
    }
    ["custom_name", "custom_version", "custom_binary_name", "custom_url", "custom_wallet_template", "custom_arguments"].forEach(key => delete data[key]);
    const saved = await api(`/api/v1/${definition.endpoint}${id ? `/${id}` : ""}`, {method: id ? "PUT" : "POST", body: JSON.stringify(data)});
    if (binaryFile) {
      const response = await fetch(`/api/v1/miners/${saved.id}/binary`, {method: "PUT", headers: {"Authorization": `Bearer ${state.token}`, "Content-Type": "application/octet-stream"}, body: binaryFile});
      if (!response.ok) throw new Error((await response.text()).trim() || response.statusText);
    }
    saveCommitted = true;
    $("#resource-dialog").close();
    await loadAll();
  } catch (error) {
    if (createdCustomMinerID && !saveCommitted) {
      try {
        await api(`/api/v1/miners/${createdCustomMinerID}`, {method: "DELETE"});
      } catch (cleanupError) {
        error = new Error(`${error.message} The unused custom miner could not be removed: ${cleanupError.message}`);
      }
    }
    if (!saveCommitted) {
      for (const resource of createdResources.reverse()) {
        try {
          await api(`/api/v1/${resource.endpoint}/${resource.id}`, {method: "DELETE"});
        } catch (cleanupError) {
          error = new Error(`${error.message} The new ${resource.kind} could not be removed: ${cleanupError.message}`);
        }
      }
    }
    if (saveCommitted) showNotice(`Saved, but the screen could not refresh: ${error.message}`);
    else $("#form-error").textContent = error.message;
  }
}

async function resolveInlineFlightSheetResources(data, createdResources) {
  const resources = [
    {field: "wallet_id", marker: "__new_wallet__", endpoint: "wallets", kind: "wallet", prefix: "new_wallet", coinField: "coin"},
    {field: "pool_id", marker: "__new_pool__", endpoint: "pools", kind: "pool", prefix: "new_pool"},
    {field: "secondary_wallet_id", marker: "__new_secondary_wallet__", endpoint: "wallets", kind: "wallet", prefix: "new_secondary_wallet", coinField: "secondary_coin"},
    {field: "secondary_pool_id", marker: "__new_secondary_pool__", endpoint: "pools", kind: "pool", prefix: "new_secondary_pool"}
  ];
  for (const resource of resources) {
    if (data[resource.field] !== resource.marker) continue;
    const payload = resource.kind === "wallet"
      ? {
          name: String(data[`${resource.prefix}_name`] || "").trim(),
          coin: String(data[resource.coinField] || "").trim(),
          address: String(data[`${resource.prefix}_address`] || "").trim()
        }
      : {
          name: String(data[`${resource.prefix}_name`] || "").trim(),
          url: String(data[`${resource.prefix}_url`] || "").trim(),
          password: String(data[`${resource.prefix}_password`] || "").trim()
        };
    if (!payload.name || (resource.kind === "wallet" && (!payload.coin || !payload.address)) || (resource.kind === "pool" && !payload.url)) {
      throw new Error(`Complete the new ${resource.kind} fields before saving the flight sheet.`);
    }
    const saved = await api(`/api/v1/${resource.endpoint}`, {method: "POST", body: JSON.stringify(payload)});
    data[resource.field] = saved.id;
    createdResources.push({endpoint: resource.endpoint, id: saved.id, kind: resource.kind});
  }
  resources.forEach(resource => {
    ["name", "address", "url", "password"].forEach(field => delete data[`${resource.prefix}_${field}`]);
  });
}

function setOptions(select, records = [], selected = "", emptyLabel) {
  select.replaceChildren();
  if (emptyLabel) select.add(new Option(emptyLabel, ""));
  (records || []).forEach(record => select.add(new Option(record.name, record.id)));
  select.value = selected || "";
}

function formatUptime(seconds = 0) {
  const days = Math.floor(seconds / 86400);
  const hours = Math.floor((seconds % 86400) / 3600);
  return days ? `${days}d ${hours}h` : `${hours}h`;
}

function formatAge(seconds) {
  if (!Number.isFinite(seconds)) return "Unknown";
  if (seconds < 60) return `${Math.round(seconds)}s ago`;
  if (seconds < 3600) return `${Math.round(seconds / 60)}m ago`;
  return `${(seconds / 3600).toFixed(1)}h ago`;
}

function showNotice(message) {
  const notice = $("#notice");
  notice.textContent = message;
  notice.classList.remove("hidden");
  setTimeout(() => notice.classList.add("hidden"), 6000);
}

function updateCoinPreview(input) {
  const preview = input.closest(".coin-input")?.querySelector(".coin-preview");
  if (preview) preview.innerHTML = coinBadge(input.value);
}

function setSecondaryRequired(required) {
  const form = $("#quick-sheet-form");
  ["secondary_coin", "secondary_wallet_address", "secondary_pool_url", "secondary_algorithm"].forEach(name => {
    form.elements[name].required = required;
  });
}

function toggleDualWorkload() {
  const form = $("#quick-sheet-form");
  const section = $("#secondary-workload");
  const opening = section.classList.contains("hidden");
  section.classList.toggle("hidden", !opening);
  setSecondaryRequired(opening);
  $("#toggle-dual-workload").textContent = opening ? "Remove second workload" : "+ Add CPU + GPU dual workload";
  if (opening) {
    const secondaryType = form.elements.device_type.value === "CPU" ? "GPU" : "CPU";
    form.elements.secondary_device_type.value = secondaryType;
    form.elements.secondary_device_label.value = secondaryType;
    updateCoinPreview(form.elements.secondary_coin);
  }
}

function selectSavedWallet() {
  const form = $("#quick-sheet-form");
  const wallet = (state.data.wallets || []).find(item => item.id === form.elements.wallet_choice.value);
  if (!wallet) return;
  form.elements.coin.value = wallet.coin;
  form.elements.wallet_address.value = wallet.address;
  updateCoinPreview(form.elements.coin);
}

function selectSavedPool() {
  const form = $("#quick-sheet-form");
  const pool = (state.data.pools || []).find(item => item.id === form.elements.pool_choice.value);
  if (!pool) return;
  form.elements.pool_url.value = pool.url;
  form.elements.pool_password.value = pool.password || "x";
}

$("#token-form").addEventListener("submit", async event => {
  event.preventDefault();
  $("#login-error").classList.remove("success");
  try {
    const bootstrapToken = $("#token").value.trim();
    if (bootstrapToken) {
      state.token = bootstrapToken;
    } else {
      const response = await fetch("/api/v1/login", {method: "POST", headers: {"Content-Type": "application/json"}, body: JSON.stringify({username: $("#username").value, password: $("#password").value})});
      if (!response.ok) throw new Error((await response.text()).trim() || response.statusText);
      state.token = (await response.json()).token;
    }
    sessionStorage.setItem("minerdash-token", state.token);
    await loadAll();
  } catch (error) { $("#login-error").textContent = error.message; }
});
$("#forgot-password").addEventListener("click", () => {
  const form = $("#password-recovery-form");
  form.reset();
  form.elements.username.value = $("#username").value;
  $("#password-recovery-error").textContent = "";
  $("#password-recovery-dialog").showModal();
});
$("#password-recovery-form").addEventListener("submit", async event => {
  event.preventDefault();
  const form = event.target;
  const error = $("#password-recovery-error");
  error.textContent = "";
  if (form.elements.new_password.value !== form.elements.confirm_password.value) {
    error.textContent = "The new passwords do not match.";
    return;
  }
  try {
    const response = await fetch("/api/v1/password-recovery", {
      method: "POST",
      headers: {"Content-Type": "application/json"},
      body: JSON.stringify({
        username: form.elements.username.value,
        recovery_code: form.elements.recovery_code.value,
        new_password: form.elements.new_password.value
      })
    });
    if (!response.ok) throw new Error((await response.text()).trim() || response.statusText);
    $("#password-recovery-dialog").close();
    $("#username").value = form.elements.username.value;
    $("#password").value = "";
    $("#login-error").classList.add("success");
    $("#login-error").textContent = "Password changed. Sign in with your new password.";
  } catch (requestError) {
    error.textContent = requestError.message;
  }
});
$("#close-password-recovery").addEventListener("click", () => $("#password-recovery-dialog").close());
$("#cancel-password-recovery").addEventListener("click", () => $("#password-recovery-dialog").close());
async function navigateToView(view) {
  setMobileNavigation(false);
  closeFocusedRig();
  state.view = view;
  const definition = definitions[state.view];
  if (definition && state.data[definition.endpoint] === undefined) {
    try { state.data[definition.endpoint] = await api(`/api/v1/${definition.endpoint}`); }
    catch (error) { state.data[definition.endpoint] = []; showNotice(error.message); }
  }
  render();
  closeNavigationGroups();
}

$$(".nav").forEach(button => {
  button.title = button.textContent.trim();
  button.addEventListener("click", () => navigateToView(button.dataset.view));
});
setupGroupedNavigation();
document.addEventListener("keydown", event => {
  if (event.key === "Escape" && state.focusedRigID) closeFocusedRig();
});
document.addEventListener("click", event => {
  if (navigationToggle.contains(event.target) || getComputedStyle(navigationToggle).display === "none") return;
  const bounds = navigationToggle.getBoundingClientRect();
  if (event.clientX < bounds.left || event.clientX > bounds.right || event.clientY < bounds.top || event.clientY > bounds.bottom) return;
  event.preventDefault();
  event.stopPropagation();
  setMobileNavigation(navigationToggle.getAttribute("aria-expanded") !== "true");
}, true);
document.addEventListener("click", event => {
  if (!event.target.closest?.("aside") && !event.target.closest?.("#nav-toggle")) setMobileNavigation(false);
  if (state.focusedRigID) return;
  const clickedCard = event.target.closest?.(".rig-card");
  $$(".rig-card.expanded:not(.focused)").forEach(card => {
    if (card === clickedCard) return;
    state.expandedRigIDs.delete(card.dataset.rigId);
    card.classList.remove("expanded");
    card.title = `Show ${card.querySelector("h3")?.textContent || "rig"} details`;
  });
  const clickedSurface = event.target.closest?.(".expandable-row");
  $$(".expandable-row.expanded").forEach(row => {
    if (row === clickedSurface) return;
    row.classList.remove("expanded");
    row.setAttribute("aria-expanded", "false");
    for (const key of state.expandedSurfaceIDs) {
      if (key.endsWith(`:${row.dataset.id}`)) state.expandedSurfaceIDs.delete(key);
    }
  });
});
navigationToggle.addEventListener("click", () => {
  setMobileNavigation(navigationToggle.getAttribute("aria-expanded") !== "true");
});
$("#system-update-control").addEventListener("click", async () => {
  if (state.data["system-update"]?.update_available) {
    await installSystemUpdate();
    return;
  }
  await refreshSystemUpdate({notify: true});
});
$("#refresh").addEventListener("click", () => {
  if (["workers", "gpu-mining", "cpu-mining", "farms"].includes(state.view)) refreshLiveRigs();
  else loadAll({preserveWorkerUI: true});
});
$("#mining-workspace-select").addEventListener("change", event => {
  closeFocusedRig();
  state.view = event.target.value;
  render();
});
$("#mining-worker-select").addEventListener("change", event => {
  if (event.target.value) openRigFromSwitcher(event.target.value);
});
$("#logout").addEventListener("click", async () => {
  try { await api("/api/v1/logout", {method: "POST"}); } catch (_) {}
  sessionStorage.removeItem("minerdash-token");
  state.token = "";
  location.reload();
});
$("#create-resource").addEventListener("click", () => openResourceDialog());
$("#quick-sheet-form").addEventListener("submit", saveQuickSheet);
$("#quick-sheet-form").elements.miner_choice.addEventListener("change", updateQuickMinerHelp);
$("#quick-sheet-form").elements.wallet_choice.addEventListener("change", selectSavedWallet);
$("#quick-sheet-form").elements.pool_choice.addEventListener("change", selectSavedPool);
$("#toggle-dual-workload").addEventListener("click", toggleDualWorkload);
$("#quick-sheet-form").elements.coin.addEventListener("input", event => {
  const algorithms = {QRL: "qrandomx", ZCL: "equihash192_7", XMR: "rx/0"};
  const algorithm = $("#quick-sheet-form").elements.algorithm;
  const suggestion = algorithms[event.target.value.trim().toUpperCase()];
  if (suggestion && !algorithm.value) algorithm.value = suggestion;
  updateCoinPreview(event.target);
});
$("#quick-sheet-form").elements.secondary_coin.addEventListener("input", event => updateCoinPreview(event.target));
$("#close-quick-sheet").addEventListener("click", () => $("#quick-sheet-dialog").close());
$("#cancel-quick-sheet").addEventListener("click", () => $("#quick-sheet-dialog").close());
$("#custom-miner-builder-form").addEventListener("submit", saveCustomMinerBuilder);
$("#custom-miner-builder-form").elements.package_format.addEventListener("change", updateCustomMinerBuilderMode);
$("#close-custom-miner-builder").addEventListener("click", () => $("#custom-miner-builder-dialog").close());
$("#cancel-custom-miner-builder").addEventListener("click", () => $("#custom-miner-builder-dialog").close());
$("#miner-package-form").addEventListener("submit", saveMinerPackage);
$("#close-miner-package").addEventListener("click", () => $("#miner-package-dialog").close());
$("#cancel-miner-package").addEventListener("click", () => $("#miner-package-dialog").close());
$("#close-miner-update").addEventListener("click", () => $("#miner-update-dialog").close());
$("#cancel-miner-update").addEventListener("click", () => $("#miner-update-dialog").close());
$("#apply-miner-update").addEventListener("click", applyMinerUpdateFromDialog);
$("#gpu-tuning-form").addEventListener("submit", saveGPUTuning);
$("#copy-first-gpu-tuning").addEventListener("click", () => {
  const rows = [...$("#gpu-tuning-rows").querySelectorAll("fieldset")];
  if (rows.length < 2) return;
  const names = ["power_limit_w", "fan_percent", "core_clock_mhz", "core_offset_mhz", "memory_clock_mhz", "memory_offset_mhz"];
  names.forEach(name => {
    const value = rows[0].querySelector(`[name="${name}"]`).value;
    rows.slice(1).forEach(row => row.querySelector(`[name="${name}"]`).value = value);
  });
});
$(".gpu-tuning-tabs").addEventListener("click", event => {
  const button = event.target.closest(".gpu-tuning-tab");
  if (button) selectGPUTuningPane(button.dataset.tuningPane);
});
$("#close-gpu-tuning").addEventListener("click", () => $("#gpu-tuning-dialog").close());
$("#cancel-gpu-tuning").addEventListener("click", () => $("#gpu-tuning-dialog").close());
$("#resource-form").addEventListener("submit", saveResource);
document.addEventListener("error", event => {
  if (event.target.matches?.(".coin-badge img")) event.target.remove();
}, true);
$("#flight-sheet-editor-tabs").addEventListener("click", event => {
  const button = event.target.closest(".editor-tab");
  if (!button) return;
  const detailed = button.dataset.editorPane === "detailed";
  $("#resource-fields").classList.toggle("hidden", detailed);
  $("#resource-detailed-fields").classList.toggle("hidden", !detailed);
  $$("#flight-sheet-editor-tabs .editor-tab").forEach(tab => tab.classList.toggle("active", tab === button));
});
$("#delete-dialog-resource").addEventListener("click", async () => {
  const id = $("#resource-form").dataset.id;
  const sheet = (state.data["flight-sheets"] || []).find(item => item.id === id);
  if (!sheet || !confirm(`Delete ${sheet.name}?`)) return;
  try {
    await api(`/api/v1/flight-sheets/${id}`, {method: "DELETE"});
    $("#resource-dialog").close();
    await loadAll();
  } catch (error) { $("#form-error").textContent = error.message; }
});
$("#close-dialog").addEventListener("click", () => $("#resource-dialog").close());
$("#cancel-dialog").addEventListener("click", () => $("#resource-dialog").close());
if (state.token) loadAll({silentAuthFailure: true});
setInterval(() => {
  if (state.token && ["workers", "gpu-mining", "cpu-mining", "farms"].includes(state.view)) refreshLiveRigs();
}, 10000);
setInterval(() => {
  if (state.token) refreshSystemUpdate();
}, applicationUpdatePollInterval);
