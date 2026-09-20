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
  set innerHTML(v) { this._html = String(v); this.children = []; }
  appendChild(c) { this.children.push(c); return c; }
  setAttribute(k, v) { this.attrs[k] = String(v); }
  getAttribute(k) { return this.attrs[k]; }
  addEventListener() {}
  removeEventListener() {}
  querySelector() { return null; }
  querySelectorAll() { return []; }
  // 递归取出文本(用于断言渲染结果)
  text() { return (this.textContent || "") + this.children.map((c) => c.text?.() ?? "").join(""); }
  html() { return (this._html || "") + this.children.map((c) => c.html?.() ?? "").join(""); }
}

// makeAppStub 生成常用绑定桩, 记录每次调用到 calls。
export function makeAppStub(overrides = {}) {
  const calls = [];
  const rec = (name, ret) => (...args) => { calls.push({ name, args }); return Promise.resolve(typeof ret === "function" ? ret(...args) : ret); };
  const stub = {
    ListControllers: rec("ListControllers", []),
    AutoConnectAll: rec("AutoConnectAll", { ctlIndex: -1, dimms: [] }),
    Connect: rec("Connect", null),
    Scan: rec("Scan", []),
    Select: rec("Select", null),
    Dump: rec("Dump", null),
    Decode: rec("Decode", null),
    WPStatus: rec("WPStatus", null),
    WPSet: rec("WPSet", null),
    WPClear: rec("WPClear", null),
    ...overrides,
  };
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
  vm.createContext(ctx);
  vm.runInContext(fs.readFileSync(APP_JS, "utf8"), ctx, { filename: "app.js" });
  const el = (id) => document.getElementById(id);
  return { ctx, el, document, window };
}

// flush 让挂起的 promise 链跑完。
export const flush = () => new Promise((r) => setTimeout(r, 0));
