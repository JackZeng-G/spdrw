// 前端契约测试(编辑器): 标签页、字段渲染/应用、hex 编辑、写入护栏。
import test from "node:test";
import assert from "node:assert/strict";
import { loadApp, makeAppStub, flush } from "./harness.mjs";

const state = {
  source: "设备 0x50", generation: "DDR4", size: 512,
  dirty: false, changeCount: 0, crcOk: true, canWrite: true,
};
const fields = [
  { key: "partNumber", name: "部件号", group: "常用信息", kind: "string", value: "TEST16GB-DDR4-3200", offset: "0x149-20B", risk: "low" },
  { key: "serial", name: "序列号(hex)", group: "常用信息", kind: "hex", value: "DEADBEEF", offset: "0x145-4B", risk: "low" },
  { key: "ddr4.tAA", name: "tAA", group: "JEDEC 时序", kind: "float", unit: "ns", value: "1.5", offset: "0x18/0x7B", risk: "medium" },
];
const diffDirty = {
  changes: [{ offset: 325, old: 0xde, new: 0x11, field: "序列号", risk: "low" }],
  fields: [{ region: "序列号", risk: "low", count: 4, ranges: "0x145-0x148" }],
  highRisk: 0, crcFields: 2, changeCount: 5, crcOk: true, truncated: false,
};

function setup(overrides = {}) {
  const { stub, calls } = makeAppStub({
    EditLoadFromDevice: () => state,
    EditFields: () => fields,
    EditDiff: () => diffDirty,
    EditSetField: () => ({ ...state, dirty: true, changeCount: 1 }),
    EditApplyToDevice: () => ({ dryRun: false, written: 5, total: 5, backupPath: "/root/.spdrw/backups/x.bin", verified: true, message: "写入并校验通过" }),
    ...overrides,
  });
  const h = loadApp({ appStub: stub });
  const alerts = [];
  h.ctx.alert = (m) => alerts.push(String(m));
  return { ...h, calls, alerts };
}

test("编辑器: 标签页切换与从设备载入", async () => {
  const { el, calls } = setup();
  await flush();
  assert.equal(el("view-edit").classList.contains("hidden"), true, "默认显示 SPD 信息");

  el("tab-edit").onclick();
  assert.equal(el("view-edit").classList.contains("hidden"), false);
  assert.equal(el("view-info").classList.contains("hidden"), true);

  await el("btn-edit-load-dev").onclick();
  await flush();
  assert.ok(calls.some((c) => c.name === "EditLoadFromDevice"), "应调用 EditLoadFromDevice");
  assert.ok(calls.some((c) => c.name === "EditFields"), "载入后应拉字段");
  assert.match(el("edit-state").textContent, /设备 0x50/);
  assert.match(el("edit-fields").html(), /部件号/);
  assert.equal(el("btn-edit-write").disabled, false, "CRC 通过且有变更时应可写入");
});

test("编辑器: 分组切换(JEDEC 时序/常用信息分开渲染)", async () => {
  const { el } = setup();
  await flush();
  el("tab-edit").onclick();
  await el("btn-edit-load-dev").onclick();
  await flush();
  const tabs = el("edit-groups").children;
  assert.ok(tabs.length >= 2, "应有多个分组按钮: " + tabs.length);
  const names = tabs.map((t) => t.textContent).join("|");
  assert.match(names, /常用信息/);
  assert.match(names, /JEDEC 时序/);
  // 切到 JEDEC 分组后应渲染 tAA 输入框
  const jedec = tabs.find((t) => t.textContent.includes("JEDEC"));
  jedec.onclick();
  await flush();
  const keys = el("edit-fields").querySelectorAll("input").map((i) => i.getAttribute("data-key"));
  assert.ok(keys.includes("ddr4.tAA"), "JEDEC 分组应含 tAA: " + keys.join(","));
  assert.ok(!keys.includes("partNumber"), "切换分组后不应再渲染常用信息字段");
});

