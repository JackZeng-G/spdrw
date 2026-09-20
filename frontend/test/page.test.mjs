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

function collect(source, re) {
  return new Set([...source.matchAll(re)].map((m) => m[1]));
}

test("app.js 引用的元素 id 都存在于真实 index.html", () => {
  const htmlIDs = collect(html, /id="([^"]+)"/g);
  const used = collect(appJS, /\$\("([^"]+)"\)/g);
  const missing = [...used].filter((id) => !htmlIDs.has(id));
  assert.deepEqual(missing, [], "index.html 缺少这些元素: " + missing.join(", "));
  assert.ok(used.size > 20, "只解析到 " + used.size + " 个 id, 解析可能失效");
});

test("app.js 调用的绑定方法都有桩(冒烟页才不会报找不到方法)", () => {
  const methods = collect(appJS, /\bcall\("([A-Za-z0-9_]+)"/g);
  const missing = [...methods].filter((m) => !new RegExp("\\b" + m + ":\\s*rec\\(").test(stub));
  assert.deepEqual(missing, [], "stub.js 缺少这些方法的桩: " + missing.join(", "));
  assert.ok(methods.size > 20, "只解析到 " + methods.size + " 个方法, 解析可能失效");
});

test("index.html 仍可被 make-real-page.mjs 改写(只有一个 app.js 标签)", () => {
  const hits = html.match(/<script src="app\.js"><\/script>/g) || [];
  assert.equal(hits.length, 1, "app.js 脚本标签数量 = " + hits.length);
});
