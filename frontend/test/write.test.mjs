// 前端契约测试(写入流程): 预检渲染、阻断、确认串与参数形态。
import test from "node:test";
import assert from "node:assert/strict";
import { loadApp, makeAppStub, flush } from "./harness.mjs";

const preflightOK = {
  path: "/tmp/dump.bin", addr: 0x50, generation: "DDR4", deviceSize: 512, fileSize: 512,
  sizeOk: true, changeCount: 3, crcBytes: 2,
  changes: [
    { offset: 325, old: 0x01, new: 0xab, block: 2, isCRC: false },
    { offset: 126, old: 0x11, new: 0x40, block: 0, isCRC: true },
    { offset: 127, old: 0x11, new: 0x52, block: 0, isCRC: true },
  ],
  changesTruncated: false,
  fields: [{ region: "序列号", risk: "low", count: 1, ranges: "0x145" }],
  highRiskCount: 0, protectedBlocks: [], unknownBlocks: [], pswp: false,
  targetCrcValid: true, currentCrcValid: true, dryRun: false,
  warnings: [], blocked: false, blockReason: "",
};

function setup(pf, extra = {}) {
  const { stub, calls } = makeAppStub({
    PickWriteFile: () => "/tmp/dump.bin",
    PreflightWrite: () => pf,
    WriteConfirmed: () => ({ dryRun: false, written: 3, total: 3, backupPath: "/root/.spdrw/backups/x.bin", verified: true, message: "写入并校验通过" }),
    ...extra,
  });
  const h = loadApp({ appStub: stub });
  const alerts = [];
  h.ctx.alert = (m) => alerts.push(String(m));
  return { ...h, calls, alerts };
}

test("写入: 先预检再弹确认面板, diff/字段/CRC 渲染", async () => {
  const { el, calls } = setup(preflightOK);
  await flush();
  await el("btn-write").onclick();
  await flush();

  assert.equal(el("write-modal").classList.contains("hidden"), false, "确认面板应显示");
  const names = calls.map((c) => c.name);
  const iP = names.indexOf("PickWriteFile"), iF = names.indexOf("PreflightWrite");
  assert.ok(iP >= 0 && iF > iP, "必须先取文件再预检: " + names.join(","));
  const pfCall = calls.find((c) => c.name === "PreflightWrite");
  assert.equal(pfCall.args[1], false, "force 默认 false");

  assert.match(el("write-summary").innerHTML, /变更 <b>3<\/b> 字节/);
  assert.match(el("write-summary").innerHTML, /目标文件 CRC 校验通过/);
  assert.match(el("write-fields").innerHTML, /序列号/);
  const diff = el("write-changes").innerHTML;
  assert.match(diff, /0x145 {2}01 → AB/);
  assert.match(diff, /0x07E {2}11 → 40 {2}\(CRC\)/);
  assert.equal(el("btn-write-go").disabled, false);
});

test("写入: 预检阻断时禁止执行并显示原因", async () => {
  const blocked = {
    ...preflightOK, targetCrcValid: false, blocked: true,
    blockReason: "目标文件 CRC 校验不通过(先用编辑器修复 CRC 或重新生成 dump)",
    warnings: ["变更包含 1 个高危字节(容量/组织/电压/PMIC 等), 写错可能导致无法开机"],
    highRiskCount: 1,
  };
  const { el } = setup(blocked);
  await flush();
  await el("btn-write").onclick();
  await flush();

  assert.equal(el("btn-write-go").disabled, true, "阻断时必须禁用执行按钮");
  assert.match(el("write-summary").innerHTML, /已阻断/);
  assert.match(el("write-summary").innerHTML, /高危字节 1 个/);
});

test("写入: 备份勾选已移除, 确认串仍需传给后端", async () => {
  const { el, calls } = setup(preflightOK);
  await flush();
  await el("btn-write").onclick();
  await flush();
  // 界面不再有"我已另有备份"(每次写入都会自动备份)
  assert.equal(el("chk-backup").className, "", "备份勾选应已移除");
  assert.equal(el("chk-dryrun").className, "", "干跑勾选应已移除");
  el("inp-ack").value = "WRITE";
  await el("btn-write-go").onclick();
  await flush();
  const call = calls.find((c) => c.name === "WriteConfirmed");
  assert.ok(call, "应调用 WriteConfirmed");
  assert.deepEqual([...call.args], ["/tmp/dump.bin", false, false, "WRITE"]);
});

test("写入: 强制模式勾选 → force=true", async () => {
  const { el, calls } = setup(preflightOK);
  await flush();
  await el("btn-write").onclick();
  await flush();
  el("chk-force").checked = true;
  el("inp-ack").value = "WRITE";
  await el("btn-write-go").onclick();
  await flush();
  const call = calls.find((c) => c.name === "WriteConfirmed");
  assert.ok(call);
  assert.deepEqual([...call.args], ["/tmp/dump.bin", true, false, "WRITE"]);
  assert.equal(el("write-modal").classList.contains("hidden"), true, "执行后应关闭面板");
});
