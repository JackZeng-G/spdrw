// 极简 DOM/VM 测试夹具: 在 Node 里加载 frontend/dist/app.js 并驱动它。
//
// 为什么需要它: 本项目的 UI bug 大多不是"渲染错", 而是**JS↔Go 绑定契约**错
// (返回值过多被 Wails 序列化成 null、[]byte 参数收不到 JS 数组等)。
// 这类错误只有把真实 app.js 跑起来并断言"传给后端的是什么"才能提前发现。
//
// 用法: `node --test frontend/test/`
import fs from "node:fs";
import path from "node:path";
import vm from "node:vm";
import { fileURLToPath } from "node:url";

const here = path.dirname(fileURLToPath(import.meta.url));
const APP_JS = path.join(here, "..", "dist", "app.js");

class ClassList {
  constructor() { this.set = new Set(); }
  add(...c) { c.forEach((x) => this.set.add(x)); }
  remove(...c) { c.forEach((x) => this.set.delete(x)); }
  // 真实 DOM 支持 classList.toggle(cls, force)
  toggle(c, force) {
    const want = force === undefined ? !this.set.has(c) : !!force;
    if (want) this.set.add(c); else this.set.delete(c);
    return want;
  }
  contains(c) { return this.set.has(c); }
  toString() { return [...this.set].join(" "); }
}

class El {
  constructor(tag = "div") {
    this.tagName = tag;
    this.children = [];
    this._html = "";
    this.textContent = "";
    this.classList = new ClassList();
    this.style = {};
    this.attrs = {};
    this.disabled = false;
    this.checked = false; // 真实 checkbox 默认 false(不能是 undefined)
    this.value = "";
    this.title = "";
    this.onclick = null;
    this.onchange = null;
    this.scrollTop = 0;
    this.scrollHeight = 0;
  }
  // 真实 DOM 里 className 与 classList 是同一份数据, 夹具必须保持一致,
  // 否则基于 className 赋值的实现会被误判为没有样式。
  get className() { return this.classList.toString(); }
  set className(v) {
    this.classList = new ClassList();
    String(v).split(/\s+/).filter(Boolean).forEach((c) => this.classList.add(c));
  }
  get innerHTML() { return this._html; }
  set innerHTML(v) {
    this._html = String(v);
    this.children = parseTags(this._html);
  }
  appendChild(c) { this.children.push(c); return c; }
  setAttribute(k, v) { this.attrs[k] = String(v); }
  getAttribute(k) { return this.attrs[k]; }
  addEventListener() {}
  removeEventListener() {}
  // 极简选择器: 支持 tag、tag[attr]、tag[attr="value"]
  querySelectorAll(sel) { return this.children.filter((c) => matches(c, sel)); }
  querySelector(sel) { return this.querySelectorAll(sel)[0] || null; }
  // 递归取出文本(用于断言渲染结果)
  text() { return (this.textContent || "") + this.children.map((c) => c.text?.() ?? "").join(""); }
  html() { return (this._html || "") + this.children.map((c) => c.html?.() ?? "").join(""); }
}

// makeAppStub 生成常用绑定桩, 记录每次调用到 calls(含 overrides)。
export function makeAppStub(overrides = {}) {
  const calls = [];
  const wrap = (name, ret) => (...args) => {
    calls.push({ name, args });
    return Promise.resolve(typeof ret === "function" ? ret(...args) : ret);
  };
  // 默认 Dump 返回 512 字节的 base64(Wails 的 []byte 形态), 避免测试里踩渲染分支
  const dumpB64 = Buffer.from(new Uint8Array(512)).toString("base64");
  const all = {
    ListControllers: [],
    AutoConnectAll: { ctlIndex: -1, dimms: [] },
    Connect: null,
    Scan: [],
    Select: null,
    Dump: dumpB64,
    Decode: null,
    ReadFileBytes: dumpB64,
    BusStats: null,
    ResetBusStats: null,
    WPStatus: null,
    WPSet: null,
    WPClear: null,
    // 文件/写入/编辑器绑定(未显式覆盖时返回中性值)
    SaveDumpDialog: null,
    DecodeFileDialog: null,
    VerifyFileDialog: null,
    PickWriteFile: null,
    PreflightWrite: null,
    WriteConfirmed: null,
    EditLoadFromDevice: null,
    EditLoadFileDialog: null,
    EditLoadPath: null,
    EditFields: [],
    EditSetField: null,
    EditSetByte: null,
    EditFixCRC: null,
    EditReset: null,
    EditDiff: { changes: [], fields: [], highRisk: 0, crcFields: 0, changeCount: 0, crcOk: true, truncated: false },
    EditBytes: null,
    EditExportDialog: null,
    EditApplyToDevice: null,
    EditState: null,
    MfgSearch: [],
    ...overrides,
  };
  const stub = {};
  for (const [name, ret] of Object.entries(all)) stub[name] = wrap(name, ret);
  return { stub, calls };
}

