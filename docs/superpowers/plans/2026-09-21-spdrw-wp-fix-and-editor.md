# SPD-RW Go — 写保护修复 + 写入加固 + SPD 编辑器 实施计划

> 承接 `2026-09-20-spdrw-go.md`。目标、范围、顺序已与需求方逐条确认（2026-09-21）。

**Goal:** 修好写保护状态查询；把写入路径加固到"可离线严格验证 + 真机 dry-run"的程度（真机写入放最后）；补强解析层；新增可视 SPD 编辑器（常用信息 / JEDEC 时序 / XMP 2.0 / XMP 3.0 / EXPO / 原始 hex；含 DDR2/DDR3）。

**Spec:** `docs/superpowers/specs/2026-09-20-spdrw-go-design.md`
**前置文档:** `docs/实现文档.md`、`docs/验证清单.md`

## Global Constraints

- `gofmt` / `go vet ./...` / `go test ./...` 全绿（Linux 容器内；测试需自定 `GOPATH/GOCACHE`）。
- 纯逻辑（解析、编辑、协议层、封送、状态映射）不得依赖 Windows。
- Wails v2 绑定方法**最多 2 个返回值**（3+ 静默返回 null，见 `internal/binding/boundMethod.go`）；多值一律打包成单结构体。
- Wails v2 参数经 `json.Unmarshal` 解码：`[]byte` 参数只能收 base64 字符串，**JS 数组必须用 `[]int`/`[]string`**。
- 编辑层纯函数、零 I/O：只改内存 `[]byte`，返回变更列表；写设备是独立动作。
- 危险操作双层防护：服务层预检 + UI 确认；写入前必须有备份与 diff 预览。
- 用户可见文案中文。

## 已确认的决策

1. 顺序：Phase 0 → 1 → 2 → 3 → 4 → 5 → 6；写入类改动全部最后，先 dry-run。
2. 编辑器入口：右侧信息面板新增"编辑器"标签页（推荐方案）。
3. **需要**原始 hex 任意字节编辑（高风险，需二次确认）。
4. **需要** DDR2/DDR3 编辑（部件号/序列号/日期/厂商等基本信息）。
5. 本机无真实 dump 样本 → 先在网上搜集并校验（`testdata/spd/` + `MANIFEST.md`）。

---

## Phase 0 准备

**Files:** `testdata/spd/**`、`internal/smbus/recording.go`、本计划文档

- [x] 分支 `feat/wp-fix-and-spd-editor`；计划落盘
- [x] 搜集真实 SPD dump（DDR4 带 XMP / DDR5 带 XMP3·EXPO / DDR2 / DDR3），逐个用 CRC16+类型码校验，写 `MANIFEST.md`（文件名/大小/类型/CRC/XMP·EXPO/来源 URL/许可证）
- [x] `RecordingTransport`：包装任意 `Transport`，按序记录全部事务，支持故障注入（第 N 次写失败、cmd≥X 的写 NACK）与零延时，供"半写/中止/写保护"类测试
- [x] 基线：`go test ./internal/...` 全绿提交

## Phase 1 修写保护状态查询

**Files:** `internal/eeprom/eeprom.go`(+`_test.go`)、`internal/app/app.go`(+`_test.go`)、`frontend/dist/app.js`、`frontend/dist/index.html`、`frontend/dist/style.css`

