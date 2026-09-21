# SPD Reader Writer (Go)

<img src="build/icon/appicon.png" alt="图标" width="96" align="right">

用 Go 写的 SPD 读写工具: Windows 桌面程序, 通过 **PawnIO** 内核驱动直连芯片组 SMBus, 读取/解析/编辑/写入内存条 SPD。

本项目的范围取舍: 只走芯片组 SMBus; 内核访问用开源签名驱动
**PawnIO**(namazso, OpenRGB/LibreHardwareMonitor 同款), 不用 CPU-Z 驱动。

**当前版本 v1.0.0**(第一个正式版): 全链路(读取/解析/编辑器/干跑/写入+CRC/回滚)已在
AMD 7840HS 笔记本与 7735HS 小主机(均 DDR5 SO-DIMM)真机实测通过; DDR4 平台实测进行中,
平台矩阵见 [docs/真机实测记录.md](docs/真机实测记录.md)。支持 DDR3/DDR4/DDR5, **不支持 DDR2**。

## 功能

- **等待模式(读速的真正瓶颈)**: PawnIO 模块把每次事务的等待交给 Windows 线程休眠时,
  一次等待就是一个时钟中断(约 15.6ms)→ 每次事务固定 ~31ms、整片 1024B 要 16 秒。
  本工具默认用**忙等**(µs 精确), 并把模块的 SMBus 时钟读出来显示(真机实测 392.9 kHz);
  1024B 整片读取因此从 16.5 秒降到**约 0.3 秒**。等待模式不需要手动开关:
  自动选忙等, 出错时自动降级为"轮询忙等+长等待休眠"。
- **读取加速(三级自适应)**: **SMBus Block Read**(协议 5, 32 字节/事务) → **Word Read**(2 字节/事务) → 逐字节。
  每一级都先只读探测、失败即降级(AMD FCH 实测会被 HUB 拒绝块读, 于是自动落到字读:
  真机 1024B = 512 次事务 ≈ 0.32 秒)。也没有手动开关; 日志与"SPD 信息"里的读取方式
  那一行会写明实际档位、事务数、耗时与回退原因。
- **控制器发现**: Intel PCH (I801) / AMD FCH (PIIX4, 双端口) / Intel Skylake-SP IMC (SKX, 双总线) — 与 OpenRGB 相同的 PawnIO 模块
- **SPD 扫描** 0x50-0x57, DDR4 (EE1004 SPA0/SPA1 快速命令分页) 与 DDR5 (MR11 寄存器分页) 自动识别
- **读取/保存/校验/写入** SPD dump; 写入默认**增量模式**(只写与设备不同的字节)并回读校验,
  需要修复时可强制全量写入(写入入口的 `force` 参数)
- **解析**:
  - DDR4/LPDDR3/4/4X: 模块类型、密度/ banks /行列、组织、位宽、容量、全部主时序(中+细粒度)、CAS 掩码、双段 CRC(含修复)、厂商/日期/序列号/部件号、**XMP 2.0 双 Profile**(时序换算+电压)
  - DDR5/LPDDR5(X): 密度/组织/通道/位宽/容量、身份区、**完整 JEDEC 时序**(byte 20-102, ps/ns + lower limit)、CRC(**基础段 + XMP 3.0 header/各槽 + EXPO**)、按规范修正的 XMP 3.0 槽位(0x2C0..0x3C0)
  - DDR3: 容量/组织/部件号/厂商/日期/序列号/校验
  - **不支持 DDR2**(太老, 也找不到任何真实 dump 用于验证; 2026-09-21 起移除, byte2=0x08-0x0A 按"未知类型"拒绝解析与写入)
- **SPD 编辑器**(内存中编辑, 不落盘不写设备):
  - 常用信息: 厂商(JEP106 反查/搜索)、厂商码、生产地点、日期、序列号、部件号、修订码、DRAM 厂商/stepping
  - JEDEC 时序: DDR4 medium+fine、DDR5 16bit(ps/ns, 含 lower limit)、DDR3 MTB/FTB
  - 扩展信息: **XMP 2.0**(DDR4, 2×63B)、**XMP 3.0**(DDR5, header + 5 槽)、**EXPO**(DDR5, 与 XMP 槽 3/User1 互斥) —— 可创建/修改/清除, CRC 自动重算
  - hex 视图内点格子就地编辑任意字节(带列头 00-0F); **实时校验状态**与**重算 CRC**都在标题行;
    界面说明收进"?"问号里, 点一下才展开
  - 字段两列网格, 默认只显示常用字段(JEDEC 时序的关键项/第 1 份 profile), 可"显示全部字段"
  - **读一次即自动载入编辑器**(无需"从设备载入");文件操作(打开 dump/另存为/校验文件)在顶栏右侧;
  - 写保护整块移到编辑器页; **写入前自动备份、写完自动三层校验 + 独立复核**(无需手点);
  - 控制器列表只列出**探测到 SPD 的**控制器(AMD FCH 的 5 个端口里只有实际接条的那个有意义)
