// XMP 3.0 槽位卡片(DDR5): 3 份 Profile + 2 份 User。
//
// 数据来自编辑器的字段列表(group "XMP 3.0", 后端已把每槽的电压/时序解好),
// 所以这里要断的是"聚合与展示"是否正确: 槽号→槽名、启用状态、空槽、EXPO 占用、
// tCK 换算成速率, 以及字段还没到手时会不会退回一行式简表。
import test from "node:test";
import assert from "node:assert/strict";
import { loadApp, makeAppStub, flush, readDevice } from "./harness.mjs";

const ddr5Decode = {
  valid: true, ramType: "DDR5", moduleType: "UDIMM", size: 1024,
  totalMib: 32768, totalHuman: "32 GiB", ranks: 2, deviceWidth: 16, busWidth: 64,
  manufacturer: "G.Skill", partNumber: "F5-6000J3038F16G", dateYear: 2024, dateWeek: 8,
  serialHex: "01020304", crcOk: true, hasTimings: false,
  hasXmp: true, hasExpo: false,
};

// xmp3Field 造一个后端会给出的字段(键名与 internal/spd/editor_profiles.go 一致)
function xmp3Field(key, value, unit) {
  return { key, name: key, group: "XMP 3.0", kind: unit ? "float" : "string", value, unit, offset: "0x2C0", risk: "medium" };
}

const xmp3Fields = [
  xmp3Field("xmp3.present", "true"),
  xmp3Field("xmp3.version", "30"),
  xmp3Field("xmp3.enabled1", "true"),
  xmp3Field("xmp3.enabled2", "false"),
  xmp3Field("xmp3.name1", "6000 CL30"),
  xmp3Field("xmp3.p1.vdd", "1.350", "V"),
  xmp3Field("xmp3.p1.vddq", "1.350", "V"),
  xmp3Field("xmp3.p1.vpp", "1.800", "V"),
  xmp3Field("xmp3.p1.tCK", "0.333", "ns"),
  xmp3Field("xmp3.p1.cl", "30,32,34"),
  xmp3Field("xmp3.p1.tAA", "0.5", "ns"),
  xmp3Field("xmp3.p1.tRCD", "0.5", "ns"),
  xmp3Field("xmp3.p1.tRFC1", "160", "ns"),
  // 槽 2: 只有一部分字段(未启用), 仍应算"有数据"而不是"空槽"
  xmp3Field("xmp3.p2.tCK", "0.416", "ns"),
  xmp3Field("xmp3.name2", ""),
  // 槽 3/4/5: 后端不给字段(空槽)
];

const editState = {
  source: "设备 0x51", generation: "DDR5", size: 1024,
  dirty: false, changeCount: 0, crcOk: true, canWrite: true,
};

function setup(overrides = {}) {
  const { stub, calls } = makeAppStub({
    Decode: () => ddr5Decode,
    EditState: () => editState,
    EditFields: () => xmp3Fields,
    ...overrides,
  });
  const h = loadApp({ appStub: stub });
  return { ...h, calls };
}

test("XMP 3.0: 5 个槽位卡片, 关键项带频率与速率换算", async () => {
  const { el } = setup();
  await flush();
  await readDevice(el, { addr: "81", flushes: 2 });
  const html = el("info-body").html();

  assert.match(html, /Intel XMP 3\.0/, "应出 XMP 3.0 专用卡片");
  const slots = html.match(/class="slot(?: (?:on|empty|expo))?"/g) || [];
  assert.equal(slots.length, 5, "XMP 3.0 应有 5 个槽(3 Profile + 2 User), 实际 " + slots.length);

  // 槽名按 XMP 3.0 布局
  for (const n of ["Profile 1", "Profile 2", "Profile 3", "User 1", "User 2"]) {
    assert.ok(html.includes(n), "缺少槽名 " + n);
  }
  // 启用的槽与名称
  assert.match(html, /class="slot on"/, "启用的槽应有 on 标记");
  assert.match(html, /已启用/);
  assert.match(html, /6000 CL30/, "槽名称要显示出来");
  // tCK 0.333 ns ≈ 3003 MHz ≈ 6006 MT/s(DDR 双沿)
  assert.match(html, /0\.333 ns/);
  assert.match(html, /3003 MHz/);
  assert.match(html, /6006 MT\/s/, "tCK 应换算成速率");
  assert.match(html, /<th>tAA<\/th><td>0\.5 ns<\/td>/, "主时序 tAA 要在表里");
  assert.match(html, /<th>tRCD<\/th><td>0\.5 ns<\/td>/, "主时序 tRCD 要在表里");
  assert.match(html, /30,32,34/, "CL 掩码要显示");
  assert.match(html, /1\.350 V/, "电压要显示");
  // 只填了部分字段的槽算有数据
  assert.ok(html.includes("0.416 ns"), "槽 2 的 tCK 应显示");
  assert.match(html, /空槽/, "没有字段的槽显示空槽");
});

test("XMP 3.0: EXPO 存在时槽 3 / User 1 标为被占用", async () => {
  const { el } = setup({ Decode: () => ({ ...ddr5Decode, hasExpo: true }) });
  await flush();
  await readDevice(el, { addr: "81", flushes: 2 });
  const html = el("info-body").html();
  const expo = html.match(/class="slot expo"/g) || [];
  assert.equal(expo.length, 2, "EXPO 占两个槽(0x340/0x380)");
  assert.match(html, /EXPO 占用/);
  assert.match(html, /EXPO 占用槽 3 \/ User 1/, "卡片标题要说明谁被占用");
});

test("XMP 3.0: 字段未到手时退回一行式简表(不能空白)", async () => {
  const { el } = setup({
    EditFields: () => [],
    Decode: () => ({
      ...ddr5Decode,
      xmp: [{ number: 1, enabled: true, version: 0x30, summary: "6000 MT/s · 1.35V", casLatencies: "30" }],
    }),
  });
  await flush();
  await readDevice(el, { addr: "81", flushes: 2 });
  const html = el("info-body").html();
  assert.match(html, /Intel XMP/, "回退也要有 XMP 信息");
  assert.match(html, /6000 MT\/s · 1\.35V/, "简表显示后端给的 summary");
  assert.equal((html.match(/class="slot(?: (?:on|empty|expo))?"/g) || []).length, 0, "没有字段时不该画槽位卡片");
});
