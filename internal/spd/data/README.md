# JEP106 厂商表(idcodes.json)

## 这是什么

`idcodes.json` 是 **JEP106 厂商识别码表**: 外层数组是"银行"(continuation count),
内层是厂商名, 下标 +1 即厂商码(忽略 MSB 奇校验位)。查询入口是 `spd.ManufacturerName(cont, code)`。

## 数据来源(参考项目已从工作区移除, 需要时按下面重新拉)

- 上游: https://github.com/1a2m3/SPD-Reader-Writer (GPL-3.0)
- 取用版本: commit `53d05bc15cd6d68aef187d083854e75874f13846` (2023-12-14)
- 原始形态: `src/SpdReaderWriterCore/Resources.cs` 里 `IdCodes` 是 14 个 gzip 块的十六进制字面量,
  解压后按 `0x0A` 切分成名字列表
- **取用的是数据, 不是代码**: 上游项目是 GPL-3.0, 而这张表是 JEDEC JEP106 标准的厂商名录
  (事实数据)。本项目只把这批名字提取成本仓库的 JSON, 并在此记录出处与取用 commit 以备核查;
  上游的 C# 代码一行都没有进入本仓库。

## 为什么要拆成 15 个银行(而不是原样的 14 个块)

原资源的**块号 ≠ JEP106 银行号**:

| 原始块 | 实际内容 | 本表银行 |
|---|---|---|
| 块 0 | 银行 0(126 条) | 0 |
| 块 1 | 银行 1(125 条) + 银行 2(126 条), 共 251 条 | 1 / 2 |
| 块 n (n≥2) | 银行 n+1 | n+1 |

按"块号 = 银行号"直接输出会让 **bank ≥ 1 的厂商全部查错或查不到**
(实测: G.Skill→"PLX Technology"、Corsair→"Chipcon AS"、Crucial→"Memory Corp NV")。
拆分对齐点是实测确定的: Corsair 在银行 2 的第 30 条(`0x9E`)才与真实 dump 吻合。

## 重新生成

```bash
# 1) 拉上游(任意临时目录)
git clone --depth 1 https://github.com/1a2m3/SPD-Reader-Writer /tmp/spd-rw

# 2) 提取(脚本就在本仓库)
export GOPATH=$PWD/.gopath GOMODCACHE=$PWD/.gopath/pkg/mod GOCACHE=$PWD/.gocache
go run ./tools/extract_idcodes /tmp/spd-rw/src/SpdReaderWriterCore/Resources.cs internal/spd/data/idcodes.json

# 3) 校验
go test ./internal/spd/ -run "TestIdcodesIntegrity|TestRealDumpManufacturerNames"
```

第 3 步会用 `testdata/spd/` 里 18 份**厂商已知的实物条**核对(不依赖本表), 表错即失败。
