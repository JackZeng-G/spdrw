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
    step("hex 有列头(00..0F)", $("hexgrid").querySelector(".row.head") !== null &&
      $("hexgrid").querySelectorAll(".colhead").length === 16);
    step("信息面板显示厂商", $("info-body").textContent.includes("Micron"));
    step("信息面板显示容量", $("info-body").textContent.includes("8 GiB"));
    step("读后即显示校验状态", $("crc-status").textContent.includes("CRC 通过"));
    step("读取方式挂在 CRC 行", /CRC/.test($("info-body").textContent) &&
      /次事务/.test($("read-mode").textContent) && /kHz|时钟/.test($("read-mode").textContent));
    step("控制器枚举按钮已合并(无 ⟳)", !document.getElementById("btn-refresh-ctl") &&
      $("btn-scan").textContent.trim() === "重扫");
    // 总线统计面板已移除(信息并入日志与读取方式一行)
    step("总线统计面板已移除", !document.getElementById("btn-bus-stats") &&
      !document.getElementById("bus-stats"));

    // 读一次就自动载入编辑器(界面已无"从设备载入")
    step("读取后编辑器自动载入", $("edit-state").textContent.includes("设备 0x50"));
    step("界面无“从设备载入”按钮", !document.getElementById("btn-edit-load-dev"));
    step("文件操作在顶栏", !!document.querySelector("#topbar #btn-edit-load-file") &&
      !!document.querySelector("#topbar #btn-edit-export") &&
      !!document.querySelector("#topbar #btn-verify"));
    step("旧的重复按钮已移除", !document.getElementById("btn-save") &&
      !document.getElementById("btn-load-decode") && !document.getElementById("btn-write"));
    step("写入确认面板已移除", !document.getElementById("write-modal"));
    $("tab-edit").onclick();
    step("编辑器页可见", !$("view-edit").classList.contains("hidden"));
    step("字段两列网格渲染", $("edit-fields").querySelectorAll(".fitem").length >= 2);
    step("字段默认只显示常用", /常用 \d+ \/ \d+ 个字段/.test($("edit-field-count").textContent));
    step("分组页签带常用/全部计数", /\(\d+\/\d+\)/.test($("edit-groups").textContent));
    step("变更面板显示 CRC 通过", $("edit-diff").textContent.includes("CRC 校验通过"));
    step("hex 标题旁有重算 CRC 按钮", !!document.getElementById("btn-edit-fixcrc"));
    step("写入能力探测按钮存在", !!document.getElementById("btn-write-probe"));
    step("写保护面板在编辑器页", !!document.getElementById("wp-blocks"));
    step("写保护确认串输入框存在", !!document.getElementById("inp-wp-ack"));
    step("缺确认串时 RSWP 不下发", (() => {
      window.prompt = () => "1";
      window.confirm = () => true;
      $("inp-wp-ack").value = "";
      $("btn-wp-set").onclick();
      return !window.__CALLS.some((c) => c.name === "WPSet");
    })());

    // 应用一个字段
    const btn = $("edit-fields").querySelectorAll("button")[0];
    btn.click();
    await wait(60);
    step("应用字段调用了 EditSetField", window.__CALLS.some((c) => c.name === "EditSetField"));

    // 编辑器写入: 确认串 + 自动备份(界面已无备份/干跑勾选)
    step("备份与干跑勾选已移除", !document.getElementById("chk-edit-backup") &&
      !document.getElementById("chk-edit-dryrun"));
    $("inp-edit-ack").value = "WRITE";
    await $("btn-edit-write").onclick();
    await wait(120);
    step("写入调用了 EditApplyToDevice(false,false,WRITE)",
      window.__CALLS.some((c) => c.name === "EditApplyToDevice"));
    step("写入后自动复核(EditVerifyFile)",
      window.__CALLS.some((c) => c.name === "EditVerifyFile"));

    // 问号提示: 点一下才展开
    const hint = document.getElementById("hint-write");
    hint.classList.add("hidden");
    document.querySelector('button.q[data-hint="hint-write"]').click();
    step("问号提示可展开", !hint.classList.contains("hidden"));

    out.jsErrors = window.__ERRORS;
    window.__SMOKE = out;
  })();
