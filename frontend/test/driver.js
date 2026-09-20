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
    // 校验状态直接读左侧 dump(此时还没有编辑器内容): 正常 dump 应显示"CRC 通过"
    step("读后即显示校验状态", $("crc-status").textContent.includes("CRC 通过"));
    step("校验状态按后端结果渲染", window.__CALLS.some((c) => c.name === "CRCStatus"));

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
    // RSWP 加保护/清除都是真写设备且可能不可逆 → 必须带确认串(后端也校验)
    step("写保护确认串输入框存在", !!document.getElementById("inp-wp-ack"));
    step("缺确认串时 RSWP 不下发", (() => {
      window.prompt = () => "1";
      window.confirm = () => true;
      $("inp-wp-ack").value = "";
      $("btn-wp-set").onclick();
      return !window.__CALLS.some((c) => c.name === "WPSet");
    })());

    // 编辑器标签页
    $("tab-edit").onclick();
    step("编辑器页可见", !$("view-edit").classList.contains("hidden"));
    await $("btn-edit-load-dev").onclick();
    await wait(60);
    step("编辑器显示来源", $("edit-state").textContent.includes("设备 0x50"));
    // 字段按分组页签渲染, 一次只画当前组 → 输入框数 = 当前组字段数, 组名在 #edit-groups
    step("字段表单渲染输入框", $("edit-fields").querySelectorAll("input").length >= 2);
    step("分组页签含 JEDEC 时序", $("edit-groups").textContent.includes("JEDEC 时序"));
    step("变更面板显示 CRC 通过", $("edit-diff").textContent.includes("CRC 校验通过"));
    // 校验状态: 依据左侧 dump 判定, 并把"影响校验/不影响校验"分开
    step("hex 标题旁显示校验状态", $("crc-status").textContent.includes("CRC 通过"));
    step("校验状态给出校验区提示", ($("crc-status").title || "").includes("参与校验的区域"));
    step("CRC 值字节被标出", $("hexgrid").querySelectorAll(".crcbyte").length >= 4);
    step("改动不影响校验时标蓝", (() => {
      // 注意: 浏览器里 querySelectorAll 返回 NodeList(没有 find), 必须先转数组
      const btn = Array.from($("hexgrid").querySelectorAll("span[data-off]"))
        .find((b) => b.getAttribute("data-off") === "325");
      return btn && btn.classList.contains("chg-free");
    })());
    step("校验状态说明不影响校验", $("crc-status").textContent.includes("不在校验范围"));
    step("原始 hex 表单已移除", document.getElementById("btn-hex-apply") === null &&
      document.getElementById("hex-off") === null);

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
