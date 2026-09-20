// SPD Reader Writer (Go) 前端逻辑。Wails 绑定在 window.go.bindings.App。
"use strict";

const $ = (id) => document.getElementById(id);
let currentDump = null;

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
onEvent("log", (entry) => addLog(`[${entry.Time}] ${entry.Text}`));
function addLog(text) {
  const log = $("log");
  const div = document.createElement("div");
  div.innerHTML = `<span class="t"></span>${escapeHtml(text)}`;
  div.querySelector(".t").textContent = (div.textContent.match(/^\[[\d:]+\]/) || [""])[0];
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
  } catch (e) { addLog("连接/扫描失败: " + e); }
};

function fillDimmSelect(dimms) {
  const sel = $("dimm-select");
  sel.innerHTML = "";
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
    await call("Select", addr);
    await doDump();
  } catch (e) { addLog("选择设备失败: " + e); }
};

async function doDump() {
  const dump = await call("Dump");
  currentDump = Array.from(dump);
  renderHex(currentDump);
  enableOps(true);
  await decodeCurrent();
}

$("btn-scan").onclick = async () => {
  try {
    const dimms = await call("Scan");
    fillDimmSelect(dimms);
    addLog(`发现 ${dimms.length} 个 SPD 设备`);
  } catch (e) { addLog("扫描失败: " + e); }
};

$("btn-dump").onclick = () => doDump().catch((e) => addLog("读取失败: " + e));

// ---------- 十六进制视图 ----------
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
    const r = await call("Decode", currentDump);
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
  if (!currentDump) return;
  const path = await window.runtime.SaveFileDialog({
    Title: "保存 SPD dump",
    DefaultFilename: `spd-${Date.now()}.bin`,
  });
  if (!path) return;
  // 先存临时路径由后端读取: 直接传 dump 数组写文件
  try {
    await call("SaveDumpData", path, currentDump);
    addLog("已保存 " + path);
  } catch (e) { addLog("保存失败: " + e); }
};

$("btn-load-decode").onclick = () => pickFile(async (path) => {
  try {
    const r = await call("DecodeFile", path);
    renderInfo(r);
    const dump = await call("ReadFileBytes", path);
    currentDump = Array.from(dump);
    renderHex(currentDump);
    addLog("已解析 " + path);
  } catch (e) { addLog("解析失败: " + e); }
});

$("btn-verify").onclick = () => pickFile(async (path) => {
  try {
    await call("VerifyFile", path);
    addLog("校验通过: " + path);
  } catch (e) { addLog("校验失败: " + e); }
});

$("btn-write").onclick = () => pickFile(async (path) => {
  if (!confirm(`确定把 ${path} 写入 SPD?\n写错内容可能导致主板无法启动!`)) return;
  try {
    await call("WriteFromFile", path, false);
    addLog("写入完成: " + path);
    await doDump();
  } catch (e) { addLog("写入失败: " + e); }
});

function pickFile(cb) {
  window.runtime.OpenFileDialog({ Title: "选择文件" }).then((path) => path && cb(path));
}

// ---------- 写保护 ----------
$("btn-wp-status").onclick = async () => {
  try {
    const [blocks, pswp, offline] = await call("WPStatus");
    const el = $("wp-blocks");
    el.innerHTML = "";
    el.classList.remove("muted");
    blocks.forEach((on, i) => {
      const d = document.createElement("span");
      d.className = "blk " + (on ? "on" : "off");
      d.textContent = `B${i} ${on ? "🔒" : "🔓"}`;
      el.appendChild(d);
    });
    let extra = [];
    if (pswp) extra.push("⚠ PSWP 永久保护已生效");
    if (offline) extra.push("DDR5 离线模式(可清 RSWP)");
    addLog("保护状态: " + (extra.join("; ") || blocks.map((b, i) => `B${i}=${b ? "保护" : "开放"}`).join(", ")));
  } catch (e) { addLog("查询保护状态失败: " + e); }
};

$("btn-wp-set").onclick = async () => {
  const inp = prompt("输入要保护的块号(0-15, 逗号分隔):", "0");
  if (!inp) return;
  const blocks = inp.split(",").map((s) => parseInt(s.trim(), 10)).filter((n) => !isNaN(n));
  try {
    await call("WPSet", blocks);
    addLog("RSWP 已设置: " + blocks.join(","));
  } catch (e) { addLog("RSWP 设置失败: " + e); }
};

$("btn-wp-clear").onclick = async () => {
  if (!confirm("确定清除全部可逆写保护 (RSWP)?")) return;
  try {
    await call("WPClear");
    addLog("RSWP 已清除");
  } catch (e) { addLog("RSWP 清除失败: " + e); }
};

// ---------- 事件 ----------
onEvent("dump:done", (n) => addLog(`读取完成 ${n} 字节`));
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
whenBindingsReady(checkEnv);
