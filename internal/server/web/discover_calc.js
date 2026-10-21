discoverState.calcHardware = null;
discoverState.calcHardwareLoading = false;
discoverState.calcHardwareError = "";
discoverState.calcAlgorithms = null;
discoverState.calcAlgorithmsLoading = false;

async function mcLoadAlgorithms() {
  if (discoverState.calcAlgorithms || discoverState.calcAlgorithmsLoading) return;
  discoverState.calcAlgorithmsLoading = true;
  try {
    discoverState.calcAlgorithms = await api("/api/v1/discover/calc-algorithms");
  } catch {
    discoverState.calcAlgorithms = {cpu: [], gpu: [], asic: []};
  } finally {
    discoverState.calcAlgorithmsLoading = false;
    if (state.view === "miner-calc") renderMinerCalc();
  }
}

defaultCalcInputs = function() {
  const defaults = defaultPowerInputs();
  return {
    rate: defaults.rate,
    watts: defaults.power * defaults.miners,
    rigs: {},
    added: [],
    hardware: [],
    hardwareType: "gpu",
    powerByAlgorithm: {},
    status: "",
    calculated: false,
    calculating: false,
    results: [],
    btcUSD: 0,
    algorithmFilter: ""
  };
};

function mcAlgorithmKey(value) {
  return String(value || "").toLowerCase().replace(/[^a-z0-9]/g, "");
}

function mcResolveAlgorithm(coins, benchmarkAlgorithm) {
  const key = mcAlgorithmKey(benchmarkAlgorithm);
  const aliases = {
    autolykos2: "autolykos",
    equihash1445: "equihash",
    zhash: "equihash"
  };
  const target = aliases[key] || key;
  return calcAlgorithms(coins).find(algorithm => {
    const candidate = mcAlgorithmKey(algorithm);
    return candidate === key || candidate === target;
  }) || "";
}

function mcDisplayHashrate(base) {
  let prefix = "";
  for (const [candidate, multiplier] of hashrateMultipliers) {
    if (base / multiplier >= 1) prefix = candidate;
  }
  const multiplier = hashrateMultipliers.find(([candidate]) => candidate === prefix)?.[1] || 1;
  return {value: Number((base / multiplier).toPrecision(6)), prefix};
}

async function mcLoadHardware() {
  if (discoverState.calcHardware || discoverState.calcHardwareLoading) return;
  discoverState.calcHardwareLoading = true;
  discoverState.calcHardwareError = "";
discoverState.calcAlgorithms = null;
discoverState.calcAlgorithmsLoading = false;

async function mcLoadAlgorithms() {
  if (discoverState.calcAlgorithms || discoverState.calcAlgorithmsLoading) return;
  discoverState.calcAlgorithmsLoading = true;
  try {
    discoverState.calcAlgorithms = await api("/api/v1/discover/calc-algorithms");
  } catch {
    discoverState.calcAlgorithms = {cpu: [], gpu: [], asic: []};
  } finally {
    discoverState.calcAlgorithmsLoading = false;
    if (state.view === "miner-calc") renderMinerCalc();
  }
}
  try {
    const payload = await api("/api/v1/discover/calc-hardware");
    discoverState.calcHardware = payload.hardware || [];
  } catch (error) {
    discoverState.calcHardwareError = error.message || "Mining hardware is temporarily unavailable";
  } finally {
    discoverState.calcHardwareLoading = false;
    if (state.view === "miner-calc") renderMinerCalc();
  }
}

