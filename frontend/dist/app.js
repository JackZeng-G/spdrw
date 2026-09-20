// SPD Reader Writer (Go) 前端逻辑。Wails 绑定在 window.go.bindings.App。
"use strict";

const $ = (id) => document.getElementById(id);
let currentDump = null;
let selectedAddr = null;
let editorLoaded = false;   // 编辑器已载入时, 左侧 hex 可直接点击修改

// ---------- Wails 绑定桥 ----------
// Wails v2 绑定键 = 绑定结构体的包名.结构体名(官方模板是 main.App;
// 本项目服务层在 app 包 → app.App)。按名解析并兜底遍历, 不依赖具体包名。
let _appObj = undefined;
function appObj() {
  if (_appObj) return _appObj;
  const g = window.go;
  if (!g) return null;
  const candidates = [g.main && g.main.App, g.app && g.app.App];
  for (const c of candidates) {
    if (c && typeof c.ListControllers === "function") return (_appObj = c);
  }
  for (const pkg of Object.keys(g)) {
    for (const structName of Object.keys(g[pkg])) {
      const o = g[pkg][structName];
      if (o && typeof o.ListControllers === "function") return (_appObj = o);
    }
  }
  return null;
}
function call(name, ...args) {
  const a = appObj();
  if (!a || typeof a[name] !== "function") {
    throw new Error("Wails 绑定未就绪(找不到方法 " + name + ")——请确认使用了 desktop,production 标签构建");
  }
  return a[name](...args);
}
function onEvent(name, cb) {
  if (window.runtime && window.runtime.EventsOn) {
    window.runtime.EventsOn(name, cb);
  }
}

// ---------- 环境/启动 ----------
async function checkEnv() {
  // 管理员与 PawnIO 通过后端 ListControllers 探测:
  // - 非管理员 / 未装 PawnIO 时返回带提示文案的错误。
  const el = $("env-status");
  try {
    const list = await call("ListControllers");
    el.textContent = `就绪 · ${list.length} 个控制器`;
    el.className = "ok";
    fillCtlSelect(list);
    setWarn("");
    // 自动遍历控制器, 停在第一个扫到设备的上
    await autoConnectAll();
  } catch (e) {
    const msg = String(e || "");
    if (msg.includes("管理员") || msg.includes("0x80070005")) {
      el.textContent = "需要管理员";
      el.className = "bad";
      setWarn("请以管理员身份运行本程序 —— SMBus 直连需要内核访问权限。");
    } else if (msg.includes("0x80070002") || msg.toLowerCase().includes("找不到") || msg.toLowerCase().includes("not found")) {
      el.textContent = "缺少 PawnIO";
      el.className = "bad";
      setWarn('未检测到 PawnIO 驱动。请到 <a href="https://pawnio.eu/" style="color:#ffd479">pawnio.eu</a> 下载安装后重启程序。');
    } else if (msg.includes("当前平台")) {
      el.textContent = "非 Windows";
      el.className = "warn";
      setWarn("当前平台不支持 SMBus 直连, 仅可离线解析 dump 文件。");
    } else {
      el.textContent = "环境异常";
      el.className = "warn";
      setWarn(`后端初始化失败: ${msg}`);
    }
  }
}

async function autoConnectAll() {
  try {
    const r = await call("AutoConnectAll");
    const idx = (r && r.ctlIndex) || -1;
    const list = (r && r.dimms) || [];
    if (idx >= 0) {
      $("ctl-select").value = String(idx);
    }
    fillDimmSelect(list);
    if (list.length) {
      addLog("", `发现 ${list.length} 个 SPD 设备, 请在列表中选择要读取的 DIMM`);
      setWarn("");
    } else {
      setWarn("已枚举控制器但未扫到 SPD 设备。多端口主板请尝试手动切换控制器后重扫。");
    }
  } catch (e) { addLog("", "自动连接失败: " + e); }
}

function setWarn(html) {
  const bar = $("warn-bar");
  if (!html) { bar.classList.add("hidden"); bar.innerHTML = ""; }
  else { bar.classList.remove("hidden"); bar.innerHTML = html; }
}

function fillCtlSelect(list) {
  const sel = $("ctl-select");
  sel.innerHTML = "";
  list.forEach((c, i) => {
    const opt = document.createElement("option");
    // 值必须是真实控制器下标(c.index): 列表已按"有设备"过滤, 位置 != 下标
    opt.value = Number.isInteger(c.index) ? c.index : i;
    // 名称后附"探测到的设备数": 列表里只剩有设备的控制器, 一眼能看出哪条真的接了条
    const dev = c.devices ? ` · ${c.devices} 个设备` : "";
    opt.textContent = `${c.name}${dev}${c.wpKnown ? (c.noSpdWp ? " · SPD写可" : " · BIOS禁写SPD") : ""}`;
    sel.appendChild(opt);
  });
  if (!list.length) {
    const opt = document.createElement("option");
    opt.value = "-1";
    opt.textContent = "未发现控制器";
    sel.appendChild(opt);
  }
}

// ---------- 日志 ----------
// 注意: Go 侧 LogEntry 的 JSON tag 是小写 time/text
onEvent("log", (entry) => addLog(entry.time, entry.text));

const LOG_MAX = 400;   // 只留最近若干条: 长时间跑总线不该让 DOM 无限长大
function addLog(time, text) {
  const log = $("log");
  const s = String(text ?? "");
  const line = document.createElement("div");
  line.className = "line" +
    (/失败|错误|不通过|无法|拒绝|error/i.test(s) ? " err"
      : /警告|注意|已取消|回滚|warn/i.test(s) ? " warn" : "");
  const t = document.createElement("span");
  t.className = "t";
  t.textContent = time ? `[${time}]` : "";
  const m = document.createElement("span");
  m.className = "m";
  m.textContent = s;
  line.appendChild(t);
  line.appendChild(m);
  log.appendChild(line);
  while (log.childElementCount > LOG_MAX) log.removeChild(log.firstElementChild);
  log.scrollTop = log.scrollHeight;
  const n = $("log-count");
  if (n) n.textContent = `${log.childElementCount} 条`;
}

$("btn-log-toggle").onclick = () => {
  const collapsed = $("log-panel").classList.toggle("collapsed");
  $("btn-log-toggle").textContent = collapsed ? "展开" : "收起";
};
$("btn-log-clear").onclick = () => {
  $("log").innerHTML = "";
  $("log-count").textContent = "";
};

// ---------- 日志面板高度: 可拖拽 ----------
// 高度写进 CSS 变量 --log-h(样式只认变量), 并存 localStorage 供下次启动恢复。
// 键盘也要能调(WCAG): 把手可聚焦, ↑/↓ 步进, Shift 加速, Home 复位。
const LOG_H_KEY = "spdrw.logHeight";
const LOG_H_MIN = 56;
const LOG_H_DEFAULT = 150;

function logMaxH() {
  const h = (typeof window !== "undefined" && window.innerHeight) || 800;
  return Math.max(LOG_H_MIN + 40, Math.round(h * 0.62));
}
function getLogHeight() {
  const panel = $("log-panel");
  // 以实际渲染高度为准(折叠时不参与键盘步进, 展开后从当前值继续)
  const rect = panel.getBoundingClientRect ? panel.getBoundingClientRect() : null;
  return rect && rect.height ? rect.height : LOG_H_DEFAULT;
}
function setLogHeight(px, persist = true) {
  const h = Math.min(logMaxH(), Math.max(LOG_H_MIN, Math.round(px)));
  const root = document.documentElement;
  if (root && root.style && root.style.setProperty) root.style.setProperty("--log-h", h + "px");
  if (persist) { try { localStorage.setItem(LOG_H_KEY, String(h)); } catch (e) { /* 无 localStorage(测试夹具) */ } }
  return h;
}
try {
  const saved = parseInt(localStorage.getItem(LOG_H_KEY) || "", 10);
  if (saved > 0) setLogHeight(saved, false);
} catch (e) { /* 没有 localStorage 就用默认高度 */ }

