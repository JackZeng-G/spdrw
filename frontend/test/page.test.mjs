// 页面/桩的漂移守卫。
//
// 冒烟测试只有"页面里的元素与桩里的方法都齐全"才有意义, 所以这里机械地对三件事:
//   1. app.js 里 $("x") 用到的每个 id 都必须在真实 index.html 里存在;
//   2. app.js 里 call("M") 调用的每个绑定方法都必须在 stub.js 里有桩;
//   3. dist/index.html 必须仍然只有一个 app.js 脚本标签(make-real-page.mjs 依赖它)。
import test from "node:test";
import assert from "node:assert/strict";
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const here = path.dirname(fileURLToPath(import.meta.url));
const appJS = fs.readFileSync(path.join(here, "..", "dist", "app.js"), "utf8");
const html = fs.readFileSync(path.join(here, "..", "dist", "index.html"), "utf8");
const stub = fs.readFileSync(path.join(here, "stub.js"), "utf8");
const css = fs.readFileSync(path.join(here, "..", "dist", "style.css"), "utf8");

function collect(source, re) {
  return new Set([...source.matchAll(re)].map((m) => m[1]));
}

test("app.js 引用的元素 id 都存在于真实 index.html(动态生成的除外)", () => {
  // 少数元素由 app.js 渲染时动态生成(例如信息面板里 CRC 行附带的读取方式),
  // 它们不在 index.html 里; 列在白名单里, 并额外校验 app.js 确实会生成该 id。
  const DYNAMIC_IDS = new Set(["read-mode"]);
  const htmlIDs = collect(html, /id="([^"]+)"/g);
  const used = collect(appJS, /\$\("([^"]+)"\)/g);
  const missing = [...used].filter((id) => !htmlIDs.has(id) && !DYNAMIC_IDS.has(id));
  assert.deepEqual(missing, [], "index.html 缺少这些元素: " + missing.join(", "));
  for (const id of DYNAMIC_IDS) {
    assert.ok(appJS.includes(`id="${id}"`), `动态 id ${id} 必须在 app.js 里被生成`);
  }
  assert.ok(used.size > 20, "只解析到 " + used.size + " 个 id, 解析可能失效");
});

test("app.js 调用的绑定方法都有桩(冒烟页才不会报找不到方法)", () => {
  const methods = collect(appJS, /\bcall\("([A-Za-z0-9_]+)"/g);
  const missing = [...methods].filter((m) => !new RegExp("\\b" + m + ":\\s*rec\\(").test(stub));
  assert.deepEqual(missing, [], "stub.js 缺少这些方法的桩: " + missing.join(", "));
  assert.ok(methods.size > 20, "只解析到 " + methods.size + " 个方法, 解析可能失效");
});

// app.js 的顶层会直接 $("x").onclick = ...; 少一个元素就是整个脚本在顶层抛错,
// 页面停在"检测环境…"——这类"骨架漂移"曾经真的发生过(新增 btn-write-probe 后忘了同步 smoke.html)。
test("骨架页 smoke.html 必须有 app.js 引用的每个元素", () => {
  const smoke = fs.readFileSync(path.join(here, "smoke.html"), "utf8");
  const DYNAMIC_IDS = new Set(["read-mode"]);
  const smokeIDs = collect(smoke, /id="([^"]+)"/g);
  const used = collect(appJS, /\$\("([^"]+)"\)/g);
  const missing = [...used].filter((id) => !smokeIDs.has(id) && !DYNAMIC_IDS.has(id));
  assert.deepEqual(missing, [], "smoke.html 缺少这些元素: " + missing.join(", "));
});

// driver.js 是"按真实用户顺序点一遍", 两个页面都要点得动(缺元素会在驱动里静默中断, __SMOKE 永远为空)。
// 注意 driver 里还有一批 `!document.getElementById("x")` 的反向断言(检查旧元素确已删除),
// 那些 id 是"必须不存在", 不能算进"必须有"。
test("driver.js 引用的元素在真实页面与骨架页都存在", () => {
  const driver = fs.readFileSync(path.join(here, "driver.js"), "utf8");
  const smoke = fs.readFileSync(path.join(here, "smoke.html"), "utf8");
  const mustBeAbsent = collect(driver, /!\s*document\.getElementById\("([^"]+)"\)/g);
  const need = new Set([
    ...collect(driver, /\$\("([^"]+)"\)/g),
    ...collect(driver, /getElementById\("([^"]+)"\)/g),
  ].filter((id) => !mustBeAbsent.has(id)));
  const DYNAMIC_IDS = new Set(["read-mode"]);
  for (const [name, src] of [["dist/index.html", html], ["smoke.html", smoke]]) {
    const ids = collect(src, /id="([^"]+)"/g);
    const missing = [...need].filter((id) => !ids.has(id) && !DYNAMIC_IDS.has(id));
    assert.deepEqual(missing, [], `${name} 缺少驱动要用的元素: ${missing.join(", ")}`);
  }
});

test("index.html 仍可被 make-real-page.mjs 改写(只有一个 app.js 标签)", () => {
  const hits = html.match(/<script src="app\.js"><\/script>/g) || [];
  assert.equal(hits.length, 1, "app.js 脚本标签数量 = " + hits.length);
});

test("图例必须用真实色块, 不能拿绿字写个「蓝」(颜色要与格子里一致)", () => {
  // 曾经的 bug: 图例写 <b class="ok">蓝</b> —— .ok 是绿色, 于是"蓝"字显示为绿色。
  assert.match(html, /class="sw sw-crc"/, "应有红色块图例");
  assert.match(html, /class="sw sw-free"/, "应有蓝色块图例");
  assert.match(html, /class="sw sw-crcbyte"/, "应有黄框图例");
  assert.ok(!/class="ok">蓝/.test(html), "图例不得用彩色文字冒充蓝色(会渲染成绿色)");
  // 图例文字与实际格子配色必须来自同一份样式定义
  assert.match(css, /\.sw-free\s*\{[^}]*#123f6b/, "图例蓝色块要与 .chg-free 同色");
  assert.match(css, /\.hexgrid \.hexbyte\.chg-free\s*\{[^}]*#123f6b/, "格子的蓝底要在样式里");
  assert.match(css, /\.hexgrid \.hexbyte\.chg-crc\s*\{[^}]*#7a1f24/, "格子的红底要在样式里");
});

test("SPD 内容: 列头与行偏移必须固定(sticky), 否则滚动后看不出位置", () => {
  assert.match(css, /\.hexgrid \.row\.head\s*\{[^}]*position:\s*sticky/, "列头应 sticky");
  assert.match(css, /\.hexgrid \.row \.offset\s*\{[^}]*position:\s*sticky/, "行偏移应 sticky");
  assert.match(html, /id="hexgrid"/, "hex 网格容器仍在");
});
