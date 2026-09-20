// 冒烟驱动脚本: 按真实用户顺序点一遍, 结果写到 window.__SMOKE。
// ---- 驱动脚本: 按真实用户顺序点一遍, 结果写入 window.__SMOKE ----
  (async () => {
    const $ = (id) => document.getElementById(id);
    const wait = (ms) => new Promise((r) => setTimeout(r, ms));
    const out = { steps: [], errors: window.__ERRORS, calls: () => window.__CALLS.map((c) => c.name) };
    const step = (name, v) => out.steps.push({ name, ok: !!v, detail: v === undefined ? "" : String(v) });
    await wait(120); // 等绑定轮询与 checkEnv/autoConnectAll

    step("启动后环境状态", $("env-status").textContent.includes("控制器"));
    step("设备下拉有 DDR4 条目", $("dimm-select").textContent.includes("DDR4"));

    // 选择设备 → 读 dump → 信息面板
    $("dimm-select").value = "80";
    await $("dimm-select").onchange();
    await wait(60);
    step("hex 视图渲染", $("hexgrid").querySelector(".row") !== null);
    step("hex 行数 > 10", $("hexgrid").querySelectorAll(".row").length > 10);
    step("信息面板显示厂商", $("info-body").textContent.includes("Micron"));
    step("信息面板显示容量", $("info-body").textContent.includes("8 GiB"));
    step("时序表出现", $("info-body").textContent.includes("tCK"));

    // 总线统计(真机 V2 的证据入口)
    await $("btn-bus-stats").onclick();
    await wait(40);
    step("总线统计显示读事务", $("bus-stats").textContent.includes("2140"));
    step("总线统计显示 NVM 写 0", /NVM 写\s*0/.test($("bus-stats").textContent));
    step("总线统计高亮零写入为正常色", $("bus-stats").querySelectorAll("b").length >= 4);
    await $("btn-bus-reset").onclick();
    await wait(40);
    step("清零调用后端", window.__CALLS.some((c) => c.name === "ResetBusStats"));

    // 写保护
    await $("btn-wp-status").onclick();
    await wait(60);
    step("保护块渲染 4 个", $("wp-blocks").children.length === 4);
    step("块显示字节范围", $("wp-blocks").textContent.includes("0x000-0x07F"));
    step("B0 显示锁定", $("wp-blocks").children[0].className.includes("on"));
    step("PSWP 文案(DDR4 适用)", $("wp-summary").textContent.includes("PSWP 未设置"));

    // 编辑器标签页
    $("tab-edit").onclick();
    step("编辑器页可见", !$("view-edit").classList.contains("hidden"));
    await $("btn-edit-load-dev").onclick();
    await wait(60);
    step("编辑器显示来源", $("edit-state").textContent.includes("设备 0x50"));
    step("字段表单渲染输入框", $("edit-fields").querySelectorAll("input").length >= 3);
    step("字段含 JEDEC 时序分组", $("edit-fields").textContent.includes("JEDEC 时序"));
    step("变更面板显示 CRC 通过", $("edit-diff").textContent.includes("CRC 校验通过"));

    // 应用一个字段
    const btn = $("edit-fields").querySelectorAll("button")[0];
    btn.click();
    await wait(60);
    step("应用字段调用了 EditSetField", window.__CALLS.some((c) => c.name === "EditSetField"));

    // 编辑器写入: 未勾备份应被拦
    let alerted = null;
    window.alert = (m) => { alerted = m; };
    $("chk-edit-backup").checked = false;
    $("inp-edit-ack").value = "WRITE";
    await $("btn-edit-write").onclick();
    await wait(40);
    step("未勾备份被拦", alerted !== null);
    step("未勾备份未调用后端", !window.__CALLS.some((c) => c.name === "EditApplyToDevice"));

    // 勾上备份 + 确认串 → 应调用
    $("chk-edit-backup").checked = true;
    await $("btn-edit-write").onclick();
    await wait(60);
    step("勾选后执行写入", window.__CALLS.some((c) => c.name === "EditApplyToDevice"));

    // 文件写入面板
    $("tab-info").onclick();
    await $("btn-write").onclick();
    await wait(60);
    step("写入确认面板弹出", !$("write-modal").classList.contains("hidden"));
    step("面板显示变更数", $("write-summary").textContent.includes("变更"));
    step("面板显示 diff", $("write-changes").textContent.includes("0x145"));
    $("btn-write-cancel").onclick();
    step("取消后关闭", $("write-modal").classList.contains("hidden"));

    // 干跑提示
    $("tab-edit").onclick();
    $("chk-edit-dryrun").checked = true;
    $("chk-edit-dryrun").onchange();
    step("干跑提示确认串 DRYRUN", $("inp-edit-ack").placeholder === "DRYRUN");

    out.jsErrors = window.__ERRORS;
    window.__SMOKE = out;
  })();