let logDragFrom = null;
const logResizer = $("log-resizer");
logResizer.addEventListener("pointerdown", (ev) => {
  const panel = $("log-panel");
  logDragFrom = { y: ev.clientY, h: panel.getBoundingClientRect().height };
  panel.classList.add("dragging");
  if (panel.classList.contains("collapsed")) {
    panel.classList.remove("collapsed");
    $("btn-log-toggle").textContent = "收起";
  }
  if (logResizer.setPointerCapture) { try { logResizer.setPointerCapture(ev.pointerId); } catch (e) {} }
  ev.preventDefault();
});
logResizer.addEventListener("pointermove", (ev) => {
  if (!logDragFrom) return;
  // 面板贴在底部: 鼠标往上拖(Δy<0) → 高度增加
  setLogHeight(logDragFrom.h - (ev.clientY - logDragFrom.y));
});
const endLogDrag = (ev) => {
  if (!logDragFrom) return;
  logDragFrom = null;
  $("log-panel").classList.remove("dragging");
  if (ev && logResizer.releasePointerCapture) { try { logResizer.releasePointerCapture(ev.pointerId); } catch (e) {} }
};
logResizer.addEventListener("pointerup", endLogDrag);
logResizer.addEventListener("pointercancel", endLogDrag);
logResizer.addEventListener("dblclick", () => setLogHeight(LOG_H_DEFAULT));
logResizer.addEventListener("keydown", (ev) => {
  const step = ev.shiftKey ? 40 : 12;
  if (ev.key === "ArrowUp") { setLogHeight(getLogHeight() + step); ev.preventDefault(); }
  else if (ev.key === "ArrowDown") { setLogHeight(getLogHeight() - step); ev.preventDefault(); }
  else if (ev.key === "Home") { setLogHeight(LOG_H_DEFAULT); ev.preventDefault(); }
});
function escapeHtml(s) {
  return String(s).replace(/[&<>"]/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;" }[c]));
}

// ---------- 控制器/扫描/设备 ----------

$("ctl-select").onchange = async () => {
  const idx = parseInt($("ctl-select").value, 10);
  if (isNaN(idx) || idx < 0) return;
  try {
    await call("Connect", idx);
    const dimms = await call("Scan");
    fillDimmSelect(dimms);
  } catch (e) { addLog("", "连接/扫描失败: " + e); }
};

function fillDimmSelect(dimms) {
  const sel = $("dimm-select");
  sel.innerHTML = "";
  if (dimms.length) {
    // 默认不选中: 首项为占位, 用户手动选择后才读取
    const ph = document.createElement("option");
    ph.value = "-1";
    ph.textContent = "— 请选择 —";
    sel.appendChild(ph);
  }
  dimms.forEach((d) => {
    const opt = document.createElement("option");
    opt.value = d.addr;
    opt.textContent = `0x${d.addr.toString(16).padStart(2, "0")} · ${d.ramType || "?"} · ${d.size}B`;
    sel.appendChild(opt);
  });
  if (!dimms.length) {
    const opt = document.createElement("option");
    opt.value = "-1";
    opt.textContent = "未发现 SPD";
    sel.appendChild(opt);
  }
}

$("dimm-select").onchange = async () => {
  const addr = parseInt($("dimm-select").value, 10);
  if (isNaN(addr) || addr < 0) return;
  try {
    selectedAddr = addr;
    // 换设备会清空后端的编辑器(避免把 A 条的改动写进 B 条): 前端必须同步复位,
    // 否则界面还留着上一根条的内容与改动, 用户会误以为在操作当前这根。
    if (editorLoaded) addLog("", "已切换设备: 编辑器内容已失效, 需要重新“从设备载入”");
    resetEditorState();
    await call("Select", addr);
    await doDump();
  } catch (e) { addLog("", "选择设备失败: " + e); }
};

// resetEditorState 把前端编辑器视图恢复到"未载入"状态(与后端 editor=nil 对齐)。
function resetEditorState() {
  editorLoaded = false;
  editFieldsCache = [];
  resetEditMarks();
  $("edit-fields").innerHTML =
    `<div class="empty-state">
       <svg width="26" height="26" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.4">
         <path d="M12 20h9M16.5 3.5a2.1 2.1 0 0 1 3 3L7 19l-4 1 1-4Z"/>
       </svg>
       <div class="es-title">编辑器还没有载入数据</div>
       <div class="es-body">读取一次设备会自动载入; 也可以"打开 dump 文件…"离线编辑。</div>
     </div>`;
  $("edit-groups").innerHTML = "";
  $("edit-diff").innerHTML = "—";
  $("edit-state").textContent = "";
  setEditEnabled(false);
  setEditLoadedUI(false);
}

// setEditLoadedUI 区分"可以载入"和"已载入可操作"两档:
// 载入类按钮由 enableOps 控制, 操作类(放弃修改/重算 CRC/另存/与设备比对/写入)只有真载入后才可用。
function setEditLoadedUI(loaded) {
  ["btn-edit-reset", "btn-edit-fixcrc", "btn-edit-export"].forEach(
    (id) => ($(id).disabled = !loaded));
  if (!loaded) $("btn-edit-write").disabled = true;
}

async function doDump() {
  let dump = await call("Dump");
  // Wails v2 将 Go []byte 序列化为 base64 字符串; 保持 base64 形态传递,
  // 渲染/展示时再解码。
  if (typeof dump === "string") {
    currentDump = dump;
  } else {
    // 兜底(测试注入 Uint8Array 等): 转成 base64
    currentDump = btoa(String.fromCharCode(...dump));
  }
  resetEditMarks(); // 设备内容覆盖了左侧视图: 编辑器改动标记不再适用
  renderHexB64(currentDump);
  enableOps(true);
  await decodeCurrent();
  await refreshReadMode().catch(() => {});
  await refreshCRCStatus().catch(() => {});
  // 读一次就把内容放进编辑器(用户反馈: 编辑器再点一次"从设备载入"是多余的)
  await adoptDeviceEditor();
}

// adoptDeviceEditor 把"刚读完的设备内容"接进编辑器(后端在 Dump 时已建好工作副本)。
async function adoptDeviceEditor() {
  try {
    const st = await call("EditState");
    if (!st) { resetEditorState(); return; }
    editorLoaded = true;
    renderEditState(st);
    editFieldsCache = (await call("EditFields")) || [];
    renderEditFields();
    setEditLoadedUI(true);
    await refreshEditBytes();
    await refreshEditDiff();
    // 字段到手后信息面板才能画 XMP 3.0 槽位卡片(它比 Decode 晚一步)
    await rerenderInfo();
  } catch (e) {
    resetEditorState();
  }
}

// 重扫: 一次做完"重新枚举控制器 → 连接 → 扫描 SPD 设备"
// (原先"重扫设备"与 ⟳"重新枚举控制器"两个按钮功能重叠, 已合并为一个)
$("btn-scan").onclick = async () => {
  try {
    const list = (await call("ListControllers")) || [];
    fillCtlSelect(list);
    let idx = parseInt($("ctl-select").value, 10);
    if (isNaN(idx) || idx < 0) {
      if (!list.length) throw new Error("未枚举到控制器");
      idx = 0;
    }
    await call("Connect", idx);
    const dimms = (await call("Scan")) || [];
    selectedAddr = null; // 重扫后回到空白, 等待用户手动选择
    fillDimmSelect(dimms);
    addLog("", `重扫完成: ${list.length} 个控制器 / ${dimms.length} 个 SPD 设备, 请选择要读取的 DIMM`);
  } catch (e) { addLog("", "扫描失败: " + e); }
};

// ---------- 十六进制视图 ----------
function renderHexB64(b64) {
  const bin = atob(b64);
  const dump = new Uint8Array(bin.length);
  for (let i = 0; i < bin.length; i++) dump[i] = bin.charCodeAt(i);
  renderHex(dump);
}

