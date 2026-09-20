// SPD Reader Writer (Go) 前端逻辑。Wails 绑定在 window.go.bindings.App。
"use strict";

const $ = (id) => document.getElementById(id);
let currentDump = null;
let selectedAddr = null;

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
  renderHexB64(currentDump);
  enableOps(true);
  await decodeCurrent();
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

function renderHex(dump) {
  const grid = $("hexgrid");
  $("hex-meta").textContent = `${dump.length} 字节`;
  let html = "";
  for (let off = 0; off < dump.length; off += 16) {
    let line = `<span class="offset">${off.toString(16).padStart(4, "0")}</span>`;
    let ascii = "";
    for (let i = 0; i < 16; i++) {
      if (off + i >= dump.length) break;
      const b = dump[off + i];
      const hi = (b >> 4).toString(16);
      line += `<span class="c${hi}">${b.toString(16).padStart(2, "0")}</span> `;
      ascii += b >= 0x20 && b < 0x7f ? escapeHtml(String.fromCharCode(b)) : "·";
    }
    html += `<div class="row">${line}<span class="ascii">${ascii}</span></div>`;
  }
  grid.innerHTML = html;
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
  html += kv("厂商", escapeHtml(r.manufacturer || "—"));
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
  ["btn-save", "btn-load-decode", "btn-verify", "btn-write", "btn-wp-status", "btn-wp-set", "btn-wp-clear"].forEach((id) => ($(id).disabled = !on));
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
    renderHexB64(currentDump);
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
  rows.push(`变更 <b>${pf.changeCount}</b> 字节(其中 CRC <b>${pf.crcBytes}</b> 字节, 按计划最后写入)`);
  rows.push(pf.targetCrcValid
    ? `目标文件 CRC 校验通过`
    : `<span class="danger">目标文件 CRC 校验不通过</span>`);
  if (!pf.currentCrcValid) rows.push(`<span class="warn">设备当前内容 CRC 已不通过</span>`);
  if (pf.highRiskCount) rows.push(`<span class="danger">含高危字节 ${pf.highRiskCount} 个(容量/组织/电压/PMIC 等)</span>`);
  if (pf.protectedBlocks && pf.protectedBlocks.length) rows.push(`<span class="danger">受写保护块: ${pf.protectedBlocks.join(", ")}</span>`);
  if (pf.unknownBlocks && pf.unknownBlocks.length) rows.push(`<span class="warn">保护状态未知块: ${pf.unknownBlocks.join(", ")}</span>`);
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
  } catch (e) {
    addLog("", "写入失败: " + e);
  }
};

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
  addLog("", `保护状态: ${prot.length ? "受保护 " + prot.join(",") : "全部开放"}` +
    (st.pswpApplicable ? (st.pswp ? " · PSWP 永久保护" : " · PSWP 未设置") : " · PSWP 不适用"));
}

$("btn-wp-set").onclick = async () => {
  const inp = prompt("输入要保护的块号(0-15, 逗号分隔):", "0");
  if (!inp) return;
  const blocks = inp.split(",").map((s) => parseInt(s.trim(), 10)).filter((n) => !isNaN(n));
  if (!blocks.length) { addLog("", "未输入有效块号"); return; }
  if (!confirm("确定对这些块启用 RSWP 写保护?\n" + blocks.join(",") + "\n\n注意: 部分颗粒的 RSWP 不可逆!")) return;
  try {
    await call("WPSet", blocks);
    addLog("", "RSWP 已设置: " + blocks.join(","));
    await refreshWP().catch(() => {});
  } catch (e) { addLog("", "RSWP 设置失败: " + e); }
};

$("btn-wp-clear").onclick = async () => {
  if (!confirm("确定清除全部可逆写保护 (RSWP)?")) return;
  try {
    await call("WPClear");
    addLog("", "RSWP 已清除");
    await refreshWP().catch(() => {});
  } catch (e) { addLog("", "RSWP 清除失败: " + e); }
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
  ["btn-edit-reset", "btn-edit-fixcrc", "btn-edit-export", "btn-hex-apply"].forEach(
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

function renderEditFields() {
  const box = $("edit-fields");
  if (!editFieldsCache.length) { box.innerHTML = `<div class="placeholder">无可编辑字段</div>`; return; }
  const groups = [];
  for (const f of editFieldsCache) {
    let g = groups.find((x) => x.name === f.group);
    if (!g) { g = { name: f.group, items: [] }; groups.push(g); }
    g.items.push(f);
  }
  let html = "";
  for (const g of groups) {
    html += `<div class="grp">${escapeHtml(g.name)}</div><table>`;
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
  html += `<datalist id="mfg-list"></datalist>`;
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
    await refreshEditDiff();
  } catch (e) { addLog("", "重置失败: " + e); }
};

$("btn-edit-fixcrc").onclick = async () => {
  try {
    const st = await call("EditFixCRC");
    renderEditState(st);
    editFieldsCache = (await call("EditFields")) || [];
    renderEditFields();
    await refreshEditDiff();
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

$("btn-hex-apply").onclick = async () => {
  const off = parseHexOrDec($("hex-off").value);
  const val = parseHexOrDec($("hex-val").value);
  if (off == null || val == null) { addLog("", "偏移/值格式无效(可用 0x1F0 或 496)"); return; }
  try {
    const st = await call("EditSetByte", off, val);
    renderEditState(st);
    await refreshEditDiff();
    await refreshEditBytes();
  } catch (e) { addLog("", "原始编辑失败: " + e); }
};

function parseHexOrDec(s) {
  s = String(s || "").trim();
  if (!s) return null;
  const v = /^0x/i.test(s) ? parseInt(s, 16) : parseInt(s, 10);
  return isNaN(v) ? null : v;
}

async function refreshEditBytes() {
  try {
    const b64 = await call("EditBytes");
    if (typeof b64 === "string") { currentDump = b64; renderHexB64(b64); }
  } catch (e) { /* 编辑器未载入 */ }
}

async function refreshEditDiff() {
  const d = await call("EditDiff");
  const lines = [];
  lines.push(`变更 <b>${d.changeCount}</b> 字节 · CRC 字段 ${d.crcFields} · 高危 ${d.highRisk}`);
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
  } catch (e) { addLog("", "写入失败: " + e); }
};

// 设备连接/选择后允许把设备内容载入编辑器
const _origEnableOps = enableOps;
enableOps = function (on) {
  _origEnableOps(on);
  $("btn-edit-load-dev").disabled = !on;
};
