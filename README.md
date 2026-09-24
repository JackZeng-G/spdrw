# SPD Reader Writer (Go)

<img src="build/icon/appicon.png" alt="图标" width="96" align="right">

Windows 桌面工具, 读取 / 解析 / 编辑 / 写入内存条 **SPD**(Serial Presence Detect)。
Go + Wails v2 实现, 经开源签名驱动 **PawnIO**(namazso, OpenRGB/LibreHardwareMonitor 同款)
直连芯片组 SMBus —— 不用 Arduino 串口, 不用 CPU-Z 驱动。

**支持 DDR3 / DDR4 / DDR5**(含 LPDDR 变体与 XMP 2.0 / XMP 3.0 / EXPO), **不支持 DDR2**。

> ## ⚠️ 风险提醒 —— 先读这里
>
> - **写入是唯一可能让内存条变砖的操作**: 错误内容(尤其 DDR5 的 PMIC/电压/容量/组织字段)
>   可能导致**无法开机**; 时序/电压改错, 轻则不稳定蓝屏, 重则主板自检过不去。
> - **修改前务必备份**: 工具会在写入前自动把当前整片备份到程序同级 `backup\` 目录,
>   但最稳的是自己再另存一份原始 dump; 界面上确认串 `WRITE` 之后才会真正落笔。
> - **RSWP/PSWP 属于不可逆操作**: RSWP 在多数内存条上设置后**无法清除**(清除是尽力而为,
>   依赖平台与条目支持), 设置前三思。
> - **BIOS 开了 "SPD Write Disable" 时工具只读**, 这是保护不是故障; 想写先去 BIOS 关掉。
> - 本工具按"原样"提供, 写坏内存条**作者不承担责任**; 第一次写入请严格照
>   [docs/写入操作手册.md](docs/写入操作手册.md) 的顺序做。

## 功能一览

- **读**: 整片 1024B 约 0.3 秒(忙等 + 块读/字读自适应, 全自动无开关)
- **看**: 解析容量/组织/时序/厂商/部件号/序列号/CRC; hex 视图有区段底色、改动标记、
  行尾参数标注三套视觉信号, 每个字节都能悬停出说明
- **编辑**: 内存中编辑(不碰硬件) —— 常用厂商下拉(自动写 JEP106 ID)、JEDEC 时序按 ns 或
  clk 两种写法、XMP/EXPO profile 增删改、hex 格子就地改字节、CRC 一键重算
- **写**: 增量写入 + 逐字节回读校验 + 写后自动复核; 写前自动备份、高危字段预检、
  支持干跑; 写保护(RSWP/PSWP)状态检测与按块设置/清除

## 快速开始

1. Windows 10/11 x64, 双击 `SPDReaderWriter.exe`(清单内置 `requireAdministrator`,
   自动弹 UAC 提权; 拒绝提权则无法启动)
2. 安装 [PawnIO](https://pawnio.eu/)(缺失时界面会提示下载)
3. 扫描 → 选择内存条 → 读取。**写入有变砖风险**, 第一次写请先看
   [docs/写入操作手册.md](docs/写入操作手册.md)

> 构建、测试、架构与协议细节等开发向内容全部在 **[docs/实现文档.md](docs/实现文档.md)**。

## 文档

| 文档 | 内容 |
|---|---|
| **[docs/实现文档.md](docs/实现文档.md)** | 实现细节: 分层架构、SMBus/DDR4/DDR5 协议语义、构建与测试体系、踩坑实录(开发向总入口) |
| [docs/写入操作手册.md](docs/写入操作手册.md) | 真机写入的照做顺序(预检→首写→校验)、失败处理与恢复路径 |
| [docs/真机验证.md](docs/真机验证.md) | 真机验证清单(勾选进度) + 各平台实测原始证据 |
| [docs/离线验证报告.md](docs/离线验证报告.md) | 无硬件环境下的验证结论与审计记录(归档) |
| [docs/设计文档.md](docs/设计文档.md) | 立项时的目标/范围/架构决策(历史 spec) |

## 许可证

- 本项目代码: MIT, 版权 `by jackzeng 2026`(界面顶栏与 exe 属性里都会显示)
- PawnIO 模块与 PawnIOLib.dll: LGPL-2.1(见 `third_party/pawnio/`)
- JEP106 厂商名录: JEDEC 标准事实数据, 提取自上游开源项目并保留出处 —— 详见 `internal/spd/data/README.md`
