// 页面/桩的漂移守卫。
//
// 冒烟测试只有"页面里的元素与桩里的方法都齐全"才有意义, 所以这里机械地对四件事:
//   1. app.js 里 $("x") 用到的每个 id 都必须在真实 index.html 里存在;
//   2. app.js 里 call("M") 调用的每个绑定方法都必须在 stub.js 里有桩;
//   3. driver.js 冒烟驱动用到的元素必须都在真实 index.html 里(反向断言的要确实不在);
//   4. dist/index.html 必须仍然只有一个 app.js 脚本标签(make-real-page.mjs 依赖它)。
//
// 历史: 曾有一份手写的骨架页 smoke.html, 它与 dist/index.html 双份维护、必然漂移
// (实测驱动在它上面直接抛错, 冒烟静默失效), 已删除; 冒烟统一跑 make-real-page.mjs
// 从**发布页**生成的 _real-page.html。
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

// driver.js 是"按真实用户顺序点一遍"的冒烟驱动; 它用到的元素必须在**发布页**里存在
// (缺元素会让驱动中途抛错, `window.__SMOKE` 永远是 undefined —— 这类"静默失效"很难发现)。
// 注意 driver 里还有一批 `!document.getElementById("x")` 的反向断言(检查旧元素确已删除),
// 那些 id 是"必须不存在", 不能算进"必须有"。
test("driver.js 引用的元素都在真实 index.html 里", () => {
  const driver = fs.readFileSync(path.join(here, "driver.js"), "utf8");
  // 只认"单独的 !document.getElementById(...)"(反向断言); `!!document.getElementById(...)`
  // 是正向断言, 必须靠 (?<![!]) 排除掉, 否则会把"必须存在"的元素误判成"必须不存在"。
  const mustBeAbsent = collect(driver, /(?<![!])!\s*document\.getElementById\("([^"]+)"\)/g);
  const need = new Set([
    ...collect(driver, /\$\("([^"]+)"\)/g),
    ...collect(driver, /getElementById\("([^"]+)"\)/g),
  ].filter((id) => !mustBeAbsent.has(id)));
  const DYNAMIC_IDS = new Set(["read-mode"]);
  const ids = collect(html, /id="([^"]+)"/g);
  const missing = [...need].filter((id) => !ids.has(id) && !DYNAMIC_IDS.has(id));
  assert.deepEqual(missing, [], `发布页缺少驱动要用的元素: ${missing.join(", ")}`);
  // 反向断言也要成立: 驱动声称"已删除"的元素确实不在发布页里
  for (const id of mustBeAbsent) {
    assert.ok(!ids.has(id), `驱动断言 ${id} 已删除, 但 index.html 里还在`);
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
  // 图例文字与实际格子配色必须来自同一份样式定义(单一真相: --chg-* 变量)
  assert.match(css, /--chg-free-bg:\s*#123f6b/, "自由区底色变量应在 :root 里定义");
  assert.match(css, /--chg-crc-bg:\s*#7a1f24/, "校验区底色变量应在 :root 里定义");
  assert.match(css, /\.sw-free\s*\{[^}]*var\(--chg-free-bg\)/, "图例色块要引用变量, 不能另写一份色值");
  assert.match(css, /\.hexgrid \.hexbyte\.chg-free\s*\{[^}]*var\(--chg-free-bg\)/, "格子的蓝底要引用同一个变量");
  assert.match(css, /\.hexgrid \.hexbyte\.chg-crc\s*\{[^}]*var\(--chg-crc-bg\)/, "格子的红底要引用同一个变量");
  // 改动色只允许出现在变量定义处各一次(避免又漂移成多份字面量)。
  // 注意: #4f9cf9 是 --accent 本身, "自由区描边"刻意复用它, 因此不算漂移。
  for (const hex of ["#7a1f24", "#ffe9ea", "#ff5c5c", "#123f6b", "#e8f2ff"]) {
    const n = (css.match(new RegExp(hex, "g")) || []).length;
    assert.equal(n, 1, `${hex} 只应出现在 --chg-* 变量定义里一次, 实际 ${n} 次`);
  }
  assert.match(css, /--chg-free-line:\s*#4f9cf9/, "自由区描边应复用 accent 蓝");
});

test("SPD 内容: 列头与行偏移必须固定(sticky), 否则滚动后看不出位置", () => {
  assert.match(css, /\.hexgrid \.row\.head\s*\{[^}]*position:\s*sticky/, "列头应 sticky");
  assert.match(css, /\.hexgrid \.row \.offset\s*\{[^}]*position:\s*sticky/, "行偏移应 sticky");
  assert.match(html, /id="hexgrid"/, "hex 网格容器仍在");
});
