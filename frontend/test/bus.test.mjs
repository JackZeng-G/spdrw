// 前端契约测试(总线统计): 真机 V2 读证据的入口。
import test from "node:test";
import assert from "node:assert/strict";
import { loadApp, makeAppStub, flush } from "./harness.mjs";

const stats = {
  generation: "DDR5", reads: 2140, quickWrites: 17, byteDataWrites: 0, byteWrites: 0, nvmWrites: 0,
};

test("读取方式: 紧凑挂在信息面板的 CRC 行(不再单独占一行)", async () => {
  const readStats = {
    bytes: 1024, transactions: 512, blockBytes: 0, wordBytes: 1024, fallbackBytes: 0,
    blockReadOK: false, blockReadKnown: true, mode: "字读(2 字节/事务)", elapsedMs: 334, sleepMs: 2,
  };
  const tuning = { tunable: true, clockHz: 392900, sleepMode: 0, sleepModeName: "忙等(最快)" };
  const { stub, calls } = makeAppStub({
    ReadStats: () => readStats,
    BusTuning: () => tuning,
    Decode: () => ({ ramType: "DDR4", size: 512, crcOk: true, totalMib: 8192, totalHuman: "8 GiB" }),
  });
  const { el } = loadApp({ appStub: stub });
  await flush();
  // 信息面板渲染后才会生成 #read-mode(CRC 行内): 先走一次完整读取流程
  await globalThis.__ctx.doDump();
  await globalThis.__ctx.refreshReadMode();
  assert.ok(calls.some((c) => c.name === "ReadStats"), "应读取读取方式统计");
  assert.match(el("info-body").innerHTML, /CRC/, "信息面板应有 CRC 行");
  assert.match(el("info-body").innerHTML, /id="read-mode"/, "读取方式应挂在 CRC 行里");
  const html = el("read-mode").innerHTML;
  assert.match(html, /字读\(2 字节\/事务\)/, "应显示实际档位");
  assert.match(html, /512/, "应显示事务数");
  assert.match(html, /0\.33s/, "应显示耗时");
  assert.match(html, /392\.9kHz/, "应显示 SMBus 时钟");
  assert.match(html, /忙等/, "应显示等待模式");
});

test("总线统计面板已移除(信息并入日志与读取方式一行)", async () => {
  const { stub } = makeAppStub({});
  const { document } = loadApp({ appStub: stub });
  await flush();
  for (const id of ["bus-stats", "bus-tuning", "btn-bus-stats", "btn-bus-reset"]) {
    assert.equal(document.getElementById(id).className, "", `${id} 应已从界面移除`);
  }
});

test("问号提示: 点一下才展开说明", async () => {
  const { stub } = makeAppStub({});
  const { el, document } = loadApp({ appStub: stub });
  await flush();
  const hint = el("hint-write");
  hint.className = "hint hidden";
  document.dispatch("click", { target: { getAttribute: (k) => (k === "data-hint" ? "hint-write" : null) } });
  assert.equal(hint.classList.contains("hidden"), false, "点问号后应展开");
  document.dispatch("click", { target: { getAttribute: (k) => (k === "data-hint" ? "hint-write" : null) } });
  assert.equal(hint.classList.contains("hidden"), true, "再点应收起");
});