- [x] `eeprom.WPStatusDetail()` → 单结构体：`DDR5 / BlockSize / Blocks / Protected[] / Known[] / MR11 / MR12 / MR13 / MR48 / MR52 / ProtectionHit / Offline / PSWPApplicable / PSWP / Warnings[]`
- [x] DDR5 块 = 16×64B（MR12/MR13 位图，bit=1 为受保护，[FMSPD5118 9.3](https://www.fmsh.com/nvm/FMSPD5118_ds_eng.pdf)）；DDR4 = 4×128B；更早 = 1×128B
- [x] DDR4 写测试安全化：写→回读确认→还原→**回读确认还原**→失败重试 3 次→仍失败则报"状态未知"且绝不谎报 protected；返回 `Known[]`
- [x] DDR5 **PSWP 不适用**（修掉 0x30 NACK 假阳性）；DDR4 才检测 PSWP
- [x] MR52[6]（写受保护块被忽略的标志）作为只读诊断；RSWP 清除失败时文案指向"需离线模式/断电（MR12/13 正常模式不可清零）"
- [x] `App.WPStatus() (*WPStatusResult, error)`（单结构体，修 4 返回值 null）；`App.WPSet([]int)`（修 JS 数组解不进 `[]byte`）
- [x] 前端：块视图显示字节范围 `B0 0x000-0x03F 🔒`、PSWP/离线/MR 原始值、状态未知提示、"查询状态会写入 1 字节再还原"提示
- [x] 测试：DDR5 PSWP 不适用、DDR4 还原失败上报为未知、MR52 命中、`WPSet` 数字数组、DDR5/DDR4/DDR3 块数与块大小

## Phase 2 写入路径加固 + 严格离线验证

**Files:** `internal/eeprom/eeprom.go`、`internal/app/app.go`、`internal/app/write.go`(新)、`frontend/dist/*`、`docs/验证清单.md`

- [x] 长度严格校验（修 `dump[:size]` 切片越界 panic）
- [x] 写入计划：`Plan{Changes[]ByteChange, CRCFirst/CRCLast, Blocks}`；**CRC 字节最后写**（中断只会留下 CRC 不符，而非"看似有效却错"的 SPD）
- [x] 失败语义：任何写/校验失败立即中止，错误附"已写 N 字节 / 未写 M 字节 + 恢复建议"
- [x] `PreflightWrite`：大小/类型匹配、目标 dump CRC 必须有效、RSWP/PSWP 状态、高危字段（容量/组织/PMIC/电压/die 密度）变更清单
- [x] 写前自动备份到 `~/.spdrw/backups/spd-<addr>-<ts>.bin` 并回显路径
- [x] **Dry-run 模式**：写入走影子 Transport（一个字节都不上总线），输出"将写 N 字节 + 位置 + 前后 CRC"
- [x] UI 写入确认：diff 面板（偏移/旧值/新值/块/受影响 CRC）+ 勾选"已备份" + 输入 `WRITE`
- [x] 测试矩阵：update 只写差异、force 全量、受保护块拒绝且零部分写、第 N 次写失败中止、CRC 写序、dry-run 零写事务、短/长 dump、DDR4/DDR5 回读校验

## Phase 3 解析层补强

**Files:** `internal/spd/ddr5.go`、`ddr4.go`、`ddr23.go`、`edit.go`(新)、`profile.go`(新)、`_test.go`

- [x] 修 DDR5 XMP 3.0 槽位偏移（真实槽位 `0x2C0/0x300/0x340/0x380/0x3C0`，`0x280-0x2BF` 为 header）；补 header CRC（`0x280` 段 62B）与逐槽 CRC/启用位
- [x] DDR5 JEDEC 时序解析（byte 20-102：tCK min/max、CL 掩码、tAA/tRCD/tRP/tRAS/tRC/tWR、tRFC1/2/sb（单双 rank）、tRRD_L/tCCD_L/tCCD_L_WR/WR2/tFAW/tCCD_L_WTR/tCCD_S_WTR/tRTP + lower limit）并在信息面板展示
- [x] DDR4 时序补全（tWTR_S/L、tCCD_S、tRTP 等）与 XMP 2.0 全字段
- [x] 统一"可编辑块"模型（offset/长度/字段表/CRC 段/启用位/风险级），编辑与校验共用
- [x] 真实 dump 回归（Phase 0 样本）

## Phase 4 SPD 编辑器

**Files:** `internal/spd/edit*.go`、`internal/app/edit.go`、`frontend/dist/*`

- [x] `internal/spd/edit` 纯函数：位域写入原语 `setSubByteR/setBit`、16bit LE、BCD、ns↔medium/fine 编码器（含可表示性校验）
- [x] 常用信息（DDR4/DDR5/DDR3/DDR2）：厂商 JEP106（`FindManufacturer` 反查）、生产地点、日期、序列号、部件号、模块/SPD 版本、DRAM 厂商/stepping
- [x] JEDEC 时序：DDR4 byte18-42+fine117-125；DDR5 byte20-102
- [x] XMP 2.0（DDR4，384 起 2×63B）：启用位、版本、tCK/CL 掩码/tAA/tRCD/tRP/tRAS/tRC/tRFC1-4/tFAW/tRRD_S-L/电压；支持从参考 dump 导入整套 profile
- [x] XMP 3.0（DDR5，header+5 槽）：版本/启用位/3 个 profile 名/header CRC + 每 profile（VDD/VDDQ/VPP/VMEMCTRL、minCycleTime、CL 掩码、tAA…tRTP、command rate、CRC）
- [x] EXPO（DDR5，0x340 共 128B）：header + 2×40B profile + CRC；与 XMP 槽 3/User1 互斥
- [x] 增/改/删/清空扩展区；CRC 自动重算（DDR4 双段、DDR5 基础段+header+各 profile+EXPO）
- [x] 原始 hex 编辑（任意字节，高风险确认）
- [x] `EditSession` 服务层（原始副本+工作副本）：`EditLoad/EditApply/EditReset/EditDiff/EditPreview/EditSaveFile/EditWriteToDevice`
- [x] 前端编辑器标签页：分类表单（偏移+当前值+范围+单位+风险）、实时 diff 与 CRC 状态、导出 dump、写入设备
- [x] 测试：字段编码 golden、编辑后重解析、**可逆性**（改回原值 → 整片逐字节相同）、CRC、非法值拒绝、扩展区创建→解析→清空、EXPO 互斥、真实 dump 编辑后仍通过 `ValidateSpd`+CRC

## Phase 5 真机分阶段验证（最后）

- [ ] V1 只读：dump/解析/CRC/保护状态（确认 DDR5 PSWP 文案已修）
- [ ] V2 dry-run 写入：真机 dump 跑完整流程，断言总线零写事务
- [ ] V3 牺牲条：改序列号 1 字节 → 写 → 回读 → 复原
- [ ] V4 牺牲条：写 XMP/EXPO 修改 → BIOS 确认识别
- [ ] V5 RSWP set/clear（可选）

## Phase 6 文档与交付

- [x] 更新 `README.md` / `docs/实现文档.md` / `docs/验证清单.md`（写入分阶段验证章节）
- [x] 交叉编译 `bin/SPDReaderWriter.exe`
