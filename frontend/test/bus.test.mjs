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