- hex 视图有**两套正交的视觉信号**(说明都收在 hex 标题的"?"里):
  **区段底色** = 参与校验(蓝) / 不参与校验(绿) / 身份信息(紫) / JEDEC 时序(青) / XMP-EXPO(黄);
  **改动标记** = 红底(影响校验, 须重算 CRC) / 蓝底(序列号、日期等不影响校验) / 黄框(CRC 值本身)
- **写入护栏**(写入是唯一可能变砖的操作):
  - 写前预检: 长度/类型匹配、目标 dump CRC 必须有效、受保护块检测、高危字段(容量/组织/电压/PMIC)清单
  - 自动备份当前整片到**程序同级**的 `backup\` 目录(自动创建); 确认串 `WRITE` 才真正执行
  - 写入计划按字节 diff, **CRC 字节最后写**(中断只会留下 CRC 不符的 SPD, 而不是"看着有效但内容错")
  - 中途失败给出"已写/未写字节数 + 恢复建议"; 逐字节回读校验
  - **干跑**: 完整跑预检/计划/校验, 一个字节都不上总线(界面上不再单独放开关, 由写入入口的参数决定;
    日志会给出"读 / 页选择与命令写 / 字节写 / 其中 NVM 数据写"这一行 —— 真机验证"干跑没碰 SPD"
    就看它, 实测为"对 SPD NVM 的数据写 0 次")
  - **写入能力探测**: 先在保留字节上做一次"写 → 读回 → 还原", 证明这个平台真的能写, 再动真正的字段
- **写保护**:
  - RSWP 状态检测: DDR5 读 MR12/MR13 位图(16×64B)、DDR4 及更早块首写测试(4×128B, **还原后回读确认**, 失败重试并报"状态未知", 绝不谎报)
  - RSWP 设置(按块)/清除; DDR5 附 MR11/MR29/MR48/MR52 原始值与"写受保护块被忽略"标志
  - PSWP: 仅 DDR3 可经 PWPB(0110b)探测; DDR4/DDR5 无该设备类型, 明确显示"不适用"(旧版会误报"永久保护已生效")
  - 显示 BIOS "SPD write disable" 状态 (I801)

## 构建

需 Go 1.27+ (`go.mod` 的 toolchain 版本; 逻辑层不用 CGO)。
**必须带 `desktop,production` 标签**, 否则启动时报
"Wails applications will not build without the correct build tags"(这是 Wails 的运行时保护, 不是代码问题)。

产物统一放在 **`build/`**(exe 本身不入库, 见 `.gitignore`)。

```bash
./build.sh          # Linux/macOS 上交叉编译(无 CGO)
```
```powershell
.\build.ps1          # Windows 上本地构建(等价)
```

两条路做的都是(把提交号打进二进制, 日志里能看到在跑哪一版):

```bash
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -tags desktop,production -trimpath \
  -ldflags="-H windowsgui -s -w -X spdrw/internal/app.BuildHash=$(git rev-parse --short HEAD)" \
  -o build/SPDReaderWriter.exe .
