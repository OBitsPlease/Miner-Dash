const test = require("node:test");
const assert = require("node:assert/strict");
const {configuredHashrates, hashrateHistoryCharts} = require("../web/hashrates.js");

const normalizeHashrate = (value, unit = "H/s") => ({
  value: Number(value),
  unit
});

test("attributes live hashrate to the reported fallback coin", () => {
  const sheets = {
    epic: {
      id: "epic",
      coin: "EPIC",
      algorithm: "randomx",
      fallback_flight_sheet_id: "qrl"
    },
    qrl: {id: "qrl", coin: "QRL", algorithm: "qrandomx"}
  };
  const rigs = [{
    desired: {flight_sheet_id: "epic"},
    metrics: {
      mining_coin: "QRL",
      mining_algorithm: "qrandomx",
      miner: {running: true, hashrate: 60000, hashrate_unit: "H/s"}
    }
  }];

  const result = configuredHashrates(rigs, sheets, normalizeHashrate);

  assert.deepEqual(result.get("EPIC|randomx"), {
    coin: "EPIC", algorithm: "randomx", total: 0, unit: "H/s"
  });
  assert.deepEqual(result.get("QRL|qrandomx"), {
    coin: "QRL", algorithm: "qrandomx", total: 60000, unit: "H/s"
  });
});

test("includes secondary workloads at zero while preserving single-coin telemetry fallback", () => {
  const sheets = {
    dual: {
      id: "dual",
      coin: "EPIC",
      algorithm: "randomx",
      secondary_coin: "ZCL",
      secondary_algorithm: "equihash192_7"
    }
  };
  const rigs = [{
    desired: {flight_sheet_id: "dual"},
    metrics: {miner: {running: true, hashrate: 25, hashrate_unit: "KH/s"}}
  }];

  const result = configuredHashrates(rigs, sheets, normalizeHashrate);

  assert.equal(result.get("EPIC|randomx").total, 25);
  assert.equal(result.get("EPIC|randomx").unit, "KH/s");
  assert.equal(result.get("ZCL|equihash192_7").total, 0);
});

test("builds detailed per-algorithm history with zeroes during switches and dropouts", () => {
  const sheets = {
    epic: {
      id: "epic",
      coin: "EPIC",
      algorithm: "randomx",
      fallback_flight_sheet_id: "qrl"
    },
    qrl: {id: "qrl", coin: "QRL", algorithm: "qrandomx"}
  };
  const history = [
    {mining_coin: "EPIC", mining_algorithm: "randomx", miner: {running: true, hashrate: 40, hashrate_unit: "KH/s"}},
    {mining_coin: "EPIC", mining_algorithm: "randomx", miner: {running: true, hashrate: 35, hashrate_unit: "KH/s"}},
    {mining_coin: "QRL", mining_algorithm: "qrandomx", miner: {running: true, hashrate: 60, hashrate_unit: "KH/s"}},
    {mining_coin: "QRL", mining_algorithm: "qrandomx", miner: {running: false, hashrate: 0, hashrate_unit: "KH/s"}}
  ];

  const charts = hashrateHistoryCharts(history, sheets, sheets.epic, normalizeHashrate);

  assert.deepEqual(charts.find(chart => chart.coin === "EPIC").values, [40, 35, 0, 0]);
  assert.deepEqual(charts.find(chart => chart.coin === "QRL").values, [0, 0, 60, 0]);
});

test("builds a configured CPU algorithm chart from legacy history samples", () => {
  const sheet = {id: "qrl", coin: "QRL", algorithm: "qrandomx", device_type: "CPU"};
  const history = [
    {miner: {running: true, profile: "qrl", hashrate: 15200, hashrate_unit: "H/s"}},
    {miner: {running: true, profile: "qrl", hashrate: 15100, hashrate_unit: "H/s"}},
    {miner: {running: false, profile: "qrl", hashrate: 0, hashrate_unit: "H/s"}}
  ];

  const charts = hashrateHistoryCharts(history, {qrl: sheet}, sheet, normalizeHashrate);

  assert.equal(charts.length, 1);
  assert.equal(charts[0].coin, "QRL");
  assert.equal(charts[0].algorithm, "qrandomx");
  assert.deepEqual(charts[0].values, [15200, 15100, 0]);
});
