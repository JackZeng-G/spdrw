# SPD Reader Writer (Go)

SPD-Reader-Writer 的 Go 复刻版: Windows 桌面工具, 通过 **PawnIO** 内核驱动直连芯片组 SMBus, 读写内存条 SPD 并解析。

原版: https://github.com/1a2m3/SPD-Reader-Writer (C# WinForms + Arduino/USB 或内核驱动 SMBus)。
本项目**去除 Arduino 串口通道**, 仅保留芯片组 SMBus 直连, 后端从 CPU-Z 驱动换成开源签名驱动 **PawnIO**(namazso, OpenRGB/LibreHardwareMonitor 同款)。

## 功能

- **控制器发现**: Intel PCH (I801) / AMD FCH (PIIX4, 双端口) / Intel Skylake-SP IMC (SKX, 双总线) — 与 OpenRGB 相同的 PawnIO 模块
- **SPD 扫描** 0x50-0x57, DDR4 (EE1004 SPA0/SPA1 快速命令分页) 与 DDR5 (MR11 寄存器分页) 自动识别
- **读取/保存/校验/写入** SPD dump; 写入默认增量模式(跳过相同字节)并回读校验; 支持 `-force` 全量
- **解析**:
  - DDR4/LPDDR3/4/4X: 模块类型、密度/ banks /行列、组织、位宽、容量、全部主时序(中+细粒度)、CAS 掩码、双段 CRC(含修复)、厂商/日期/序列号/部件号、**XMP 2.0 双 Profile**(时序换算+电压)
  - DDR5/LPDDR5(X): 按**原版同范围**解析(密度×2、组织、通道/位宽、容量、身份区、CRC 含 XMP 3.0/EXPO 段; 原版不含 DDR5 时序字段, 故不解析)
  - DDR2/DDR3: 基本信息(容量/组织/部件号/厂商/CRC/tCK)
- **写保护**:
  - RSWP 状态检测(DDR4 块写测试 / DDR5 MR12/MR13 位图)、RSWP 设置(按块)、RSWP 清除
  - PSWP **只检测不支持设置** —— 永久写保护需要高压编程器, 原版 SMBus 路径同样仅检测
  - 显示 BIOS "SPD write disable" 状态 (I801)

## 构建

需 Go 1.21+ (开发用 1.24)。Windows 上:

```powershell
go build -ldflags="-H windowsgui" -trimpath -o SPDReaderWriter.exe
```

或从 Linux/macOS 交叉编译 (无 CGO):

```bash
GOOS=windows GOARCH=amd64 go build -ldflags="-H windowsgui" -trimpath -o SPDReaderWriter.exe
```

测试:

```bash
go test ./...
```

## 运行要求

1. **Windows 10/11 x64**
2. **管理员身份**运行 (SMBus 内核访问)
3. 安装 **PawnIO**: https://pawnio.eu/ (开源签名驱动, 不在 MS 恒定封禁名单; 缺失时界面会提示下载)

> ⚠️ **写 SPD 有变砖风险**: 错误内容(尤其 DDR5 PMIC/内存相关字段)可能导致无法开机。修改前务必备份 dump, 并确认校验通过。BIOS 设置里若开启了 "SPD Write Disable", 需先关闭才能写入。

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