// hex 视图的字节带 data-off, 编辑器载入后可点击**就地编辑**(不再弹原生 prompt):
// 点击把该格换成 2 位输入框, 回车提交 / Esc 取消 / 失焦提交; 输入按十六进制解析。
$("hexgrid").onclick = (ev) => {
  const t = ev.target;
  if (!t || !t.classList || !t.classList.contains("hexbyte")) return;
  if (!editorLoaded) {
    addLog("", '左侧 hex 要能直接改, 需先在"编辑器"里点"从设备载入"或"打开 dump 文件…"');
    return;
  }
  if (t.querySelector && t.querySelector("input")) return; // 已在编辑中
  const off = parseInt(t.getAttribute("data-off"), 10);
  const cur = (t.textContent || "").trim();

  const inp = document.createElement("input");
  inp.className = "hexedit";
  inp.value = cur;
  inp.setAttribute("maxlength", "2");
  inp.setAttribute("size", "2");
  t.textContent = "";
  t.appendChild(inp);
  if (inp.focus) inp.focus();
  if (inp.select) inp.select();

  let done = false;
  const finish = async (commit) => {
    if (done) return;
    done = true;
    const raw = String(inp.value || "").trim();
    inp.remove && inp.remove();
    t.textContent = cur; // 先恢复显示, 提交成功后整体重绘
    if (!commit) return;
    const nv = parseHexByte(raw);
    if (nv == null) { addLog("", `字节值必须是十六进制 00-FF(收到 ${raw})`); return; }
    try {
      const st = await call("EditSetByte", off, nv);
      renderEditState(st);
      await refreshEditBytes();   // 先让左侧视图拿到新字节
      await refreshEditDiff();    // 再按新字节 + 改动分类刷新校验状态
      addLog("", `修改 ${hex(off, 3)} = ${hex(nv, 2)}${st && st.crcStale ? "(该改动影响校验, 记得\"重算 CRC\")" : "(不影响校验, 无需重算)"}`);
    } catch (e) {
      const msg = String(e);
      if (msg.includes("编辑器尚未载入")) {
        // 典型场景: 切换过设备(后端编辑器已清空)或程序刚启动
        addLog("", '修改失败: 编辑器尚未载入数据 —— 请先在"编辑器"里点"从设备载入"或"打开 dump 文件…"');
        resetEditorState();
      } else {
        addLog("", "修改失败: " + msg);
      }
    }
  };
  inp.onkeydown = (e) => {
    if (e.key === "Enter") { e.preventDefault(); finish(true); }
    else if (e.key === "Escape") { e.preventDefault(); finish(false); }
  };
  inp.onblur = () => finish(true);
};
// ---------- 区域模型: 每个字节"属于哪一段" ----------
// 数据来源是后端 CRCStatus 的校验区段/自由区 + 编辑器字段的 offset, 不硬编码 JEDEC 规范:
// 于是 DDR4/DDR5/DDR3/DDR2 自动适配, 规范修订或字段增删也不会让色带失真。
//
// 底色语义(与 hex 标题旁的"?"图例同一份色值):
//   1 z-crc  参与校验的数据区(改这里必须重算 CRC)
//   2 z-free 不参与校验的自由区(序列号/日期等)
//   3 z-id   身份信息(厂商/部件号/序列号/修订)
//   4 z-xmp  XMP / EXPO 配置区
//   5 z-tmg  JEDEC 时序字段
const ZONE_CLASS = ["", "z-crc", "z-free", "z-id", "z-xmp", "z-tmg"];
const ZONE_NAME = ["", "参与校验", "不参与校验", "身份信息", "XMP / EXPO", "JEDEC 时序"];
let hexZoneKinds = new Uint8Array(0);
let hexFieldMap = new Map();   // 字节偏移 → 字段(悬停时显示字段名)

// parseFieldSpans 解析字段 offset 文本。后端给的形态有四种:
//   "0x050"          单字节
//   "0x140-0x141"    起-止
//   "0x149-20B"      起 + 长度(字节)
//   "0x18/0x7B"      多处(斜杠分隔, 如 DDR4 的 medium/fine 两处时序)
// 认不出的片段直接跳过: 色带只是提示, 不能因为一个怪 offset 让整片渲染失败。
function parseFieldSpans(s) {
  const out = [];
  for (const part of String(s || "").split("/")) {
    const m = /^0x([0-9A-Fa-f]+)(?:\s*-\s*(?:0x([0-9A-Fa-f]+)|(\d+)\s*B))?$/.exec(part.trim());
    if (!m) continue;
    const start = parseInt(m[1], 16);
    let end = start + 1;
    if (m[2] != null) end = parseInt(m[2], 16) + 1;
    else if (m[3] != null) end = start + parseInt(m[3], 10);
    if (end > start) out.push({ start, end });
  }
  return out;
}

function zoneKindOfField(f) {
  const g = String(f.group || "");
  if (/XMP|EXPO/i.test(g)) return 4;
  if (g.includes("常用信息")) return 3;
  if (/时序/.test(g)) return 5;
  return 0;
}

function buildZoneModel(size) {
  const kinds = new Uint8Array(size);
  const fields = new Map();
  const paint = (start, end, k) => {
    for (let i = Math.max(0, start); i < Math.min(size, end); i++) kinds[i] = k;
  };
  for (const r of crcState.ranges) paint(r.start, r.end, 1);
  for (const a of crcState.freeAreas) paint(a.start, a.end, 2);
  for (const f of (editFieldsCache || [])) {
    const k = zoneKindOfField(f);
    for (const sp of parseFieldSpans(f.offset)) {
      if (k) paint(sp.start, sp.end, k);
      for (let i = Math.max(0, sp.start); i < Math.min(size, sp.end); i++) fields.set(i, f);
    }
  }
  hexZoneKinds = kinds;
  hexFieldMap = fields;
}

// ---------- 字节提示气泡 ----------
const tipEl = $("tip");
function hideTip() { tipEl.classList.remove("show"); }
function moveTip(ev) {
  const pad = 16;
  const w = tipEl.offsetWidth || 200, h = tipEl.offsetHeight || 44;
  let x = ev.clientX + pad, y = ev.clientY + pad;
  if (x + w > window.innerWidth - 8) x = ev.clientX - w - pad;
  if (y + h > window.innerHeight - 8) y = ev.clientY - h - pad;
  tipEl.style.left = Math.max(8, x) + "px";
  tipEl.style.top = Math.max(8, y) + "px";
}
function showByteTip(ev, span) {
  const off = parseInt(span.getAttribute("data-off"), 10);
  const kind = hexZoneKinds[off] || 0;
  const f = hexFieldMap.get(off);
  const bits = [`0x${off.toString(16).toUpperCase().padStart(3, "0")} = ${span.textContent}`];
  if (kind) bits.push(`<span class="tip-zone">${ZONE_NAME[kind]}</span>`);
  if (crcState.crcBytes.has(off)) bits.push(`<span class="tip-zone">CRC 值本身</span>`);
  const rows = [];
  if (f) rows.push(`<div class="tip-name">${escapeHtml(f.name)}${f.unit ? ` <span class="tip-meta">(${escapeHtml(f.unit)})</span>` : ""}</div>`);
  rows.push(`<div class="tip-meta">${bits.join(" · ")}</div>`);
  if (f && f.note) rows.push(`<div class="tip-meta">${escapeHtml(f.note)}</div>`);
  if (!f && !kind && !crcState.crcBytes.has(off)) rows.push(`<div class="tip-meta">未映射到字段</div>`);
  tipEl.innerHTML = rows.join("");
  tipEl.classList.add("show");
  moveTip(ev);
}
$("hexgrid").addEventListener("mousemove", (ev) => {
  const t = ev.target;
  if (!t || !t.classList || !t.classList.contains("hexbyte")) { hideTip(); return; }
  showByteTip(ev, t);
});
$("hexgrid").addEventListener("mouseleave", hideTip);
$("hexgrid").addEventListener("scroll", hideTip);

function renderHex(dump) {
  const grid = $("hexgrid");
  $("hex-meta").textContent = `${dump.length} 字节`;
  grid.classList.toggle("editable", editorLoaded);
  buildZoneModel(dump.length);
  // 列头(00..0F): 没有它就看不出某一列对应哪个偏移
  let html = `<div class="row head"><span class="offset">off</span>`;
  for (let i = 0; i < 16; i++) html += `<span class="colhead">${i.toString(16).padStart(2, "0")}</span>`;
  html += `</div>`;
  for (let off = 0; off < dump.length; off += 16) {
    let line = `<span class="offset">${off.toString(16).padStart(4, "0")}</span>`;
    let ascii = "";
    for (let i = 0; i < 16; i++) {
      if (off + i >= dump.length) break;
      const b = dump[off + i];
      const hi = (b >> 4).toString(16);
      const zone = hexZoneKinds[off + i] ? " " + ZONE_CLASS[hexZoneKinds[off + i]] : "";
      line += `<span class="hexbyte c${hi}${zone}" data-off="${off + i}">${b.toString(16).padStart(2, "0")}</span>`;
      ascii += b >= 0x20 && b < 0x7f ? escapeHtml(String.fromCharCode(b)) : "·";
    }
    html += `<div class="row">${line}<span class="ascii">${ascii}</span></div>`;
  }
  grid.innerHTML = html;
  applyHexMarks();
}

// ---------- 校验状态(依据左侧正在显示的 dump) ----------
// 后端按"我们传过去的字节"算校验, 所以面板结论与屏幕上看到的永远是同一份数据。
let crcState = { known: null, ok: null, ranges: [], crcBytes: new Set(), freeAreas: [] };
// 编辑器改动的偏移 → 是否影响校验(EditDiff 给的全量分类, 不受列表截断影响)
let hexChangeMap = new Map();
let editDiffCache = null;

function b64ToIntArray(b64) {
  const bin = atob(b64);
  const out = new Array(bin.length);
  for (let i = 0; i < bin.length; i++) out[i] = bin.charCodeAt(i);
  return out;
}

function resetEditMarks() {
  hexChangeMap = new Map();
  editDiffCache = null;
}

