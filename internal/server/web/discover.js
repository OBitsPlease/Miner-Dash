const discoverViews = ["new-listings", "power-costs", "pow-prices", "miner-calc", "upcoming-events", "find-my-miner"];

const discoverPageMeta = {
  "new-listings": {title: "New Listings", subtitle: "Proof-of-work coins announced in the last 14 days"},
  "power-costs": {title: "Power Costs", subtitle: "Estimate what your miners cost to run"},
  "pow-prices": {title: "PoW Prices", subtitle: "Live prices, hashrate, and difficulty for mineable coins"},
  "miner-calc": {title: "Miner Calc", subtitle: "Select hardware and compare daily mining profit"},
  "upcoming-events": {title: "Upcoming Events", subtitle: "Halvings, algorithm changes, and forks"},
  "find-my-miner": {title: "Find My Miner", subtitle: "Scan your local network for mining hardware"}
};

const discoverState = {
  loading: {},
  error: {},
  data: {},
  search: {},
  sort: {"pow-prices": {key: "market_cap", direction: "desc"}},
  listingSort: "newest",
  minersOnly: false,
  scanTimer: null,
  scanCIDR: "",
  power: null,
  calc: null
};

const hashrateMultipliers = [
  ["", 1], ["k", 1e3], ["M", 1e6], ["G", 1e9], ["T", 1e12], ["P", 1e15]
];

const defaultCalcAlgorithms = [
  "SHA-256", "Scrypt", "Ethash", "Etchash", "KawPow", "RandomX", "RandomX Veil",
  "FLEX", "xelishashv3", "Autolykos", "kHeavyHash", "Blake3", "X11", "Equihash", "Octopus"
];

function escapeHTML(value) {
  return String(value ?? "")
    .replaceAll("&", "&amp;").replaceAll("<", "&lt;").replaceAll(">", "&gt;")
    .replaceAll('"', "&quot;").replaceAll("'", "&#39;");
}

