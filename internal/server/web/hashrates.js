(function (root, factory) {
  const helpers = factory();
  if (typeof module === "object" && module.exports) module.exports = helpers;
  Object.assign(root, helpers);
})(typeof globalThis !== "undefined" ? globalThis : this, function () {
  function configuredHashrates(rigs, sheets, normalizeHashrate) {
    const hashrates = new Map();

    const addWorkload = (coin, algorithm) => {
      if (!coin) return null;
      const normalizedCoin = String(coin).toUpperCase();
      const normalizedAlgorithm = algorithm || "Hashrate";
      const key = `${normalizedCoin}|${normalizedAlgorithm}`;
      if (!hashrates.has(key)) {
        hashrates.set(key, {coin: normalizedCoin, algorithm: normalizedAlgorithm, total: 0, unit: "H/s"});
      }
      return hashrates.get(key);
    };

    const addSheetWorkloads = (sheet, visited = new Set()) => {
      if (!sheet || visited.has(sheet.id)) return;
      if (sheet.id) visited.add(sheet.id);
      addWorkload(sheet.coin, sheet.algorithm);
      addWorkload(sheet.secondary_coin, sheet.secondary_algorithm);
      if (sheet.fallback_flight_sheet_id) {
        addSheetWorkloads(sheets[sheet.fallback_flight_sheet_id], visited);
      }
    };

    rigs.forEach(rig => {
      const sheet = sheets[rig.desired?.flight_sheet_id];
      addSheetWorkloads(sheet);
      const miner = rig.metrics?.miner;
      if (!miner?.running || !miner.hashrate) return;
      const normalized = normalizeHashrate(miner.hashrate, miner.hashrate_unit);
      const activeCoin = rig.metrics?.mining_coin || sheet?.coin || "Mining";
      const activeAlgorithm = rig.metrics?.mining_algorithm || sheet?.algorithm || miner.profile || "Hashrate";
      const workload = addWorkload(activeCoin, activeAlgorithm);
      workload.total += normalized.value;
      workload.unit = normalized.unit;
    });

    return hashrates;
  }

  function hashrateHistoryCharts(history, sheets, sheet, normalizeHashrate) {
    const workloads = new Map();
    const addWorkload = (coin, algorithm) => {
      if (!coin) return;
      const normalizedCoin = String(coin).toUpperCase();
      const normalizedAlgorithm = algorithm || "Hashrate";
      const key = `${normalizedCoin}|${String(normalizedAlgorithm).toLowerCase()}`;
      if (!workloads.has(key)) {
        workloads.set(key, {coin: normalizedCoin, algorithm: normalizedAlgorithm, unit: "H/s"});
      }
    };
    const addSheetWorkloads = (current, visited = new Set()) => {
      if (!current || visited.has(current.id)) return;
      if (current.id) visited.add(current.id);
      addWorkload(current.coin, current.algorithm);
      addWorkload(current.secondary_coin, current.secondary_algorithm);
      if (current.fallback_flight_sheet_id) {
        addSheetWorkloads(sheets[current.fallback_flight_sheet_id], visited);
      }
    };

    addSheetWorkloads(sheet);
    history.forEach(sample => {
      const miner = sample.miner || {};
      const coin = sample.mining_coin || sheet?.coin;
      const algorithm = sample.mining_algorithm || sheet?.algorithm || miner.profile;
      addWorkload(coin, algorithm);
      if (coin && miner.hashrate_unit) {
        const key = `${String(coin).toUpperCase()}|${String(algorithm || "Hashrate").toLowerCase()}`;
        workloads.get(key).unit = normalizeHashrate(0, miner.hashrate_unit).unit;
      }
    });

    return [...workloads].map(([key, workload]) => ({
      ...workload,
      values: history.map(sample => {
        const miner = sample.miner || {};
        const activeCoin = String(sample.mining_coin || sheet?.coin || "").toUpperCase();
        const activeAlgorithm = String(sample.mining_algorithm || sheet?.algorithm || miner.profile || "Hashrate").toLowerCase();
        if (!miner.running || `${activeCoin}|${activeAlgorithm}` !== key) return 0;
        return normalizeHashrate(miner.hashrate, miner.hashrate_unit).value;
      })
    }));
  }

  return {configuredHashrates, hashrateHistoryCharts};
});