// refreshZoneClasses 把最新的区域模型刷到已渲染的格子上。
// 不能只在 renderHex 里做: 渲染发生在 CRC 状态/字段列表到手之前(先有字节, 后知道区段)。
function refreshZoneClasses(grid) {
  const bytes = grid.querySelectorAll("span[data-off]");
  if (!bytes.length) return;
  let size = 0;
  for (const b of bytes) size = Math.max(size, parseInt(b.getAttribute("data-off"), 10) + 1);
  buildZoneModel(size);
  for (const b of bytes) {
    const k = hexZoneKinds[parseInt(b.getAttribute("data-off"), 10)] || 0;
    b.classList.remove("z-crc", "z-free", "z-id", "z-xmp", "z-tmg");
    if (k) b.classList.add(ZONE_CLASS[k]);
  }
}

// applyHexMarks 给已渲染的格子补上"改动/CRC 字节/区段"样式, 并刷新标题旁的校验状态。
// 只切 class, 不重绘 —— 否则会把正在输入的格子里的 input 一起抹掉。
function applyHexMarks() {
  const grid = $("hexgrid");
  refreshZoneClasses(grid);
  for (const n of grid.querySelectorAll("span[data-off]")) {
    const off = parseInt(n.getAttribute("data-off"), 10);
    const chg = hexChangeMap.get(off);
    n.classList.toggle("chg-crc", !!chg && chg.inCRC);
    n.classList.toggle("chg-free", !!chg && !chg.inCRC);
    n.classList.toggle("crcbyte", crcState.crcBytes.has(off));
  }
  renderCRCBadge();
}

function renderCRCBadge() {
  const el = $("crc-status");
  if (!el) return;
  const d = editDiffCache;
  const inRange = d ? d.crcDirty || 0 : 0;
  const free = d ? d.crcFreeDirty || 0 : 0;
  const ok = d ? !!d.crcOk : crcState.ok;
  let text = "—", cls = "crc-badge muted", title = "";
  if (crcState.known === null && !d) {
    text = "—";
  } else if (crcState.known === false && !d) {
    text = "无法校验(世代未知)";
    cls = "crc-badge warn";
  } else if (ok) {
    if (inRange > 0) {
      text = `CRC 通过 · ${inRange} 处改动已计入校验`;
    } else if (free > 0) {
      text = `CRC 通过 · ${free} 处改动不在校验范围`;
    } else {
      text = "CRC 通过";
    }
    cls = "crc-badge ok";
  } else if (inRange > 0) {
    text = `CRC 需重算 · ${inRange} 处在校验范围内`;
    cls = "crc-badge bad";
  } else {
    text = "CRC 不通过";
    cls = "crc-badge bad";
  }
  if (crcState.ranges.length) {
    title = "参与校验的区域:\n" + crcState.ranges.map((r) =>
      `  ${r.name}${r.checksum ? "(校验和)" : ""} → ${fmtOff(r.start)}-${fmtOff(r.end - 1)}` +
      `, 校验值在 ${fmtOff(r.crcOff)}-${fmtOff(r.crcOff + r.crcLen - 1)}`).join("\n");
    if (crcState.freeAreas.length) {
      title += "\n不参与校验(改了不用重算):\n" + crcState.freeAreas.map((a) =>
        `  ${a.name} ${fmtOff(a.start)}-${fmtOff(a.end - 1)}`).join("\n");
    }
  }
  el.textContent = text;
  el.className = cls;
  el.title = title;
}

function fmtOff(off) { return "0x" + Number(off).toString(16).toUpperCase().padStart(3, "0"); }

// refreshCRCStatus 把左侧视图当前的字节交给后端判定校验状态。
async function refreshCRCStatus() {
  if (!currentDump) { applyHexMarks(); return; }
  try {
    const st = await call("CRCStatus", b64ToIntArray(currentDump));
    crcState = {
      known: st ? !!st.known : null,
      ok: st ? !!st.ok : null,
      ranges: (st && st.ranges) || [],
      crcBytes: new Set((st && st.crcBytes) || []),
      freeAreas: (st && st.freeAreas) || [],
    };
  } catch (e) {
    crcState = { known: null, ok: null, ranges: [], crcBytes: new Set(), freeAreas: [] };
    // 不吞错: 绑定缺失/参数不合契约时必须在日志里看得见(以前这类错误静默成"没有状态")
    addLog("", "校验状态查询失败: " + e);
  }
  applyHexMarks();
}

// ---------- 信息面板 ----------
// lastDecode 缓存最近一次解析结果: XMP 3.0 槽位卡片的详细数据来自编辑器字段
// (EditFields), 它比 Decode 晚到 —— 字段到手后要用同一份解析结果再渲染一次。
let lastDecode = null;

async function decodeCurrent() {
  if (!currentDump) return;
  try {
    const r = await call("Decode", currentDump); // base64 string → Go []byte
    lastDecode = r;
    renderInfo(r);
  } catch (e) {
    lastDecode = null;
    $("info-body").innerHTML = `<div class="placeholder">解析失败: ${escapeHtml(String(e))}</div>`;
  }
}

// rerenderInfo 用缓存的解析结果重画信息面板(编辑器字段到手后调用)。
async function rerenderInfo() {
  if (!lastDecode) return;
  renderInfo(lastDecode);
  await refreshReadMode().catch(() => {});
}

// ---------- XMP 3.0 槽位 ----------
// 布局: header(0x280) + 5 个 64B 槽(0x2C0/0x300/0x340/0x380/0x3C0) = 3 份 Profile + 2 份 User;
// AMD EXPO(0x340 起 128B) 与槽 3 / User 1 重叠。
//
// 数据来源是编辑器字段列表(group "XMP 3.0"): 后端已经把每槽的电压/时序解成字段,
// 前端只做"按槽聚合 + 展示", 不重复解析 dump —— 解析规则的唯一真相留在后端。
const XMP3_SLOT_NAMES = ["Profile 1", "Profile 2", "Profile 3", "User 1", "User 2"];
const XMP3_VOLT_FIELDS = [["vdd", "VDD"], ["vddq", "VDDQ"], ["vpp", "VPP"], ["vmemctrl", "VMEMCTRL"]];
// 槽内时序的展示顺序: 主时序在前, 其余按 JEDEC 常见顺序(tCK 单独放在关键行, 它要带频率换算)
const XMP3_TIMINGS = ["tAA", "tRCD", "tRP", "tRAS", "tRC", "tWR",
  "tRFC1", "tRFC2", "tRFC", "tRRD_L", "tCCD_L", "tCCD_L_WR",
  "tCCD_L_WR2", "tCCD_L_WTR", "tCCD_S_WTR", "tRTP", "tFAW"];

function collectXmp3(fields) {
  const res = { header: null, version: "", enabled: {}, names: {}, slots: {} };
  for (const f of (fields || [])) {
    if (!f || f.group !== "XMP 3.0") continue;
    const key = String(f.key || "");
    if (key === "xmp3.present") { res.header = String(f.value) === "true"; continue; }
    if (key === "xmp3.version") { res.version = String(f.value == null ? "" : f.value); continue; }
    let m = /^xmp3\.enabled(\d)$/.exec(key);
    if (m) { res.enabled[+m[1]] = String(f.value) === "true"; continue; }
    m = /^xmp3\.name(\d)$/.exec(key);
    if (m) { res.names[+m[1]] = String(f.value == null ? "" : f.value); continue; }
    m = /^xmp3\.p(\d)\.(.+)$/.exec(key);
    if (m) {
      const idx = +m[1];
      if (!res.slots[idx]) res.slots[idx] = {};
      res.slots[idx][m[2]] = f;
    }
  }
  return res;
}

function xmp3HasData(slot) {
  for (const [k, f] of Object.entries(slot || {})) {
    const v = String(f && f.value != null ? f.value : "").trim();
    if (v !== "" && v !== "0") return true;
    if (k === "cl" && v !== "") return true;
  }
  return false;
}

function xmp3Val(slot, key) {
  const f = slot && slot[key];
  return f ? String(f.value == null ? "" : f.value).trim() : "";
}

// xmp3TimingText 输出一条时序值的文本: tCK 额外换算成频率与速率(0.333 ns → 3000 MHz → 6000 MT/s)
function xmp3TimingText(key, slot) {
  const v = xmp3Val(slot, key);
  if (!v) return "";
  if (key === "tCK") {
    const ns = parseFloat(v);
    if (ns > 0) {
      const mhz = 1000 / ns;
      return `${ns.toFixed(3)} ns <span class="muted">· ${Math.round(mhz)} MHz · ${Math.round(mhz * 2)} MT/s</span>`;
    }
  }
  const unit = slot[key] && slot[key].unit ? " " + escapeHtml(slot[key].unit) : "";
  return escapeHtml(v) + unit;
}
function xmp3TimingCell(key, slot) {
  const t = xmp3TimingText(key, slot);
  return t ? `<td>${t}</td>` : `<td class="muted">—</td>`;
}

