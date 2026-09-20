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
  assert.match(el("edit-fields").html(), /JEDEC 时序/);
  assert.equal(el("btn-edit-write").disabled, false, "CRC 通过且有变更时应可写入");
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

test("编辑器: 原始 hex 编辑解析 0x 前缀并拒绝非法输入", async () => {
  const { el, calls } = setup();
  await flush();
  el("tab-edit").onclick();
  await el("btn-edit-load-dev").onclick();
  await flush();

  el("hex-off").value = "0x1F0";
  el("hex-val").value = "0x5A";
  await el("btn-hex-apply").onclick();
  await flush();
  let call = calls.find((c) => c.name === "EditSetByte");
  assert.ok(call, "应调用 EditSetByte");
  assert.deepEqual([...call.args], [0x1f0, 0x5a]);

  calls.length = 0;
  el("hex-off").value = "abc";
  await el("btn-hex-apply").onclick();
  await flush();
  assert.equal(calls.some((c) => c.name === "EditSetByte"), false, "非法偏移不应调用后端");
});
