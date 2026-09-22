// 前端契约测试(写保护): 断言 app.js 渲染与"传给后端的参数形态"。
// 运行: node --test frontend/test/
import test from "node:test";
import assert from "node:assert/strict";
import fs from "node:fs";
import { loadApp, makeAppStub, flush } from "./harness.mjs";

// DDR5 样本(16 块 × 64B): 块 1、15 受保护; MR52[6] 命中; 离线模式
const ddr5Status = {
  ddr5: true, generation: "DDR5", blocks: 16, blockSize: 64,
  protected: Array.from({ length: 16 }, (_, i) => i === 1 || i === 15),
  known: Array.from({ length: 16 }, () => true),
  mr11: 2, mr12: 0x0a, mr13: 0x80, mr29: 0, mr48: 0x04, mr52: 0x40,
  protectionHit: true, offline: true,
  pswpApplicable: false, pswp: false,
  warnings: ["MR12/MR13 已置位: 需离线模式"],
};

function withStatus(status) {
  return makeAppStub({ WPStatus: () => status }).stub;
}

test("WPStatus 渲染块范围/状态/寄存器与 PSWP 不适用", async () => {
  const { el, ctx } = loadApp({ appStub: withStatus(ddr5Status) });
  await flush();
  await ctx.renderWP(ddr5Status);

  const blocks = el("wp-blocks");
  assert.equal(blocks.children.length, 16, "应有 16 个块");
  assert.match(blocks.children[0].textContent, /B0 0x000-0x03F/);
  assert.match(blocks.children[1].textContent, /B1 0x040-0x07F 🔒/);
  assert.equal(blocks.children[0].classList.contains("off"), true);
  assert.equal(blocks.children[1].classList.contains("on"), true);

  const summary = el("wp-summary").innerHTML;
  assert.match(summary, /MR12=0x0A/);
  assert.match(summary, /MR13=0x80/);
  assert.match(summary, /PSWP 不适用/, "DDR5 不得显示 PSWP 结论");
  assert.match(summary, /离线模式/);
  assert.match(summary, /MR52\[6\]/);
  assert.doesNotMatch(summary, /PSWP 永久保护已生效/, "不得出现 DDR5 假阳性文案");
});

test("WPStatus 对未知状态显示 ❔ 而不是谎报受保护", async () => {
  const st = {
    ...ddr5Status, ddr5: false, generation: "DDR4", blocks: 4, blockSize: 128,
    protected: [true, false, false, false], known: [false, true, true, true],
    mr11: 0, mr12: 0, mr13: 0, mr48: 0, mr52: 0, protectionHit: false, offline: false,
    pswpApplicable: true, pswp: false,
    warnings: ["块 0(0x000)状态未知: 写测试还原失败"],
  };
  const { el, ctx } = loadApp({ appStub: withStatus(st) });
  await flush();
  await ctx.renderWP(st);

  const b0 = el("wp-blocks").children[0];
  assert.equal(b0.classList.contains("unknown"), true, "未知状态应有独立样式");
  assert.match(b0.textContent, /❔/);
  assert.match(el("wp-summary").innerHTML, /PSWP 未设置/);
  assert.match(el("wp-summary").innerHTML, /状态未知/);
});

test("点击查询保护状态: 走绑定并写日志, 不再抛 null 解构错", async () => {
  const appStub = withStatus(ddr5Status);
  const { el } = loadApp({ appStub });
  await flush();
  await el("btn-wp-status").onclick();
  await flush();
  const log = el("log").text();
  assert.doesNotMatch(log, /失败/, "不应出现失败日志: " + log);
  assert.match(log, /保护状态/);
});

test("汇总行区分「受保护」与「未知/不可写」: 写测试全败(i801 平台拒绝)不得谎报受保护", async () => {
  // 真机 i3-7100(2026-09-22): 4 块写测试都失败(known=false, 后端保守置 protected=true)
  const st = {
    ...ddr5Status, ddr5: false, generation: "DDR4", blocks: 4, blockSize: 128,
    protected: [true, true, true, true], known: [false, false, false, false],
    mr11: 0, mr12: 0, mr13: 0, mr48: 0, mr52: 0, protectionHit: false, offline: false,
    pswpApplicable: false, pswp: false,
    warnings: ["所有块的写测试都未能生效: ..."],
  };
  const { el, ctx } = loadApp({ appStub: withStatus(st) });
  await flush();
  await ctx.renderWP(st);
  const log = el("log").text();
  assert.match(log, /未知\/不可写 B0,B1,B2,B3/);
  assert.doesNotMatch(log, /受保护 B0/, "未知块不得汇总成受保护: " + log);
});