function renderXmp3SlotGrid(r, x3) {
  const hasExpo = !!r.hasExpo;
  let out = "";
  for (let idx = 1; idx <= 5; idx++) {
    const slot = x3.slots[idx] || null;
    const name = x3.names[idx] || "";
    const enabled = !!x3.enabled[idx];
    const expoBusy = hasExpo && (idx === 3 || idx === 4);
    const hasData = !!slot && xmp3HasData(slot);

    let cls = "slot", badge = "", body = "";
    if (expoBusy) {
      cls += " expo";
      badge = `<span class="badge muted">EXPO 占用</span>`;
      body = `<div class="slot-empty">该槽与 AMD EXPO 区块重叠(0x340 / 0x380), 内容由 EXPO 使用</div>`;
    } else if (!hasData) {
      cls += " empty";
      badge = `<span class="badge muted">空槽</span>`;
      body = `<div class="slot-empty">该槽未写入 profile</div>`;
    } else {
      if (enabled) cls += " on";
      badge = enabled ? `<span class="badge ok">已启用</span>` : `<span class="badge muted">未启用</span>`;
      // 关键行: tCK(带频率/速率) + CL 支持
      const tck = xmp3Val(slot, "tCK");
      const cl = xmp3Val(slot, "cl");
      const cr = xmp3Val(slot, "commandRate");
      let key = `<div class="slot-kv">`;
      if (tck) key += `<span><b>tCK</b> ${xmp3TimingText("tCK", slot)}</span>`;
      key += `<span><b>CL</b> ${cl ? escapeHtml(cl) : "—"}</span>`;
      if (cr && cr !== "0") key += `<span><b>CR</b> ${escapeHtml(cr)}N</span>`;
      key += `</div>`;

      // 主时序 + 其余时序两列排布
      const rest = XMP3_TIMINGS.filter((k) => xmp3Val(slot, k) !== "");
      let tbl = "";
      if (rest.length) {
        tbl = `<table class="timing two-col">`;
        for (let i = 0; i < rest.length; i += 2) {
          const cell = (k) => `<th>${k}</th>${xmp3TimingCell(k, slot)}`;
          tbl += `<tr>${cell(rest[i])}` + (rest[i + 1] ? cell(rest[i + 1]) : `<th></th><td></td>`) + `</tr>`;
        }
        tbl += `</table>`;
      }

      const volts = XMP3_VOLT_FIELDS
        .map(([k, label]) => {
          const v = xmp3Val(slot, k);
          return v ? `<span><b>${label}</b> ${escapeHtml(v)} V</span>` : "";
        })
        .filter(Boolean).join("");

      body = key + tbl + (volts ? `<div class="slot-volt">${volts}</div>` : "");
    }

    out += `<article class="${cls}">
      <header class="slot-head">
        <span class="slot-title">${XMP3_SLOT_NAMES[idx - 1]}</span>
        ${name ? `<span class="slot-name">${escapeHtml(name)}</span>` : ""}
        ${badge}
      </header>
      <div class="slot-body">${body}</div>
    </article>`;
  }
  return `<div class="slot-grid">${out}</div>`;
}

function renderInfo(r) {
  const kv = (k, v, cls) => `<div class="k">${k}</div><div class="v ${cls || ""}">${v ?? "—"}</div>`;
  const card = (title, body, tag) =>
    `<section class="card"><div class="card-head"><span>${title}</span>` +
    (tag ? `<span class="tag">${tag}</span>` : "") + `</div><div class="card-body">${body}</div></section>`;

  // 概要卡片: 一眼要看到的身份与容量信息
  let head = `<div class="kv">`;
  head += kv("类型", escapeHtml(r.ramType) + (r.moduleType ? ` · ${escapeHtml(r.moduleType)}` : ""));
  head += kv("容量", `<b>${escapeHtml(r.totalHuman || `${r.totalMib} MiB`)}</b>`);
  if (r.ranks) head += kv("组织", `${r.ranks} Rank × ${r.deviceWidth}bit · 总线 ${r.busWidth}bit`);
  head += kv("厂商", escapeHtml(r.manufacturer || "—") +
    (r.manufacturerNote ? ` <span class="muted small">${escapeHtml(r.manufacturerNote)}</span>` : ""));
  head += kv("部件号", `<span class="mono">${escapeHtml(r.partNumber || "—")}</span>`);
  if (r.dateYear) head += kv("生产日期", `${r.dateYear} 年第 ${r.dateWeek} 周`);
  if (r.serialHex) head += kv("序列号", `<span class="mono">0x${escapeHtml(r.serialHex)}</span>`);
  head += kv("校验", `<span class="badge ${r.crcOk ? "ok" : "bad"}">${r.crcOk ? "CRC 通过" : "CRC 不通过"}</span>`);
  head += `</div>`;
  // 读取方式整行放(时钟/事务数/耗时/回退原因): 挤在"校验"那一行会折成两行, 很难看
  head += `<div class="read-line"><span id="read-mode" class="muted small"></span></div>`;

  let html = card("概要", head);

  if (r.hasTimings && r.tck) {
    // tCK 的"周期数"只有 DDR5 给得出。DDR4 只报 ns/频率, 以前会渲染成 "— (0.750 ns)",
    // 看着像缺数据 —— 这里改成只显示真实有的那部分。
    const ns = r.tck.ns || 0;
    const tckTxt = ns
      ? `${ns.toFixed(3)} ns · ${(1000 / ns).toFixed(0)} MHz` + (r.tck.cycles ? ` · ${r.tck.cycles} clk` : "")
      : "—";
    let t = `<table class="timing two-col"><tr><th>tCK</th><td>${tckTxt}</td></tr>`;
    if (r.casLatencies) t += `<tr><th>CL</th><td>${escapeHtml(r.casLatencies)}</td></tr>`;
    const rows = [["tAA", r.taa], ["tRCD", r.trcd], ["tRP", r.trp], ["tRAS", r.tras], ["tRC", r.trc], ["tRFC1", r.trfc1], ["tRFC2", r.trfc2], ["tRFC4", r.trfc4], ["tFAW", r.tfaw], ["tRRD_S", r.trrdS], ["tRRD_L", r.trrdL], ["tCCD_L", r.tccdL], ["tWR", r.twr]]
      .filter(([, t2]) => t2 && t2.ns);
    for (let i = 0; i < rows.length; i += 2) {
      const cell = ([name, t2]) =>
        `<th>${name}</th><td>${t2.cycles ? t2.cycles + " clk · " : ""}${t2.ns.toFixed(3)} ns</td>`;
      t += `<tr>${cell(rows[i])}` + (rows[i + 1] ? cell(rows[i + 1]) : "<th></th><td></td>") + `</tr>`;
    }
    t += `</table>`;
    html += card("时序", t);
  }

  if (r.ddr5Timings && r.ddr5Timings.length) {
    // 两列显示: 一行放两组"名称/值", 省一半纵向空间
    let t = `<table class="timing two-col">`;
    const cellsOf = (x) => {
      const ns = (x.ns != null) ? `${x.ns.toFixed(3)} ns` : "—";
      const cyc = x.cycles ? ` · ${x.cycles} clk` : "";
      const low = x.lower ? ` <span class="muted">(下限 ${x.lower})</span>` : "";
      return `<th>${escapeHtml(x.name)}</th><td>${ns}${cyc}${low}</td>`;
    };
    for (let i = 0; i < r.ddr5Timings.length; i += 2) {
      t += `<tr>${cellsOf(r.ddr5Timings[i])}` +
        (r.ddr5Timings[i + 1] ? cellsOf(r.ddr5Timings[i + 1]) : `<th></th><td></td>`) + `</tr>`;
    }
    t += `</table>`;
    if (r.casLatencies) t += `<div class="kv" style="margin-top:6px"><div class="k">CL 支持</div><div class="v mono">${escapeHtml(r.casLatencies)}</div></div>`;
    html += card("JEDEC 时序(DDR5)", t, `共 ${r.ddr5Timings.length} 项`);
  }

  // XMP: DDR5 走"XMP 3.0 槽位卡片"(数据来自编辑器字段, 见 collectXmp3);
  // 字段还没到手(例如只解码了文件、编辑器未载入)时, 退回原来的一行式简表。
  const x3 = collectXmp3(editFieldsCache);
  if (r.hasXmp && Object.keys(x3.slots).length) {
    const tag = `版本 0x${escapeHtml(x3.version || "??")}` +
      (x3.header === false ? " · 头缺失" : "") +
      (r.hasExpo ? " · EXPO 占用槽 3 / User 1" : "");
    html += card("Intel XMP 3.0", renderXmp3SlotGrid(r, x3), tag);
  } else if (r.hasXmp && r.xmp && r.xmp.length) {
    let t = `<table class="timing">`;
    r.xmp.forEach((p) => {
      t += `<tr><th>Profile ${p.number}${p.enabled ? " · 启用" : ""}</th>` +
        `<td>v${(p.version >> 4) & 0xF}.${p.version & 0xF} · ${escapeHtml(p.summary || "未设置")}` +
        `${p.casLatencies ? " · CL " + escapeHtml(p.casLatencies) : ""}</td></tr>`;
    });
    t += `</table>`;
    html += card("Intel XMP", t);
  }
  if (r.hasExpo) html += card("AMD EXPO", `<div class="muted small">存在 EXPO 配置区</div>`);
  if (r.basic) {
    html += card("基本信息", `<div class="kv">${kv("tCK", `${r.basic.tckminNs?.toFixed(2)} ns`)}</div>`);
  }
  $("info-body").innerHTML = html;
}

