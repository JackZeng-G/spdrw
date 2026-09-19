# SPD-RW Go — 设计文档 (spec)

日期：2026-09-20
状态：已与需求方确认（对话中逐节过审）

## 1. 目标

以 Go 复刻 [SPD-Reader-Writer](https://github.com/1a2m3/SPD-Reader-Writer)（C# + Arduino），
产出一个 **Windows 桌面应用 `spdrw.exe`**：

- **去掉 Arduino**：不再经串口 + Arduino 做 I2C 主控，改为直接使用主板 SMBus 控制器读写内存条 SPD EEPROM。
- **Windows 桌面 UI**（对标原版 WinForms GUI）：扫描、彩色 hex 视图、SPD 解析信息面板、读/写/校验、写保护管理。
- 内核访问采用 **PawnIO**（官方签名驱动 + 签名 Pawn 模块），用户态 Go 程序通过 `PawnIO.dll` 调用。

## 2. 范围

**支持**：
- SMBus 控制器：Intel PCH（i801 家族，含 MMIO/IO 两种映射）、AMD FCH（KernCZ PIIX4，1022:790b，含多端口）、Intel Skylake-X/Cascade Lake IMC。
- SPD 世代：SDRAM/DDR 仅识别类型；DDR2/DDR3 基本信息解析；**DDR4/DDR5 完整字段解析**（容量、组织、时序、CRC16 校验/修复、XMP 2.0 / EXPO、厂商/日期/序列号/部件号）。
- EEPROM 操作：整片读、update/force 写、verify、分页（DDR4 SPA / DDR5 MR11）、RSWP 状态/设置/清除、PSWP 状态检测与设置（强确认）、写测试。
- JEDEC 厂商 ID 数据库（从原仓库 Resources.cs 提取，go:embed）。

**不做**：Nvidia/VIA 芯片组、GUI 之外的独立 CLI、Arduino 串口协议、固件导出、Linux/原生 macOS。

## 3. 架构

```
cmd/spdrw/main.go          Wails 入口
internal/app               前端绑定的服务层(状态机 + 事件)
internal/smbus             Transport 接口 + PawnIO 后端(Windows only) + Fake(测试)
internal/eeprom            SPD EEPROM 协议层(分页/读写/RSWP/PSWP),仅依赖 Transport
internal/spd               SPD 解析(纯函数): ddr4/ddr5/ddr3/ddr2/common/mfg
internal/spd/data          JEDEC 厂商表(嵌入)
frontend/dist              原生 HTML/JS/CSS(无 node 构建链)
third_party/pawnio/*.bin   PawnIO 官方签名模块(固定版本, go:embed)
```

核心接口：

```go
package smbus

type Transport interface {
    Identity() (Controller, error)          // 类型/IO基址/PCI ID
    Quick(addr byte, write bool) error      // 快速命令: 探测/SPA0/SPA1/CWP/SWP
    ReadByte(addr byte, cmd byte) (byte, error)     // SMBus BYTE_DATA 读
    WriteByte(addr byte, cmd byte, val byte) error  // SMBus BYTE_DATA 写
    WriteByteNoData(addr byte) error        // BYTE 协议写(PSWP 检测等)
    ReadWord(addr byte, cmd byte) (uint16, error)   // 备用
    Close() error
}
```

`eeprom`/`spd`/`app` 只依赖接口 → 可离线单测；PawnIO 后端为薄壳（封送/状态映射为纯函数可在 Linux 单测）。

## 4. PawnIO 后端

- 动态加载 `PawnIO.dll`（`PawnIO_Open/LoadModule/ModuleExecute/ModuleFree/Close`，以 PawnIO.h 为准）。
- 依次尝试加载模块：`SmbusI801` → `SmbusPIIX4` → `SmbusIntelSkylakeIMC`（go:embed 释放到临时目录后 LoadModule），成功即用；`ioctl_identity` 返回控制器类型（'i801'/'PIIX4'）+ IO 基址 + PCI ID。
- 事务统一走 `ioctl_smbus_xfer`：cells = `[]uint64{addr, readWrite, command, protocol, data...}`；
  protocol 常量与 Linux i2c-dev 一致（QUICK=0, BYTE=1, BYTE_DATA=2, WORD_DATA=3）。
- 每次事务前后获取/释放全局互斥体 `\BaseNamedObjects\Access_SMBUS.HTP.Method`（模块文档要求，与 Thaiphoon/OpenRGB 仲裁）。
- AMD 多端口：封装 `ioctl_piix4_port_sel`，扫描遍历端口 0/1。
- Intel `ioctl_write_protection` 读 BIOS SPD 写禁止位（对应原版 SpdWriteDisabled）→ UI 黄色警告条。
- DDR5 检测（原版逻辑）：probe `0x48|(addr&7)` PMIC 存在且 SPD byte0==0x51。
- NTSTATUS 映射为可读错误（NACK / busy / timeout / 未装 PawnIO / 模块验签失败 / 非管理员）。

## 5. EEPROM 协议层

- 分页：DDR4 = QUICK 写 0x36/0x37（JEDEC EE1004 标准；修正原版用 BYTE 协议的历史怪癖）；
  DDR5 = 写 MR11（页 0..15），NVM 访问地址 = `offset%128 | 0x80`。内部维护当前页，跨页自动切换。
- 大小：DDR5=1024、DDR4/LPDDR3/4=512、DDR3 及更早=256（byte2 分发表）。
- RSWP：
  - 状态：DDR5 读 MR12/MR13 位图；DDR4 及更早 = 块首字节写测试（原版同款）。
  - 设置：DDR5 置位 MR12/MR13；DDR4 QUICK 写 SWP 命令地址（block0..3 → 0x31/0x34/0x35/0x30）。
  - 清除：DDR4 QUICK 写 CWP=0x33；DDR5 写 0 到 MR12/MR13 并回读验证。
- PSWP：检测按原版（BYTE 无数据读 `0x30|(addr&7)`）；设置走 DDR4 永久保护命令，UI 强制输入 CONFIRM。
- 块数校验：DDR5=16、DDR4=4、更早=1。

## 6. SPD 解析层

- `spd.Identify(dump)` → RamType/SPD 大小/有效长度。
- DDR4/DDR5 完整结构体：容量（die 密度×rank×位宽）、模块组织、时序（tCKAVGmin/max、CL 掩码、tAA/tRCD/tRP/tRAS/tRC/tRFC1-4/tFAW/tRRD_S-L/tWR/tWTR，medium+fine 精度换算 ns）、CRC16 双区（DDR4 两段 0x1021 CCITT；DDR5 512 前后各一）校验与修复、XMP 2.0/EXPO profile、厂商/地点/日期(BCD)/序列号/部件号。
- DDR2/DDR3：类型、容量、组织、部件号、厂商、CRC。
- 厂商名：continuation code → 厂商名表（嵌入），查询忽略奇偶校验位。

## 7. UI（Wails v2 + 原生 HTML/JS/CSS）

布局：顶栏（控制器选择/扫描/设备选择/连接状态 + BIOS 写禁止警告条）；左 SPD 彩色 hex 网格（16 列，DDR5 分页指示）；右信息面板（解析字段 + 保护块勾选图）；底部操作区（读取/保存、写入 update/force + verify、RSWP 设置/清除、PSWP 永久保护红色按钮）+ 进度条 + 日志窗格。

交互：Go 服务层方法绑定 + Wails Events 推送进度/日志；安全交互（受保护写入需确认、PSWP 需输入 CONFIRM、非管理员引导提权、未装 PawnIO 引导下载页）。

## 8. 错误处理与安全

- 写前检查目标块保护状态；受保护拒绝并提示恢复路径。
- 所有写操作在 UI 明示总线/地址/范围；PSWP 二次确认。
- PawnIO 缺失/版本不符/模块验签失败 → 各自专门提示。
- NTSTATUS 与 Win32 错误统一映射成用户可读文案。

## 9. 测试与验证策略

- Linux 容器内：spd/eeprom/smbus 封送层表驱动单测（合成 fixture + 正确 CRC）；`go vet`。
- Windows 真机（需求方执行）：安装 PawnIO → list/scan/dump 读真实内存条 → 单字节写入回读 → RSWP 状态轮询 → （可选）RSWP set/clear。随 exe 附验证清单。

## 10. 构建

- `GOOS=windows GOARCH=amd64 go build`（Wails v2 资产 go:embed，无 CGO，容器内交叉编译）。
- 产物：`spdrw.exe`（-H windowsgui）+ README/验证清单。