function mcHardwareOptions(inputs) {
  const matching = (discoverState.calcHardware || []).filter(device => device.type === inputs.hardwareType);
  if (!matching.length) {
    return `<option value="">${discoverState.calcHardwareLoading ? "Loading hardware…" : "No hardware available"}</option>`;
  }
  let currentVendor = "";
  let options = `<option value="">Select ${escapeHTML(inputs.hardwareType.toUpperCase())} model…</option>`;
  matching.forEach(device => {
    const vendor = device.vendor || "Other";
    if (vendor !== currentVendor) {
      if (currentVendor) options += "</optgroup>";
      currentVendor = vendor;
      options += `<optgroup label="${escapeHTML(vendor)}">`;
    }
    options += `<option value="${escapeHTML(device.type + ":" + device.id)}">${escapeHTML(device.name)}</option>`;
  });
  return options + (currentVendor ? "</optgroup>" : "");
}

async function mcFillHardwareHashrates(root) {
  const inputs = discoverState.calc;
  if (!inputs.hardware.length) {
    inputs.status = "Add at least one GPU, CPU, or ASIC first.";
    renderMinerCalc(root);
    return;
  }
  inputs.status = "Loading hardware benchmarks…";
  renderMinerCalc(root);
  try {
    const catalog = discoverState.calcHardware || [];
    const results = await Promise.all(inputs.hardware.map(async selection => {
      const device = catalog.find(item => item.type === selection.type && item.id === selection.id);
      if (device?.benchmarks?.length) return device.benchmarks;
      const query = `type=${encodeURIComponent(selection.type)}&id=${encodeURIComponent(selection.id)}`;
      const payload = await api(`/api/v1/discover/calc-benchmarks?${query}`);
      return payload.benchmarks || [];
    }));
    const coins = discoverState.data["miner-calc"]?.coins || [];
    const totals = new Map();
    const failed = [];
    inputs.hardware.forEach((selection, index) => {
      const benchmarks = results[index] || [];
      if (!benchmarks.length) {
        failed.push(selection.name);
        return;
      }
      const used = new Set();
      benchmarks.forEach(benchmark => {
        const algorithm = mcResolveAlgorithm(coins, benchmark.algorithm);
        if (!algorithm || used.has(algorithm)) return;
        used.add(algorithm);
        const total = totals.get(algorithm) || {hashrate: 0, watts: 0};
        total.hashrate += Number(benchmark.hashrate || 0) * selection.count;
        total.watts += Number(benchmark.watts || 0) * selection.count;
        totals.set(algorithm, total);
      });
    });
    inputs.rigs = {};
    inputs.powerByAlgorithm = {};
    totals.forEach((total, algorithm) => {
      inputs.rigs[algorithm] = mcDisplayHashrate(total.hashrate);
      inputs.powerByAlgorithm[algorithm] = total.watts;
      if (!inputs.added.includes(algorithm)) inputs.added.push(algorithm);
    });
    inputs.watts = Math.max(0, ...Object.values(inputs.powerByAlgorithm));
    inputs.calculated = false;
    inputs.status = `Filled ${totals.size} algorithm${totals.size === 1 ? "" : "s"} from hardware benchmarks${failed.length ? ` · no data for ${failed.join(", ")}` : ""}. Review the values, then click Calculate profits.`;
  } catch (error) {
    inputs.status = `Could not load benchmarks: ${error.message || error}`;
  }
  renderMinerCalc(root);
}

function mcHardwareRows(inputs) {
  if (!inputs.hardware.length) return `<span class="discover-status">No hardware added.</span>`;
  return inputs.hardware.map((selection, index) => `
    <div class="calc-hardware-row">
      <span><strong>${escapeHTML(selection.name)}</strong><small>${escapeHTML(selection.type.toUpperCase())}</small></span>
      <input type="number" min="1" step="1" value="${selection.count}" data-hardware-count="${index}" title="Quantity">
      <button class="secondary calc-hardware-remove" data-hardware-remove="${index}" type="button" title="Remove">×</button>
    </div>`).join("");
}