test("编辑器: 左侧 hex 点击可就地改字节(十六进制解析)", async () => {
  const { el, calls, ctx } = setup();
  await flush();
  el("tab-edit").onclick();
  await el("btn-edit-load-dev").onclick();
  await flush();
  assert.match(el("hexgrid").innerHTML, /data-off="260"/, "hex 视图的字节应带 data-off(可点击)");
  assert.match(el("hex-hint").textContent, /点击/, "应提示左侧 hex 可直接修改");

  // 每次提交后 hex 视图会整体重绘, 所以要重新取格子(与真实点击一致)
  const findTarget = () =>
    el("hexgrid").querySelectorAll("span[data-off]").find((b) => b.getAttribute("data-off") === "260");
  let target = findTarget();
  assert.ok(target, "应能取到 data-off=260 的字节格");
  el("hexgrid").onclick({ target });
  const inp = target.querySelector("input");
  assert.ok(inp, "点击后该格应出现输入框");
  // 关键: 输入 5A 必须按十六进制解析(=90), 不是十进制 5 / 0
  inp.value = "5A";
  await inp.onkeydown({ key: "Enter", preventDefault() {} });
  await flush();
  let call = calls.find((c) => c.name === "EditSetByte");
  assert.ok(call, "回车后应调用 EditSetByte");
  assert.deepEqual([...call.args], [260, 0x5a]);

  // 0x 前缀同样按十六进制
  calls.length = 0;
  target = findTarget();
  el("hexgrid").onclick({ target });
  const inp2 = target.querySelector("input");
  inp2.value = "0x0b";
  await inp2.onkeydown({ key: "Enter", preventDefault() {} });
  await flush();
  call = calls.find((c) => c.name === "EditSetByte");
  assert.deepEqual([...call.args], [260, 0x0b]);

  // Esc 取消: 不调后端
  calls.length = 0;
  target = findTarget();
  el("hexgrid").onclick({ target });
  const inp3 = target.querySelector("input");
  inp3.value = "FF";
  await inp3.onkeydown({ key: "Escape", preventDefault() {} });
  await flush();
  assert.equal(calls.some((c) => c.name === "EditSetByte"), false, "Esc 不应提交");

  // 非法输入: 报错且不调后端
  calls.length = 0;
  target = findTarget();
  el("hexgrid").onclick({ target });
  const inp4 = target.querySelector("input");
  inp4.value = "zz";
  await inp4.onkeydown({ key: "Enter", preventDefault() {} });
  await flush();
  assert.equal(calls.some((c) => c.name === "EditSetByte"), false, "非法十六进制不应提交");
  assert.match(el("log").text(), /十六进制/);
});

// 校验状态徽标: 依据"左侧 hex 正在显示的字节"由后端判定, 且要区分
// "改动影响校验"(必须重算)与"改动不在校验范围"(序列号/日期等, 不用管)。
function cellAt(el, off) {
  return el("hexgrid").querySelectorAll("span[data-off]")
    .find((b) => b.getAttribute("data-off") === String(off));
}

test("校验状态: 面板把左侧 dump 的字节交给后端判定并显示结果", async () => {
  const { el, calls } = setup();
  await flush();
  el("tab-edit").onclick();
  await el("btn-edit-load-dev").onclick();
  await flush();

  const call = calls.find((c) => c.name === "CRCStatus");
  assert.ok(call, "应调用 CRCStatus 查询校验状态");
  assert.equal(call.args[0].length, 512, "应把左侧视图的字节([]int)传给后端");
  assert.match(el("crc-status").textContent, /CRC 通过/, "桩里 dump 的 CRC 是通过的");
  assert.match(el("crc-status").title, /参与校验的区域/, "悬停应给出校验区划分");
  assert.equal(cellAt(el, 126).classList.contains("crcbyte"), true, "CRC 值字节要标出来");
  assert.equal(cellAt(el, 20).classList.contains("crcbyte"), false);
});

test("校验状态: 改动在校验范围外 → CRC 仍通过, 格子标蓝且不提示重算", async () => {
  const onlyFree = () => ({
    ...diffDirty, crcOk: true, crcDirty: 0, crcFreeDirty: 3,
    dirtyInCrc: [], dirtyFree: [323, 325, 329],
  });
  const { el } = setup({ EditDiff: onlyFree });
  await flush();
  el("tab-edit").onclick();
  await el("btn-edit-load-dev").onclick();
  await flush();

  assert.match(el("crc-status").textContent, /CRC 通过/);
  assert.match(el("crc-status").textContent, /3 处改动不在校验范围/);
  assert.equal(cellAt(el, 325).classList.contains("chg-free"), true, "序列号改动应标蓝");
  assert.equal(cellAt(el, 325).classList.contains("chg-crc"), false);
  assert.match(el("edit-diff").innerHTML, /不影响校验/);
});

test("校验状态: 改动落在校验范围内 → 提示重算, 格子标红", async () => {
  const inRange = () => ({
    ...diffDirty, crcOk: false, crcDirty: 1, crcFreeDirty: 0,
    dirtyInCrc: [20], dirtyFree: [],
  });
  const { el } = setup({ EditDiff: inRange });
  await flush();
  el("tab-edit").onclick();
  await el("btn-edit-load-dev").onclick();
  await flush();

  assert.match(el("crc-status").textContent, /CRC 需重算/);
  assert.match(el("crc-status").textContent, /1 处在校验范围内/);
  assert.equal(cellAt(el, 20).classList.contains("chg-crc"), true, "时序改动应标红");
  assert.equal(cellAt(el, 20).classList.contains("chg-free"), false);
  assert.match(el("edit-diff").innerHTML, /影响校验/);
  assert.equal(el("btn-edit-write").disabled, true, "CRC 不通过时禁止写入");
});

