# SPD Reader Writer (Go)

SPD-Reader-Writer 的 Go 复刻版: Windows 桌面工具, 通过 **PawnIO** 内核驱动直连芯片组 SMBus, 读写内存条 SPD 并解析。

原版: https://github.com/1a2m3/SPD-Reader-Writer (C# WinForms + Arduino/USB 或内核驱动 SMBus)。
本项目**去除 Arduino 串口通道**, 仅保留芯片组 SMBus 直连, 后端从 CPU-Z 驱动换成开源签名驱动 **PawnIO**(namazso, OpenRGB/LibreHardwareMonitor 同款)。

## 功能

- **读取加速(三级自适应)**: **SMBus Block Read**(协议 5, 32 字节/事务) → **Word Read**(2 字节/事务) → 逐字节。
  真机单次事务约 30ms, 逐字节读 1024B 要 30 多秒, 块读可降到 1~2 秒, 字读约 16 秒;
  每一级都先只读探测、失败即降级(AMD FCH 实测会被 HUB 拒绝块读, 于是自动落到字读)。
  界面开关可强制逐字节对照; 日志与"读取方式"面板会写明实际档位、事务数、耗时与回退原因。
- **控制器发现**: Intel PCH (I801) / AMD FCH (PIIX4, 双端口) / Intel Skylake-SP IMC (SKX, 双总线) — 与 OpenRGB 相同的 PawnIO 模块
- **SPD 扫描** 0x50-0x57, DDR4 (EE1004 SPA0/SPA1 快速命令分页) 与 DDR5 (MR11 寄存器分页) 自动识别
- **读取/保存/校验/写入** SPD dump; 写入默认增量模式(跳过相同字节)并回读校验; 支持 `-force` 全量
- **解析**:
  - DDR4/LPDDR3/4/4X: 模块类型、密度/ banks /行列、组织、位宽、容量、全部主时序(中+细粒度)、CAS 掩码、双段 CRC(含修复)、厂商/日期/序列号/部件号、**XMP 2.0 双 Profile**(时序换算+电压)
  - DDR5/LPDDR5(X): 密度/组织/通道/位宽/容量、身份区、**完整 JEDEC 时序**(byte 20-102, ps/ns + lower limit)、CRC(**基础段 + XMP 3.0 header/各槽 + EXPO**)、按规范修正的 XMP 3.0 槽位(0x2C0..0x3C0)
  - DDR2/DDR3: 容量/组织/部件号/厂商/日期/序列号/校验(DDR2 按 JEDEC 修正了总线与芯片位宽偏移)
- **SPD 编辑器**(内存中编辑, 不落盘不写设备):
  - 常用信息: 厂商(JEP106 反查/搜索)、厂商码、生产地点、日期、序列号、部件号、修订码、DRAM 厂商/stepping
  - JEDEC 时序: DDR4 medium+fine、DDR5 16bit(ps/ns, 含 lower limit)、DDR3 MTB/FTB、DDR2 BCD(含扩展码)
  - 扩展信息: **XMP 2.0**(DDR4, 2×63B)、**XMP 3.0**(DDR5, header + 5 槽)、**EXPO**(DDR5, 与 XMP 槽 3/User1 互斥) —— 可创建/修改/清除, CRC 自动重算
  - hex 视图内点格子就地编辑任意字节; **实时校验状态**:顶部显示"CRC 通过 / 需重算",
    并按"改动是否在校验范围内"分色(红=影响校验, 蓝=序列号/日期等不影响校验的字段)
- **写入护栏**(写入是唯一可能变砖的操作):
  - 写前预检: 长度/类型匹配、目标 dump CRC 必须有效、受保护块检测、高危字段(容量/组织/电压/PMIC)清单
  - 自动备份当前整片到 `~/.spdrw/backups`; 确认串 `WRITE` 才真正执行
  - 写入计划按字节 diff, **CRC 字节最后写**(中断只会留下 CRC 不符的 SPD, 而不是"看着有效但内容错")
  - 中途失败给出"已写/未写字节数 + 恢复建议"; 逐字节回读校验
  - **干跑模式**(DRYRUN): 完整跑预检/计划/校验, 一个字节都不上总线
  - **总线统计**: 干跑/写入后直接报出"读 / 页选择与命令写 / 字节写 / 其中 NVM 数据写",
    真机验证"干跑没碰 SPD"就看这一行(界面也有"读取总线统计/清零计数")
- **写保护**:
  - RSWP 状态检测: DDR5 读 MR12/MR13 位图(16×64B)、DDR4 及更早块首写测试(4×128B, **还原后回读确认**, 失败重试并报"状态未知", 绝不谎报)
  - RSWP 设置(按块)/清除; DDR5 附 MR11/MR29/MR48/MR52 原始值与"写受保护块被忽略"标志
  - PSWP: 仅 DDR2/DDR3 可经 PWPB(0110b)探测; DDR4/DDR5 无该设备类型, 明确显示"不适用"(旧版会误报"永久保护已生效")
  - 显示 BIOS "SPD write disable" 状态 (I801)

## 构建

