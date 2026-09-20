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

test("编辑器: 原始 hex 表单按十六进制解析(5A=90, 不是十进制)", async () => {
  const { el, calls, ctx } = setup();
  await flush();
  el("tab-edit").onclick();
  await el("btn-edit-load-dev").onclick();
  await flush();

  el("hex-off").value = "104";   // 十六进制 0x104 = 260
  el("hex-val").value = "5A";    // 十六进制 0x5A = 90
  await el("btn-hex-apply").onclick();
  await flush();
  const call = calls.find((c) => c.name === "EditSetByte");
  assert.ok(call, "应调用 EditSetByte");
  assert.deepEqual([...call.args], [0x104, 0x5a]);

  calls.length = 0;
  el("hex-val").value = "zz";
  await el("btn-hex-apply").onclick();
  await flush();
  assert.equal(calls.some((c) => c.name === "EditSetByte"), false, "非法值不应提交");
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

