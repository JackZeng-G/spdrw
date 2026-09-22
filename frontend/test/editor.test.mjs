// 前端契约测试(编辑器): 标签页、字段渲染/应用、hex 编辑、写入护栏。
import test from "node:test";
import assert from "node:assert/strict";
import fs from "node:fs";
import { loadApp, makeAppStub, flush, readDevice, EMPTY_DIFF } from "./harness.mjs";

// 真实发布页(静态文案的唯一来源, 例如确认串占位符)
const html = fs.readFileSync(new URL("../dist/index.html", import.meta.url), "utf8");

const state = {
  source: "设备 0x50", generation: "DDR4", size: 512,
  dirty: false, changeCount: 0, crcOk: true, canWrite: true, tckNs: 0.625,
};
const fields = [
  { key: "partNumber", name: "部件号", group: "常用信息", kind: "string", value: "TEST16GB-DDR4-3200", offset: "0x149-20B", risk: "low", primary: true },
  { key: "serial", name: "序列号(hex)", group: "常用信息", kind: "hex", value: "DEADBEEF", offset: "0x145-4B", risk: "low", primary: true },
  { key: "ddr4.tAA", name: "tAA", group: "JEDEC 时序", kind: "float", unit: "ns", value: "1.5", offset: "0x18/0x7B", risk: "medium", primary: true, hint: "2 clk" },
  // 非常用字段: 默认收起, 勾"显示全部字段"后才出现
  { key: "ddr4.tCCD_L_WR2", name: "tCCD_L_WR2", group: "JEDEC 时序", kind: "float", unit: "ns", value: "8", offset: "0x50", risk: "medium" },
];
const diffDirty = {
  changes: [{ offset: 325, old: 0xde, new: 0x11, field: "序列号", risk: "low" }],
  fields: [{ region: "序列号", risk: "low", count: 4, ranges: "0x145-0x148" }],
  highRisk: 0, crcFields: 2, changeCount: 5, crcOk: true, truncated: false,
};