test("汇总行: 已知保护与未知混合时各自归类", async () => {
  const st = {
    ...ddr5Status, ddr5: false, generation: "DDR4", blocks: 4, blockSize: 128,
    protected: [true, true, false, false], known: [true, false, true, false],
    mr11: 0, mr12: 0, mr13: 0, mr48: 0, mr52: 0, protectionHit: false, offline: false,
    pswpApplicable: false, pswp: false, warnings: [],
  };
  const { el, ctx } = loadApp({ appStub: withStatus(st) });
  await flush();
  await ctx.renderWP(st);
  const log = el("log").text();
  assert.match(log, /受保护 B0 · 未知\/不可写 B1,B3/);
});

test("WPSet 传数字数组 + 确认串([]int 契约, 且不可逆操作必须带 ack)", async () => {
  const { stub, calls } = makeAppStub({ WPStatus: () => ddr5Status });
  const { el, ctx } = loadApp({ appStub: stub });
  await flush();
  ctx.prompt = () => "1, 15";
  ctx.confirm = () => true;

  // 没输确认串 → 不得调用后端(RSWP 在部分颗粒上不可逆)
  await el("btn-wp-set").onclick();
  await flush();
  assert.equal(calls.some((c) => c.name === "WPSet"), false, "缺少确认串时不得下发");
  assert.match(el("log").text(), /CLEAR/, "应提示需要确认串");

  el("inp-wp-ack").value = "clear"; // 统一确认串, 大小写不敏感
  await el("btn-wp-set").onclick();
  await flush();
  const call = calls.find((c) => c.name === "WPSet");
  assert.ok(call, "应调用 WPSet");
  assert.equal(Array.isArray(call.args[0]), true, "WPSet 第一个参数必须是 JS 数组");
  // 跨 vm realm 的数组原型不同, 展开后比较
  assert.deepEqual([...call.args[0]], [1, 15]);
  assert.equal(typeof call.args[0][0], "number", "元素必须是 number(→ Go []int)");
  assert.equal(call.args[1], "CLEAR", "第二个参数必须是确认串(Go 侧再校验一次)");
});

test("WPClear 也必须带 CLEAR 确认串", async () => {
  const { stub, calls } = makeAppStub({ WPStatus: () => ddr5Status });
  const { el, ctx } = loadApp({ appStub: stub });
  await flush();
  ctx.confirm = () => true;

  await el("btn-wp-clear").onclick();
  await flush();
  assert.equal(calls.some((c) => c.name === "WPClear"), false, "缺少确认串时不得清除");

  el("inp-wp-ack").value = "CLEAR";
  await el("btn-wp-clear").onclick();
  await flush();
  const call = calls.find((c) => c.name === "WPClear");
  assert.ok(call, "应调用 WPClear");
  assert.deepEqual([...call.args], ["CLEAR"]);
});

test("写保护: 操作按钮在结果上面(先操作后看结果)", () => {
  // 用户反馈: 点"查询保护状态"后块列表出现在按钮上面不好看 —— 结果要放下面。
  const body = fs.readFileSync(new URL("../dist/index.html", import.meta.url), "utf8");
  const iStatus = body.indexOf('id="btn-wp-status"');
  const iBlocks = body.indexOf('id="wp-blocks"');
  const iSummary = body.indexOf('id="wp-summary"');
  assert.ok(iStatus > 0 && iBlocks > 0 && iSummary > 0, "三个元素都应存在");
  assert.ok(iStatus < iBlocks, "块列表必须在“查询保护状态”按钮之后");
  assert.ok(iStatus < iSummary, "状态摘要必须在按钮之后");
  assert.ok(body.includes('class="wp-result"'), "结果区应有独立容器(便于样式上分隔)");
});
