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
    opt.value = i;
    opt.textContent = `${c.name}${c.wpKnown ? (c.noSpdWp ? " · SPD写可" : " · BIOS禁写SPD") : ""}`;
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
function addLog(time, text) {
  const log = $("log");
  const div = document.createElement("div");
  const t = document.createElement("span");
  t.className = "t";
  t.textContent = `[${time ?? ""}] `;
  div.appendChild(t);
  div.appendChild(document.createTextNode(text ?? ""));
  log.appendChild(div);
  log.scrollTop = log.scrollHeight;
}
function escapeHtml(s) {
  return String(s).replace(/[&<>"]/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;" }[c]));
}

// ---------- 控制器/扫描/设备 ----------
$("btn-refresh-ctl").onclick = () => checkEnv();

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
    await call("Select", addr);
    await doDump();
  } catch (e) { addLog("", "选择设备失败: " + e); }
};

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
  await refreshBusTuning().catch(() => {});
  await refreshCRCStatus().catch(() => {});
}

$("btn-scan").onclick = async () => {
  try {
    const idx = parseInt($("ctl-select").value, 10);
    if (isNaN(idx) || idx < 0) throw new Error("未枚举到控制器");
    await call("Connect", idx); // 重复连接无害, 保证状态就绪
    const dimms = (await call("Scan")) || [];
    selectedAddr = null; // 重扫后回到空白, 等待用户手动选择
    fillDimmSelect(dimms);
    addLog("", `重扫完成: ${dimms.length} 个 SPD 设备, 请选择要读取的 DIMM`);
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
  if (!editorLoaded) { addLog("", "左侧 hex 需先在编辑器标签页载入数据后才能直接修改"); return; }
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
    } catch (e) { addLog("", "原始编辑失败: " + e); }
  };
  inp.onkeydown = (e) => {
    if (e.key === "Enter") { e.preventDefault(); finish(true); }
    else if (e.key === "Escape") { e.preventDefault(); finish(false); }
  };
  inp.onblur = () => finish(true);
};
function renderHex(dump) {
  const grid = $("hexgrid");
  $("hex-meta").textContent = `${dump.length} 字节`;
  $("hex-hint").textContent = editorLoaded
    ? "· 点击字节就地修改(hex) · 红=改动影响校验, 蓝=不影响"
    : "";
  grid.classList.toggle("editable", editorLoaded);
  let html = "";
  for (let off = 0; off < dump.length; off += 16) {
    let line = `<span class="offset">${off.toString(16).padStart(4, "0")}</span>`;
    let ascii = "";
    for (let i = 0; i < 16; i++) {
      if (off + i >= dump.length) break;
      const b = dump[off + i];
      const hi = (b >> 4).toString(16);
      line += `<span class="hexbyte c${hi}" data-off="${off + i}">${b.toString(16).padStart(2, "0")}</span> `;
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

// applyHexMarks 给已渲染的格子补上"改动/CRC 字节"样式, 并刷新标题旁的校验状态。
// 只切 class, 不重绘 —— 否则会把正在输入的格子里的 input 一起抹掉。
function applyHexMarks() {
  const grid = $("hexgrid");
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
async function decodeCurrent() {
  if (!currentDump) return;
  try {
    const r = await call("Decode", currentDump); // base64 string → Go []byte
    renderInfo(r);
  } catch (e) {
    $("info-body").innerHTML = `<div class="placeholder">解析失败: ${escapeHtml(String(e))}</div>`;
  }
}

function renderInfo(r) {
  const kv = (k, v, cls) => `<div class="k">${k}</div><div class="v ${cls || ""}">${v ?? "—"}</div>`;
  let html = `<div class="kv">`;
  html += kv("类型", escapeHtml(r.ramType) + (r.moduleType ? ` · ${escapeHtml(r.moduleType)}` : ""));
  html += kv("容量", escapeHtml(r.totalHuman || `${r.totalMib} MiB`));
  if (r.ranks) html += kv("组织", `${r.ranks} Rank × ${r.deviceWidth}bit · 总线 ${r.busWidth}bit`);
  html += kv("厂商", escapeHtml(r.manufacturer || "—") +
    (r.manufacturerNote ? `<br><span class="muted small">${escapeHtml(r.manufacturerNote)}</span>` : ""));
  html += kv("部件号", escapeHtml(r.partNumber || "—"));
  if (r.dateYear) html += kv("生产日期", `${r.dateYear} 年第 ${r.dateWeek} 周`);
  if (r.serialHex) html += kv("序列号", `0x${r.serialHex}`);
  html += kv("CRC", r.crcOk ? "校验通过" : "校验失败", r.crcOk ? "good" : "bad");
  html += `</div>`;

  if (r.hasTimings && r.tck) {
    html += `<div class="section">时序</div><table class="timing"><tr><th>tCK</th><td>${fmtT(r.tck)} (${r.tck.ns.toFixed(3)} ns${r.tck.ns ? " · " + (1000 / r.tck.ns).toFixed(0) + " MHz" : ""})</td></tr>`;
    if (r.casLatencies) html += `<tr><th>CL</th><td>${escapeHtml(r.casLatencies)}</td></tr>`;
    const rows = [["tAA", r.taa], ["tRCD", r.trcd], ["tRP", r.trp], ["tRAS", r.tras], ["tRC", r.trc], ["tRFC1", r.trfc1], ["tRFC2", r.trfc2], ["tRFC4", r.trfc4], ["tFAW", r.tfaw], ["tRRD_S", r.trrdS], ["tRRD_L", r.trrdL], ["tCCD_L", r.tccdL], ["tWR", r.twr]];
    for (const [name, t] of rows) {
      if (t && t.ns) html += `<tr><th>${name}</th><td>${fmtT(t)} (${t.ns.toFixed(3)} ns)</td></tr>`;
    }
    html += `</table>`;
  }

  if (r.ddr5Timings && r.ddr5Timings.length) {
    html += `<div class="section">JEDEC 时序(DDR5)</div><table class="timing">`;
    for (const t of r.ddr5Timings) {
      const ns = (t.ns != null) ? `${t.ns.toFixed(3)} ns` : "—";
      const cyc = t.cycles ? ` · ${t.cycles} clk` : "";
      const low = t.lower ? ` <span class="muted">(下限 ${t.lower})</span>` : "";
      html += `<tr><th>${escapeHtml(t.name)}</th><td>${ns}${cyc}${low}</td></tr>`;
    }
    html += `</table>`;
    if (r.casLatencies) html += `<div class="kv"><div class="k">CL 支持</div><div class="v">${escapeHtml(r.casLatencies)}</div></div>`;
  }

  if (r.hasXmp && r.xmp && r.xmp.length) {
    html += `<div class="section">Intel XMP</div><table class="timing">`;
    r.xmp.forEach((p) => {
      html += `<tr><th>Profile ${p.number}${p.enabled ? " (启用)" : ""}</th><td>v${(p.version >> 4) & 0xF}.${p.version & 0xF} · ${escapeHtml(p.summary || "未设置")} ${p.casLatencies ? "· CL " + escapeHtml(p.casLatencies) : ""}</td></tr>`;
    });
    html += `</table>`;
  }
  if (r.hasExpo) html += `<div class="section">AMD EXPO</div><div>存在 EXPO 配置区</div>`;
  if (r.basic) {
    html += `<div class="section">基本信息</div><div class="kv">`;
    html += kv("tCK", `${r.basic.tckminNs?.toFixed(2)} ns`);
    html += `</div>`;
  }
  $("info-body").innerHTML = html;
}

function fmtT(t) {
  return t.cycles ? `${t.cycles} clk` : "—";
}

// ---------- 文件操作 ----------
function enableOps(on) {
  ["btn-save", "btn-load-decode", "btn-verify", "btn-write", "btn-wp-status", "btn-wp-set", "btn-wp-clear", "btn-write-probe"].forEach((id) => ($(id).disabled = !on));
}

$("btn-save").onclick = async () => {
  // 保存对话框在 Go 侧弹出(v2 JS 运行时无对话框 API), 数据走后端缓存
  try {
    await call("SaveDumpDialog");
  } catch (e) { addLog("", "保存失败: " + e); }
};

$("btn-load-decode").onclick = async () => {
  // 打开对话框在 Go 侧弹出, 直接返回解析结果与 dump
  try {
    const r = await call("DecodeFileDialog");
    if (!r) return; // 用户取消
    renderInfo(r);
    currentDump = await call("ReadFileBytes", r.path); // base64 string
    resetEditMarks();
    renderHexB64(currentDump);
    await refreshCRCStatus().catch(() => {});
    addLog("", "已解析 " + r.path);
  } catch (e) {
    if (String(e).includes("已取消")) { addLog("", "已取消"); return; }
    addLog("", "解析失败: " + e);
  }
};

$("btn-verify").onclick = async () => {
  try {
    const path = await call("VerifyFileDialog");
    addLog("", "校验通过: " + path);
  } catch (e) {
    if (String(e).includes("已取消")) { addLog("", "已取消"); return; }
    addLog("", "校验失败: " + e);
  }
};

// ---------- 写入(预检 → diff → 确认串 → 执行) ----------
let writeState = { path: null, preflight: null };

$("btn-write").onclick = async () => {
  try {
    // 旧实现弹框后直接写; 现在先只取路径, 走预检/确认面板
    const path = await call("PickWriteFile");
    await openWritePanel(path);
  } catch (e) {
    if (String(e).includes("已取消")) { addLog("", "已取消"); return; }
    addLog("", "写入准备失败: " + e);
  }
};

async function openWritePanel(path) {
  const force = $("chk-force").checked;
  const pf = await call("PreflightWrite", path, force);
  if (!pf) throw new Error("预检无结果");
  writeState = { path, preflight: pf };
  renderPreflight(pf);
  $("write-modal").classList.remove("hidden");
}

$("chk-force").onchange = async () => {
  if (!writeState.path) return;
  try { await openWritePanel(writeState.path); } catch (e) { addLog("", "预检失败: " + e); }
};
$("chk-dryrun").onchange = () => {
  $("inp-ack").placeholder = $("chk-dryrun").checked ? "DRYRUN" : "WRITE";
};

function closeWritePanel() {
  $("write-modal").classList.add("hidden");
  writeState = { path: null, preflight: null };
  $("inp-ack").value = "";
}
$("btn-write-cancel").onclick = closeWritePanel;

function renderPreflight(pf) {
  const esc = escapeHtml;
  const rows = [];
  const addr = pf.addr != null ? "0x" + Number(pf.addr).toString(16).padStart(2, "0") : "?";
  $("write-target").textContent = `${addr} · ${pf.generation} ← ${pf.path}`;
  rows.push(pf.sizeOk
    ? `大小校验通过(${pf.fileSize} 字节)`
    : `<span class="danger">大小不符: 文件 ${pf.fileSize} 字节 / SPD ${pf.deviceSize} 字节</span>`);
  // 增量写入: 只把"与设备不同"的字节下到总线。这是"只改一个小字段做写入测试"的依据 ——
  // 改一个字节就只写一个字节(勾了"强制模式"才会写全部)。
  rows.push(`变更 <b>${pf.changeCount}</b> 字节(其中 CRC <b>${pf.crcBytes}</b> 字节, 按计划最后写入)`);
  rows.push(pf.changeCount > 0
    ? `本次**只会**写上面这 ${pf.changeCount} 个字节: 其余 ${Math.max(0, pf.deviceSize - pf.changeCount)} 个字节不发送任何写事务` +
      `(增量模式; 只有勾"强制模式"才会写全部 ${pf.deviceSize} 字节)`
    : `设备内容与目标一致: 不会写入任何字节`);
  if (pf.targetGeneration) rows.push(`世代对照: 目标 ${esc(pf.targetGeneration)} = 设备 ${esc(pf.generation)} ✓`);
  rows.push(`写入后自动校验: ① 每个字节写完立即回读 ② 整片 ${pf.deviceSize} 字节逐字节比对 ` +
    `③ 改动字节再用逐字节读法复核(绕过块读) —— 任一不符立即自动回滚`);
  rows.push(pf.targetCrcValid
    ? `目标文件 CRC 校验通过`
    : `<span class="danger">目标文件 CRC 校验不通过</span>`);
  if (!pf.currentCrcValid) rows.push(`<span class="warn">设备当前内容 CRC 已不通过</span>`);
  if (pf.highRiskCount) rows.push(`<span class="danger">含高危字节 ${pf.highRiskCount} 个(容量/组织/电压/PMIC 等)</span>`);
  if (pf.protectedBlocks && pf.protectedBlocks.length) rows.push(`<span class="danger">受写保护块: ${pf.protectedBlocks.join(", ")}</span>`);
  if (pf.unknownBlocks && pf.unknownBlocks.length) {
    rows.push(`<span class="warn">保护状态未知块: ${pf.unknownBlocks.join(", ")}` +
      `(本次是预览, 未做写保护探测以免写入设备; 真正写入时会检测并在受保护时拒绝)</span>`);
  }
  if (pf.pswp) rows.push(`<span class="danger">该条处于 PSWP 永久写保护</span>`);
  for (const w of (pf.warnings || [])) rows.push(`<span class="warn">提示: ${esc(w)}</span>`);
  if (pf.blocked) rows.push(`<span class="danger">已阻断: ${esc(pf.blockReason)}</span>`);
  $("write-summary").innerHTML = rows.join("<br>");

  let html = `<table><tr><th>区域</th><th>风险</th><th>字节</th><th>偏移</th></tr>`;
  for (const f of (pf.fields || [])) {
    const cls = f.risk === "high" ? "danger" : f.risk === "medium" ? "warn" : "";
    html += `<tr><td>${esc(f.region)}</td><td class="${cls}">${esc(f.risk)}</td><td>${f.count}</td><td>${esc(f.ranges)}</td></tr>`;
  }
  html += `</table>`;
  $("write-fields").innerHTML = html;

  const hexb = (n, w) => Number(n).toString(16).toUpperCase().padStart(w, "0");
  let d = "";
  for (const c of (pf.changes || [])) {
    d += `<div>0x${hexb(c.offset, 3)}  ${hexb(c.old, 2)} → ${hexb(c.new, 2)}${c.isCRC ? "  (CRC)" : ""}</div>`;
  }
  if (pf.changesTruncated) d += `<div class="muted">…(变更过多, 仅显示前 300 条)</div>`;
  if (!d) d = `<div class="muted">无差异</div>`;
  $("write-changes").innerHTML = d;

  $("btn-write-go").disabled = !!pf.blocked || pf.changeCount === 0;
  $("inp-ack").placeholder = $("chk-dryrun").checked ? "DRYRUN" : "WRITE";
}

$("btn-write-go").onclick = async () => {
  const pf = writeState.preflight;
  if (!pf) return;
  const dryRun = $("chk-dryrun").checked;
  const force = $("chk-force").checked;
  if (pf.blocked) { addLog("", "预检未通过, 已阻断: " + pf.blockReason); return; }
  if (!dryRun && !$("chk-backup").checked) {
    alert("请先勾选“我已另有备份”——写错 SPD 可能导致主板无法启动。");
    return;
  }
  try {
    const res = await call("WriteConfirmed", writeState.path, force, dryRun, $("inp-ack").value);
    addLog("", (res && res.message) || (dryRun ? "干跑完成" : "写入完成"));
    if (res && res.backupPath) addLog("", "备份: " + res.backupPath);
    closeWritePanel();
    if (dryRun) addLog("", "干跑模式: SPD 未被改动");
    else await doDump();
    await refreshBusStats().catch(() => {});
    await refreshReadMode().catch(() => {});
  } catch (e) {
    addLog("", "写入失败: " + e);
    // 失败后必须重新读设备: 是否已回滚只有重读才知道, 左侧视图/校验状态也要同步
    await resyncAfterFailure();
  }
};

// resyncAfterFailure 在写入失败后重新读取设备内容并刷新视图与校验状态。
async function resyncAfterFailure() {
  addLog("", "正在重新读取设备内容(确认回滚结果)…");
  try {
    await doDump();
    addLog("", "已重新读取设备: 请以左侧内容与校验状态为准");
  } catch (err) {
    addLog("", "重新读取失败: " + err);
  }
}

// ---------- 总线统计 ----------
// 真机 V2 验证: 干跑前后各读一次计数, NVM 写必须为 0。
$("btn-bus-stats").onclick = async () => {
  try {
    await refreshBusStats();
    await refreshBusTuning();
  } catch (e) { addLog("", "读取总线统计失败: " + e); }
};
$("btn-bus-reset").onclick = async () => {
  try { await call("ResetBusStats"); addLog("", "总线计数已清零"); await refreshBusStats(); }
  catch (e) { addLog("", "清零失败: " + e); }
};

// 总线调优: SMBus 时钟频率 + 等待模式。
//
// 等待模式决定读速的量级: 休眠模式下模块的每次等待都交给 Windows 线程休眠, 一个时钟
// 中断约 15.6ms, 于是一次 2 字节读固定花 ~31ms(整片 1024 字节 = 512 次事务 ≈ 16 秒);
// 忙等模式回到真实总线时间(396kHz 下一次约 116µs)。
async function refreshBusTuning() {
  try {
    const t = await call("BusTuning");
    if (!t) return;
    const clock = t.clockHz ? `${(t.clockHz / 1000).toFixed(1)} kHz` : (t.clockNote || "时钟未知");
    $("bus-tuning").innerHTML = `SMBus 时钟 <b>${escapeHtml(clock)}</b> · 等待模式 ` +
      `<b class="${t.sleepMode === 2 ? "warn" : "ok"}">${escapeHtml(t.sleepModeName || "?")}</b>` +
      ` · 读加速 <b>自动</b>(块读→字读→逐字节, 失败自动降级)` +
      (t.note ? `<br><span class="warn">${escapeHtml(t.note)}</span>` : "");
  } catch (e) { /* 未连接 */ }
}

// 读加速(块读 → 字读 → 逐字节)与等待模式(忙等 → 折中)都不再需要手动开关:
// 程序自己按"探测可用档位 + 失败自动降级"选路, 并把实际档位/降级原因写进日志。

async function refreshReadMode() {
  try {
    const st = await call("ReadStats");
    if (!st) return;
    const mode = st.mode || (st.blockReadKnown ? (st.blockReadOK ? "块读加速" : "逐字节(块读不可用)") : "尚未读取");
    $("read-mode").innerHTML = `本次读取: <b>${mode}</b> · 事务 ${st.transactions} 次` +
      (st.elapsedMs ? ` · 耗时 ${(st.elapsedMs / 1000).toFixed(2)}s` : "") +
      ` · 块读 ${st.blockBytes}B / 字读 ${st.wordBytes || 0}B / 逐字节 ${st.fallbackBytes}B` +
      (st.note ? `<br><span class="warn">${escapeHtml(st.note)}</span>` : "");
  } catch (e) { /* 未选设备 */ }
}

async function refreshBusStats() {
  const b = await call("BusStats");
  if (!b) throw new Error("后端未返回统计");
  const nvm = b.nvmWrites || 0;
  $("bus-stats").innerHTML =
    `${escapeHtml(b.generation || "")}<br>` +
    `读 <b>${b.reads}</b> · 页选择/命令写 <b>${(b.quickWrites || 0) + (b.byteWrites || 0)}</b> · ` +
    `字节写 <b>${b.byteDataWrites}</b><br>` +
    `其中 NVM 写 <b class="${nvm === 0 ? "ok" : "bad"}">${nvm}</b>`;
  addLog("", `总线统计: 读 ${b.reads} / 页选择与命令写 ${(b.quickWrites || 0) + (b.byteWrites || 0)} / ` +
    `字节写 ${b.byteDataWrites}(NVM ${nvm})`);
  return b;
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
  if (wpAck($("inp-wp-ack").value) !== "RSWP") {
    addLog("", 'RSWP 加保护需要确认串: 请在"写保护"面板的确认串框里输入 RSWP');
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
  // 注意: btn-edit-write 由 refreshEditDiff 依据 CRC/变更数决定, 不在这里放开
  ["btn-edit-reset", "btn-edit-fixcrc", "btn-edit-export", "btn-edit-verify-dev"].forEach(
    (id) => ($(id).disabled = !on));
}

$("btn-edit-load-dev").onclick = async () => {
  try { await loadEditor("EditLoadFromDevice"); }
  catch (e) { addLog("", "载入设备失败: " + e); }
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
  await refreshEditBytes();   // 让左侧 hex 进入"可直接点击修改"状态
  addLog("", `编辑器已载入: ${st.source}(${st.generation} ${st.size}B)`);
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
  // 分组按钮(字段多时不必一次渲染全部, 也更好找)
  tabs.innerHTML = "";
  for (const g of groups) {
    const b = document.createElement("button");
    b.textContent = `${g.name}(${g.items.length})`;
    b.className = g.name === editGroup ? "active" : "";
    b.onclick = () => { editGroup = g.name; renderEditFields(); };
    tabs.appendChild(b);
  }
  let html = "";
  for (const g of groups) {
    if (g.name !== editGroup) continue;
    html += `<table>`;
    for (const f of g.items) {
      const risk = f.risk === "high" ? "risk-high" : f.risk === "medium" ? "risk-medium" : "";
      const kind = f.kind === "bool" ? "text" : "text";
      const title = [f.offset, f.unit, f.note].filter(Boolean).join(" · ");
      const list = f.key === "manufacturer" || f.key === "dramManufacturer" ? ` list="mfg-list"` : "";
      html += `<tr title="${escapeHtml(title)}">` +
        `<td class="${risk}">${escapeHtml(f.name)}</td>` +
        `<td><input type="${kind}" data-key="${escapeHtml(f.key)}" value="${escapeHtml(f.value)}"${list}></td>` +
        `<td class="act"><button data-apply="${escapeHtml(f.key)}">应用</button></td></tr>`;
    }
    html += `</table>`;
  }
  // datalist 是静态元素(在 index.html 里), 这里只填选项
  box.innerHTML = html;
  box.querySelectorAll("button[data-apply]").forEach((b) => {
    b.onclick = () => applyEditField(b.getAttribute("data-apply"), box);
  });
  box.querySelectorAll("input[data-key]").forEach((inp) => {
    inp.onkeydown = (ev) => { if (ev.key === "Enter") applyEditField(inp.getAttribute("data-key"), box); };
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

// 与设备比对: 重新整片读取设备, 与编辑器内容逐字节比较(写入后的独立复核)。
// 注意: 这会在真机上重读整片(本机 DDR5 约 16s), 是"写入到底成没成"的独立证据。
$("btn-edit-verify-dev").onclick = async () => {
  try {
    addLog("", "正在重新读取设备并与编辑器内容比对…");
    const d = await call("EditVerifyFile");
    renderEditState(await call("EditState"));
    const lines = [];
    if (!d.changeCount) {
      lines.push('<span class="ok">设备内容与编辑器内容逐字节一致(校验通过)</span>');
      addLog("", "与设备比对: 逐字节一致(校验通过)");
    } else {
      lines.push(`<span class="danger">与设备不一致 ${d.changeCount} 处</span>`);
      let n = 0;
      for (const c of (d.changes || [])) {
        if (n++ >= 8) break;
        lines.push(`<span class="danger">0x${Number(c.offset).toString(16).toUpperCase().padStart(3, "0")}: ` +
          `设备 ${Number(c.old).toString(16).padStart(2, "0")} ≠ 编辑器 ${Number(c.new).toString(16).padStart(2, "0")}</span>`);
      }
      if (d.changeCount > 8) lines.push("…");
      addLog("", `与设备比对: 有 ${d.changeCount} 处不一致(见变更面板)`);
    }
    lines.push(d.crcOk ? "CRC 校验通过" : '<span class="danger">CRC 不通过</span>');
    $("edit-diff").innerHTML = lines.join("<br>");
  } catch (e) {
    addLog("", "与设备比对失败: " + e);
  }
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
  $("inp-edit-ack").placeholder = $("chk-edit-dryrun").checked ? "DRYRUN" : "WRITE";
}

$("chk-edit-dryrun").onchange = () => {
  $("inp-edit-ack").placeholder = $("chk-edit-dryrun").checked ? "DRYRUN" : "WRITE";
};

$("btn-edit-write").onclick = async () => {
  const dryRun = $("chk-edit-dryrun").checked;
  if (!dryRun && !$("chk-edit-backup").checked) {
    alert("请先勾选“我已另有备份”——写错 SPD 可能导致主板无法启动。");
    return;
  }
  try {
    const res = await call("EditApplyToDevice", false, dryRun, $("inp-edit-ack").value);
    addLog("", (res && res.message) || "写入完成");
    if (res && res.backupPath) addLog("", "备份: " + res.backupPath);
    if (res && res.verified && !dryRun) {
      addLog("", '已校验通过;要再独立确认一次可点"与设备比对(重新读取校验)"');
    }
    if (!dryRun) {
      await doDump();
      const st = await call("EditState");
      renderEditState(st);
      editFieldsCache = (await call("EditFields")) || [];
      renderEditFields();
      await refreshEditDiff();
    } else {
      addLog("", "干跑模式: SPD 未被改动");
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
  $("btn-edit-load-dev").disabled = !on;
  $("btn-bus-stats").disabled = !on;
  $("btn-bus-reset").disabled = !on;
  refreshBusTuning().catch(() => {});
};
