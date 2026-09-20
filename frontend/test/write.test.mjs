// 前端契约测试(写入路径): 界面只有一个写入入口 —— 编辑器里的"写入设备…"。
// (原"写入文件…"面板与"干跑/我已另有备份"勾选已按用户反馈移除; 备份每次自动做。)
import test from "node:test";
import assert from "node:assert/strict";
import fs from "node:fs";
import { loadApp, makeAppStub, flush, readDevice, EMPTY_DIFF } from "./harness.mjs";

// 真实发布页(确认串占位符等静态文案的唯一来源; app.js 不再重复赋值)
const html = fs.readFileSync(new URL("../dist/index.html", import.meta.url), "utf8");

const state = {
  source: "设备 0x50", generation: "DDR4", size: 512,
  dirty: true, changeCount: 3, crcOk: true, canWrite: true, crcStale: false,
};
const diff = {
  changes: [{ offset: 325, old: 0xde, new: 0x11, field: "序列号", risk: "low" }],
  fields: [], highRisk: 0, crcFields: 2, changeCount: 3, crcOk: true, truncated: false,
  crcDirty: 0, crcFreeDirty: 3, dirtyInCrc: [], dirtyFree: [323, 325, 329],
};

function setup(overrides = {}) {
  const { stub, calls } = makeAppStub({
    EditState: () => state,
    EditFields: () => [],
    EditDiff: () => diff,
    EditApplyToDevice: () => ({
      dryRun: false, written: 3, total: 3, verified: true,
      backupPath: "/root/.spdrw/backups/x.bin", message: "写入并校验通过: 3 字节(备份 …)",
    }),
    EditVerifyFile: () => EMPTY_DIFF,
    ...overrides,
  });
  const h = loadApp({ appStub: stub });
  return { ...h, calls };
}

test("写入: 编辑器是唯一入口, 传 (force=false, dryRun=false, ack)", async () => {
  const { el, calls } = setup();
  await flush();
  el("tab-edit").onclick();
  await readDevice(el);
  el("inp-edit-ack").value = "WRITE";
  await el("btn-edit-write").onclick();
  await flush();
  const call = calls.find((c) => c.name === "EditApplyToDevice");
  assert.ok(call, "应调用 EditApplyToDevice");
  assert.deepEqual([...call.args], [false, false, "WRITE"]);
  assert.equal(calls.some((c) => c.name === "WriteConfirmed"), false,
    "旧的“写入文件…”路径已移除, 不应再调用 WriteConfirmed");
});

test("写入: 界面不再有备份/干跑勾选(备份每次自动做)", async () => {
  const { el } = setup();
  await flush();
  el("tab-edit").onclick();
  await readDevice(el);
  for (const id of ["chk-edit-backup", "chk-edit-dryrun", "chk-backup", "chk-dryrun", "write-modal"]) {
    assert.equal(el(id).className, "", `${id} 应已从界面移除`);
  }
  assert.match(html, /id="inp-edit-ack"[^>]*placeholder="WRITE"/, "确认串占位符应写在 index.html 里");
});

test("写入: CRC 不通过或有未保存内容时按钮状态正确", async () => {
  const bad = setup({ EditDiff: () => ({ ...diff, crcOk: false, crcDirty: 1 }) });
  await flush();
  bad.el("tab-edit").onclick();
  await readDevice(bad.el);
  assert.equal(bad.el("btn-edit-write").disabled, true, "CRC 不通过时禁止写入");

  const clean = setup({ EditDiff: () => ({ ...diff, changeCount: 0 }) });
  await flush();
  clean.el("tab-edit").onclick();
  await readDevice(clean.el);
  assert.equal(clean.el("btn-edit-write").disabled, true, "没有改动时禁止写入");
});

test("写入: 失败时报错并把设备真实内容重新读回来", async () => {
  const { el, calls } = setup({
    EditApplyToDevice: () => { throw new Error("写入未完成: 写入中止(write)@ 0x145: …; 已自动回滚到写入前内容(3 字节, 读回校验通过)"); },
  });
  await flush();
  el("tab-edit").onclick();
  await readDevice(el);
  calls.length = 0;
  el("inp-edit-ack").value = "WRITE";
  await el("btn-edit-write").onclick();
  await flush();
  assert.match(el("log").text(), /写入失败/);
  assert.match(el("log").text(), /已自动回滚/);
  assert.ok(calls.some((c) => c.name === "Dump"), "失败后必须重新读取设备");
});
