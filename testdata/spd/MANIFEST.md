# SPD 真实 dump 测试样本清单

本目录存放**从互联网公开仓库下载的真实内存条 SPD dump**（非表格、非截图、非人工编造），供 `spdrw` 的 SPD 读写工具做回归测试。

- 生成时间：2026-09-20 05:19（UTC）
- 样本总数：**72 个文件**（67 个唯一内容 + 5 个内容重复的异源副本）
- 分类：DDR5 1024 B × 19；DDR4 512 B × 21；DDR3 256 B × 32
- 负样本：`corrupt/` 目录 9 个
- 校验脚本：[`tools/spdcheck.py`](tools/spdcheck.py)（长度 / 类型码 / CRC16-CCITT / XMP / EXPO 判定）

**来源可信度已验证**：清单里每个文件的 raw 源 URL 都重新拉取比对过一遍 —— 
44 个是字节级完全一致（直接下载的二进制），32 个是「文本 hexdump → 解析后字节完全一致」；0 个不一致、0 个失效链接。

校验算法（与题目给定一致）：CRC-16/CCITT（XMODEM 变体），多项式 `0x1021`，初值 `0`，不反射；
逐字节 `crc ^= b << 8`，再移位 8 次。自检向量：`crc16(b"123456789") == 0x31C3`。

---

## ⚠️ 关于 DDR3 CRC 覆盖率：题目描述与真实数据不一致

任务描述里写的是「DDR2/DDR3（256B）：`bytes[0:126]` 的 CRC 应等于 `bytes[126] | bytes[127]<<8`」。
**但所有真实 DDR3 dump 都不满足这条规则。** 本次收集到的每个 DDR3 样本（6 家不同厂商、SPD rev 1.0/1.1/1.2/1.3）都满足 JEDEC 规则：

| 项目 | 规则 |
|---|---|
| CRC 存放位置 | **始终**在 `bytes[126..127]`（小端：`d[126] | d[127]<<8`） |
| CRC 覆盖范围 | 由 **byte 0 的 bit 7** 决定：`bit7=1` → `bytes[0:117]`；`bit7=0` → `bytes[0:126]` |