// flashField 在左侧 hex 里高亮某个字段覆盖的字节(焦点落到编辑器输入框时触发):
// 字段与字节的对应关系本来只存在于后端, 这里用同一套 offset 解析映射回屏幕。
function flashField(key) {
  const f = (editFieldsCache || []).find((x) => x.key === key);
  if (!f) return;
  const grid = $("hexgrid");
  let first = null;
  for (const sp of parseFieldSpans(f.offset)) {
    for (let i = sp.start; i < sp.end; i++) {
      const el = grid.querySelector(`.hexbyte[data-off="${i}"]`);
      if (!el) continue;
      el.classList.add("flash");
      if (!first) first = el;
      setTimeout(() => el.classList.remove("flash"), 2400);
    }
  }
  if (first && first.scrollIntoView) first.scrollIntoView({ block: "center" });
}

// ---------- 文件操作 ----------
function enableOps(on) {
  // 顶栏的"校验文件"需要设备(它与设备内容比对); "打开 dump 文件…"纯文件操作, 一直可用
  ["btn-verify", "btn-wp-status", "btn-wp-set", "btn-wp-clear", "btn-write-probe", "btn-edit-reload"]
    .forEach((id) => ($(id).disabled = !on));
}

// 校验文件: 选一个 dump 文件与**设备当前内容**逐字节比对(需要已连接设备)。
$("btn-verify").onclick = async () => {
  try {
    const path = await call("VerifyFileDialog");
    addLog("", "校验通过(文件与设备内容一致): " + path);
  } catch (e) {
    if (String(e).includes("已取消")) { addLog("", "已取消"); return; }
    addLog("", "校验失败: " + e);
  }
};

// 读加速(块读 → 字读 → 逐字节)与等待模式(忙等 → 折中)都不再需要手动开关:
// 程序自己按"探测可用档位 + 失败自动降级"选路, 并把实际档位/降级原因写进日志。

let busTuningCache = null;

async function refreshReadMode() {
  try {
    const target = $("read-mode");
    if (!target) return; // 信息面板还没渲染(元素是动态生成的)
    const st = await call("ReadStats");
    if (!st) return;
    let tune = "";
    try {
      const t = await call("BusTuning");
      busTuningCache = t;
      if (t) {
        const clock = t.clockHz ? `${(t.clockHz / 1000).toFixed(1)}kHz` : "";
        tune = [clock, t.sleepModeName].filter(Boolean).join(" ");
      }
    } catch (e) { /* 未连接 */ }
    const mode = st.mode || (st.blockReadKnown ? (st.blockReadOK ? "块读加速" : "逐字节(块读不可用)") : "尚未读取");
    // 紧凑一行, 附在"CRC"那一行后面(避免底部再单独占一行)
    const bytes = st.wordBytes ? `字读 ${st.wordBytes}B` :
      (st.blockBytes ? `块读 ${st.blockBytes}B` : `逐字节 ${st.fallbackBytes}B`);
    target.innerHTML = ` · ${escapeHtml(mode)} · ${st.transactions} 次事务` +
      (st.elapsedMs ? ` · ${(st.elapsedMs / 1000).toFixed(2)}s` : "") +
      ` · ${bytes}` +
      (tune ? ` · ${escapeHtml(tune)}` : "") +
      (st.note ? ` <span class="warn">(${escapeHtml(st.note)})</span>` : "");
  } catch (e) { /* 未选设备 */ }
}

// ---------- 写保护 ----------
// WPStatus 返回单个结构体(Wails v2 绑定方法只支持 ≤2 个返回值, 旧版 4 返回值
// 会被序列化成 null, 前端解构直接抛错 —— 这是"查询保护状态"坏掉的根因)。
$("btn-wp-status").onclick = async () => {
  try {
    await refreshWP();
  } catch (e) { addLog("", "查询保护状态失败: " + e); }
};

async function refreshWP() {
  const st = await call("WPStatus");
  if (!st) throw new Error("后端返回空结果");
  renderWP(st);
  return st;
}

function hex(n, w) { return "0x" + Number(n).toString(16).toUpperCase().padStart(w, "0"); }

function renderWP(st) {
  const el = $("wp-blocks");
  el.innerHTML = "";
  el.classList.remove("muted");
  const bs = st.blockSize || 128;
  for (let i = 0; i < st.blocks; i++) {
    const known = st.known ? st.known[i] : true;
    const prot = st.protected ? st.protected[i] : false;
    const d = document.createElement("span");
    d.className = "blk " + (!known ? "unknown" : prot ? "on" : "off");
    const range = `${hex(i * bs, 3)}-${hex((i + 1) * bs - 1, 3)}`;
    d.textContent = `B${i} ${range} ${!known ? "❔" : prot ? "🔒" : "🔓"}`;
    d.title = known ? (prot ? "受写保护" : "可写") : "状态未知(写测试失败)";
    el.appendChild(d);
  }

  let html = [];
  html.push(`世代 ${st.generation || (st.ddr5 ? "DDR5" : "?")} · ${st.blocks} 块 × ${bs}B(每块 ${st.ddr5 ? "MR12/MR13 位图" : "块首写测试"})`);
  if (st.ddr5) {
    html.push(`寄存器 <code>MR11=${hex(st.mr11, 2)} MR12=${hex(st.mr12, 2)} MR13=${hex(st.mr13, 2)} MR29=${hex(st.mr29, 2)} MR48=${hex(st.mr48, 2)} MR52=${hex(st.mr52, 2)}</code>`);
  }
  if (st.offline) html.push(`<span class="warn">DDR5 离线模式已开启(可在离线态清 RSWP)</span>`);
  if (st.protectionHit) html.push(`<span class="danger">MR52[6]=1: 检测到对受保护块的写被忽略</span>`);
  if (st.pswpApplicable) {
    html.push(st.pswp
      ? `<span class="danger">⚠ PSWP 永久保护已生效(不可通过 SMBus 解除, 需高压编程器)</span>`
      : `PSWP 未设置`);
  } else {
    html.push(`PSWP 不适用(${st.ddr5 ? "DDR5" : "DDR4/EE1004"} 未定义 0110b/0x30 设备类型)`);
  }
  for (const w of (st.warnings || [])) html.push(`<span class="warn">提示: ${escapeHtml(w)}</span>`);
  $("wp-summary").innerHTML = html.join("<br>");

  const prot = (st.protected || []).map((p, i) => p ? `B${i}` : null).filter(Boolean);
  const unknown = (st.known || []).map((k, i) => k ? null : `B${i}`).filter(Boolean);
  const state = prot.length ? "受保护 " + prot.join(",") : (unknown.length ? `未知 ${unknown.join(",")}` : "全部开放");
  addLog("", `保护状态: ${state}` +
    (st.pswpApplicable ? (st.pswp ? " · PSWP 永久保护" : " · PSWP 未设置") : " · PSWP 不适用"));
}

// RSWP 加保护/清除都需要确认串(后端也校验): 这两个动作会真写设备, 而且在部分颗粒上
// "加保护"是不可逆的;清除命令还可能在 VHV 缺失时被器件 ACK 而忽略, 所以后端会回读复核。
function wpAck(s) { return String(s || "").trim().toUpperCase(); }

$("btn-wp-set").onclick = async () => {
  const inp = prompt("输入要保护的块号(0-15, 逗号分隔):", "0");
  if (!inp) return;
  const blocks = inp.split(",").map((s) => parseInt(s.trim(), 10)).filter((n) => !isNaN(n));
  if (!blocks.length) { addLog("", "未输入有效块号"); return; }
  if (wpAck($("inp-wp-ack").value) !== "CLEAR") {
    addLog("", '写保护操作需要确认串: 请在"写保护"面板的确认串框里输入 CLEAR');
    return;
  }
  if (!confirm("确定对这些块启用 RSWP 写保护?\n" + blocks.join(",") +
    "\n\n注意: 部分颗粒的 RSWP 不可逆!")) return;
  try {
    await call("WPSet", blocks, wpAck($("inp-wp-ack").value));
    addLog("", "RSWP 已设置并回读确认: " + blocks.join(","));
    $("inp-wp-ack").value = "";
    await refreshWP().catch(() => {});
  } catch (e) {
    addLog("", "RSWP 设置失败: " + e);
    await refreshWP().catch(() => {});
  }
};