function safeExternalURL(value) {
  const raw = String(value || "").trim();
  if (!/^https?:\/\//i.test(raw)) return "";
  return raw;
}

function externalLink(url, label, className = "") {
  const safe = safeExternalURL(url);
  if (!safe) return "";
  const classes = className ? ` class="${className}"` : "";
  return `<a${classes} href="${escapeHTML(safe)}" target="_blank" rel="noopener noreferrer">${escapeHTML(label)}</a>`;
}

function discoverNumber(value, digits = 2) {
  const number = Number(value);
  if (!Number.isFinite(number)) return "—";
  return number.toLocaleString(undefined, {maximumFractionDigits: digits});
}

function discoverCompact(value) {
  const number = Number(value);
  if (!Number.isFinite(number) || number === 0) return "—";
  return number.toLocaleString(undefined, {notation: "compact", maximumFractionDigits: 2});
}

function discoverMoney(value) {
  const number = Number(value);
  if (!Number.isFinite(number) || number === 0) return "—";
  return `$${discoverCompact(number)}`;
}

function discoverPrice(value) {
  const number = Number(value);
  if (!Number.isFinite(number) || number <= 0) return "—";
  const digits = number >= 1 ? 2 : number >= 0.01 ? 4 : 8;
  return `$${number.toLocaleString(undefined, {minimumFractionDigits: 2, maximumFractionDigits: digits})}`;
}

function discoverPercent(value) {
  if (value === null || value === undefined || !Number.isFinite(Number(value))) {
    return `<span class="trend-flat">—</span>`;
  }
  const number = Number(value);
  const trend = number > 0.005 ? "trend-up" : number < -0.005 ? "trend-down" : "trend-flat";
  const sign = number > 0.005 ? "+" : "";
  return `<span class="${trend}">${sign}${number.toFixed(2)}%</span>`;
}

function discoverHashrate(value, unit = "H/s") {
  const number = Number(value);
  if (!Number.isFinite(number) || number <= 0) return "—";
  return formatHashrate(number, unit || "H/s");
}

function discoverTimestamp(value) {
  if (!value) return "";
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? "" : date.toLocaleTimeString();
}

function discoverStatusLine(view, extra = "") {
  const updated = discoverState.data[view]?.updated_at;
  const stamp = discoverTimestamp(updated);
  const parts = [];
  if (extra) parts.push(extra);
  if (stamp) parts.push(`updated ${stamp}`);
  return parts.join(" · ");
}

function discoverHero(eyebrow, heading, blurb) {
  return `<div class="feature-hero"><span class="eyebrow">${escapeHTML(eyebrow)}</span><h2>${escapeHTML(heading)}</h2><p>${escapeHTML(blurb)}</p></div>`;
}

function discoverNotice(view, message) {
  return `<div class="discover-notice"><span>${escapeHTML(message)}</span><button class="secondary discover-refresh" data-view="${escapeHTML(view)}" type="button">Try again</button></div>`;
}

function discoverLoading(message) {
  return `<div class="discover-loading">${escapeHTML(message)}</div>`;
}

const discoverEndpoints = {
  "pow-prices": "/api/v1/discover/pow-prices",
  "new-listings": "/api/v1/discover/new-listings",
  "upcoming-events": "/api/v1/discover/events",
  "miner-calc": "/api/v1/discover/calc-coins"
};

async function loadDiscoverData(view, {force = false} = {}) {
  const endpoint = discoverEndpoints[view];
  if (!endpoint) return;
  if (discoverState.loading[view]) return;
  if (discoverState.data[view] && !force) return;
  discoverState.loading[view] = true;
  discoverState.error[view] = "";
  if (force) discoverState.data[view] = null;
  renderDiscoverPage();
  try {
    discoverState.data[view] = await api(endpoint);
  } catch (error) {
    discoverState.error[view] = error.message || "Request failed";
  } finally {
    discoverState.loading[view] = false;
    if (discoverViews.includes(state.view)) renderDiscoverPage();
  }
}

function renderDiscoverPage(root = $("#feature-content")) {
  if (!root || !discoverViews.includes(state.view)) return;
  const meta = discoverPageMeta[state.view];
  $("#page-title").textContent = meta.title;
  $("#page-subtitle").textContent = meta.subtitle;
  if (state.view !== "find-my-miner") stopScanPolling();
  if (state.view === "pow-prices") renderPOWPrices(root);
  else if (state.view === "new-listings") renderNewListings(root);
  else if (state.view === "power-costs") renderPowerCosts(root);
  else if (state.view === "miner-calc") renderMinerCalc(root);
  else if (state.view === "upcoming-events") renderUpcomingEvents(root);
  else if (state.view === "find-my-miner") renderFindMyMiner(root);
  root.querySelectorAll(".discover-refresh").forEach(button => {
    if (!button.dataset.view || button.dataset.view === "find-my-miner") return;
    button.addEventListener("click", () => loadDiscoverData(button.dataset.view, {force: true}));
  });
}

function discoverSearchInput(view, placeholder) {
  return `<input class="discover-search" data-view="${escapeHTML(view)}" type="search" placeholder="${escapeHTML(placeholder)}" value="${escapeHTML(discoverState.search[view] || "")}">`;
}

function bindDiscoverSearch(root, view, rerender) {
  const input = root.querySelector(`.discover-search[data-view="${view}"]`);
  if (!input) return;
  input.addEventListener("input", () => {
    discoverState.search[view] = input.value;
    rerender();
    const refreshed = $("#feature-content").querySelector(`.discover-search[data-view="${view}"]`);
    if (refreshed) {
      refreshed.focus();
      refreshed.setSelectionRange(refreshed.value.length, refreshed.value.length);
    }
  });
}

/* ------------------------------------------------------------------ */
/* PoW Prices                                                          */
/* ------------------------------------------------------------------ */

const powPriceColumns = [
  ["name", "Coin", "text"],
  ["symbol", "Symbol", "text"],
  ["algo", "Algorithm", "text"],
  ["price", "Price", "number"],
  ["change_1h", "1h", "number"],
  ["change_24h", "24h", "number"],
  ["change_7d", "7d", "number"],
  ["volume_24h", "24h Volume", "number"],
  ["market_cap", "Market Cap", "number"],
  ["network_hashrate", "Net Hashrate", "number"],
  ["pool_hashrate", "Pools Hashrate", "number"],
  ["diff_7d_change", "7d Diff", "number"],
  ["emissions_24h", "24h Emissions", "number"],
  ["pools", "Pools", "number"]
];

function sortDiscoverRows(rows, sort, columns) {
  if (!sort?.key) return rows;
  const column = columns.find(entry => entry[0] === sort.key);
  const numeric = column?.[2] === "number";
  const direction = sort.direction === "asc" ? 1 : -1;
  return [...rows].sort((left, right) => {
    const a = left[sort.key];
    const b = right[sort.key];
    if (numeric) {
      const first = Number.isFinite(Number(a)) && a !== null ? Number(a) : -Infinity;
      const second = Number.isFinite(Number(b)) && b !== null ? Number(b) : -Infinity;
      return (first - second) * direction;
    }
    return String(a ?? "").localeCompare(String(b ?? ""), undefined, {sensitivity: "base"}) * direction;
  });
}

function discoverSortHeader(view, columns) {
  const sort = discoverState.sort[view] || {};
  return columns.map(([key, label]) => {
    const active = sort.key === key ? ` class="sorted ${sort.direction}"` : "";
    const arrow = sort.key === key ? (sort.direction === "asc" ? " ▲" : " ▼") : "";
    return `<th${active}><button class="discover-sort" type="button" data-view="${escapeHTML(view)}" data-key="${escapeHTML(key)}">${escapeHTML(label)}${arrow}</button></th>`;
  }).join("");
}

function bindDiscoverSort(root, view, columns, rerender) {
  root.querySelectorAll(`.discover-sort[data-view="${view}"]`).forEach(button => {
    button.addEventListener("click", () => {
      const current = discoverState.sort[view] || {};
      const numeric = columns.find(entry => entry[0] === button.dataset.key)?.[2] === "number";
      if (current.key === button.dataset.key) {
        discoverState.sort[view] = {key: current.key, direction: current.direction === "asc" ? "desc" : "asc"};
      } else {
        discoverState.sort[view] = {key: button.dataset.key, direction: numeric ? "desc" : "asc"};
      }
      rerender();
    });
  });
}

function renderPOWPrices(root) {
  const view = "pow-prices";
  const payload = discoverState.data[view];
  if (!payload && !discoverState.error[view]) loadDiscoverData(view);
  const hero = discoverHero("MARKET DATA", "Live proof-of-work market data",
    "Prices, network hashrate, difficulty, and emissions for every mineable coin tracked by MiningPoolStats, with short-term price moves from CoinGecko.");
  if (discoverState.error[view]) {
    root.innerHTML = hero + discoverNotice(view, discoverState.error[view]);
    return;
  }
  if (!payload) {
    root.innerHTML = hero + discoverLoading("Loading market data…");
    return;
  }
  const query = (discoverState.search[view] || "").trim().toLowerCase();
  const coins = (payload.coins || []).filter(coin => !query
    || String(coin.name || "").toLowerCase().includes(query)
    || String(coin.symbol || "").toLowerCase().includes(query)
    || String(coin.algo || "").toLowerCase().includes(query));
  const sorted = sortDiscoverRows(coins, discoverState.sort[view], powPriceColumns);
  const rows = sorted.map((coin, index) => `
    <tr>
      <td class="discover-rank">${index + 1}</td>
      <td class="discover-coin"><strong>${escapeHTML(coin.name)}</strong></td>
      <td><span class="discover-symbol">${escapeHTML(coin.symbol)}</span></td>
      <td>${escapeHTML(coin.algo || "—")}</td>
      <td>${discoverPrice(coin.price)}</td>
      <td>${discoverPercent(coin.change_1h)}</td>
      <td>${discoverPercent(coin.change_24h)}</td>
      <td>${discoverPercent(coin.change_7d)}</td>
      <td>${discoverMoney(coin.volume_24h)}</td>
      <td>${discoverMoney(coin.market_cap)}</td>
      <td>${discoverHashrate(coin.network_hashrate, coin.unit)}</td>
      <td>${discoverHashrate(coin.pool_hashrate, coin.unit)}</td>
      <td>${discoverPercent(coin.diff_7d_change)}</td>
      <td>${discoverNumber(coin.emissions_24h)}</td>
      <td>${discoverNumber(coin.pools, 0)}</td>
      <td>${externalLink(coin.page_url, "Coin page", "discover-link") || "—"}</td>
    </tr>`).join("");
  root.innerHTML = `
    ${hero}
    <div class="discover-toolbar">
      ${discoverSearchInput(view, "Search name, symbol, or algorithm")}
      <span class="discover-status">${escapeHTML(discoverStatusLine(view, `${coins.length} of ${(payload.coins || []).length} coins`))}</span>
      <button class="secondary discover-refresh" data-view="${view}" type="button">Refresh</button>
    </div>
    <div class="discover-table-wrap">
      <table class="discover-table">
        <thead><tr><th>#</th>${discoverSortHeader(view, powPriceColumns)}<th>Links</th></tr></thead>
        <tbody>${rows || `<tr><td colspan="16" class="discover-empty">No coins match that search.</td></tr>`}</tbody>
      </table>
    </div>`;
  bindDiscoverSearch(root, view, () => renderPOWPrices(root));
  bindDiscoverSort(root, view, powPriceColumns, () => renderPOWPrices(root));
}

/* ------------------------------------------------------------------ */
/* New Listings                                                        */
/* ------------------------------------------------------------------ */

const listingSortOptions = [
  ["newest", "Newest first"],
  ["oldest", "Oldest first"],
  ["algo-asc", "Algorithm A–Z"],
  ["algo-desc", "Algorithm Z–A"]
];

function sortListings(listings) {
  const mode = discoverState.listingSort;
  const copy = [...listings];
  if (mode === "algo-asc" || mode === "algo-desc") {
    const direction = mode === "algo-asc" ? 1 : -1;
    return copy.sort((left, right) => String(left.algo || "zzz").localeCompare(String(right.algo || "zzz"), undefined, {sensitivity: "base"}) * direction);
  }
  const direction = mode === "oldest" ? 1 : -1;
  return copy.sort((left, right) => (Date.parse(left.listed_at || 0) - Date.parse(right.listed_at || 0)) * direction);
}

function listingConfidenceBadge(source) {
  const levels = {high: ["High", "badge-high"], medium: ["Med", "badge-medium"], low: ["Low", "badge-low"]};
  const [label, className] = levels[source] || ["Unknown", "badge-unknown"];
  return `<span class="algo-badge ${className}" title="Algorithm confidence: ${escapeHTML(label)}">${escapeHTML(label)}</span>`;
}

function renderNewListings(root) {
  const view = "new-listings";
  const payload = discoverState.data[view];
  if (!payload && !discoverState.error[view]) loadDiscoverData(view);
  const hero = discoverHero("EARLY DISCOVERY", "Proof-of-work coins announced in the last 14 days",
    "Announcement threads from the Bitcointalk alternate cryptocurrencies board, filtered to proof-of-work projects posted in the last 14 days and matched against live market data.");
  if (discoverState.error[view]) {
    root.innerHTML = hero + discoverNotice(view, discoverState.error[view]);
    return;
  }
  if (!payload) {
    root.innerHTML = hero + discoverLoading("Reading announcement threads…");
    return;
  }
  const query = (discoverState.search[view] || "").trim().toLowerCase();
  const listings = (payload.listings || []).filter(listing => !query
    || String(listing.name || "").toLowerCase().includes(query)
    || String(listing.title || "").toLowerCase().includes(query)
    || String(listing.symbol || "").toLowerCase().includes(query)
    || String(listing.algo || "").toLowerCase().includes(query));
  const rows = sortListings(listings).map((listing, index) => {
    const links = [
      externalLink(listing.website, "Site", "discover-link"),
      externalLink(listing.github, "Code", "discover-link"),
      externalLink(listing.explorer, "Explorer", "discover-link"),
      externalLink(listing.pools_url, "Pools", "discover-link")
    ].filter(Boolean).join(" ");
    return `
      <tr>
        <td class="discover-rank">${index + 1}</td>
        <td class="discover-coin" title="${escapeHTML(listing.title || listing.name)}"><strong>${escapeHTML(listing.name)}</strong>${listing.symbol ? `<span class="discover-symbol">${escapeHTML(listing.symbol)}</span>` : ""}</td>
        <td>${discoverPrice(listing.price)}</td>
        <td>${escapeHTML(listing.algo || "Unknown")} ${listingConfidenceBadge(listing.algo_source)}</td>
        <td>${escapeHTML(listing.listed || "—")}</td>
        <td>${externalLink(listing.thread, "Thread", "discover-link") || escapeHTML(listing.source || "—")}</td>
        <td>${links || "—"}</td>
      </tr>`;
  }).join("");
  const options = listingSortOptions.map(([value, label]) =>
    `<option value="${value}"${discoverState.listingSort === value ? " selected" : ""}>${escapeHTML(label)}</option>`).join("");
  root.innerHTML = `
    ${hero}
    <div class="discover-toolbar">
      ${discoverSearchInput(view, "Search coin, symbol, or algorithm")}
      <select id="listing-sort">${options}</select>
      <span class="discover-status">${escapeHTML(discoverStatusLine(view, `${listings.length} listing${listings.length === 1 ? "" : "s"}`))}</span>
      <button class="secondary discover-refresh" data-view="${view}" type="button">Refresh</button>
    </div>
    <div class="discover-table-wrap">
      <table class="discover-table">
        <thead><tr><th>#</th><th>Coin</th><th>Price</th><th>Algorithm</th><th>Listed</th><th>Thread</th><th>Links</th></tr></thead>
        <tbody>${rows || `<tr><td colspan="7" class="discover-empty">No proof-of-work coins were announced in the last 14 days.</td></tr>`}</tbody>
      </table>
    </div>
    <p class="discover-legend">
      Algorithm confidence: ${listingConfidenceBadge("high")} matched to a live coin ·
      ${listingConfidenceBadge("medium")} stated in the announcement ·
      ${listingConfidenceBadge("low")} inferred from the thread text ·
      ${listingConfidenceBadge("unknown")} not determined. Always research a new project before mining it.
    </p>`;
  bindDiscoverSearch(root, view, () => renderNewListings(root));
  root.querySelector("#listing-sort")?.addEventListener("change", event => {
    discoverState.listingSort = event.target.value;
    renderNewListings(root);
  });
}

/* ------------------------------------------------------------------ */
/* Power Costs                                                         */
/* ------------------------------------------------------------------ */

function defaultPowerInputs() {
  const rigs = state.data.rigs || [];
  const live = rigs.filter(rig => rig.online);
  let watts = 0;
  if (typeof estimatedRigPower === "function") {
    watts = live.reduce((total, rig) => total + Number(estimatedRigPower(rig) || 0), 0);
  }
  let rate = 0.15;
  const farm = (state.data.farms || []).find(entry => Number(entry.electricity_rate_usd_per_kwh) > 0);
  if (farm) rate = Number(farm.electricity_rate_usd_per_kwh);
  return {
    power: watts > 0 ? Math.round(watts / Math.max(live.length, 1)) : 2000,
    unit: "W",
    miners: watts > 0 ? live.length : 1,
    efficiency: 90,
    hours: 24,
    rate,
    cycleDays: 30,
    revenue: 0
  };
}

function powerCostResults(inputs) {
  const powerKw = inputs.unit === "kW" ? inputs.power : inputs.power / 1000;
  const efficiency = inputs.efficiency > 0 ? inputs.efficiency : 100;
  const effectiveWallKw = powerKw * inputs.miners / (efficiency / 100);
  const dailyKwh = effectiveWallKw * inputs.hours;
  const cycleKwh = dailyKwh * inputs.cycleDays;
  const yearlyKwh = dailyKwh * 365;
  const dailyCost = dailyKwh * inputs.rate;
  const dailyNet = inputs.revenue - dailyCost;
  return {
    effectiveWallKw, dailyKwh, cycleKwh, yearlyKwh,
    hourlyCost: effectiveWallKw * inputs.rate,
    dailyCost,
    cycleCost: cycleKwh * inputs.rate,
    monthlyCost: dailyCost * 30,
    yearlyCost: yearlyKwh * inputs.rate,
    dailyNet,
    yearlyNet: inputs.revenue * 365 - yearlyKwh * inputs.rate
  };
}

function powerMoney(value) {
  const number = Number(value) || 0;
  const sign = number < 0 ? "-" : "";
  return `${sign}$${Math.abs(number).toLocaleString(undefined, {minimumFractionDigits: 2, maximumFractionDigits: 2})}`;
}

function renderPowerCosts(root) {
  if (!discoverState.power) discoverState.power = defaultPowerInputs();
  const inputs = discoverState.power;
  const results = powerCostResults(inputs);
  const netClass = results.dailyNet > 0 ? "trend-up" : results.dailyNet < 0 ? "trend-down" : "trend-flat";
  root.innerHTML = `
    ${discoverHero("POWER PLANNER", "Know what your miners cost to run",
      "Model electricity usage for one miner or a whole farm, including power supply losses and runtime hours. Values are pre-filled from your online rigs and farm electricity rate.")}
    <div class="power-cost-grid">
      <article class="discover-card power-cost-card">
        <h3 class="discover-heading">Inputs</h3>
        <label>Power draw per miner
          <div class="power-input-row">
            <input id="power-draw" type="number" min="0" step="any" value="${inputs.power}">
            <select id="power-unit">
              <option value="W"${inputs.unit === "W" ? " selected" : ""}>W</option>
              <option value="kW"${inputs.unit === "kW" ? " selected" : ""}>kW</option>
            </select>
          </div>
        </label>
        <label>Number of miners<input id="power-miners" type="number" min="1" step="1" value="${inputs.miners}"></label>
        <label>Power supply efficiency (%)<input id="power-efficiency" type="number" min="1" max="100" step="any" value="${inputs.efficiency}"></label>
        <label>Hours used per day<input id="power-hours" type="number" min="0" max="24" step="any" value="${inputs.hours}"></label>
        <label>Electricity price ($/kWh)<input id="power-rate" type="number" min="0" step="any" value="${inputs.rate}"></label>
        <label>Billing cycle (days)<input id="power-cycle" type="number" min="1" step="1" value="${inputs.cycleDays}"></label>
        <label>Mining revenue per day ($)<input id="power-revenue" type="number" min="0" step="any" value="${inputs.revenue}"></label>
        <div class="power-actions">
          <button id="power-calculate" type="button">Calculate</button>
          <button id="power-reset" class="secondary" type="button">Reset</button>
        </div>
      </article>
      <article class="discover-card power-cost-card">
        <h3 class="discover-heading">Estimated costs</h3>
        <div class="power-result-grid">
          <span><small>Effective wall power</small><strong>${discoverNumber(results.effectiveWallKw, 3)} kW</strong></span>
          <span><small>Energy per day</small><strong>${discoverNumber(results.dailyKwh, 2)} kWh</strong></span>
          <span><small>Energy per cycle</small><strong>${discoverNumber(results.cycleKwh, 2)} kWh</strong></span>
          <span><small>Energy per year</small><strong>${discoverNumber(results.yearlyKwh, 2)} kWh</strong></span>
          <span><small>Cost per hour</small><strong>${powerMoney(results.hourlyCost)}</strong></span>
          <span><small>Cost per day</small><strong>${powerMoney(results.dailyCost)}</strong></span>
          <span><small>Cost per ${inputs.cycleDays}-day cycle</small><strong>${powerMoney(results.cycleCost)}</strong></span>
          <span><small>Cost per month (30d)</small><strong>${powerMoney(results.monthlyCost)}</strong></span>
          <span><small>Cost per year</small><strong>${powerMoney(results.yearlyCost)}</strong></span>
          <span><small>Net per day</small><strong class="${netClass}">${powerMoney(results.dailyNet)}</strong></span>
          <span><small>Net per year</small><strong class="${netClass}">${powerMoney(results.yearlyNet)}</strong></span>
        </div>
        <p class="discover-legend">Effective wall power = (draw × miners) ÷ efficiency. Energy per day = wall power × hours used. Cost = energy × electricity price. Net = mining revenue − electricity cost.</p>
      </article>
    </div>`;
  const read = () => {
    inputs.power = Number($("#power-draw").value) || 0;
    inputs.unit = $("#power-unit").value;
    inputs.miners = Math.max(Number($("#power-miners").value) || 1, 1);
    inputs.efficiency = Number($("#power-efficiency").value) || 100;
    inputs.hours = Number($("#power-hours").value) || 0;
    inputs.rate = Number($("#power-rate").value) || 0;
    inputs.cycleDays = Math.max(Number($("#power-cycle").value) || 1, 1);
    inputs.revenue = Number($("#power-revenue").value) || 0;
    renderPowerCosts(root);
  };
  root.querySelectorAll(".power-cost-card input, .power-cost-card select").forEach(field => {
    field.addEventListener("change", read);
  });
  root.querySelector("#power-calculate")?.addEventListener("click", read);
  root.querySelector("#power-reset")?.addEventListener("click", () => {
    discoverState.power = defaultPowerInputs();
    renderPowerCosts(root);
  });
}

/* ------------------------------------------------------------------ */
/* Miner Calc                                                          */
/* ------------------------------------------------------------------ */

function defaultCalcInputs() {
  const defaults = defaultPowerInputs();
  return {
    rate: defaults.rate,
    watts: defaults.power * defaults.miners,
    rigs: {},
    added: []
  };
}

function calcAlgorithms(coins) {
  const counts = new Map();
  coins.forEach(coin => {
    const algo = String(coin.algorithm || "").trim();
    if (algo) counts.set(algo, (counts.get(algo) || 0) + 1);
  });
  return [...new Set([...defaultCalcAlgorithms, ...counts.keys()])].sort((left, right) => left.localeCompare(right, undefined, {sensitivity: "base"}));
}

function calcUnitFor(coins, algorithm) {
  const match = coins.find(coin => coin.algorithm === algorithm);
  return match?.unit || "H/s";
}

function calcHashrateBase(entry) {
  const multiplier = hashrateMultipliers.find(([prefix]) => prefix === entry.prefix)?.[1] || 1;
  return (Number(entry.value) || 0) * multiplier;
}

function renderMinerCalc(root) {
  const view = "miner-calc";
  const payload = discoverState.data[view];
  if (!payload && !discoverState.error[view]) loadDiscoverData(view);
  const hero = discoverHero("PROFIT CALCULATOR", "Estimate earnings for your hashrate",
    "Enter the hashrate you can deliver on each algorithm and Miner Dash estimates daily coins, revenue, power cost, and profit using live network statistics.");
  if (discoverState.error[view]) {
    root.innerHTML = hero + discoverNotice(view, discoverState.error[view]);
    return;
  }
  if (!payload) {
    root.innerHTML = hero + discoverLoading("Loading network statistics…");
    return;
  }
  if (!discoverState.calc) discoverState.calc = defaultCalcInputs();
  const inputs = discoverState.calc;
  const coins = payload.coins || [];
  const available = calcAlgorithms(coins);
  const active = [...new Set([...defaultCalcAlgorithms.filter(algo => available.includes(algo)), ...inputs.added])];
  const rows = active.map(algorithm => {
    const entry = inputs.rigs[algorithm] || {value: "", prefix: "M"};
    const unit = calcUnitFor(coins, algorithm);
    const options = hashrateMultipliers.map(([prefix]) =>
      `<option value="${prefix}"${entry.prefix === prefix ? " selected" : ""}>${prefix}${unit}</option>`).join("");
    return `
      <label class="calc-rig-row">
        <span>${escapeHTML(algorithm)}</span>
        <input type="number" min="0" step="any" data-algo="${escapeHTML(algorithm)}" value="${escapeHTML(entry.value)}" placeholder="0">
        <select data-algo-unit="${escapeHTML(algorithm)}">${options}</select>
      </label>`;
  }).join("");
  const addOptions = available.filter(algorithm => !active.includes(algorithm))
    .map(algorithm => `<option value="${escapeHTML(algorithm)}">${escapeHTML(algorithm)}</option>`).join("");

  const powerCost = (Number(inputs.watts) || 0) / 1000 * 24 * (Number(inputs.rate) || 0);
  const entered = active.filter(algorithm => calcHashrateBase(inputs.rigs[algorithm] || {}) > 0);
  const results = coins
    .filter(coin => entered.includes(coin.algorithm) && coin.network_hashrate > 0 && coin.network_coins_per_day > 0)
    .map(coin => {
      const userHashrate = calcHashrateBase(inputs.rigs[coin.algorithm]);
      // Your share of a network can never exceed the whole network, even when an
      // upstream feed reports a stale or impossibly small network hashrate.
      const share = Math.min(1, userHashrate / coin.network_hashrate);
      const coinsPerDay = share * coin.network_coins_per_day;
      const revenue = coinsPerDay * (Number(coin.price_usd) || 0);
      return {coin, coinsPerDay, revenue, profit: revenue - powerCost};
    })
    .sort((left, right) => right.profit - left.profit);
  const resultRows = results.map(({coin, coinsPerDay, revenue, profit}) => {
    const profitClass = profit > 0.005 ? "trend-up" : profit < -0.005 ? "trend-down" : "trend-flat";
    return `
      <tr>
        <td class="discover-coin"><strong>${escapeHTML(coin.name)}</strong><span class="discover-symbol">${escapeHTML(coin.tag)}</span>${coin.is_new ? `<span class="algo-badge badge-new">NEW</span>` : ""}</td>
        <td>${escapeHTML(coin.algorithm)}</td>
        <td>${discoverNumber(coinsPerDay, 6)}</td>
        <td>${discoverPrice(coin.price_usd)}</td>
        <td>${powerMoney(revenue)}</td>
        <td>${powerMoney(powerCost)}</td>
        <td class="${profitClass}"><strong>${powerMoney(profit)}</strong></td>
        <td>${discoverNumber(coin.block_reward, 4)}</td>
        <td>${coin.block_time > 0 ? `${discoverNumber(coin.block_time, 0)}s` : "—"}</td>
      </tr>`;
  }).join("");

  root.innerHTML = `
    ${hero}
    <div class="calc-layout">
      <div class="discover-card calc-sidebar">
        <h3 class="discover-heading">Your hashrate</h3>
        <div class="calc-rig-list">${rows}</div>
        <div class="calc-add-row">
          <select id="calc-add-algo"${addOptions ? "" : " disabled"}>${addOptions || `<option>All algorithms added</option>`}</select>
          <button id="calc-add" class="secondary" type="button"${addOptions ? "" : " disabled"}>Add</button>
        </div>
        <h3 class="discover-heading">Power</h3>
        <label>Electricity price ($/kWh)<input id="calc-rate" type="number" min="0" step="any" value="${inputs.rate}"></label>
        <label>Total power draw (W)<input id="calc-watts" type="number" min="0" step="any" value="${inputs.watts}"></label>
        <div class="power-actions">
          <button id="calc-reset" class="secondary" type="button">Reset</button>
          <button class="secondary discover-refresh" data-view="${view}" type="button">Refresh data</button>
        </div>
      </div>
      <section class="discover-card calc-results">
        <h3 class="discover-heading">Estimated daily results</h3>
        <p class="discover-status">${escapeHTML(discoverStatusLine(view, `${coins.length} coins tracked · power cost ${powerMoney(powerCost)}/day`))}</p>
        <div class="discover-table-wrap">
          <table class="discover-table">
            <thead><tr><th>Coin</th><th>Algorithm</th><th>Est. coins/day</th><th>Price</th><th>Revenue/day</th><th>Power/day</th><th>Profit/day</th><th>Block reward</th><th>Block time</th></tr></thead>
            <tbody>${resultRows || `<tr><td colspan="9" class="discover-empty">Enter a hashrate for at least one algorithm to see estimates.</td></tr>`}</tbody>
          </table>
        </div>
        <p class="discover-legend">Estimates assume your hashrate runs for a full day against the current network hashrate and price. Actual results vary with luck, difficulty changes, and pool fees.</p>
      </section>
    </div>`;

  root.querySelectorAll(".calc-rig-row input").forEach(field => field.addEventListener("change", () => {
    const algorithm = field.dataset.algo;
    const current = inputs.rigs[algorithm] || {prefix: "M"};
    inputs.rigs[algorithm] = {value: field.value, prefix: current.prefix};
    renderMinerCalc(root);
  }));
  root.querySelectorAll(".calc-rig-row select").forEach(field => field.addEventListener("change", () => {
    const algorithm = field.dataset.algoUnit;
    const current = inputs.rigs[algorithm] || {value: ""};
    inputs.rigs[algorithm] = {value: current.value, prefix: field.value};
    renderMinerCalc(root);
  }));
  root.querySelector("#calc-add")?.addEventListener("click", () => {
    const algorithm = root.querySelector("#calc-add-algo")?.value;
    if (algorithm && !inputs.added.includes(algorithm)) inputs.added.push(algorithm);
    renderMinerCalc(root);
  });
  root.querySelector("#calc-rate")?.addEventListener("change", event => {
    inputs.rate = Number(event.target.value) || 0;
    renderMinerCalc(root);
  });
  root.querySelector("#calc-watts")?.addEventListener("change", event => {
    inputs.watts = Number(event.target.value) || 0;
    renderMinerCalc(root);
  });
  root.querySelector("#calc-reset")?.addEventListener("click", () => {
    discoverState.calc = defaultCalcInputs();
    renderMinerCalc(root);
  });
}

/* ------------------------------------------------------------------ */
/* Upcoming Events                                                     */
/* ------------------------------------------------------------------ */

function eventTypeBadge(type) {
  const classes = {halving: "badge-halving", algo: "badge-algo", fork: "badge-fork"};
  return `<span class="event-badge ${classes[type] || "badge-other"}">${escapeHTML(type || "event")}</span>`;
}

function eventCard(event, past) {
  const countdown = past ? `${discoverNumber(event.days, 0)}d ago` : `in ${discoverNumber(event.days, 0)}d`;
  const name = externalLink(event.coin_url, event.name || event.symbol || "Coin", "discover-link") || escapeHTML(event.name || "Coin");
  // Remote coin art is blocked by the dashboard's content security policy, so a
  // local symbol badge is drawn instead.
  const badge = String(event.symbol || event.name || "?").trim().slice(0, 2).toUpperCase();
  return `
    <article class="event-card${past ? " event-past" : ""}">
      <header>
        <span class="event-icon">${escapeHTML(badge)}</span>
        <div>
          <strong>${name}</strong>
          ${event.symbol ? `<span class="discover-symbol">${escapeHTML(event.symbol)}</span>` : ""}
        </div>
        ${eventTypeBadge(event.event_type)}
      </header>
      <div class="event-meta">
        <span class="event-countdown">${escapeHTML(countdown)}</span>
        ${event.date ? `<span>${escapeHTML(event.date)}</span>` : ""}
        ${event.block_info ? `<span>${escapeHTML(event.block_info)}</span>` : ""}
      </div>
      ${event.description ? `<p>${escapeHTML(event.description)}</p>` : ""}
    </article>`;
}

function renderUpcomingEvents(root) {
  const view = "upcoming-events";
  const payload = discoverState.data[view];
  if (!payload && !discoverState.error[view]) loadDiscoverData(view);
  const hero = discoverHero("NETWORK CALENDAR", "Plan around network events",
    "Halvings, algorithm changes, and forks for mineable networks, sourced from the MiningPoolStats calendar. Reward changes can swing profitability overnight.");
  if (discoverState.error[view]) {
    root.innerHTML = hero + discoverNotice(view, discoverState.error[view]);
    return;
  }
  if (!payload) {
    root.innerHTML = hero + discoverLoading("Loading the network calendar…");
    return;
  }
  const query = (discoverState.search[view] || "").trim().toLowerCase();
  const matches = event => !query
    || String(event.name || "").toLowerCase().includes(query)
    || String(event.symbol || "").toLowerCase().includes(query)
    || String(event.event_type || "").toLowerCase().includes(query);
  const upcoming = (payload.upcoming || []).filter(matches);
  const past = (payload.past || []).filter(matches);
  const stamp = discoverTimestamp(payload.updated_at);
  root.innerHTML = `
    ${hero}
    <div class="discover-toolbar">
      ${discoverSearchInput(view, "Search coin, symbol, or event type")}
      <span class="discover-status">${upcoming.length} upcoming · ${past.length} past${stamp ? ` · updated ${escapeHTML(stamp)}` : ""}</span>
      <button class="secondary discover-refresh" data-view="${view}" type="button">Refresh</button>
    </div>
    <div class="event-columns">
      <section>
        <h3 class="discover-heading">Upcoming</h3>
        <div class="event-list">${upcoming.map(event => eventCard(event, false)).join("") || `<div class="discover-empty">No upcoming events match that search.</div>`}</div>
      </section>
      <section>
        <h3 class="discover-heading">Recently passed</h3>
        <div class="event-list">${past.map(event => eventCard(event, true)).join("") || `<div class="discover-empty">No past events match that search.</div>`}</div>
      </section>
    </div>`;
  bindDiscoverSearch(root, view, () => renderUpcomingEvents(root));
}

/* ------------------------------------------------------------------ */
/* Find My Miner                                                       */
/* ------------------------------------------------------------------ */

function stopScanPolling() {
  if (discoverState.scanTimer) {
    clearInterval(discoverState.scanTimer);
    discoverState.scanTimer = null;
  }
}

function startScanPolling() {
  if (discoverState.scanTimer) return;
  discoverState.scanTimer = setInterval(async () => {
    if (state.view !== "find-my-miner") { stopScanPolling(); return; }
    try {
      const status = await api("/api/v1/discover/scan");
      discoverState.data.scan = status;
      if (!status.running) stopScanPolling();
      renderFindMyMiner();
    } catch (error) {
      stopScanPolling();
      discoverState.error["find-my-miner"] = error.message || "Scan status is unavailable";
      renderFindMyMiner();
    }
  }, 1500);
}

async function loadScanNetworks() {
  if (discoverState.data.networks || discoverState.loading["find-my-miner"]) return;
  discoverState.loading["find-my-miner"] = true;
  try {
    const payload = await api("/api/v1/discover/networks");
    discoverState.data.networks = payload.networks || [];
    if (!discoverState.scanCIDR) {
      discoverState.scanCIDR = discoverState.data.networks[0]?.cidr || "";
    }
  } catch (error) {
    discoverState.data.networks = [];
    discoverState.error["find-my-miner"] = error.message || "Network list is unavailable";
  }
  try {
    discoverState.data.scan = await api("/api/v1/discover/scan");
    if (discoverState.data.scan.running) startScanPolling();
  } catch { /* an idle controller has no scan state yet */ }
  discoverState.loading["find-my-miner"] = false;
  if (state.view === "find-my-miner") renderFindMyMiner();
}

async function startNetworkScan() {
  discoverState.error["find-my-miner"] = "";
  try {
    discoverState.data.scan = await api("/api/v1/discover/scan", {
      method: "POST",
      body: JSON.stringify({cidr: discoverState.scanCIDR})
    });
    startScanPolling();
  } catch (error) {
    discoverState.error["find-my-miner"] = error.message || "The scan could not be started";
  }
  renderFindMyMiner();
}

async function stopNetworkScan() {
  try {
    discoverState.data.scan = await api("/api/v1/discover/scan/stop", {method: "POST"});
  } catch (error) {
    discoverState.error["find-my-miner"] = error.message || "The scan could not be stopped";
  }
  stopScanPolling();
  renderFindMyMiner();
}

function scanConfidenceBadge(device) {
  const classes = {confirmed: "badge-high", likely: "badge-medium", possible: "badge-low"};
  return `<span class="algo-badge ${classes[device.confidence] || "badge-unknown"}" title="${escapeHTML((device.reasons || []).join(", "))}">${escapeHTML(device.confidence || "unknown")}</span>`;
}

function renderFindMyMiner(root = $("#feature-content")) {
  if (!root) return;
  if (!discoverState.data.networks) {
    loadScanNetworks();
  }
  const networks = discoverState.data.networks || [];
  const scan = discoverState.data.scan || {devices: [], scanned: 0, total: 0, running: false};
  const query = (discoverState.search["find-my-miner"] || "").trim().toLowerCase();
  const devices = (scan.devices || []).filter(device => {
    if (discoverState.minersOnly && !device.is_miner) return false;
    if (!query) return true;
    return [device.ip, device.hostname, device.platform, device.model, device.mac]
      .some(field => String(field || "").toLowerCase().includes(query));
  });
  const options = networks.map(network =>
    `<option value="${escapeHTML(network.cidr)}"${discoverState.scanCIDR === network.cidr ? " selected" : ""}>${escapeHTML(network.name)} — ${escapeHTML(network.cidr)} (${network.hosts} hosts)</option>`).join("");
  const percent = scan.total > 0 ? Math.min(Math.round(scan.scanned / scan.total * 100), 100) : 0;
  const minerCount = (scan.devices || []).filter(device => device.is_miner).length;
  const rows = devices.map(device => {
    const statusClass = device.enrolled_rig_id ? "scan-enrolled" : device.is_miner ? "scan-miner" : "scan-other";
    const statusLabel = device.enrolled_rig_id ? "Enrolled" : device.is_miner ? "Miner" : "Device";
    return `
      <tr>
        <td><span class="scan-status ${statusClass}">${statusLabel}</span></td>
        <td><strong>${escapeHTML(device.ip)}</strong></td>
        <td>${escapeHTML(device.hostname || "—")}</td>
        <td>${escapeHTML(device.platform || "—")}</td>
        <td>${escapeHTML(device.model || "—")}</td>
        <td>${scanConfidenceBadge(device)}</td>
        <td class="scan-mac">${escapeHTML(device.mac || "—")}</td>
        <td>${(device.open_ports || []).join(", ") || "—"}</td>
        <td>${externalLink(device.management_url, "Open", "discover-link") || "—"}</td>
      </tr>`;
  }).join("");
  const error = discoverState.error["find-my-miner"];
  root.innerHTML = `
    ${discoverHero("NETWORK DISCOVERY", "Find mining hardware on your network",
      "Miner Dash sweeps your local subnet with ARP and TCP connection probes — no elevated privileges and no ICMP — then fingerprints each responder to highlight mining hardware and controllers you have not enrolled yet.")}
    ${error ? discoverNotice("find-my-miner", error) : ""}
    <div class="discover-toolbar scan-toolbar">
      <select id="scan-network"${networks.length ? "" : " disabled"}>${options || `<option>No private IPv4 network detected</option>`}</select>
      <button id="scan-start" type="button"${scan.running || !networks.length ? " disabled" : ""}>${scan.running ? "Scanning…" : "Scan network"}</button>
      <button id="scan-stop" class="secondary" type="button"${scan.running ? "" : " disabled"}>Stop</button>
      ${discoverSearchInput("find-my-miner", "Search IP, hostname, or model")}
      <label class="scan-filter"><input id="scan-miners-only" type="checkbox"${discoverState.minersOnly ? " checked" : ""}> Miners only</label>
    </div>
    <div class="scan-progress">
      <div class="scan-progress-bar"><span class="scan-progress-fill"></span></div>
      <span class="discover-status">${scan.running ? "Scanning" : scan.finished_at ? "Finished" : "Idle"} · ${scan.scanned || 0} of ${scan.total || 0} hosts · ${minerCount} miner${minerCount === 1 ? "" : "s"} found${scan.cidr ? ` · ${escapeHTML(scan.cidr)}` : ""}</span>
    </div>
    ${scan.error ? `<div class="discover-notice"><span>${escapeHTML(scan.error)}</span></div>` : ""}
    <div class="discover-table-wrap">
      <table class="discover-table">
        <thead><tr><th>Status</th><th>IP address</th><th>Hostname</th><th>Platform</th><th>Model</th><th>Confidence</th><th>MAC</th><th>Open ports</th><th>Login</th></tr></thead>
        <tbody>${rows || `<tr><td colspan="9" class="discover-empty">${scan.running ? "Scanning the network…" : "Run a scan to discover devices on your network."}</td></tr>`}</tbody>
      </table>
    </div>`;
  const progressFill = root.querySelector(".scan-progress-fill");
  // Content-Security-Policy blocks style attributes, so the width is applied through CSSOM.
  if (progressFill) progressFill.style.width = `${percent}%`;
  root.querySelector("#scan-network")?.addEventListener("change", event => {
    discoverState.scanCIDR = event.target.value;
  });
  root.querySelector("#scan-start")?.addEventListener("click", startNetworkScan);
  root.querySelector("#scan-stop")?.addEventListener("click", stopNetworkScan);
  root.querySelector("#scan-miners-only")?.addEventListener("change", event => {
    discoverState.minersOnly = event.target.checked;
    renderFindMyMiner(root);
  });
  bindDiscoverSearch(root, "find-my-miner", () => renderFindMyMiner(root));
  root.querySelectorAll(".discover-refresh").forEach(button => button.addEventListener("click", () => {
    discoverState.error["find-my-miner"] = "";
    discoverState.data.networks = null;
    discoverState.loading["find-my-miner"] = false;
    loadScanNetworks();
  }));
}