几乎所有实物 DDR3 DIMM 的 byte 0 都是 `0x92` / `0x93`（bit 7 = 1），因此覆盖率是 **0..116（共 117 字节）**，不是 0..125。
独立佐证：[rigred/spd_tool 的 `ddr3_decoder.py`](https://github.com/rigred/spd_tool/blob/main/ddr3_decoder.py#L829) 就是这么实现的——
`end = 125 if (self.data[0] & 0x80) == 0 else 116`。

> **实测结论（跑过本仓库的 `TestRealDumpCorpus`）**：
> `spdrw` 现有的 `CRCOK()` **已经**按 JEDEC / byte0-bit7 规则实现，对本目录 67 份语料
> **CRC 通过 67 / 不通过 0**，`go test ./internal/spd/ -run TestRealDumpCorpus -v` → **PASS**。
> 也就是说：这条差异只是**任务描述文字**与真实数据不符，`spdrw` 的代码本身是对的。
> 若日后按任务原文把 DDR3 改成固定 `bytes[0:126]`，反而会让全部 32 份 DDR3 语料误判为 CRC 失败——请不要这么改。

DDR4 / DDR5 的段划分与任务描述一致（DDR4 两段 `0:126`@126 与 `128:254`@254；DDR5 一段 `0:510`@510），实测全部通过。

---

## DDR5（1024 字节，优先项 2）

要求：`bytes[640..641] == 0C 4A`（XMP 3.0 header），`bytes[832..835] == "EXPO"`。下表「扩展区」列即按此判定。

| 文件名 | 字节 | 类型码 | 基础 CRC | 扩展区 | 来源（raw URL / 仓库路径） | 许可证 / 来源说明 |
|---|---|---|---|---|---|---|
| `ddr5-corsair-cmk32gx5m2b5600z40-cityson.bin` | 1024 | DDR5 (0x12) | ✅ | **XMP 3.0** + **EXPO** | https://raw.githubusercontent.com/cityson7/SPD-Reader-Writer-DDR5/master/SPD_Data/CMK32GX5M2B5600Z40%28VENG_5600-40-40-40-77%201.25V%29.bin | 未找到 LICENSE 文件，来源不明，仅供本地测试 |
| `ddr5-crucial-ct16g56c46u5-cityson.bin` | 1024 | DDR5 (0x12) | ✅ | **XMP 3.0** + **EXPO** | https://raw.githubusercontent.com/cityson7/SPD-Reader-Writer-DDR5/master/SPD_Data/CT16G56C46U5.M8G1%28Crucial_5600-CL46%201.1V%29.bin | 未找到 LICENSE 文件，来源不明，仅供本地测试 |
| `ddr5-geil-d5-8000-cl38-cityson.bin` | 1024 | DDR5 (0x12) | ✅ | **XMP 3.0** + **EXPO** | https://raw.githubusercontent.com/cityson7/SPD-Reader-Writer-DDR5/master/SPD_Data/CL38-48-48%20D5-8000%28GeIL_64000-CL38-48-48-100%201.45V%29.bin | 未找到 LICENSE 文件，来源不明，仅供本地测试 |
| `ddr5-gskill-f5-6000j3636f16g-cityson.bin` | 1024 | DDR5 (0x12) | ✅ | **EXPO** | https://raw.githubusercontent.com/cityson7/SPD-Reader-Writer-DDR5/master/SPD_Data/F5-6000J3636F16G%28Z5%20GSKill_6000-CL36-36-36-96%201.35V%29.bin | 未找到 LICENSE 文件，来源不明，仅供本地测试 |
| `ddr5-jedec-only-5600-sample-edlf.spd` | 1024 | DDR5 (0x12) | ✅ | — | https://raw.githubusercontent.com/edlf/DDR5SPDEditor/main/SPD%20examples/Sample%20DDR5%205600.spd | GPL-3.0（仓库 LICENSE） |
| `ddr5-klevv-kd58gu880-cityson.bin` | 1024 | DDR5 (0x12) | ✅ | — | https://raw.githubusercontent.com/cityson7/SPD-Reader-Writer-DDR5/master/SPD_Data/KD58GU880-56G4600%28KLEVV_5600-CL46-46-46%201.1V%29.bin | 未找到 LICENSE 文件，来源不明，仅供本地测试 |
| `ddr5-oloy-d5u0852382b-k69-djchumpguy.spd` | 1024 | DDR5 (0x12) | ✅ | **XMP 3.0** + **EXPO** | https://raw.githubusercontent.com/djchumpguy/ddr5-spd-diagnostic/main/docs/examples/dumps/oloy-d5u0852382b-k69-original-spd-1024-raw.txt | 仓库 LICENSE 写明"尚未选定最终许可证"，来源不明，仅供本地测试；**由文本 hexdump 转换** |
| `ddr5-samsung-m323r1gb4pb0-cityson.bin` | 1024 | DDR5 (0x12) | ✅ | — | https://raw.githubusercontent.com/cityson7/SPD-Reader-Writer-DDR5/master/SPD_Data/M323R1GB4PB0-CWMOL.bin | 未找到 LICENSE 文件，来源不明，仅供本地测试 |
| `ddr5-samsung-m323r2ga3db0-cityson.bin` | 1024 | DDR5 (0x12) | ✅ | — | https://raw.githubusercontent.com/cityson7/SPD-Reader-Writer-DDR5/master/SPD_Data/M323R2GA3DB0-CWMOD.bin | 未找到 LICENSE 文件，来源不明，仅供本地测试 |
| `ddr5-samsung-m425r1gb4bb0-cqkod-coreboot.bin` | 1024 | DDR5 (0x12) | ✅ | — | https://raw.githubusercontent.com/coreboot/coreboot/main/src/mainboard/system76/mtl/spd/samsung-M425R1GB4BB0-CQKOD.spd.hex | GPL-2.0（coreboot 仓库 LICENSE）；SPD 数据本身为厂商公开的 JEDEC 数据；**由文本 hexdump 转换** |
| `ddr5-samsung-m425r1gb4pb0-cwmod-coreboot.bin` | 1024 | DDR5 (0x12) | ✅ | — | https://raw.githubusercontent.com/coreboot/coreboot/main/src/mainboard/system76/mtl/spd/samsung-M425R1GB4PB0-CWMOD.spd.hex | GPL-2.0（coreboot 仓库 LICENSE）；SPD 数据本身为厂商公开的 JEDEC 数据；**由文本 hexdump 转换** |
| `ddr5-teamgroup-ud5-6000-0104eef6-ubihazard.spd` | 1024 | DDR5 (0x12) | ✅ | **XMP 3.0** + **EXPO** | https://raw.githubusercontent.com/ubihazard/ddr5-spd-recovery/main/dumps/teamgroup/t-create-expert_6000_38-38-38-78_1.25_1x8_16x2_%5Bctced532g6000hc38adc01%5D/ud5-6000_0104eef6.spd | MIT（LICENSE.md）<br>⚠️ 与 `ddr5-teamgroup-ud5-6000-omi.spd` 字节完全相同（同一 dump 的另一来源） |
| `ddr5-teamgroup-ud5-6000-0104eeff-ubihazard.spd` | 1024 | DDR5 (0x12) | ✅ | **XMP 3.0** + **EXPO** | https://raw.githubusercontent.com/ubihazard/ddr5-spd-recovery/main/dumps/teamgroup/t-create-expert_6000_38-38-38-78_1.25_1x8_16x2_%5Bctced532g6000hc38adc01%5D/ud5-6000_0104eeff.spd | MIT（LICENSE.md） |
| `ddr5-teamgroup-ud5-6000-omi.spd` | 1024 | DDR5 (0x12) | ✅ | **XMP 3.0** + **EXPO** | https://raw.githubusercontent.com/The-Open-Memory-Initiative-OMI/spdr/main/spdr/tests/fixtures/teamgroup-ud5-6000_0104eef6.spd | Apache-2.0（仓库 LICENSE） |
| `ddr5-tforce-ud5-6000-cityson.bin` | 1024 | DDR5 (0x12) | ✅ | **XMP 3.0** + **EXPO** | https://raw.githubusercontent.com/cityson7/SPD-Reader-Writer-DDR5/master/SPD_Data/UD5-6000%28T.FORCE_6000-CL38-38-38-78%201.25V%29.bin | 未找到 LICENSE 文件，来源不明，仅供本地测试 |
| `ddr5-tforce-ud5-8000-cityson.bin` | 1024 | DDR5 (0x12) | ✅ | **XMP 3.0** | https://raw.githubusercontent.com/cityson7/SPD-Reader-Writer-DDR5/master/SPD_Data/UD5-6000%28T.FORCE_8000-CL38-48-48-84%201.45V%29.bin | 未找到 LICENSE 文件，来源不明，仅供本地测试 |
| `ddr5-xmp3-expo-hybrid-sample-edlf.spd` | 1024 | DDR5 (0x12) | ✅ | **XMP 3.0** + **EXPO** | https://raw.githubusercontent.com/edlf/DDR5SPDEditor/main/SPD%20examples/Sample%20DDR5%205600%20-%20EXPO%20XMP%20Hybrid.spd | GPL-3.0（仓库 LICENSE） |
| `ddr5-xmp3-expo-sample-edlf.spd` | 1024 | DDR5 (0x12) | ✅ | **XMP 3.0** + **EXPO** | https://raw.githubusercontent.com/edlf/DDR5SPDEditor/main/SPD%20examples/DDR5%20XMP+EXPO.spd | GPL-3.0（仓库 LICENSE）<br>⚠️ 与 `ddr5-teamgroup-ud5-6000-omi.spd` 字节完全相同（同一 dump 的另一来源） |
| `ddr5-xmp3-only-sample-edlf.spd` | 1024 | DDR5 (0x12) | ✅ | **XMP 3.0** | https://raw.githubusercontent.com/edlf/DDR5SPDEditor/main/SPD%20examples/Sample%20DDR5%205600%20-%20XMP%20Only.spd | GPL-3.0（仓库 LICENSE） |

**补充：XMP 3.0 各 0x40 槽的 slot-CRC**（判定方式取自 [edlf/DDR5SPDEditor](https://github.com/edlf/DDR5SPDEditor/blob/master/xmp3.cpp)：
header 位于 640，其后每个 profile 占 `0x40` 字节，槽内 CRC = `crc16(slot[0:0x3E])` 与 `slot[0x3E..0x3F]` 比较）：

| 文件 | XMP3 header | Profile 1 | Profile 2 | Profile 3 | User1 | User2 |
|---|---|---|---|---|---|---|
| `ddr5-corsair-cmk32gx5m2b5600z40-cityson.bin` | ✅ | ✅ | ✅ | 空槽 | ✗ | ✅ |
| `ddr5-crucial-ct16g56c46u5-cityson.bin` | ✅ | ✅ | ✅ | 空槽 | ✗ | ✅ |
| `ddr5-geil-d5-8000-cl38-cityson.bin` | ✅ | ✅ | ✅ | 空槽 | ✗ | ✅ |
| `ddr5-gskill-f5-6000j3636f16g-cityson.bin` | 无 XMP3 header | — | — | — | — | — |
| `ddr5-klevv-kd58gu880-cityson.bin` | 无 XMP3 header | — | — | — | — | — |
| `ddr5-samsung-m323r1gb4pb0/-m323r2ga3db0-cityson.bin` | 无 XMP3 header | — | — | — | — | — |
| `ddr5-tforce-ud5-6000-cityson.bin` | ✅ | ✅ | ✅ | ✗ | ✗ | ✗ |
| `ddr5-tforce-ud5-8000-cityson.bin` | ✅ | ✅ | ✅ | ✅ | ✅ | ✗ |
| `ddr5-teamgroup-ud5-6000-omi.spd` | ✅ | ✅ | ✅ | ✗ | ✗ | ✗ |
| `ddr5-teamgroup-ud5-6000-0104eef6-ubihazard.spd` | ✅ | ✅ | ✅ | ✗ | ✗ | ✗ |
| `ddr5-teamgroup-ud5-6000-0104eeff-ubihazard.spd` | ✅ | ✅ | ✅ | ✗ | ✗ | ✗ |
| `ddr5-oloy-d5u0852382b-k69-djchumpguy.spd` | ✅ | ✅ | ✅ | 空槽 | ✗ | ✅ |
| `ddr5-xmp3-only-sample-edlf.spd` | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ |
| `ddr5-xmp3-expo-sample-edlf.spd` | ✅ | ✅ | ✅ | ✗ | ✗ | ✗ |
| `ddr5-xmp3-expo-hybrid-sample-edlf.spd` | ✅ | ✅ | ✅ | 空槽（EXPO 区） | ✗ | ✅ |
| `ddr5-jedec-only-5600-sample-edlf.spd` | 无 XMP3 header | — | — | — | — | — |

说明：**所有文件的 JEDEC 基础段 CRC（`bytes[0:510]` @ `510..511`）都通过**。「空槽」指该 0x40 槽全为 `0x00`，
其 slot-CRC 自然不等于 0，不表示数据损坏。真正值得注意的两类是：

1. `ddr5-teamgroup-ud5-6000-omi.spd` 等 TEAMGROUP 实物 dump 的 **User2 槽**有非零内容但槽内 CRC 为 `0x0001`，
   与重算值不符——这正是上级 MANIFEST 里提到的 `DDR5 XMP+EXPO.spd`「User2 槽 CRC 与实际内容不符」，两者是同一份数据。
2. `ddr5-tforce-ud5-6000-cityson.bin` 的 Profile 3 / User1 / User2 槽同样不匹配。
   这些文件**基础 CRC 良好**，适合做「XMP3 槽 CRC 校验 + 修复（fixCRC）」的正向测试输入。

---

## DDR4（512 字节，优先项 1）

要求：`bytes[384..385] == 0C 4A`（XMP 2.0 header）。

| 文件名 | 字节 | 类型码 | 基础 CRC | 扩展区 | 来源（raw URL / 仓库路径） | 许可证 / 来源说明 |
|---|---|---|---|---|---|---|
| `ddr4-16g_3200-coreboot.bin` | 512 | DDR4 (0x0C) | ✅ | — | https://raw.githubusercontent.com/coreboot/coreboot/main/src/mainboard/google/hatch/spd/16G_3200.spd.hex | GPL-2.0（coreboot 仓库 LICENSE）；SPD 数据本身为厂商公开的 JEDEC 数据；**由文本 hexdump 转换** |
| `ddr4-8g_2400-coreboot.bin` | 512 | DDR4 (0x0C) | ✅ | — | https://raw.githubusercontent.com/coreboot/coreboot/main/src/mainboard/google/hatch/spd/8G_2400.spd.hex | GPL-2.0（coreboot 仓库 LICENSE）；SPD 数据本身为厂商公开的 JEDEC 数据；**由文本 hexdump 转换** |
| `ddr4-8g_3200-coreboot.bin` | 512 | DDR4 (0x0C) | ✅ | — | https://raw.githubusercontent.com/coreboot/coreboot/main/src/mainboard/google/hatch/spd/8G_3200.spd.hex | GPL-2.0（coreboot 仓库 LICENSE）；SPD 数据本身为厂商公开的 JEDEC 数据；**由文本 hexdump 转换** |
| `ddr4-gskill-flarex-3200-2x8g-samsungb-eloaders.spd` | 512 | DDR4 (0x0C) | ✅ | **XMP 2.0** | https://raw.githubusercontent.com/eloaders/i-nex-ram-spd/master/DDR4/G.Skill%20FlareX%202%C3%97%208%20GB%20DDR-3200%20(Samsung%20B).SPD | 未找到 LICENSE 文件，来源不明，仅供本地测试 |
| `ddr4-hynix-h5anag6namr-uh-coreboot.bin` | 512 | DDR4 (0x0C) | ✅ | — | https://raw.githubusercontent.com/coreboot/coreboot/main/src/mainboard/google/kahlee/spd/hynix-H5ANAG6NAMR-UH.spd.hex | GPL-2.0（coreboot 仓库 LICENSE）；SPD 数据本身为厂商公开的 JEDEC 数据；**由文本 hexdump 转换** |
| `ddr4-hynix-h5anag6namr-uh-xabar.bin` | 512 | DDR4 (0x0C) | ✅ | — | https://raw.githubusercontent.com/xabar/spd_tool_ddr4/master/hynix-H5ANAG6NAMR-UH.spd.hex | 未找到 LICENSE 文件，来源不明，仅供本地测试；**由文本 hexdump 转换** |
| `ddr4-hynix-hma42gr7mfr4n-lvusyy.bin` | 512 | DDR4 (0x0C) | ✅ | — | https://raw.githubusercontent.com/lvusyy/SPDStudio/master/samples/DDR4_Hynix_HMA42GR7MFR4N.bin | 未找到 LICENSE 文件，来源不明，仅供本地测试 |
| `ddr4-hynix_dimm_h5an4g6nafr-uhc-coreboot.bin` | 512 | DDR4 (0x0C) | ✅ | — | https://raw.githubusercontent.com/coreboot/coreboot/main/src/mainboard/google/poppy/spd/hynix_dimm_H5AN4G6NAFR-UHC.spd.hex | GPL-2.0（coreboot 仓库 LICENSE）；SPD 数据本身为厂商公开的 JEDEC 数据；**由文本 hexdump 转换** |
| `ddr4-hynix_dimm_h5an8g6ncjr-vkc-coreboot.bin` | 512 | DDR4 (0x0C) | ✅ | — | https://raw.githubusercontent.com/coreboot/coreboot/main/src/mainboard/google/drallion/spd/hynix_dimm_H5AN8G6NCJR-VKC.spd.hex | GPL-2.0（coreboot 仓库 LICENSE）；SPD 数据本身为厂商公开的 JEDEC 数据；**由文本 hexdump 转换** |
| `ddr4-micron-ballistix-elite-4000-4x8g-eloaders.spd` | 512 | DDR4 (0x0C) | ✅ | **XMP 2.0** | https://raw.githubusercontent.com/eloaders/i-nex-ram-spd/master/DDR4/Ballistix%20Elite%204%C3%97%208%20GB%20DDR-4000%20(Micron%20E).SPD | 未找到 LICENSE 文件，来源不明，仅供本地测试 |
| `ddr4-micron_4gib_dimm_mta9asf51272pz-2g1a2-coreboot.bin` | 512 | DDR4 (0x0C) | ✅ | — | https://raw.githubusercontent.com/coreboot/coreboot/main/src/mainboard/intel/harcuvar/spd/micron_4GiB_dimm_MTA9ASF51272PZ-2G1A2.spd.hex | GPL-2.0（coreboot 仓库 LICENSE）；SPD 数据本身为厂商公开的 JEDEC 数据；**由文本 hexdump 转换** |
| `ddr4-micron_dimm_mt40a256m16ge-083e-coreboot.bin` | 512 | DDR4 (0x0C) | ✅ | — | https://raw.githubusercontent.com/coreboot/coreboot/main/src/mainboard/google/poppy/spd/micron_dimm_MT40A256M16GE-083E.spd.hex | GPL-2.0（coreboot 仓库 LICENSE）；SPD 数据本身为厂商公开的 JEDEC 数据；**由文本 hexdump 转换** |
| `ddr4-micron_dimm_mt40a512m16ly-075e-coreboot.bin` | 512 | DDR4 (0x0C) | ✅ | — | https://raw.githubusercontent.com/coreboot/coreboot/main/src/mainboard/google/drallion/spd/micron_dimm_MT40A512M16LY-075E.spd.hex | GPL-2.0（coreboot 仓库 LICENSE）；SPD 数据本身为厂商公开的 JEDEC 数据；**由文本 hexdump 转换** |
| `ddr4-patriot-viper4-blackout-3200-2x8g-hynixcjr-eloaders.spd` | 512 | DDR4 (0x0C) | ✅ | **XMP 2.0** | https://raw.githubusercontent.com/eloaders/i-nex-ram-spd/master/DDR4/Patriot%20Viper%204%20Blackout%202%C3%97%208%20GB%20DDR-3200%20(Hynix%20CJR).SPD | 未找到 LICENSE 文件，来源不明，仅供本地测试 |
| `ddr4-samsung-k4a8g165wb-bcrc-coreboot.bin` | 512 | DDR4 (0x0C) | ✅ | — | https://raw.githubusercontent.com/coreboot/coreboot/main/src/mainboard/starlabs/starbook/spd/samsung-K4A8G165WB-BCRC.spd.hex | GPL-2.0（coreboot 仓库 LICENSE）；SPD 数据本身为厂商公开的 JEDEC 数据；**由文本 hexdump 转换** |
| `ddr4-samsung-k4aag165wa-bctd-coreboot.bin` | 512 | DDR4 (0x0C) | ✅ | — | https://raw.githubusercontent.com/coreboot/coreboot/main/src/mainboard/clevo/cml-u/spd/samsung-K4AAG165WA-BCTD.spd.hex | GPL-2.0（coreboot 仓库 LICENSE）；SPD 数据本身为厂商公开的 JEDEC 数据；**由文本 hexdump 转换** |
| `ddr4-samsung-k4aag165wb-mcrc-coreboot.bin` | 512 | DDR4 (0x0C) | ✅ | — | https://raw.githubusercontent.com/coreboot/coreboot/main/src/mainboard/google/kahlee/spd/samsung-K4AAG165WB-MCRC.spd.hex | GPL-2.0（coreboot 仓库 LICENSE）；SPD 数据本身为厂商公开的 JEDEC 数据；**由文本 hexdump 转换** |
| `ddr4-samsung-m471a1g44ab0-cwe-coreboot.bin` | 512 | DDR4 (0x0C) | ✅ | — | https://raw.githubusercontent.com/coreboot/coreboot/main/src/mainboard/clevo/tgl-u/spd/samsung-M471A1G44AB0-CWE.spd.hex | GPL-2.0（coreboot 仓库 LICENSE）；SPD 数据本身为厂商公开的 JEDEC 数据；**由文本 hexdump 转换** |
| `ddr4-samsung-p4aaf165wa-bcwde-coreboot.bin` | 512 | DDR4 (0x0C) | ✅ | — | https://raw.githubusercontent.com/coreboot/coreboot/main/src/mainboard/system76/adl/spd/samsung-P4AAF165WA-BCWDE.spd.hex | GPL-2.0（coreboot 仓库 LICENSE）；SPD 数据本身为厂商公开的 JEDEC 数据；**由文本 hexdump 转换** |
| `ddr4-samsung_dimm_k4a4g165we-bcrc-coreboot.bin` | 512 | DDR4 (0x0C) | ✅ | — | https://raw.githubusercontent.com/coreboot/coreboot/main/src/mainboard/google/poppy/spd/samsung_dimm_K4A4G165WE-BCRC.spd.hex | GPL-2.0（coreboot 仓库 LICENSE）；SPD 数据本身为厂商公开的 JEDEC 数据；**由文本 hexdump 转换** |
| `ddr4-samsung_dimm_k4a8g165wc-bctd-coreboot.bin` | 512 | DDR4 (0x0C) | ✅ | — | https://raw.githubusercontent.com/coreboot/coreboot/main/src/mainboard/google/drallion/spd/samsung_dimm_K4A8G165WC-BCTD.spd.hex | GPL-2.0（coreboot 仓库 LICENSE）；SPD 数据本身为厂商公开的 JEDEC 数据；**由文本 hexdump 转换** |

**优先项 1 已达成**：3 个带 XMP 2.0 的真实 DDR4 dump（G.Skill FlareX / Crucial Ballistix Elite / Patriot Viper 4 Blackout，均由玩家从实物条读出）。

---

## DDR3（256 字节，优先项 3）

其中 `baboomerang_dimm0x50.*` 与 rigred 的部分样本在 `bytes[176..177]` 处有 XMP 1.x（DDR3 XMP）header `0C 4A`，已在「扩展区」列标注。

| 文件名 | 字节 | 类型码 | 基础 CRC | 扩展区 | 来源（raw URL / 仓库路径） | 许可证 / 来源说明 |
|---|---|---|---|---|---|---|
| `ddr3-36ksz2g72ld1g6e2a7-rigred.bin` | 256 | DDR3 (0x0B) | ✅（JEDEC 规则；题目写的 `bytes[0:126]` 规则 ❌） | — | https://raw.githubusercontent.com/rigred/spd_tool/main/samples/DDR3/JEDEC(b80_c2C)/36KSZ2G72LD1G6E2A7/36KSZ2G72LD1G6E2A7__0x00000000.spd.bin | 未找到 LICENSE 文件，来源不明，仅供本地测试 |
| `ddr3-a-data-ad73i1b1672eg-1333mhz-eloaders.bin` | 256 | DDR3 (0x0B) | ✅（JEDEC 规则；题目写的 `bytes[0:126]` 规则 ❌） | — | https://raw.githubusercontent.com/eloaders/i-nex-ram-spd/master/DDR3/A-DATA-AD73I1B1672EG-1333MHz.SPD | 未找到 LICENSE 文件，来源不明，仅供本地测试 |
| `ddr3-apple-coreboot.bin` | 256 | DDR3 (0x0B) | ✅（JEDEC 规则；题目写的 `bytes[0:126]` 规则 ❌） | — | https://raw.githubusercontent.com/coreboot/coreboot/main/src/mainboard/apple/macbookair4_2/spd/apple.spd.hex | GPL-2.0（coreboot 仓库 LICENSE）；SPD 数据本身为厂商公开的 JEDEC 数据；**由文本 hexdump 转换** |
| `ddr3-blt8g3d1869dt1tx0-rigred.bin` | 256 | DDR3 (0x0B) | ✅（JEDEC 规则；题目写的 `bytes[0:126]` 规则 ❌） | XMP 1.x | https://raw.githubusercontent.com/rigred/spd_tool/main/samples/DDR3/JEDEC(b85_c9B)/BLT8G3D1869DT1TX0/BLT8G3D1869DT1TX0.__0xC0DEB007.spd.bin | 未找到 LICENSE 文件，来源不明，仅供本地测试 |
| `ddr3-cmx8gx3m2a1600c9-rigred.bin` | 256 | DDR3 (0x0B) | ✅（JEDEC 规则；题目写的 `bytes[0:126]` 规则 ❌） | XMP 1.x | https://raw.githubusercontent.com/rigred/spd_tool/main/samples/DDR3/JEDEC(b02_c9E)/CMX8GX3M2A1600C9/CMX8GX3M2A1600C9__0x00000000.spd.bin | 未找到 LICENSE 文件，来源不明，仅供本地测试 |
| `ddr3-dimm0x50-2020-02-21-baboomerang.bin` | 256 | DDR3 (0x0B) | ✅（JEDEC 规则；题目写的 `bytes[0:126]` 规则 ❌） | XMP 1.x | https://raw.githubusercontent.com/baboomerang/overclockSPD/master/dumps/dimm0x50.2020-02-21.spd | GPL-3.0（仓库 LICENSE） |
| `ddr3-dimm0x50-2020-03-13-baboomerang.bin` | 256 | DDR3 (0x0B) | ✅（JEDEC 规则；题目写的 `bytes[0:126]` 规则 ❌） | XMP 1.x | https://raw.githubusercontent.com/baboomerang/overclockSPD/master/dumps/dimm0x50.2020-03-13.spd | GPL-3.0（仓库 LICENSE） |
| `ddr3-dimm0x50-2020-06-02-hynixbfr-baboomerang.bin` | 256 | DDR3 (0x0B) | ✅（JEDEC 规则；题目写的 `bytes[0:126]` 规则 ❌） | — | https://raw.githubusercontent.com/baboomerang/overclockSPD/master/dumps/dimm0x50.2020-06-02-hynixbfr.spd | GPL-3.0（仓库 LICENSE） |
| `ddr3-dimm0x51-2020-06-02-hynixbfr-baboomerang.bin` | 256 | DDR3 (0x0B) | ✅（JEDEC 规则；题目写的 `bytes[0:126]` 规则 ❌） | — | https://raw.githubusercontent.com/baboomerang/overclockSPD/master/dumps/dimm0x51.2020-06-02-hynixbfr.spd | GPL-3.0（仓库 LICENSE） |
| `ddr3-f3-1600c9-8gar-rigred.bin` | 256 | DDR3 (0x0B) | ✅（JEDEC 规则；题目写的 `bytes[0:126]` 规则 ❌） | XMP 1.x | https://raw.githubusercontent.com/rigred/spd_tool/main/samples/DDR3/JEDEC(b04_cCD)/F3-1600C9-8GAR/F3-1600C9-8GAR__0x00000000.spd.bin | 未找到 LICENSE 文件，来源不明，仅供本地测试 |
| `ddr3-f3-2400c11-8gar-rigred.bin` | 256 | DDR3 (0x0B) | ✅（JEDEC 规则；题目写的 `bytes[0:126]` 规则 ❌） | XMP 1.x | https://raw.githubusercontent.com/rigred/spd_tool/main/samples/DDR3/JEDEC(b04_cCD)/F3-2400C11-8GAR/F3-2400C11-8GAR__0x00000000.spd.bin | 未找到 LICENSE 文件，来源不明，仅供本地测试 |
| `ddr3-gskill-1866c11-hynixbfr-baboomerang.bin` | 256 | DDR3 (0x0B) | ✅（JEDEC 规则；题目写的 `bytes[0:126]` 规则 ❌） | — | https://raw.githubusercontent.com/baboomerang/overclockSPD/master/dumps/gskill-1866c11-hynixbfr.spd | GPL-3.0（仓库 LICENSE） |
| `ddr3-hmt351r7cfr4a-h9-rigred.bin` | 256 | DDR3 (0x0B) | ✅（JEDEC 规则；题目写的 `bytes[0:126]` 规则 ❌） | — | https://raw.githubusercontent.com/rigred/spd_tool/main/samples/DDR3/JEDEC(b80_cAD)/HMT351R7CFR4A-H9/HMT351R7CFR4A-H9__0x4AAC2E84__0xA40185E8.spd.bin | 未找到 LICENSE 文件，来源不明，仅供本地测试 |
| `ddr3-hynix_hmt425s6afr6a-coreboot.bin` | 256 | DDR3 (0x0B) | ✅（JEDEC 规则；题目写的 `bytes[0:126]` 规则 ❌） | — | https://raw.githubusercontent.com/coreboot/coreboot/main/src/mainboard/google/auron/variants/auron_paine/spd/Hynix_HMT425S6AFR6A.spd.hex | GPL-2.0（coreboot 仓库 LICENSE）；SPD 数据本身为厂商公开的 JEDEC 数据；**由文本 hexdump 转换** |
| `ddr3-hynixafr-baboomerang.bin` | 256 | DDR3 (0x0B) | ✅（JEDEC 规则；题目写的 `bytes[0:126]` 规则 ❌） | — | https://raw.githubusercontent.com/baboomerang/overclockSPD/master/dumps/hynixafr.spd | GPL-3.0（仓库 LICENSE） |
| `ddr3-hynixbfr1333-baboomerang.bin` | 256 | DDR3 (0x0B) | ✅（JEDEC 规则；题目写的 `bytes[0:126]` 规则 ❌） | — | https://raw.githubusercontent.com/baboomerang/overclockSPD/master/dumps/hynixbfr1333.spd | GPL-3.0（仓库 LICENSE）<br>⚠️ 与 `ddr3-dimm0x51-2020-06-02-hynixbfr-baboomerang.bin` 字节完全相同（同一 dump 的另一来源） |
| `ddr3-hynixbfr1600-baboomerang.bin` | 256 | DDR3 (0x0B) | ✅（JEDEC 规则；题目写的 `bytes[0:126]` 规则 ❌） | — | https://raw.githubusercontent.com/baboomerang/overclockSPD/master/dumps/hynixbfr1600.spd | GPL-3.0（仓库 LICENSE）<br>⚠️ 与 `ddr3-dimm0x50-2020-06-02-hynixbfr-baboomerang.bin` 字节完全相同（同一 dump 的另一来源） |
| `ddr3-hynixmfr-baboomerang.bin` | 256 | DDR3 (0x0B) | ✅（JEDEC 规则；题目写的 `bytes[0:126]` 规则 ❌） | — | https://raw.githubusercontent.com/baboomerang/overclockSPD/master/dumps/hynixmfr.spd | GPL-3.0（仓库 LICENSE） |
| `ddr3-kingston-kvr13ls9s6-2-017-a00lf-eloaders.bin` | 256 | DDR3 (0x0B) | ✅（JEDEC 规则；题目写的 `bytes[0:126]` 规则 ❌） | — | https://raw.githubusercontent.com/eloaders/i-nex-ram-spd/master/DDR3/KINGSTON-KVR13LS9S6-2-017-A00LF.SPD | 未找到 LICENSE 文件，来源不明，仅供本地测试 |
| `ddr3-kingston-kvr16ls11s6-2-001-a00lf-800mhz-eloaders.bin` | 256 | DDR3 (0x0B) | ✅（JEDEC 规则；题目写的 `bytes[0:126]` 规则 ❌） | — | https://raw.githubusercontent.com/eloaders/i-nex-ram-spd/master/DDR3/KINGSTON-KVR16LS11S6-2-001-A00LF-800MHz.SPD | 未找到 LICENSE 文件，来源不明，仅供本地测试 |
| `ddr3-kingston-kvr16ls11s6-2-001-a00lf-eloaders.bin` | 256 | DDR3 (0x0B) | ✅（JEDEC 规则；题目写的 `bytes[0:126]` 规则 ❌） | — | https://raw.githubusercontent.com/eloaders/i-nex-ram-spd/master/DDR3/KINGSTON-KVR16LS11S6-2-001-A00LF.SPD | 未找到 LICENSE 文件，来源不明，仅供本地测试 |
| `ddr3-kingston-kvr16ls11s6-2-014-a00lf-eloaders.bin` | 256 | DDR3 (0x0B) | ✅（JEDEC 规则；题目写的 `bytes[0:126]` 规则 ❌） | — | https://raw.githubusercontent.com/eloaders/i-nex-ram-spd/master/DDR3/KINGSTON-KVR16LS11S6-2-014-A00LF.SPD | 未找到 LICENSE 文件，来源不明，仅供本地测试 |
| `ddr3-m393b5270dh0-ck0-rigred.bin` | 256 | DDR3 (0x0B) | ✅（JEDEC 规则；题目写的 `bytes[0:126]` 规则 ❌） | — | https://raw.githubusercontent.com/rigred/spd_tool/main/samples/DDR3/JEDEC(b80_cCE)/M393B5270DH0-CK0/M393B5270DH0-CK0__0x33558390__0x2B3F4A76.spd.bin | 未找到 LICENSE 文件，来源不明，仅供本地测试 |
| `ddr3-micron-8ktf51264hz-1g6e1-1600mhz-eloaders.bin` | 256 | DDR3 (0x0B) | ✅（JEDEC 规则；题目写的 `bytes[0:126]` 规则 ❌） | — | https://raw.githubusercontent.com/eloaders/i-nex-ram-spd/master/DDR3/Micron-8KTF51264HZ-1G6E1-1600MHz.SPD | 未找到 LICENSE 文件，来源不明，仅供本地测试 |
| `ddr3-micron_4ktf25664hz-coreboot.bin` | 256 | DDR3 (0x0B) | ✅（JEDEC 规则；题目写的 `bytes[0:126]` 规则 ❌） | — | https://raw.githubusercontent.com/coreboot/coreboot/main/src/mainboard/google/auron/variants/auron_paine/spd/Micron_4KTF25664HZ.spd.hex | GPL-2.0（coreboot 仓库 LICENSE）；SPD 数据本身为厂商公开的 JEDEC 数据；**由文本 hexdump 转换** |
| `ddr3-nanya-8gb-r2x8-1600c11-baboomerang.bin` | 256 | DDR3 (0x0B) | ✅（JEDEC 规则；题目写的 `bytes[0:126]` 规则 ❌） | — | https://raw.githubusercontent.com/baboomerang/overclockSPD/master/dumps/nanya-8gb-r2x8-1600c11.spd | GPL-3.0（仓库 LICENSE） |
| `ddr3-nanya-8gb-r2x8-2133c11-baboomerang.bin` | 256 | DDR3 (0x0B) | ✅（JEDEC 规则；题目写的 `bytes[0:126]` 规则 ❌） | — | https://raw.githubusercontent.com/baboomerang/overclockSPD/master/dumps/nanya-8gb-r2x8-2133c11.spd | GPL-3.0（仓库 LICENSE） |
| `ddr3-sammyd-2gbit-2020-02-21-baboomerang.bin` | 256 | DDR3 (0x0B) | ✅（JEDEC 规则；题目写的 `bytes[0:126]` 规则 ❌） | — | https://raw.githubusercontent.com/baboomerang/overclockSPD/master/dumps/sammyD-2gbit.2020-02-21.spd | GPL-3.0（仓库 LICENSE） |
| `ddr3-samsung-m471b2873fhs-ch9-0x61cf1261-1333mhz-eloaders.bin` | 256 | DDR3 (0x0B) | ✅（JEDEC 规则；题目写的 `bytes[0:126]` 规则 ❌） | — | https://raw.githubusercontent.com/eloaders/i-nex-ram-spd/master/DDR3/Samsung-M471B2873FHS-CH9-0x61CF1261-1333MHz.SPD | 未找到 LICENSE 文件，来源不明，仅供本地测试 |
| `ddr3-samsung-m471b2873fhs-ch9-0x64436544-1333mhz-eloaders.bin` | 256 | DDR3 (0x0B) | ✅（JEDEC 规则；题目写的 `bytes[0:126]` 规则 ❌） | — | https://raw.githubusercontent.com/eloaders/i-nex-ram-spd/master/DDR3/Samsung-M471B2873FHS-CH9-0x64436544-1333MHz.SPD | 未找到 LICENSE 文件，来源不明，仅供本地测试 |
| `ddr3-samsung_m471b5674eb0-yk0-coreboot.bin` | 256 | DDR3 (0x0B) | ✅（JEDEC 规则；题目写的 `bytes[0:126]` 规则 ❌） | — | https://raw.githubusercontent.com/coreboot/coreboot/main/src/mainboard/google/auron/variants/gandof/spd/Samsung_M471B5674EB0-YK0.spd.hex | GPL-2.0（coreboot 仓库 LICENSE）；SPD 数据本身为厂商公开的 JEDEC 数据；**由文本 hexdump 转换** |
| `ddr3-unknown-01-ddr3spd.bin` | 256 | DDR3 (0x0B) | ✅（JEDEC 规则；题目写的 `bytes[0:126]` 规则 ❌） | XMP 1.x | https://raw.githubusercontent.com/mikebdp2/ddr3spd/master/myspd.bin | 未找到 LICENSE 文件，来源不明，仅供本地测试<br>⚠️ 与 `ddr3-blt8g3d1869dt1tx0-rigred.bin` 字节完全相同（同一 dump 的另一来源） |

---

## 负样本 `corrupt/`（CRC 失败或结构异常，保留供错误路径测试）

| 文件名 | 字节 | 类型码 | 失败原因 | 来源 | 许可证 / 来源说明 |
|---|---|---|---|---|---|
| `corrupt/ddr3-ocz-1g-1333m-257bytes-trailing-pad-lvusyy.bin` | 257 | n/a | 长度 257 字节（有效 256 字节 + 1 字节 0x00 尾部填充）。前 256 字节按 JEDEC 规则 CRC 通过；整体长度不合法。 | https://raw.githubusercontent.com/lvusyy/SPDStudio/master/samples/DDR3_1G_1333M_8PCS_OCZ.bin | 未找到 LICENSE 文件，来源不明，仅供本地测试 |
| `corrupt/ddr4-micron-mt40a1g16kd-062e-e-coreboot.bin` | 512 | 0x0C | CRC 校验失败（按题目给定的小端读法）。实际该文件把 CRC 以 **MSB-first** 存放：LE 读得 0xA07C / 0x7D21，BE 读得 0x7CA0 / 0x217D，与计算值完全一致。属"端序特例"而非数据损坏。 | https://raw.githubusercontent.com/coreboot/coreboot/main/src/mainboard/starlabs/starbook/spd/micron-MT40A1G16KD-062E-E.spd.hex | GPL-2.0（coreboot 仓库 LICENSE）；SPD 数据本身为厂商公开的 JEDEC 数据 |
| `corrupt/ddr3-kingston_b5116ecmdxggb-coreboot.bin` | 256 | 0x0B | CRC 字段为 0x0000（coreboot 源文件未填写 CRC），且 byte0=0x23 是 DDR4 的取值、与 DDR3(byte2=0x0B) 不符。 | https://raw.githubusercontent.com/coreboot/coreboot/main/src/mainboard/facebook/fbg1701/spd/KINGSTON_B5116ECMDXGGB.spd.hex | GPL-2.0（coreboot 仓库 LICENSE）；SPD 数据本身为厂商公开的 JEDEC 数据 |
| `corrupt/ddr3-micron_mt41k512m16ha-125a-coreboot.bin` | 256 | 0x0B | 同上：CRC 字段为 0x0000，且 byte0=0x23 与 DDR3 不符。 | https://raw.githubusercontent.com/coreboot/coreboot/main/src/mainboard/facebook/fbg1701/spd/MICRON_MT41K512M16HA-125A.spd.hex | GPL-2.0（coreboot 仓库 LICENSE）；SPD 数据本身为厂商公开的 JEDEC 数据 |
| `corrupt/ddr3-samsung_k4b8g1646d-myko-coreboot.bin` | 256 | 0x0B | 同上：CRC 字段为 0x0000，且 byte0=0x23 与 DDR3 不符。 | https://raw.githubusercontent.com/coreboot/coreboot/main/src/mainboard/facebook/fbg1701/spd/SAMSUNG_K4B8G1646D-MYKO.spd.hex | GPL-2.0（coreboot 仓库 LICENSE）；SPD 数据本身为厂商公开的 JEDEC 数据 |
| `corrupt/ddr4-coreboot-sim-set0-01.bin` | 512 | 0x0C | CRC 字段为 0x0000（数据集中留空，由 coreboot 构建期另行计算）。 | https://raw.githubusercontent.com/coreboot/coreboot/main/spd/ddr4/set-0/spd-1.hex | GPL-2.0（coreboot 仓库 LICENSE）；SPD 数据本身为厂商公开的 JEDEC 数据 |
| `corrupt/ddr4-coreboot-sim-set0-02.bin` | 512 | 0x0C | CRC 字段为 0x0000（数据集中留空，由 coreboot 构建期另行计算）。 | https://raw.githubusercontent.com/coreboot/coreboot/main/spd/ddr4/set-0/spd-2.hex | GPL-2.0（coreboot 仓库 LICENSE）；SPD 数据本身为厂商公开的 JEDEC 数据 |
| `corrupt/ddr4-coreboot-sim-set0-03.bin` | 512 | 0x0C | CRC 字段为 0x0000（数据集中留空，由 coreboot 构建期另行计算）。 | https://raw.githubusercontent.com/coreboot/coreboot/main/spd/ddr4/set-0/spd-3.hex | GPL-2.0（coreboot 仓库 LICENSE）；SPD 数据本身为厂商公开的 JEDEC 数据 |
| `corrupt/ddr4-coreboot-sim-set0-04.bin` | 512 | 0x0C | CRC 字段为 0x0000（数据集中留空，由 coreboot 构建期另行计算）。 | https://raw.githubusercontent.com/coreboot/coreboot/main/spd/ddr4/set-0/spd-4.hex | GPL-2.0（coreboot 仓库 LICENSE）；SPD 数据本身为厂商公开的 JEDEC 数据 |

> 注意：`corrupt/` 里**没有一个是「二进制内容被损坏」的 dump**，它们都是结构/字段层面的异常：
> 5 个来自 coreboot 源文件本身的 CRC 字段问题（4 个未填写 `0x0000`、1 个端序相反），
> 4 个是 coreboot 官方模拟数据集（刻意留空 CRC 的回归数据），1 个是长度异常的 257 字节 OCZ 转储（其有效部分 256 字节 CRC 正常）。
> 论坛上流传的「真正损坏的 DDR5 SPD」样本需要注册/登录才能下载，本次未能获取（见下节）。

---

## 关于 XMP / EXPO 判定

| 标志 | 判定条件（本题要求） | 出现在 |
|---|---|---|
| XMP 1.x | `bytes[176..177] == 0C 4A`（256 B 图内） | 部分 DDR3 |
| XMP 2.0 | `bytes[384..385] == 0C 4A`（512 B 图内） | 3 个 DDR4 |
| XMP 3.0 | `bytes[640..641] == 0C 4A`（1024 B 图内） | 11 个 DDR5 |
| EXPO | `bytes[832..835] == "EXPO"`（1024 B 图内） | 9 个 DDR5 |

> 补充说明：DDR3 的 XMP 1.x header 并不在题目给出的三个位置之列，这里作为额外信息标注，未作为筛选条件。

---

## 未找到 / 失败的部分

### 1. DDR2（256 B，byte 2 = `0x08`）—— **没找到任何真实 dump**

试过但都没有 DDR2 二进制样本：

| 来源 | 结果 |
|---|---|
| [coreboot/coreboot](https://github.com/coreboot/coreboot) `main` 分支全部 359 个 `.spd.hex` | 类型码只有 0x0B(74) / 0x0C(77) / 0x11(33) / 0x13(32) / 0x15(10) / 0x0F(4) / 0x12(2)，**无 0x08** |
| coreboot tag `4.9`（更早、含 DDR2 主板）全部 214 个 `.spd.hex`/`.spd.bin` | 类型码 0x0B(63) / 0x0C(26) / 0x0F(1) / 0x10(2) / 0x11(4) / 0xF1(90)，**无 0x08** |
| coreboot tag `4.8` / `4.11` | 树里同样没有 DDR2 的 `.spd.hex`（老 DDR2 主板如 x60/t60/macbook21 的 SPD 都从内存条实时读取，不存盘） |
| GitHub 仓库搜索 `ddr2 spd` / `spd ddr2` / `ddr2 spd dump` / `sdram spd dump` / `spd eeprom ddr2` | 只命中 `1a2m3/SPD-Reader-Writer`（GPL-3.0，源码支持 DDR2 但**仓库内无任何 dump**）和 `rigred/spd_tool`（README 声称支持 DDR2，但 `samples/` 下只有 `DDR3/` 与 `SDR/`，**无 `DDR2/`**） |
| [rigred/SPD](https://github.com/rigred/SPD)（"Hacking DDR Memory SPD"） | `dumps-miscellaneous/dump-*-dd.bin` 文件名里的 "dd" 容易误认成 DDR2，实测 byte 2 全是 `0x0B`，其实是 DDR3；另有一堆 117/126 字节的截断中间文件，非完整 SPD |
| [ZakKemble/RAMSPDMod](https://github.com/ZakKemble/RAMSPDMod)（博客明确说测过 DDR2 Hypertec HYUK26412882GBOE） | 仓库 `dumps/` 里只有 4 个 Kingston DDR3 `.SPD`，**没有那条 DDR2 的 dump**；`tools/SPD.zip`、`tools/SPDTool_063.zip` 解开后各只有一个 `.exe`（SPD.exe 6.3 MB / SPDTool.exe 2.9 MB），我扫描了两个 exe 的全文寻找嵌入的 256/512 字节合法 SPD 块，只得到 7 个随机巧合匹配，**没有可用的 SPD 数据库** |
| [eloaders/i-nex-ram-spd](https://github.com/eloaders/i-nex-ram-spd)（"EEPROM DDR Files"） | 只有 `DDR3/` 与 `DDR4/` 两个目录 |
| [Sensirion/i2c-tools](https://github.com/Sensirion/i2c-tools) `eeprom/`（`decode-dimms`） | 只有程序本身 + man page，**没有自带 SPD 测试语料** |
| `searchcode.com` API、GitHub gist 搜索 | searchcode 返回 404/非 JSON；gist 搜索返回 `429 Too Many Requests`（未登录被限流），均未取得结果 |
| GitHub Code Search | 匿名调用 `api.github.com/search/code` 返回 401；网页版 `github.com/search?type=code` 现在强制登录 |
| archive.org | 搜 `SPD DDR2` / `SPD dump` / `memory SPD eeprom`，命中的都是无关条目（手表固件、CIA 档案等），无 SPD 语料 |

**结论**：DDR2 的 SPD 只有 128 字节有效内容、且 DDR2 平台早已淘汰，公开仓库里几乎没人上传二进制 dump；
现存于论坛附件（Overclock.net / Badcaps / 国内论坛）的 DDR2 dump 都需要注册登录，本环境无法取得。
如果回归测试必须覆盖 DDR2，建议后续优先级为：① 从旧条实物读一份；② 让有账号的人从论坛附件搬运；
③ 退而求其次，用题目允许的「文本 hexdump 转换」路线，从论坛贴出的 `i2cdump 0x50` 文本重建（本次没找到完整的 256 字节文本）。

### 2. 真正"损坏"的 DDR5 SPD 样本（论坛帖）

题目提到的 "CORRUPTED DDR5 SPD FIX - TUTORIAL" 一类帖子，附件需登录才能下载；
退一步找的替代品是 [djchumpguy/ddr5-spd-diagnostic](https://github.com/djchumpguy/ddr5-spd-diagnostic)，
它有完整的 "bad stick" 修复证据链，但全是 `dmidecode` / hub 寄存器 / `mapall` 的**文本日志**，没有坏条的 1024 字节二进制快照。
目前 `corrupt/` 里的负样本以 coreboot 的 CRC 字段异常文件为主（见上）。

### 3. 主动放弃 / 未收录的来源

| 来源 | 为什么不收 |
|---|---|
| `ec-/DDR5XMPEditor` 与 `edlf/DDR5SPDEditor` 的 `Sample XMP3 block.bin`(384B) / `Sample XMP3 Profile.bin`(64B) / `EXPO_Profile_*.bin`(40B) / `Sample EXPO block *.bin`(128B) | 这些是**单个 profile/block 的片段**，不是完整 SPD，长度也不在 256/512/1024 之列 |
| coreboot `spd/lp5/**`(40 个, 0x13/512B)、`spd/lp4x/**`(31 个, 0x11/512B)、`spd/ddr4/set-0` 之外的 LPDDR5 文件、0x0F/256B、0xF1/256B 等 | 这些文件的「类型码 ↔ 长度」与题目给出的对照表不符（例如 LPDDR5 `0x13` 是 512 B 而非 1024 B），按规则 2 会判为不匹配，故不收录 |
| `rigred/spd_tool` 的 2 个 SDR 样本（`samples/SDR/...`, 256 B, byte2=`0x04`）和 `1a2m3` 的 DDR 代码 | SDR/DDR 的类型码不在题目允许列表内 |
| coreboot `spd/ddr4/set-0/spd-*.hex`（16 个 0x0C/512B 的 curated 数据集） | CRC 字段刻意留空（`0x0000`），且不是单条实物，只挑了 4 个放进 `corrupt/` 当负样本 |
| `cnns2022/ddrxmpeditor-pro` | 953 个文件全是 PyInstaller 打包产物（`.exe`/`.pyd`/Tcl 运行库），**没有任何 SPD 样本** |
| `GerRepair/SPD_Dumper_DDR4` / `Qeao-zl/SPD-Editor-Pro` / `xabar/spd_utils` / `xabar/spd_creator_ddr4` / `redchenjs/spd-eeprom` / `mcsee-artifacts/spd-decoder` / `peterhu/ddr5-shellkit` / `kitune-san/SPD_RW` / `Blacktempel/RAMSPDToolkit` / `Paradoxdov/memforge` 等 | 仓库里只有源码/DLL（`Blacktempel` 的 4 个 `.bin` 是 SMBus/PCI 驱动的二进制，不是 SPD），无 SPD 数据 |
| `iron2love/DDR5_SPD_Data` | 与 `cityson7/SPD-Reader-Writer-DDR5` 的 `SPD_Data/` 字节完全相同，已在来源列注明镜像关系，不重复收文件 |

### 4. 环境限制

- 本机没有 `curl` / `wget`，全部下载走 `node -e 'fetch(...)'` + `python3`；GitHub API 匿名限额 60 次/小时、搜索 10 次/分钟，触顶时退避重试。
- GitHub 匿名 Code Search 已不可用（401），因此无法直接按「文件内容含 SPD 头」检索，只能靠仓库搜索 + 遍历仓库文件树。

---

## 目录结构

```
testdata/spd/
├── MANIFEST.md              本清单
├── ddr5-*.bin|*.spd        DDR5 1024 B 样本
├── ddr4-*.bin|*.spd        DDR4 512 B 样本
├── ddr3-*.bin|*.spd        DDR3 256 B 样本
├── corrupt/                CRC 失败 / 结构异常样本
└── tools/spdcheck.py       校验脚本（可用 `python3 tools/spdcheck.py <文件或目录>` 复跑）
```

复跑校验：

```bash
python3 testdata/spd/tools/spdcheck.py testdata/spd/
python3 testdata/spd/tools/spdcheck.py testdata/spd/corrupt/
```

---

## 语料回归暴露并已修正的解析问题(2026-09-21, spdrw)

这些真实 dump 直接推翻/纠正了几处实现(详见 `internal/spd/*` 的注释):

| 问题 | 真实数据证据 | 修正 |
|---|---|---|
| JEP106 厂商名查不到 | Micron/Samsung/SK Hynix 的 continuation 字节是 `0x80`、Kingston `0x01`、Crucial `0x85`、Corsair `0x02` —— 全部是"续延计数 \| 奇校验位" | `ManufacturerName` 按 `cont & 0x7F` 查表; `FindManufacturer` 写回时给续延字节补奇校验位 |
| XMP 2.0 CL 表整体读错 | G.Skill FlareX 3200 的 CL 掩码字节 `80 00 00` 应为 CL14(bit7); 原按大端读会得到 CL30 | CL 掩码按 `+0x0D` 起的 24 位**小端**解析(bit i → CL i+7) |
| 正常内存条被误报 "CRC 校验失败" | 十铨/威刚/T-FORCE 等实物条在 XMP3 的 `User2` 槽(0x3C0)留了非零数据但没有有效 profile CRC | XMP3 槽"存在"判据改为 **槽首 VPP ≥ 0x20**(真实 profile 的 VPP ≥ 1.0V);VPP=0 的残留数据不再当 profile |
| DDR4/DDR5 模块修订码长度 | 实物条: DDR4 byte349、DDR5 byte551 各 1 字节; DDR3 byte146-147、DDR2 byte91-92 各 2 字节 | 编辑器按世代区分修订码长度, 并按偏移顺序显示/写回 hex |
| DDR3 tRC/tRAS、DDR4 tRAS/tRC/tFAW/tWR/tWTR 无法写入 | DDR3 实物条 tRC=48.125ns(medium=385 > 255) | 12 位 medium 字段改用 12 位编码上限(不再按 8 位拒绝) |
| "把字段设回原值"却改了字节 | 部分 DDR3 条用 medium 向下取整 + 正 fine, 与 JEDEC 建议的向上取整 + 负 fine 等价但字节不同 | 时间值未变时不改字节; 新值编码采用 JEDEC 约定的向上取整 + 负修正 |

**验证**:`go test ./internal/spd/ -run "TestRealDump|TestCorrupt" -v`
→ 67/67 识别与 CRC 通过; 67/67 编辑器身份往返逐字节一致; 2698 个字段赋值零字节变化; 9 份负样本无 panic。