// 检测写入能力: 单字节写反值再还原(后端自动备份 + 整片复核)。
$("btn-write-probe").onclick = async () => {
  if (!confirm("在空闲字节上做一次写入探测(写反值后立即还原, 前后都会整片校验)?\n\n" +
    "这不是写入你的修改, 只是确认这条 SPD 能否被写入。")) return;
  try {
    addLog("", "正在探测写入能力(备份 → 单字节写 → 回读 → 还原 → 整片复核)…");
    const r = await call("WriteProbe");
    const cls = r.verdict === "ok" ? "ok" : "bad";
    $("probe-result").innerHTML = `<b class="${cls}">写入能力: ` +
      `${r.verdict === "ok" ? "可写" : r.verdict === "ignored" ? "被忽略(写不进去)" : "被拒绝"}</b>` +
      ` · ${escapeHtml(r.offsetText)} ${hex(r.old, 2)}→${hex(r.new, 2)} 回读 ${hex(r.readBack, 2)}` +
      (r.mode ? ` · 档位 ${escapeHtml(r.mode)}` : "") +
      ` · 已还原 ${r.restored ? "是" : "否"} · 整片复核 ${r.verified ? "通过" : "不通过"}<br>` +
      `<span class="muted">${escapeHtml(r.note)}</span>`;
    addLog("", "写入能力探测: " + r.note);
    if (r.backupPath) addLog("", "备份: " + r.backupPath);
    await refreshWP().catch(() => {});
  } catch (e) { addLog("", "写入能力探测失败: " + e); }
};

$("btn-wp-clear").onclick = async () => {
  if (wpAck($("inp-wp-ack").value) !== "CLEAR") {
    addLog("", 'RSWP 清除需要确认串: 请在"写保护"面板的确认串框里输入 CLEAR');
    return;
  }
  if (!confirm("确定清除全部可逆写保护 (RSWP)?")) return;
  try {
    await call("WPClear", wpAck($("inp-wp-ack").value));
    addLog("", "RSWP 已清除并回读确认");
    $("inp-wp-ack").value = "";
    await refreshWP().catch(() => {});
  } catch (e) {
    addLog("", "RSWP 清除失败: " + e);
    await refreshWP().catch(() => {});
  }
};

// ---------- 事件 ----------
onEvent("dump:done", (n) => addLog("", `读取完成 ${n} 字节`));
onEvent("write:progress", (n) => { /* 进度可在此更新 */ });

// ---------- 启动 ----------
// Wails v2 的绑定在 DOM ready 后由后端异步注入, 页面脚本先于其执行,
// 因此轮询等待 window.go.main.App 就绪再初始化。
function whenBindingsReady(cb, timeoutMs = 15000) {
  const start = Date.now();
  (function poll() {
    if (appObj()) return cb();
    if (Date.now() - start > timeoutMs) {
      setWarn("Wails 绑定未就绪——请用 build.ps1(或 -tags desktop,production)构建本程序。");
      return;
    }
    setTimeout(poll, 50);
  })();
}
whenBindingsReady(() => {
  // 离线编辑 dump 不依赖设备, 绑定就绪即可用
  $("btn-edit-load-file").disabled = false;
  checkEnv();
});

// ---------- SPD 编辑器 ----------
let editFieldsCache = [];

// 悬浮问号: 点一下展开/收起说明(不占常驻空间)
document.addEventListener("click", (ev) => {
  const t = ev.target;
  if (!t || !t.getAttribute) return;
  const id = t.getAttribute("data-hint");
  if (!id) return;
  const box = $(id);
  if (box) box.classList.toggle("hidden");
});

$("chk-edit-all").onchange = () => renderEditFields();

$("tab-info").onclick = () => switchTab("info");
$("tab-edit").onclick = () => switchTab("edit");
function switchTab(which) {
  const info = which === "info";
  $("tab-info").classList.toggle("active", info);
  $("tab-edit").classList.toggle("active", !info);
  $("view-info").classList.toggle("hidden", !info);
  $("view-edit").classList.toggle("hidden", info);
}

function setEditEnabled(on) {
  // 操作类按钮(放弃修改/重算 CRC/另存/与设备比对)由 setEditLoadedUI 按"真载入了没有"控制;
  // btn-edit-write 另由 refreshEditDiff 依据 CRC/变更数决定。
  if (!on) setEditLoadedUI(false);
}

// 重新读取设备: 读取后会自动把内容接进编辑器(见 doDump/adoptDeviceEditor)
$("btn-edit-reload").onclick = async () => {
  try { await doDump(); addLog("", "已重新读取设备并刷新编辑器"); }
  catch (e) { addLog("", "重新读取失败: " + e); }
};
$("btn-edit-load-file").onclick = async () => {
  try { await loadEditor("EditLoadFileDialog"); }
  catch (e) {
    if (String(e).includes("已取消")) return;
    addLog("", "载入文件失败: " + e);
  }
};

async function loadEditor(method) {
  const st = await call(method);
  renderEditState(st);
  editFieldsCache = (await call("EditFields")) || [];
  renderEditFields();
  await refreshEditDiff();
  setEditEnabled(true);
  editorLoaded = true;
  setEditLoadedUI(true);
  await refreshEditBytes();   // 让左侧 hex 进入"可直接点击修改"状态
  await syncInfoFromEditor(); // 用户要求: 用编辑器打开 dump 文件时, SPD 信息也要同步显示
  addLog("", `编辑器已载入: ${st.source}(${st.generation} ${st.size}B)`);
}

// syncInfoFromEditor 用编辑器当前内容刷新"SPD 信息"面板(打开文件后信息面板不再空白)。
async function syncInfoFromEditor() {
  try {
    const b64 = await call("EditBytes");
    if (typeof b64 !== "string") return;
    currentDump = b64;
    const r = await call("Decode", b64);
    lastDecode = r;
    renderInfo(r);
  } catch (e) {
    addLog("", "同步 SPD 信息失败: " + e);
  }
}

function renderEditState(st) {
  if (!st) { $("edit-state").textContent = ""; return; }
  const parts = [`${st.source} · ${st.generation} ${st.size}B`];
  parts.push(st.crcOk ? "CRC 通过" : "CRC 不通过");
  if (st.dirty) parts.push(`${st.changeCount} 处改动`);
  $("edit-state").textContent = parts.join(" | ");
  $("edit-state").className = "muted small " + (st.crcOk ? "" : "danger");
}

let editGroup = null;   // 当前分组(基本信息/JEDEC 时序/XMP 2.0/XMP 3.0/EXPO)

function renderEditFields() {
  const box = $("edit-fields");
  const tabs = $("edit-groups");
  if (!editFieldsCache.length) {
    box.innerHTML = `<div class="placeholder">无可编辑字段</div>`;
    tabs.innerHTML = "";
    $("edit-field-count").textContent = "";
    return;
  }
  const groups = [];
  for (const f of editFieldsCache) {
    let g = groups.find((x) => x.name === f.group);
    if (!g) { g = { name: f.group, items: [] }; groups.push(g); }
    g.items.push(f);
  }
  if (!editGroup || !groups.some((g) => g.name === editGroup)) {
    editGroup = groups[0].name;
  }
  tabs.innerHTML = "";
  for (const g of groups) {
    const b = document.createElement("button");
    const primary = g.items.filter((f) => f.primary).length;
    b.textContent = `${g.name}(${primary}/${g.items.length})`;
    b.className = g.name === editGroup ? "active" : "";
    b.onclick = () => { editGroup = g.name; renderEditFields(); };
    tabs.appendChild(b);
  }
  const cur = groups.find((g) => g.name === editGroup);
  const showAll = $("chk-edit-all").checked;
  const shown = showAll ? cur.items : cur.items.filter((f) => f.primary);
  $("edit-field-count").textContent = showAll
    ? `共 ${cur.items.length} 个字段`
    : `常用 ${shown.length} / ${cur.items.length} 个字段`;
  let html = `<div class="field-grid">`;
  for (const f of shown) {
    const risk = f.risk === "high" ? "risk-high" : f.risk === "medium" ? "risk-medium" : "";
    const title = [f.offset, f.unit, f.note].filter(Boolean).join(" · ");
    const list = f.key === "manufacturer" || f.key === "dramManufacturer" ? ` list="mfg-list"` : "";
    html += `<div class="fitem" title="${escapeHtml(title)}">` +
      `<label class="${risk}" for="fld-${escapeHtml(f.key)}">${escapeHtml(f.name)}</label>` +
      `<div class="frow">` +
      `<input id="fld-${escapeHtml(f.key)}" type="text" data-key="${escapeHtml(f.key)}" value="${escapeHtml(f.value)}"${list}>` +
      `<button data-apply="${escapeHtml(f.key)}">应用</button>` +
      `</div></div>`;
  }
  if (!shown.length) {
    html += `<div class="placeholder">该分组没有常用字段, 勾选"显示全部字段"查看</div>`;
  }
  html += `</div>`;
  box.innerHTML = html;
  box.querySelectorAll("button[data-apply]").forEach((b) => {
    b.onclick = () => applyEditField(b.getAttribute("data-apply"), box);
  });
  box.querySelectorAll("input[data-key]").forEach((inp) => {
    inp.onkeydown = (ev) => { if (ev.key === "Enter") applyEditField(inp.getAttribute("data-key"), box); };
    // 焦点落到字段上 → 左侧 hex 里闪一下这个字段占的字节(省得用户自己对偏移)
    inp.onfocus = () => flashField(inp.getAttribute("data-key"));
    if (inp.getAttribute("data-key") === "manufacturer" || inp.getAttribute("data-key") === "dramManufacturer") {
      inp.oninput = debounce(async () => {
        try {
          const list = await call("MfgSearch", inp.value, 30);
          $("mfg-list").innerHTML = list.map((m) => `<option value="${escapeHtml(m.name)}"></option>`).join("");
        } catch (e) { /* 忽略搜索错误 */ }
      }, 300);
    }
  });
}