需 Go 1.21+ (开发用 1.24)。

**必须带 `desktop,production` 构建标签**, 否则启动时报
"Wails applications will not build without the correct build tags"(这是 Wails 的运行时保护, 不是代码问题)。

Windows PowerShell:

```powershell
.\build.ps1
# 等价于:
go build -tags desktop,production -ldflags="-H windowsgui -s -w" -trimpath -o bin\SPDReaderWriter.exe .
```

或从 Linux/macOS 交叉编译 (无 CGO):

```bash
GOOS=windows GOARCH=amd64 go build -tags desktop,production -ldflags="-H windowsgui -s -w" -trimpath -o bin/SPDReaderWriter.exe .
```

安装了 Wails CLI 的话也可以直接 `wails build` (自动加标签)。

测试:

```bash
go test ./...
```

## 运行要求

1. **Windows 10/11 x64**
2. **管理员身份**运行 (SMBus 内核访问)
3. 安装 **PawnIO**: https://pawnio.eu/ (开源签名驱动, 不在 MS 恒定封禁名单; 缺失时界面会提示下载)

> ⚠️ **写 SPD 有变砖风险**: 错误内容(尤其 DDR5 PMIC/内存相关字段)可能导致无法开机。修改前务必备份 dump, 并确认校验通过。BIOS 设置里若开启了 "SPD Write Disable", 需先关闭才能写入。

## 仓库布局说明

本仓库**自带全部可复现材料**, 不内嵌任何参考项目:

- 上游 SPD-Reader-Writer 只作为出处引用(见下)。JEP106 厂商表已提取为
  `internal/spd/data/idcodes.json`, 出处与重建流程记在 `internal/spd/data/README.md`
  (上游 URL + 取用 commit + 为什么必须拆成 15 个银行 + 三步重建命令)。
- 第三方二进制(PawnIO 模块与 DLL)出处与许可证见 `third_party/pawnio/README.md`。
- 真实 dump 语料(67 份)与其来源清单见 `testdata/spd/MANIFEST.md`。

## 文档

- **[docs/实现文档.md](docs/实现文档.md)** — 实现细节:分层架构、SMBus/DDR4/DDR5 协议语义、Wails 绑定要点、测试体系、真机调试经验(踩坑实录)
- **[docs/验证清单.md](docs/验证清单.md)** — 真机逐项验证步骤
- **[docs/离线验证报告.md](docs/离线验证报告.md)** — 上线前我们到底验过什么、结论是什么、还剩什么必须真机确认(含可重跑命令与实测数字)
- **[docs/写入操作手册.md](docs/写入操作手册.md)** — 真机写入的照做顺序(干跑→首写→校验)、失败处理与恢复路径
- **[docs/真机实测记录.md](docs/真机实测记录.md)** — 真机回帖的原始证据(读取档位/事务数/耗时、编辑器、还差哪些验证)
- **[docs/superpowers/specs/](docs/superpowers/specs/)** — 设计文档(需求与范围)

## 架构

```
frontend/dist        原生 HTML/JS/CSS (无 node 构建链)
main.go              Wails v2 装配(事件桥/深色窗口)
internal/app         GUI 服务层: 连接/扫描/读写/保护/解析编排
internal/spd         SPD 解析: DDR4 全量 / DDR5(原版范围) / DDR2-3 基本信息 + JEP106 厂商表
internal/eeprom      设备语义: 分页(EE1004 quick / DDR5 MR11)、RSWP/PSWP、增量写+校验
internal/smbus       Transport 接口 + PawnIO 后端(i801/PIIX4×2/SKX×2) + 内存 Fake(测试)
internal/assets      内嵌 PawnIO 模块(SmbusI801/SmbusPIIX4/SmbusIntelSkylakeIMC .bin)与 PawnIOLib.dll
third_party/pawnio   模块与 DLL 的来源与许可证说明(LGPL-2.1)
```

每次 SMBus 事务持有全局互斥 `Global\Access_SMBUS.HTP.Method`(与 Thaiphoon 等工具仲裁); 每个(控制器×总线)一个 PawnIO 会话, 会话开启 AlwaysSleep 模式。

## 与原版的差异

| 项 | 原版 | 本项目 |
|---|---|---|
| 语言/UI | C# WinForms | Go + Wails v2 (WebView2) |
| SMBus 后端 | CPU-Z 内核驱动(封禁风险) | PawnIO(开源签名) |
| Arduino/USB 通道 | 支持 | 移除 |
| DDR4 分页协议 | BYTE 写 0x36/0x37 | **快速命令写**(EE1004 规范行为) |
| PSWP 设置 | 不支持(SMBus 路径) | 同样不支持, 明确提示 |
| DDR5 解析 | 无时序字段 | 保持一致 |
| 平台 | Windows | Windows (逻辑层跨平台可测) |

## 许可证

- 本项目代码: MIT
- PawnIO 模块与 PawnIOLib.dll: LGPL-2.1 (见 `third_party/pawnio/`)
- JEP106 厂商识别表自原版项目(gzip 资源)提取为 `internal/spd/data/idcodes.json`