test("校验状态: 重算 CRC 后回到通过, 且不再提示重算", async () => {
  let fixed = false;
  const { el, calls } = setup({
    EditDiff: () => fixed
      ? { ...diffDirty, crcOk: true, crcDirty: 2, crcFreeDirty: 1, dirtyInCrc: [126, 127], dirtyFree: [325] }
      : { ...diffDirty, crcOk: false, crcDirty: 1, crcFreeDirty: 0, dirtyInCrc: [20], dirtyFree: [] },
    EditFixCRC: () => { fixed = true; return { ...state, dirty: true, changeCount: 4 }; },
  });
  await flush();
  el("tab-edit").onclick();
  await el("btn-edit-load-dev").onclick();
  await flush();
  assert.match(el("crc-status").textContent, /CRC 需重算/);

  await el("btn-edit-fixcrc").onclick();
  await flush();
  assert.ok(calls.some((c) => c.name === "EditFixCRC"), "应调用 EditFixCRC");
  assert.match(el("crc-status").textContent, /CRC 通过/);
  assert.equal(el("btn-edit-write").disabled, false, "重算后可以写入");
});

test("编辑器: 修改字段会调用 EditSetField 并刷新 diff", async () => {
  const { el, calls } = setup();
  await flush();
  el("tab-edit").onclick();
  await el("btn-edit-load-dev").onclick();
  await flush();

  // 触发第一个字段的应用按钮
  el("edit-fields").querySelectorAll("button[data-apply]").forEach((b) => b.onclick());
  await flush();
  const call = calls.find((c) => c.name === "EditSetField");
  assert.ok(call, "应调用 EditSetField");
  assert.equal(call.args[0], "partNumber");
  assert.equal(call.args[1], "TEST16GB-DDR4-3200");
});

test("编辑器: CRC 不通过时禁止写入", async () => {
  const { el } = setup({ EditDiff: () => ({ ...diffDirty, crcOk: false }) });
  await flush();
  el("tab-edit").onclick();
  await el("btn-edit-load-dev").onclick();
  await flush();

  assert.equal(el("btn-edit-write").disabled, true, "CRC 不通过必须禁用写入");
  assert.match(el("edit-diff").innerHTML, /CRC 不通过/);
});

test("编辑器: 未勾选备份拒绝写入; 干跑走 DRYRUN", async () => {
  const { el, calls, alerts } = setup();
  await flush();
  el("tab-edit").onclick();
  await el("btn-edit-load-dev").onclick();
  await flush();

  el("chk-edit-backup").checked = false;
  el("inp-edit-ack").value = "WRITE";
  await el("btn-edit-write").onclick();
  await flush();
  assert.equal(calls.some((c) => c.name === "EditApplyToDevice"), false, "未备份不得写入");
  assert.equal(alerts.length, 1);

  // 干跑
  el("chk-edit-dryrun").checked = true;
  el("chk-edit-dryrun").onchange();
  assert.equal(el("inp-edit-ack").placeholder, "DRYRUN");
  el("inp-edit-ack").value = "DRYRUN";
  await el("btn-edit-write").onclick();
  await flush();
  const call = calls.find((c) => c.name === "EditApplyToDevice");
  assert.ok(call);
  assert.deepEqual([...call.args], [false, true, "DRYRUN"]);
});


test("编辑器: 写入失败后自动重读设备(回滚结果只有重读才知道)", async () => {
  const { el, calls } = setup({
    EditApplyToDevice: () => { throw new Error("写入中止(write)@ 0x2C5: 已写 3/4 字节…; 已自动回滚到写入前内容(3 字节, 读回校验通过)"); },
  });
  await flush();
  el("tab-edit").onclick();
  await el("btn-edit-load-dev").onclick();
  await flush();

  calls.length = 0;
  el("chk-edit-backup").checked = true;
  el("inp-edit-ack").value = "WRITE";
  await el("btn-edit-write").onclick();
  await flush();

  assert.ok(calls.some((c) => c.name === "EditApplyToDevice"));
  assert.ok(calls.some((c) => c.name === "Dump"), "写入失败后必须重新读设备");
  assert.match(el("log").text(), /已自动回滚/, "回滚结果要出现在日志里");
  assert.match(el("log").text(), /设备当前实际内容/, "要说明左侧现在显示的是设备内容");
});
