// 前端契约测试(总线统计): 真机 V2 读证据的入口。
import test from "node:test";
import assert from "node:assert/strict";
import { loadApp, makeAppStub, flush } from "./harness.mjs";

const stats = {
  generation: "DDR5", reads: 2140, quickWrites: 17, byteDataWrites: 0, byteWrites: 0, nvmWrites: 0,
};

test("总线统计: 渲染读数与 NVM 写, 并在 NVM=0 时用正常色", async () => {
  const { stub, calls } = makeAppStub({ BusStats: () => stats, ResetBusStats: () => null });
  const { el } = loadApp({ appStub: stub });
  await flush();

  await el("btn-bus-stats").onclick();
  await flush();
  const el2 = el("bus-stats");
  assert.match(el2.innerHTML, /DDR5/);
  assert.match(el2.innerHTML, /读 <b>2140<\/b>/);
  assert.match(el2.innerHTML, /NVM 写 <b class="ok">0<\/b>/, "NVM=0 应为正常色");
  assert.match(el("log").text(), /总线统计: 读 2140/);

  assert.ok(calls.some((c) => c.name === "BusStats"));
});

test("总线统计: NVM 写非 0 时标红(干跑出问题要显眼)", async () => {
  const { stub } = makeAppStub({ BusStats: () => ({ ...stats, nvmWrites: 3, byteDataWrites: 3 }) });
  const { el } = loadApp({ appStub: stub });
  await flush();
  await el("btn-bus-stats").onclick();
  await flush();
  assert.match(el("bus-stats").innerHTML, /NVM 写 <b class="bad">3<\/b>/);
});

test("总线统计: 清零按钮调用后端并刷新", async () => {
  let n = 0;
  const { stub, calls } = makeAppStub({
    BusStats: () => ({ ...stats, reads: n === 0 ? 500 : 0 }),
    ResetBusStats: () => { n++; return null; },
  });
  const { el } = loadApp({ appStub: stub });
  await flush();
  await el("btn-bus-reset").onclick();
  await flush();
  assert.ok(calls.some((c) => c.name === "ResetBusStats"), "应调用清零");
  assert.match(el("bus-stats").innerHTML, /读 <b>0<\/b>/, "清零后应刷新为 0");
});

test("块读加速: 开关调后端并提示, 读取方式显示事务数", async () => {
  const readStats = {
    bytes: 1024, transactions: 34, blockBytes: 1024, fallbackBytes: 0,
    blockReadOK: true, blockReadKnown: true,
  };
  const { stub, calls } = makeAppStub({ ReadStats: () => readStats, SetFastRead: () => true });
  const { el } = loadApp({ appStub: stub });
  await flush();
  await el("btn-bus-stats").onclick();
  await flush();

  el("chk-fastread").checked = false;
  await el("chk-fastread").onchange();
  await flush();
  const call = calls.find((c) => c.name === "SetFastRead");
  assert.ok(call, "切换开关应调用 SetFastRead");
  assert.deepEqual([...call.args], [false]);
  assert.match(el("log").text(), /块读加速/);

  // refreshReadMode 在 app.js 里是函数声明(挂在 vm 全局上), 直接调用它
  await globalThis.__ctx.refreshReadMode();
  assert.match(el("read-mode").innerHTML, /块读加速/);
  assert.match(el("read-mode").innerHTML, /34/);
});

test("读取方式: 后端给 mode/wordBytes/elapsedMs 时按加速档渲染(块读与字读都要显示)", async () => {
  // 后端 Read() 会算出实际生效的档位: 块读(32B) → 字读(2B) → 逐字节(1B)。
  // 真机上块读常被 FCH 拒绝, 落到字读——界面必须如实显示, 便于对照耗时。
  const wordStats = {
    bytes: 1024, transactions: 520, blockBytes: 0, wordBytes: 1024, fallbackBytes: 0,
    blockReadOK: false, blockReadKnown: true, wordReadOK: true, wordReadKnown: true,
    mode: "字读(2 字节/事务)", elapsedMs: 16050, sleepMs: 0,
    note: "块读失败: 设备无响应 NACK(0xC000000E)",
  };
  const { stub } = makeAppStub({ ReadStats: () => wordStats, BusStats: () => stats });
  const { el } = loadApp({ appStub: stub });
  await flush();
  await el("btn-bus-stats").onclick();
  await flush();
  await globalThis.__ctx.refreshReadMode();
  const html = el("read-mode").innerHTML;
  assert.match(html, /字读\(2 字节\/事务\)/, "应显示后端给的实际档位");
  assert.match(html, /520/, "应显示事务数");
  assert.match(html, /16\.05s/, "应显示耗时秒数");
  assert.match(html, /字读 1024B/, "应分别显示块读/字读/逐字节字节数");
  assert.match(html, /NACK/, "块读失败原因要能看到");
});