// loadApp 在最小 DOM 中加载 app.js, 返回可断言的句柄。
export function loadApp({ appStub } = {}) {
  const els = new Map();
  const document = {
    getElementById(id) { if (!els.has(id)) els.set(id, new El()); return els.get(id); },
    createElement(t) { return new El(t); },
    createTextNode(t) { const e = new El("#text"); e.textContent = t; return e; },
  };
  const window = { runtime: undefined };
  const go = appStub ? { app: { App: appStub } } : undefined;
  window.go = go;
  const ctx = {
    document, window, console, setTimeout, clearTimeout, atob, btoa,
    prompt: () => null, confirm: () => true, alert: () => {},
  };
  ctx.globalThis = ctx;
  const HTM = path.join(here, "..", "dist", "index.html");
  if (fs.existsSync(HTM)) seedFromHTML(document, fs.readFileSync(HTM, "utf8"));
  vm.createContext(ctx);
  vm.runInContext(fs.readFileSync(APP_JS, "utf8"), ctx, { filename: "app.js" });
  const el = (id) => document.getElementById(id);
  return { ctx, el, document, window };
}

// flush 让挂起的 promise 链跑完。
export const flush = () => new Promise((r) => setTimeout(r, 0));

function matches(el, sel) {
  const m = /^([a-zA-Z]+)?\[([\w-]+)(?:="([^"]*)")?\]$/.exec(sel);
  if (m) {
    const [, tag, attr, val] = m;
    if (tag && el.tagName.toLowerCase() !== tag.toLowerCase()) return false;
    const got = el.getAttribute(attr);
    if (got === undefined) return false;
    return val === undefined || got === val;
  }
  const m2 = /^([a-zA-Z]+)$/.exec(sel);
  if (m2) return el.tagName.toLowerCase() === m2[1].toLowerCase();
  return false;
}

// parseTags 从 HTML 字符串里抽出带 data-*/type/value 属性的标签, 变成可交互的元素。
// (只覆盖测试需要的形态: input / button / option)
function parseTags(html) {
  const out = [];
  const re = /<(input|button|option)\b([^>]*)>/g;
  let m;
  while ((m = re.exec(html)) !== null) {
    const tag = m[1];
    const el = new El(tag);
    const attrRe = /([\w-]+)="([^"]*)"/g;
    let a;
    while ((a = attrRe.exec(m[2])) !== null) {
      el.setAttribute(a[1], unescapeHtml(a[2]));
      if (a[1] === "value") el.value = unescapeHtml(a[2]);
    }
    out.push(el);
  }
  return out;
}

function unescapeHtml(s) {
  return String(s).replace(/&quot;/g, '"').replace(/&lt;/g, "<").replace(/&gt;/g, ">").replace(/&amp;/g, "&");
}

// seedFromHTML 依据 index.html 的静态 class 属性初始化元素(真实浏览器语义)。
export function seedFromHTML(document, html) {
  const re = /<([a-zA-Z]+)\b([^>]*\bid="([^"]+)"[^>]*)>/g;
  let m;
  while ((m = re.exec(html)) !== null) {
    const id = m[3];
    const el = document.getElementById(id);
    const cls = /class="([^"]*)"/.exec(m[2]);
    if (cls) el.className = cls[1];
  }
}