```

### 图标与版本信息

exe 的**图标**和**属性里的版本/版权**(`by jackzeng 2026`)来自仓库根目录的
`rsrc_windows_amd64.syso`: Go 会自动把它链进 windows/amd64 产物, Linux 构建时忽略。
它由 `build/winres/winres.json` + `build/icon/` 里的图标生成 —— 改图标或改版权信息后按
[`build/winres/README.md`](build/winres/README.md) 重新生成(三步命令都在那里)。
图标本身是 `go run ./tools/makeicon` 用纯标准库画出来的, 不依赖 ImageMagick/PIL。

### 测试

```bash
export GOPATH=$PWD/.gopath GOMODCACHE=$PWD/.gopath/pkg/mod GOCACHE=$PWD/.gocache   # 容器内需自定
go test ./internal/...                          # 149 个顶层用例
CGO_ENABLED=1 go test -race ./internal/...      # 并发/死锁问题只有 race 抓得到
node --test "frontend/test/*.test.mjs"          # 前端契约与漂移守卫(39 条)
go test ./internal/app/ -run TestBuiltExe -v    # 产物自检(需先构建: 前端/PawnIO/图标/版本信息)
```

真实浏览器冒烟: `node frontend/test/make-real-page.mjs` 后打开 `frontend/test/_real-page.html?v=<时间戳>`,
读 `window.__SMOKE`(全部 ok)与 `window.__ERRORS`(应为空)。

## 运行要求

1. **Windows 10/11 x64**
2. **管理员权限**(SMBus 内核访问)。exe 的清单里写了 `requireAdministrator`, 所以
   **双击即自动弹出 UAC 请求提权**, 不需要右键"以管理员身份运行"; 拒绝提权则无法启动。
   想免掉每次弹窗: 用「任务计划程序」建一个"使用最高权限运行"的任务, 或调低 UAC 通知级别
   (后者会同时降低整机安全性, 不推荐)
3. 安装 **PawnIO**: https://pawnio.eu/ (开源签名驱动, 不在 MS 恒定封禁名单; 缺失时界面会提示下载)

> ⚠️ **写 SPD 有变砖风险**: 错误内容(尤其 DDR5 PMIC/内存相关字段)可能导致无法开机。修改前务必备份 dump, 并确认校验通过。BIOS 设置里若开启了 "SPD Write Disable", 需先关闭才能写入。

## 仓库布局说明

本仓库**自带全部可复现材料**, 不含任何外部项目的代码:

- 第三方二进制(PawnIO 模块与 DLL)出处与许可证见 `third_party/pawnio/README.md`。
- 真实 dump 语料(67 份)与其来源清单见 `testdata/spd/MANIFEST.md`。
- `build/` **不全是构建产物**: `build/icon/`(图标源)与 `build/winres/`(Windows 资源与版本信息
  的配置)是要入库的源文件, 只有 exe 被 `.gitignore` 忽略。
- `.gitattributes` 固定了行尾(文本 LF、`.ps1` CRLF)并把 exe/dll/syso/ico/png 标记为二进制, 免得
  Windows 与 Linux 之间来回检出时出现整文件 diff。

## 文档

- **[docs/实现文档.md](docs/实现文档.md)** — 实现细节:分层架构、SMBus/DDR4/DDR5 协议语义、Wails 绑定要点、测试体系、真机调试经验(踩坑实录)
- **[docs/验证清单.md](docs/验证清单.md)** — 真机逐项验证步骤
- **[docs/离线验证报告.md](docs/离线验证报告.md)** — 上线前我们到底验过什么、结论是什么、还剩什么必须真机确认(含可重跑命令与实测数字)
- **[docs/写入操作手册.md](docs/写入操作手册.md)** — 真机写入的照做顺序(预检→首写→校验)、失败处理与恢复路径
- **[docs/真机实测记录.md](docs/真机实测记录.md)** — 真机回帖的原始证据(读取档位/事务数/耗时、编辑器、还差哪些验证)
- **[docs/superpowers/specs/](docs/superpowers/specs/)** — 设计文档(需求与范围)

## 架构

```
frontend/dist        原生 HTML/JS/CSS (无 node 构建链)
main.go              Wails v2 装配(事件桥/深色窗口)
build/icon           图标源(go run ./tools/makeicon 生成: png/ico/各尺寸 png)
build/winres         Windows 资源配置(图标 + 版本信息 + 版权) → rsrc_windows_amd64.syso
tools/makeicon       纯标准库图标生成器(4 倍超采样; 16/24 用简化版)
internal/app         GUI 服务层: 连接/扫描/读写/保护/解析编排
internal/spd         SPD 解析: DDR4 全量 / DDR5 / DDR3 基本信息 + JEP106 厂商表
internal/eeprom      设备语义: 分页(EE1004 quick / DDR5 MR11)、RSWP/PSWP、增量写+校验
internal/smbus       Transport 接口 + PawnIO 后端(i801/PIIX4×2/SKX×2) + 内存 Fake(测试)
internal/assets      内嵌 PawnIO 模块(SmbusI801/SmbusPIIX4/SmbusIntelSkylakeIMC .bin)与 PawnIOLib.dll
third_party/pawnio   模块与 DLL 的来源与许可证说明(LGPL-2.1)
```

每次 SMBus 事务持有全局互斥 `Global\Access_SMBUS.HTP.Method`(与 Thaiphoon 等工具仲裁); 每个(控制器×总线)一个 PawnIO 会话, 会话开启 AlwaysSleep 模式。

## 许可证

- 本项目代码: MIT, 版权 `by jackzeng 2026`(界面顶栏与 exe 属性里都会显示)
- PawnIO 模块与 PawnIOLib.dll: LGPL-2.1 (见 `third_party/pawnio/`)
- JEP106 厂商识别表: 从上述参考项目的 gzip 资源里提取的**数据**(非代码), 存为
  `internal/spd/data/idcodes.json`。表本身是 JEDEC JEP106 标准的厂商名录(事实数据), 上游项目
  为 GPL-3.0, 本项目只取用这份数据并保留了出处与取用 commit —— 详见 `internal/spd/data/README.md`