function debounce(fn, ms) {
  let t = null;
  return (...a) => { clearTimeout(t); t = setTimeout(() => fn(...a), ms); };
}

async function applyEditField(key, box) {
  const inp = box.querySelector(`input[data-key="${key}"]`);
  if (!inp) return;
  const val = inp.type === "checkbox" ? String(inp.checked) : inp.value;
  try {
    const st = await call("EditSetField", key, val);
    renderEditState(st);
    editFieldsCache = (await call("EditFields")) || [];
    renderEditFields();
    await refreshEditBytes();
    await refreshEditDiff();
    addLog("", `编辑 ${key} = ${val}`);
  } catch (e) {
    addLog("", `编辑失败(${key}): ${e}`);
  }
}

$("btn-edit-reset").onclick = async () => {
  if (!confirm("放弃全部修改?")) return;
  try {
    const st = await call("EditReset");
    renderEditState(st);
    editFieldsCache = (await call("EditFields")) || [];
    renderEditFields();
    await refreshEditBytes();
    await refreshEditDiff();
  } catch (e) { addLog("", "重置失败: " + e); }
};

$("btn-edit-fixcrc").onclick = async () => {
  try {
    const st = await call("EditFixCRC");
    renderEditState(st);
    editFieldsCache = (await call("EditFields")) || [];
    renderEditFields();
    await refreshEditBytes();
    await refreshEditDiff();
    addLog("", "已重算 CRC(校验值字节已更新)");
  } catch (e) { addLog("", "重算 CRC 失败: " + e); }
};

$("btn-edit-export").onclick = async () => {
  try {
    const path = await call("EditExportDialog");
    addLog("", "已导出: " + path);
  } catch (e) {
    if (String(e).includes("已取消")) return;
    addLog("", "导出失败: " + e);
  }
};

// parseHexByte 解析一个字节: **一律按十六进制**(允许 0x 前缀, 1~2 位)。
// 早先的实现剥掉 0x 后用 parseInt(s,10), 于是 "5A"/"0b" 被当十进制(5A→5, 0b→0),
// 是明确的错误 —— 这是 hex 编辑器, 输入的就是十六进制。
function parseHexByte(s) {
  s = String(s || "").trim().replace(/^0x/i, "");
  if (!/^[0-9a-fA-F]{1,2}$/.test(s)) return null;
  return parseInt(s, 16);
}

async function refreshEditBytes() {
  try {
    const b64 = await call("EditBytes");
    if (typeof b64 === "string") { currentDump = b64; renderHexB64(b64); }
    await refreshCRCStatus();
  } catch (e) { /* 编辑器未载入 */ }
}

async function refreshEditDiff() {
  const d = await call("EditDiff");
  editDiffCache = d;
  hexChangeMap = new Map();
  for (const off of d.dirtyInCrc || []) hexChangeMap.set(off, { inCRC: true });
  for (const off of d.dirtyFree || []) hexChangeMap.set(off, { inCRC: false });
  applyHexMarks();
  const lines = [];
  lines.push(`变更 <b>${d.changeCount}</b> 字节 · CRC 字段 ${d.crcFields} · 高危 ${d.highRisk}`);
  const inRange = d.crcDirty || 0, free = d.crcFreeDirty || 0;
  lines.push(`其中影响校验 <b>${inRange}</b> 处(需重算 CRC) · 不影响校验 <b>${free}</b> 处(序列号/日期等)`);
  lines.push(d.crcOk ? "CRC 校验通过" : `<span class="danger">CRC 不通过(记得"重算 CRC")</span>`);
  for (const f of (d.fields || [])) {
    const cls = f.risk === "high" ? "danger" : f.risk === "medium" ? "warn" : "";
    lines.push(`<span class="${cls}">${escapeHtml(f.region)} × ${f.count}(${escapeHtml(f.ranges)})</span>`);
  }
  if (d.truncated) lines.push("(变更过多, 列表已截断)");
  $("edit-diff").innerHTML = lines.join("<br>");
  $("btn-edit-write").disabled = !d.crcOk || d.changeCount === 0;
  $("inp-edit-ack").placeholder = "WRITE";
}



// resyncAfterFailure 写入失败后重新读取设备并刷新视图与校验状态。
// (回滚成功与否只有重读才知道; 左侧必须显示设备的真实内容。)
async function resyncAfterFailure() {
  addLog("", "正在重新读取设备内容(确认回滚结果)…");
  try {
    await doDump();
    addLog("", "已重新读取设备: 请以左侧内容与校验状态为准");
  } catch (err) {
    addLog("", "重新读取失败: " + err);
  }
}

// 写入设备: 界面不再需要勾"我已另有备份"(每次都会自动备份), 也不再提供干跑;
// 确认串仍是唯一且必要的闸门(后端同样校验)。
// 写入成功后自动做一次独立复核: 重新整片读取设备并与编辑器内容逐字节比较。
// (写入内部已有三层校验; 这一步是"再读一遍"的独立确认, 读速修好后只要约 1 秒。)
async function autoVerifyAfterWrite() {
  try {
    addLog("", "自动复核: 正在重新读取设备并与写入内容逐字节比对…");
    const d = await call("EditVerifyFile");
    if (!d || !d.changeCount) {
      addLog("", "自动复核通过: 设备内容与写入内容逐字节一致");
      $("edit-diff").innerHTML = '<span class="ok">自动复核通过: 设备内容与写入内容逐字节一致</span>';
      return;
    }
    addLog("", `自动复核发现 ${d.changeCount} 处不一致(见变更面板) —— 请勿断电, 把日志发我`);
    const lines = [`<span class="danger">自动复核: 有 ${d.changeCount} 处与写入内容不一致</span>`];
    let n = 0;
    for (const c of (d.changes || [])) {
      if (n++ >= 8) break;
      lines.push(`<span class="danger">0x${Number(c.offset).toString(16).toUpperCase().padStart(3, "0")}: ` +
        `设备 ${Number(c.old).toString(16).padStart(2, "0")} ≠ 目标 ${Number(c.new).toString(16).padStart(2, "0")}</span>`);
    }
    $("edit-diff").innerHTML = lines.join("<br>");
  } catch (e) {
    addLog("", "自动复核失败: " + e + "(写入本身已通过三层校验)");
  }
}

$("btn-edit-write").onclick = async () => {
  const dryRun = false;
  try {
    const res = await call("EditApplyToDevice", false, dryRun, $("inp-edit-ack").value);
    addLog("", (res && res.message) || "写入完成");
    if (res && res.backupPath) addLog("", "备份: " + res.backupPath);
    if (!dryRun) {
      // 写完后: 左侧显示设备实际内容, 编辑器基线也更新为设备当前内容(复用刚读的缓存),
      // 然后自动做一次独立复核 —— 三步走完界面上的"变更"归零、复核显示逐字节一致。
      await doDump();
      await loadEditor("EditLoadFromDevice");
      if (res && res.verified) await autoVerifyAfterWrite();
    } else {
      await autoVerifyAfterWrite();
    }
  } catch (e) {
    addLog("", "写入失败: " + e);
    await resyncAfterFailure();
    addLog("", "编辑器里仍是你的目标内容(可修正后重试); 左侧显示的是设备当前实际内容");
  }
};

// 设备连接/选择后允许把设备内容载入编辑器
const _origEnableOps = enableOps;
enableOps = function (on) {
  _origEnableOps(on);
};
