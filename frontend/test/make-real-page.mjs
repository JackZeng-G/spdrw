// 从真正发布的 frontend/dist/index.html 生成一份"可注入桩"的测试页。
//
// 为什么要这么做: 冒烟页如果自己手写一份 index.html 的骨架, 就会与真实页面漂移
// (app.js 新要一个 id、真实页漏了/多了元素, 骨架看不出来)。这里直接读真实页面,
// 只在 app.js 之前插桩、之后插驱动, 于是"测的就是发布的那份 DOM 结构"。
//
// 用法: node frontend/test/make-real-page.mjs  → 生成 frontend/test/_real-page.html
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const here = path.dirname(fileURLToPath(import.meta.url));
const distDir = path.join(here, "..", "dist");
let html = fs.readFileSync(path.join(distDir, "index.html"), "utf8");

const before = html;
html = html.replace(
  /<script src="app\.js"><\/script>/,
  '<script src="stub.js"></script>\n  <script src="../dist/app.js"></script>\n  <script src="driver.js"></script>'
);
if (html === before) {
  console.error("未找到 <script src=\"app.js\"></script>, 无法生成");
  process.exit(1);
}
// 只调整资源路径(页面挪到了 test/ 下): style.css 指向 dist
html = html.replace('<link rel="stylesheet" href="style.css">', '<link rel="stylesheet" href="../dist/style.css">');
html = html.replace("<title>SPD Reader Writer (Go)</title>", "<title>SPDRW 真实页面冒烟</title>");

const out = path.join(here, "_real-page.html");
fs.writeFileSync(out, html);
console.log("已生成", out);