function mcAlgorithmGroup(algorithm) {
  const key = mcAlgorithmKey(algorithm);
  if (["randomx", "ghostrider", "yespower", "yescrypt", "verushash", "cryptonight"].some(prefix => key.startsWith(prefix))) return "CPU";
  if (["sha256", "scrypt", "x11", "blake2s", "equihash"].some(prefix => key.startsWith(prefix))) return "ASIC";
  if (["kawpow", "ethash", "etchash", "autolykos", "firopow", "octopus", "blake3", "zelhash", "beamhash"].some(prefix => key.startsWith(prefix))) return "GPU";
  return "Other";
}

function mcInputWatts(inputs, algorithm) {
  const target = mcAlgorithmKey(algorithm);
  const match = Object.entries(inputs.powerByAlgorithm).find(([candidate]) => {
    const key = mcAlgorithmKey(candidate);
    return key === target || (target === "zhash" && key === "equihash") || (target === "autolykos" && key === "autolykos2");
  });
  return Number(match?.[1]) || Number(inputs.watts) || 0;
}

renderMinerCalc = function(root = $("#feature-content")) {
  const view = "miner-calc";
  const payload = discoverState.data[view];
  if (!payload && !discoverState.error[view]) loadDiscoverData(view);
  if (!discoverState.calcHardware && !discoverState.calcHardwareLoading && !discoverState.calcHardwareError) mcLoadHardware();
  const hero = discoverHero("HARDWARE PROFIT CALCULATOR", "Calculate mining profits for your hardware",
    "Add GPUs, CPUs, and ASICs, fill their algorithm benchmarks, then calculate every matching coin's revenue, electricity cost, and profit.");
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
  mcLoadAlgorithms();
  const catalogAlgorithms = discoverState.calcAlgorithms || {cpu: [], gpu: [], asic: []};
  const liveAlgorithms = calcAlgorithms(coins);
  const hardwareAlgorithms = [...new Set([...catalogAlgorithms.cpu, ...catalogAlgorithms.gpu, ...catalogAlgorithms.asic])];
  const available = [...new Set([...hardwareAlgorithms, ...liveAlgorithms])].sort((left, right) => left.localeCompare(right, undefined, {sensitivity: "base"}));
  const active = [...new Set([...available, ...inputs.added])];
  const algorithmRowsByGroup = {GPU: [], CPU: [], ASIC: [], Other: []};
  active.forEach(algorithm => {
    const entry = inputs.rigs[algorithm] || {value: "", prefix: "M"};
    const unit = calcUnitFor(coins, algorithm);
    const watts = Number(inputs.powerByAlgorithm[algorithm]) || 0;
    const coinNames = coins.filter(coin => mcAlgorithmKey(coin.algorithm) === mcAlgorithmKey(algorithm))
      .slice(0, 3).map(coin => coin.tag || coin.name).join(", ");
    const options = hashrateMultipliers.map(([prefix]) =>
      `<option value="${prefix}"${entry.prefix === prefix ? " selected" : ""}>${prefix}${unit}</option>`).join("");
    algorithmRowsByGroup[mcAlgorithmGroup(algorithm)].push(`
      <label class="calc-rig-row${watts > 0 ? " calc-rig-filled" : ""}">
        <span>${escapeHTML(algorithm)}<small>${watts > 0 ? `${discoverNumber(watts, 0)} W` : escapeHTML(coinNames)}</small></span>
        <input type="number" min="0" step="any" data-algo="${escapeHTML(algorithm)}" value="${escapeHTML(entry.value)}" placeholder="0">
        <select data-algo-unit="${escapeHTML(algorithm)}">${options}</select>
      </label>`);
  });
  const algorithmRows = Object.entries(algorithmRowsByGroup)
    .filter(([, rows]) => rows.length)
    .map(([group, rows]) => `<div class="calc-algorithm-group"><h4>${group} algorithms</h4>${rows.join("")}</div>`).join("");
  const addOptions = available.filter(algorithm => !active.includes(algorithm))
    .map(algorithm => `<option value="${escapeHTML(algorithm)}">${escapeHTML(algorithm)}</option>`).join("");
  const entered = active.filter(algorithm => calcHashrateBase(inputs.rigs[algorithm] || {}) > 0);
  const allResults = inputs.calculated ? (inputs.results || [])
    .map(coin => {
      const revenue = Number(coin.revenue_usd) || 0;
      const watts = mcInputWatts(inputs, coin.algorithm);
      const powerCost = watts / 1000 * 24 * (Number(inputs.rate) || 0);
      return {coin, revenue, powerCost, profit: revenue - powerCost};
    })
    .sort((left, right) => right.profit - left.profit) : [];
  const results = inputs.algorithmFilter
    ? allResults.filter(({coin}) => mcAlgorithmKey(coin.algorithm) === inputs.algorithmFilter)
    : allResults;
  const filterAlgorithms = [...new Set(allResults.map(({coin}) => coin.algorithm).filter(Boolean))]
    .sort().map(algorithm => `<option value="${mcAlgorithmKey(algorithm)}"${inputs.algorithmFilter === mcAlgorithmKey(algorithm) ? " selected" : ""}>${escapeHTML(algorithm)}</option>`).join("");
  const resultRows = results.map(({coin, revenue, powerCost, profit}, index) => {
    const profitClass = profit > 0.005 ? "trend-up" : profit < -0.005 ? "trend-down" : "trend-flat";
    return `
      <tr>
        <td>${index + 1}</td>
        <td class="discover-coin"><strong>${escapeHTML(coin.name)}</strong><span class="discover-symbol">${escapeHTML(coin.tag)}</span>${coin.is_new ? `<span class="algo-badge badge-new">NEW</span>` : ""}</td>
        <td>${escapeHTML(coin.algorithm)}</td>
        <td>${discoverNumber(coin.estimated_rewards, 6)}</td>
        <td>${discoverPrice(coin.price_usd)}${coin.exchange ? `<span class="discover-symbol">${escapeHTML(coin.exchange)}</span>` : ""}</td>
        <td>${discoverNumber(coin.btc_revenue, 8)}</td>
        <td>${powerMoney(revenue)}</td>
        <td>${powerMoney(powerCost)}</td>
        <td class="${profitClass}"><strong>${powerMoney(profit)}</strong></td>
        <td>${discoverNumber(coin.block_reward, 4)}</td>
        <td><span class="algo-badge">${escapeHTML(coin.status || (coin.listed ? "Listed" : "Active"))}</span></td>
        <td>${externalLink(coin.page_url, "Pools", "discover-link") || "—"}</td>
      </tr>`;
  }).join("");

  root.innerHTML = `
    ${hero}
    <div class="calc-layout">
      <div class="discover-card calc-sidebar">
        <h3 class="discover-heading">My hardware</h3>
        <p class="discover-legend">Select any combination of GPUs, CPUs, and ASICs, then fill hashrates from live benchmark data.</p>
        <div class="calc-hardware-type">
          ${["gpu", "cpu", "asic"].map(type => `<button class="${inputs.hardwareType === type ? "" : "secondary"}" data-hardware-type="${type}" type="button">${type.toUpperCase()}</button>`).join("")}
        </div>
        <div class="calc-hardware-picker">
          <select id="calc-hardware-select">${mcHardwareOptions(inputs)}</select>
          <input id="calc-hardware-quantity" type="number" min="1" step="1" value="1" title="Quantity">
          <button id="calc-hardware-add" class="secondary" type="button">Add</button>
        </div>
        ${discoverState.calcHardwareError ? `<div class="calc-status trend-down">${escapeHTML(discoverState.calcHardwareError)}</div>` : ""}
        <div class="calc-hardware-list">${mcHardwareRows(inputs)}</div>
        <div class="power-actions">
          <button id="calc-fill-hashrates" type="button">Fill hashrates</button>
          <button id="calc-clear-hardware" class="secondary" type="button">Clear hardware</button>
        </div>
        ${inputs.status ? `<div class="calc-status">${escapeHTML(inputs.status)}</div>` : ""}
        <hr class="calc-divider">
        <h3 class="discover-heading">Hashrates by algorithm</h3>
        <div class="calc-rig-list">${algorithmRows}</div>
        <div class="calc-add-row">
          <select id="calc-add-algo"${addOptions ? "" : " disabled"}>${addOptions || `<option>All algorithms added</option>`}</select>
          <button id="calc-add" class="secondary" type="button"${addOptions ? "" : " disabled"}>Add algorithm</button>
        </div>
        <h3 class="discover-heading">Electricity</h3>
        <label>Cost ($/kWh)<input id="calc-rate" type="number" min="0" step="any" value="${inputs.rate}"></label>
        <label>Total draw fallback (W)<input id="calc-watts" type="number" min="0" step="any" value="${inputs.watts}"></label>
        <div class="power-actions">
          <button id="calc-run" type="button"${inputs.calculating ? " disabled" : ""}>${inputs.calculating ? "Calculating…" : "Calculate profits"}</button>
          <button id="calc-reset" class="secondary" type="button">Clear</button>
        </div>
      </div>
      <section class="discover-card calc-results">
        <div class="calc-results-heading">
          <div><h3 class="discover-heading">Profitability ranked by daily profit</h3>
          <p class="discover-status">${escapeHTML(discoverStatusLine(view, `BTC ${powerMoney(inputs.btcUSD || payload.btc_usd)} · ${results.length} matching results`))}</p></div>
          <label class="calc-filter">Algorithm
            <select id="calc-algorithm-filter"><option value="">All algorithms</option>${filterAlgorithms}</select>
          </label>
          <button class="secondary discover-refresh" data-view="${view}" type="button">Refresh data</button>
        </div>
        <div class="discover-table-wrap">
          <table class="discover-table">
            <thead><tr><th>#</th><th>Coin</th><th>Algorithm</th><th>Est. coins/day</th><th>Sell price</th><th>BTC/day</th><th>Revenue/day</th><th>Power/day</th><th>Profit/day</th><th>Block reward</th><th>Status</th><th>Pools</th></tr></thead>
            <tbody>${resultRows || `<tr><td colspan="12" class="discover-empty">${inputs.calculated ? "No matching coin data was found." : "Fill or enter hashrates, then click Calculate profits."}</td></tr>`}</tbody>
          </table>
        </div>
        <p class="discover-legend">Hardware benchmarks are estimates. Results vary with overclocks, pool luck, difficulty, fees, and live prices.</p>
      </section>
    </div>`;

  root.querySelectorAll("[data-hardware-type]").forEach(button => button.addEventListener("click", () => {
    inputs.hardwareType = button.dataset.hardwareType;
    renderMinerCalc(root);
  }));
  root.querySelector("#calc-hardware-add")?.addEventListener("click", () => {
    const key = root.querySelector("#calc-hardware-select")?.value || "";
    const [type, id] = key.split(":");
    const device = (discoverState.calcHardware || []).find(item => item.type === type && item.id === id);
    if (!device) {
      inputs.status = "Select a hardware model first.";
      renderMinerCalc(root);
      return;
    }
    const count = Math.max(1, Number(root.querySelector("#calc-hardware-quantity")?.value) || 1);
    const existing = inputs.hardware.find(item => item.type === type && item.id === id);
    if (existing) existing.count += count;
    else inputs.hardware.push({type, id, name: device.name, count});
    inputs.status = "";
    inputs.calculated = false;
    renderMinerCalc(root);
    mcFillHardwareHashrates(root);
  });
  root.querySelectorAll("[data-hardware-count]").forEach(field => field.addEventListener("change", () => {
    const selection = inputs.hardware[Number(field.dataset.hardwareCount)];
    if (selection) selection.count = Math.max(1, Number(field.value) || 1);
    inputs.calculated = false;
    mcFillHardwareHashrates(root);
  }));
  root.querySelectorAll("[data-hardware-remove]").forEach(button => button.addEventListener("click", () => {
    inputs.hardware.splice(Number(button.dataset.hardwareRemove), 1);
    inputs.calculated = false;
    mcFillHardwareHashrates(root);
  }));
  root.querySelector("#calc-fill-hashrates")?.addEventListener("click", () => mcFillHardwareHashrates(root));
  root.querySelector("#calc-clear-hardware")?.addEventListener("click", () => {
    inputs.hardware = [];
    inputs.rigs = {};
    inputs.powerByAlgorithm = {};
    inputs.watts = 0;
    inputs.status = "";
    inputs.calculated = false;
    renderMinerCalc(root);
  });
  root.querySelectorAll(".calc-rig-row input").forEach(field => field.addEventListener("change", () => {
    const algorithm = field.dataset.algo;
    const current = inputs.rigs[algorithm] || {prefix: "M"};
    inputs.rigs[algorithm] = {value: field.value, prefix: current.prefix};
    inputs.calculated = false;
    renderMinerCalc(root);
  }));
  root.querySelectorAll(".calc-rig-row select").forEach(field => field.addEventListener("change", () => {
    const algorithm = field.dataset.algoUnit;
    const current = inputs.rigs[algorithm] || {value: ""};
    inputs.rigs[algorithm] = {value: current.value, prefix: field.value};
    inputs.calculated = false;
    renderMinerCalc(root);
  }));
  root.querySelector("#calc-add")?.addEventListener("click", () => {
    const algorithm = root.querySelector("#calc-add-algo")?.value;
    if (algorithm && !inputs.added.includes(algorithm)) inputs.added.push(algorithm);
    inputs.calculated = false;
    renderMinerCalc(root);
  });
  root.querySelector("#calc-rate")?.addEventListener("change", event => {
    inputs.rate = Number(event.target.value) || 0;
    inputs.calculated = false;
    renderMinerCalc(root);
  });
  root.querySelector("#calc-watts")?.addEventListener("change", event => {
    inputs.watts = Number(event.target.value) || 0;
    inputs.calculated = false;
    renderMinerCalc(root);
  });
  root.querySelector("#calc-run")?.addEventListener("click", async () => {
    if (!entered.length) {
      inputs.status = "Enter or fill at least one hashrate.";
      renderMinerCalc(root);
      return;
    }
    inputs.calculating = true;
    inputs.status = "Loading exact WhatToMine profitability and new MiningPoolStats coins…";
    renderMinerCalc(root);
    try {
      const algorithms = entered.map(algorithm => ({
        algorithm,
        hashrate: calcHashrateBase(inputs.rigs[algorithm]),
        watts: mcInputWatts(inputs, algorithm)
      }));
      const payload = await api("/api/v1/discover/calc-profitability", {
        method: "POST",
        body: JSON.stringify({algorithms})
      });
      inputs.results = payload.coins || [];
      inputs.btcUSD = Number(payload.btc_usd) || 0;
      inputs.calculated = true;
      inputs.status = `Calculated ${inputs.results.length} coin${inputs.results.length === 1 ? "" : "s"} from ${entered.length} algorithm${entered.length === 1 ? "" : "s"}.`;
    } catch (error) {
      inputs.calculated = false;
      inputs.status = `Could not calculate live profitability: ${error.message || error}`;
    } finally {
      inputs.calculating = false;
      renderMinerCalc(root);
    }
  });
  root.querySelector("#calc-algorithm-filter")?.addEventListener("change", event => {
    inputs.algorithmFilter = event.target.value;
    renderMinerCalc(root);
  });
  root.querySelector("#calc-reset")?.addEventListener("click", () => {
    discoverState.calc = defaultCalcInputs();
    renderMinerCalc(root);
  });
  root.querySelector(".discover-refresh")?.addEventListener("click", () => loadDiscoverData(view, {force: true}));
};