// 读取设备后编辑器会自动载入(界面已无"从设备载入"按钮), 测试统一走这条路径。
function setup(overrides = {}) {
  const { stub, calls } = makeAppStub({
    EditState: () => state,
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

  await readDevice(el);
  await flush();
  assert.ok(calls.some((c) => c.name === "Dump"), "应读取设备");
  assert.ok(calls.some((c) => c.name === "EditState"), "读取后应自动接进编辑器");
  assert.ok(calls.some((c) => c.name === "EditFields"), "应拉字段");
  assert.match(el("edit-state").textContent, /设备 0x50/);
  assert.match(el("edit-state").textContent, /1clk=0\.625ns · 3200MT\/s/, "状态行应显示 tCK 基准与等效频率");
  assert.match(el("edit-fields").html(), /部件号/);
  // 周期提示在"JEDEC 时序"分组里, 先切过去
  const grp = [...el("edit-groups").children].find((b) => b.textContent.includes("JEDEC 时序"));
  if (grp) grp.onclick();
  assert.match(el("edit-fields").html(), /2 clk/, "时序字段应显示周期提示");
  assert.match(el("edit-field-count").textContent, /常用 \d+ \/ \d+ 个字段/, "应显示常用/全部字段数");
  assert.equal(el("btn-edit-write").disabled, false, "CRC 通过且有变更时应可写入");
});

test("SPD 布局说明只用于 hex 区标注: 不进编辑列表, 行尾标参数名, 注记偏移也能定位", async () => {
  const withInfo = [
    ...fields,
    // 带注记的时序偏移(修复目标: [nib] 与 (单位) 之前认不出 → 无法定位)
    { key: "ddr4.tRAS", name: "tRAS", group: "JEDEC 时序", kind: "float", unit: "ns",
      value: "8", offset: "0x1C/0x1B[3:0]", risk: "medium", primary: true },
    { key: "ddr4.tRC", name: "tRC", group: "JEDEC 时序", kind: "float", unit: "ns",
      value: "9", offset: "0x1D(12b)/0x78", risk: "medium", primary: true },
    // 只读布局说明: 只在 hex 区出现
    { key: "info.000", name: "保留(JEDEC)", group: "SPD 布局", kind: "info",
      value: "", offset: "0x1F0-0x1FF", risk: "low", note: "未定义字节" },
  ];
  const { el } = setup({ EditFields: () => withInfo });
  await flush();
  el("tab-edit").onclick();
  await readDevice(el);
  await flush();

  // 1) 编辑器里没有 SPD 布局分组/info 字段
  const grp = [...el("edit-groups").children].find((b) => b.textContent.includes("SPD 布局"));
  assert.equal(grp, undefined, "编辑器不应出现 SPD 布局分组");
  assert.doesNotMatch(el("edit-fields").html(), /保留\(JEDEC\)/, "info 字段不进编辑列表");

  // 2) 原始 hex 数据行尾有参数标注
  const hex = el("hexgrid").html();
  assert.match(hex, /部件号/, "身份区行尾应标注部件号");
  assert.match(hex, /tAA/, "时序行尾应标注 tAA");
  assert.match(hex, /tRAS/, "nibble 注记偏移([3:0])应能解析出标注");
  assert.match(hex, /tRC/, "(12b) 注记偏移应能解析出标注");
  assert.match(hex, /保留\(JEDEC\)/, "info 说明标注在 hex 行尾(0x1F0 行)");

  // 3) 注记偏移解析(时序"点击/聚焦定位原始数据"的根因): [nib] 按整字节, (单位) 剥掉
  // (夹具的极简选择器不支持 class 选择器, flash 的 DOM 断言在真机页验证)
  const p = (t) => globalThis.__ctx.parseFieldSpans(t).map((sp) => sp.start + "-" + (sp.end - 1));
  // vm realm 的数组原型与宿主不同, 用字符串比较
  assert.equal(p("0x1D(12b)/0x78").join(","), "29-29,120-120", "tRC 的 (12b) 注记偏移");
  assert.equal(p("0x1C/0x1B[3:0]").join(","), "28-28,27-27", "tRAS 的 nibble 注记偏移");
  assert.equal(p("0x46-0x47(ps)").join(","), "70-71", "DDR5 ps 单位注记偏移");
});

test("编辑器: 输入时序 ns 时右侧 clk 提示实时折算", async () => {
  const { el } = setup();
  await flush();
  el("tab-edit").onclick();
  await readDevice(el);
  await flush();
  const grp = [...el("edit-groups").children].find((b) => b.textContent.includes("JEDEC 时序"));
  if (grp) grp.onclick();

  const inp = el("edit-fields").querySelector('input[data-key="ddr4.tAA"]');
  const hint = el("edit-fields").querySelector('span[data-key="ddr4.tAA"]');
  assert.ok(hint, "时序字段应有周期提示");
  // 基准 = state.tckNs = 0.625ns: 输入 1.25 → 恰好 2 clk
  inp.value = "1.25";
  await inp.oninput();
  assert.equal(hint.textContent, "2 clk");
  assert.equal(hint.getAttribute("data-clk"), "2clk", "data-clk 应同步成可填写的写法");
  // 非整除: 1.4 / 0.625 = 2.24 → 向上取整 3
  inp.value = "1.4";
  await inp.oninput();
  assert.equal(hint.textContent, "3 clk");
  // clk 写法/乱码不折算(等提交后由后端重算)
  inp.value = "16clk";
  await inp.oninput();
  assert.equal(hint.textContent, "3 clk", "带 clk 后缀的输入不应按 ns 折算");
  inp.value = "abc";
  await inp.oninput();
  assert.equal(hint.textContent, "3 clk", "乱码保留原提示");
});

test("编辑器: 改原始字节后 SPD 信息与编辑器字段同步刷新", async () => {
  const { el, calls } = setup();
  await flush();
  el("tab-edit").onclick();
  await readDevice(el);
  await flush();

  calls.length = 0;
  const findTarget = () =>
    el("hexgrid").querySelectorAll("span[data-off]").find((b) => b.getAttribute("data-off") === "260");
  const target = findTarget();
  el("hexgrid").onclick({ target });
  const inp = target.querySelector("input");
  inp.value = "5A";
  await inp.onkeydown({ key: "Enter", preventDefault() {} });
  await flush();

  assert.ok(calls.some((c) => c.name === "EditSetByte"), "应先提交字节");
  assert.ok(calls.some((c) => c.name === "EditFields"), "编辑器字段值应重新拉取(字段值可能随字节变化)");
  assert.ok(calls.some((c) => c.name === "Decode"), "SPD 信息面板应重新解码");
  assert.doesNotMatch(el("log").text(), /同步 SPD 信息失败/, "同步失败不应出现在日志里");
});

test("编辑器: 周期提示点击填入 clk 写法, 按周期与按时间都可写", async () => {
  const { el, calls } = setup();
  await flush();
  el("tab-edit").onclick();
  await readDevice(el);
  await flush();

  // 切到 JEDEC 时序分组再找提示
  const grp2 = [...el("edit-groups").children].find((b) => b.textContent.includes("JEDEC 时序"));
  if (grp2) grp2.onclick();
  // 点击 tAA 的周期提示 → 输入框被填成 "2clk"
  const hint = el("edit-fields").querySelector('span[data-clk]');
  assert.ok(hint, "时序字段应有周期提示");
  hint.onclick();
  const inp = el("edit-fields").querySelector('input[data-key="ddr4.tAA"]');
  assert.equal(inp.value, "2clk", "点击提示应把按周期的写法填进输入框");
  await inp.onkeydown({ key: "Enter" });
  await flush();
  const setCall = calls.filter((c) => c.name === "EditSetField").pop();
  assert.deepEqual(setCall.args, ["ddr4.tAA", "2clk"], "应把 clk 输入原样交给后端(换算在后端做)");
  assert.match(el("log").text(), /编辑 ddr4\.tAA = 2clk/);
});

test("编辑器: 分组切换(JEDEC 时序/常用信息分开渲染)", async () => {
  const { el } = setup();
  await flush();
  el("tab-edit").onclick();
  await readDevice(el);
  const tabs = el("edit-groups").children;
  assert.ok(tabs.length >= 2, "应有多个分组按钮: " + tabs.length);
  const names = tabs.map((t) => t.textContent).join("|");
  assert.match(names, /常用信息/);
  assert.match(names, /JEDEC 时序/);
  assert.match(names, /\(\d+\/\d+\)/, "分组按钮应带 常用/全部 计数");
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
  await readDevice(el);
  assert.match(el("hexgrid").innerHTML, /data-off="260"/, "hex 视图的字节应带 data-off(可点击)");
  assert.match(el("hexgrid").innerHTML, /colhead/, "hex 视图应有列头(00..0F)");

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
  await readDevice(el);

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
  await readDevice(el);

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
  await readDevice(el);

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
  await readDevice(el);
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
  await readDevice(el);

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
  await readDevice(el);

  assert.equal(el("btn-edit-write").disabled, true, "CRC 不通过必须禁用写入");
  assert.match(el("edit-diff").innerHTML, /CRC 不通过/);
});

test("写入设备: 不再需要勾备份/干跑, 只需确认串(自动备份)", async () => {
  const { el, calls } = setup();
  await flush();
  el("tab-edit").onclick();
  await readDevice(el);

  // 界面上已没有"我已另有备份"与"干跑"控件(查询返回空壳元素: className 为空)
  assert.equal(el("chk-edit-backup").className, "", "备份勾选应已移除(每次自动备份)");
  assert.equal(el("chk-edit-dryrun").className, "", "干跑勾选应已移除");

  el("inp-edit-ack").value = "WRITE";
  await el("btn-edit-write").onclick();
  await flush();
  const call = calls.find((c) => c.name === "EditApplyToDevice");
  assert.ok(call, "应调用 EditApplyToDevice");
  assert.deepEqual([...call.args], [false, false, "WRITE"]);
  assert.match(html, /id="inp-edit-ack"[^>]*placeholder="WRITE"/, "确认串占位符应写在 index.html 里");
});

test("编辑器: 写入进行中双击必须被拒(不排队第二次写流水线)", async () => {
  let resolveWrite;
  let writeCalls = 0;
  const { el, calls } = setup({
    EditApplyToDevice: () => new Promise((res) => { writeCalls++; resolveWrite = res; }),
  });
  await flush();
  el("tab-edit").onclick();
  await readDevice(el);

  el("inp-edit-ack").value = "WRITE";
  const first = el("btn-edit-write").onclick();
  await flush();
  const second = el("btn-edit-write").onclick(); // 写入还没返回, 双击
  await second;
  assert.equal(writeCalls, 1, "进行中双击不得发起第二次 EditApplyToDevice");
  assert.match(el("log").text(), /已有一次写入在进行/);

  resolveWrite({ written: 5, total: 5, verified: true, backupPath: "/tmp/b", message: "ok" });
  await first;
  await flush();
  assert.equal(writeCalls, 1, "完成后才允许下一次写入");
});

test("编辑器: 写入失败后自动重读设备(回滚结果只有重读才知道)", async () => {
  const { el, calls } = setup({
    EditApplyToDevice: () => { throw new Error("写入中止(write)@ 0x2C5: 已写 3/4 字节…; 已自动回滚到写入前内容(3 字节, 读回校验通过)"); },
  });
  await flush();
  el("tab-edit").onclick();
  await readDevice(el);

  calls.length = 0;
  el("inp-edit-ack").value = "WRITE";
  await el("btn-edit-write").onclick();
  await flush();

  assert.ok(calls.some((c) => c.name === "EditApplyToDevice"));
  assert.ok(calls.some((c) => c.name === "Dump"), "写入失败后必须重新读设备");
  assert.match(el("log").text(), /已自动回滚/, "回滚结果要出现在日志里");
  assert.match(el("log").text(), /设备当前实际内容/, "要说明左侧现在显示的是设备内容");
});

test("写入成功后自动复核(不需要用户手点)", async () => {
  let verified = false;
  const { el, calls } = setup({
    EditVerifyFile: () => {
      verified = true;
      return EMPTY_DIFF;
    },
  });
  await flush();
  el("tab-edit").onclick();
  await readDevice(el);

  el("inp-edit-ack").value = "WRITE";
  await el("btn-edit-write").onclick();
  await flush();
  assert.ok(calls.some((c) => c.name === "EditApplyToDevice"), "应先写入");
  assert.ok(verified, "写入成功后必须自动复核(调用 EditVerifyFile)");
  assert.match(el("log").text(), /自动复核通过/, "复核结果要写进日志");
  assert.match(el("edit-diff").innerHTML, /逐字节一致/);
});

test("自动复核发现不一致时要显眼提示", async () => {
  const { el } = setup({
    EditVerifyFile: () => ({
      changes: [{ offset: 0x208, old: 0x00, new: 0x01, field: "与设备不一致", risk: "medium" }],
      fields: [], highRisk: 0, crcFields: 0, changeCount: 1, crcOk: true, truncated: false,
    }),
  });
  await flush();
  el("tab-edit").onclick();
  await readDevice(el);
  el("inp-edit-ack").value = "WRITE";
  await el("btn-edit-write").onclick();
  await flush();
  assert.match(el("edit-diff").innerHTML, /不一致/);
  assert.match(el("edit-diff").innerHTML, /0x208/);
  assert.match(el("log").text(), /不一致/);
});

test("切换设备后编辑器内容随之刷新(不会残留上一根条的内容)", async () => {
  let addr = "0x50";
  const { el, calls } = setup({
    EditState: () => ({ ...state, source: `设备 ${addr}` }),
  });
  await flush();
  el("tab-edit").onclick();
  await readDevice(el);
  assert.equal(el("btn-edit-fixcrc").disabled, false, "读取后重算 CRC 应可用");
  assert.match(el("edit-state").textContent, /设备 0x50/);

  // 切到另一根条: 读取后编辑器自动换成新设备的内容
  addr = "0x51";
  el("dimm-select").value = "81";
  await el("dimm-select").onchange();
  await flush();
  assert.ok(calls.some((c) => c.name === "Select"), "应调用 Select");
  assert.match(el("edit-state").textContent, /设备 0x51/, "编辑器应显示新设备的内容");
});

test("读到的内容无法编辑时编辑器清空并禁用操作(不会留下旧内容)", async () => {
  const { el } = setup({
    EditState: () => { throw new Error("编辑器尚未载入数据"); },
  });
  await flush();
  el("tab-edit").onclick();
  await readDevice(el);
  assert.equal(el("edit-fields").html().includes("部件号"), false, "字段表单应清空");
  assert.match(el("edit-fields").html(), /编辑器还没有载入数据/);
  assert.equal(el("btn-edit-fixcrc").disabled, true, "未载入时重算 CRC 应禁用");
  assert.equal(el("btn-edit-write").disabled, true, "未载入时禁止写入");
});
