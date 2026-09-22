package spd

import "fmt"

// infoFields 返回"SPD 布局说明"字段 —— 只读, 不提供编辑, 唯一作用是把原始数据
// 视图里每个字节的含义补全: 悬停任意字节都能看到它属于哪个区域, 而不是
// "未映射到字段"。可编辑字段(身份/时序/XMP/EXPO)在 infoFields 之后追加
// (见 Editor.Fields), 同一字节上会覆盖这里的粗粒度说明。
//
// 命名原则: 仓库解析器实际使用的字节用解析器里的名字(经过真实语料往返验证);
// 其余按 JEDEC 布局给出区间说明, 拿不准的一律诚实标"保留(JEDEC)", 不编造字节含义。
func (e *Editor) infoFields() []Field {
	out := []Field{}
	first := true
	add := func(name, offset, note string) {
		out = append(out, Field{
			Key: "info." + fmt.Sprintf("%03d", len(out)), Name: name,
			Group: "SPD 布局", Kind: "info",
			Offset: offset, Risk: "low",
			Primary: first, // 分组至少一个常用字段(界面约定), 其余收进"显示全部"
			Note:    note,
		})
		first = false
	}

	switch e.rt {
	case DDR3, DDR, SDRAM:
		add("报文头: 字节数/SPD 版本", "0x000-0x001", "byte0 = 字节使用/总容量配置, byte1 = SPD 版本")
		add("DRAM 器件类型(0x0B=DDR3)", "0x002", "原始数据视图的世代判定就来自这个字节")
		add("模块类型", "0x003", "UDIMM/SODIMM/RDIMM 等(位 6:3)")
		add("SDRAM 密度与 Bank", "0x004", "单 die 容量(位 6:3)与 Bank 数, 容量换算的输入之一")
		add("SDRAM 行/列地址数", "0x005", "行地址数(位 7:3)与列地址数(位 2:0)")
		add("保留(JEDEC)", "0x006", "DDR3 rev1.x 未定义此字节")
		add("模块组织(Rank/器件位宽)", "0x007", "Rank 数(位 7:5)与器件位宽(位 4:2), 容量换算输入")
		add("模块总线宽度", "0x008", "主位宽(位 4:2)与扩展位宽(位 3)")
		add("时基(MTB/FTB)", "0x009-0x00B", "中速时基(9/10)与细速时基(11)的分子分母, 时序换算成 ns 的依据")
		add("保留(JEDEC)", "0x00D-0x00F", "tCKmin(0x00C)之后的未定义字节")
		// 时序区 12-38(tCK/tAA/tRCD/tRP/tRAS/tRC/tRFC/tFAW/tWR/tRRD/tWTR/tRTP/tCCD + FTB 细调)
		// 与 CRC(126-127)由可编辑字段/校验色带给出, 这里不重复。
		add("保留(JEDEC)", "0x01F-0x021", "时序区之后的未定义字节")
		add("保留(JEDEC)", "0x027-0x074", "未定义(身份区从 0x075 开始)")
		add("保留(JEDEC)", "0x096-0x0AF", "未定义(厂商超频/XMP 区从 0x0B0 开始)")
		add("XMP/厂商超频数据区", "0x0B0-0x0FF", "Intel XMP 等厂商私有数据; 本工具不提供该区的 DDR3 编辑")

	case DDR4, DDR4E, LPDDR3, LPDDR4, LPDDR4X:
		add("报文头: 字节数/SPD 版本", "0x000-0x001", "byte0 = 字节使用/总容量配置, byte1 = SPD 版本")
		add("DRAM 器件类型(0x0C=DDR4)", "0x002", "原始数据视图的世代判定就来自这个字节")
		add("模块类型", "0x003", "UDIMM/SODIMM/RDIMM/LRDIMM 等(位 6:3)")
		add("SDRAM 密度与 Bank", "0x004", "单 die 容量与 Bank 组/Bank 数, 容量换算的输入之一")
		add("SDRAM 行/列地址数", "0x005", "行地址数(位 7:3)与列地址数(位 2:0)")
		add("SDRAM 封装类型", "0x006", "单体/堆叠与封装高度")
		add("SDRAM 可选特性", "0x007", "DLL 等可选特性")
		add("SDRAM 配置与热选项", "0x008", "自刷新倍率(1x/2x/4x)等")
		add("SDRAM 板载温度传感器", "0x009", "TS 支持与刷新倍率选项")
		add("SDRAM 器件类型(单体/3DS)", "0x00A", "传统单片 DRAM 或 3D 堆叠(3DS)")
		add("模块温度传感器", "0x00B", "模块上的热传感器(如 TS5110)")
		add("信号负载", "0x00C", "多层负载/TSV 描述")
		add("模块组织(Rank/器件位宽)", "0x00D", "Rank 数(位 7:5)与器件位宽(位 2:0), 容量换算输入")
		add("模块总线宽度", "0x00E", "主位宽(位 2:0)与扩展位宽(位 4:3)")
		add("模块标称电压(VDD)", "0x00F", "1.2V 级别的标称工作电压")
		add("模块厚度", "0x010-0x011", "正面/背面最大元件高度")
		// 18-19(tCKAVG)、20-23(CL 掩码)、24-45(时序)、117-125(FTB 细调)由可编辑字段给出
		add("保留(JEDEC)", "0x02E-0x074", "时序区与 FTB 细调区之间的未定义字节")
		add("保留(JEDEC)", "0x080-0x13F", "未定义(身份区从 0x140 开始)")
		add("保留(JEDEC)", "0x161-0x17F", "身份区(0x140-0x160)与 XMP 2.0 区(0x180)之间")
		add("XMP 2.0 头部与 Profile 区", "0x180-0x1FF",
			"XMP 头(0x0C 0x4A magic/版本/启用位)+ 两份 profile(各 63 字节); 拆到字节的可编辑字段存在时以它们为准")

	case DDR5, LPDDR5, DDR5NVDIMMP, LPDDR5X:
		add("报文头: 字节数/SPD 版本", "0x000-0x001", "byte0 = 字节使用/总容量配置, byte1 = SPD 版本")
		add("键字节/总线命令协议", "0x002", "高位 = 键字节, 低位 = 主机总线命令协议(DDR5 SDRAM)")
		add("键字节/模块类型", "0x003", "UDIMM/SODIMM/RDIMM/MRDIMM 等(位 6:3)")
		add("封装0: 密度与 Bank", "0x004", "die 数(位 7:4)与单 die 容量(位 3:0), 容量换算输入")
		add("封装0: 行/列地址数", "0x005", "行地址数(位 7:5)与列地址数(位 2:0)")
		add("封装0: SDRAM IO 位宽", "0x006", "x4/x8/x16/x32(位 7:5), 容量换算输入")
		add("封装0: 选项/保留", "0x007", "封装 0 的其余特征位")
		add("封装1: 密度与 Bank", "0x008", "非对称封装时与封装 0 不同; 对称封装通常全 0")
		add("封装1: 行/列地址数", "0x009", "封装 1 的地址结构")
		add("封装1: SDRAM IO 位宽", "0x00A", "封装 1 的 IO 位宽")
		add("封装1: 选项/保留", "0x00B", "封装 1 的其余特征位")
		add("模块温度传感器", "0x00C", "位 7 = 板载 TS 支持等(语料实测该字节有区分度)")
		add("信号负载/模块特征", "0x00D", "位 3:0 = 线端负载描述")
		add("边沿到 DRAM 地址映射", "0x00E", "哪些地址线在金手指与 DRAM 之间做了换位(位掩码)")
		add("保留(JEDEC)", "0x00F", "未定义")
		add("模块厚度", "0x010-0x011", "正面/背面最大元件高度(语料多为 0 = 未填写)")
		add("保留(JEDEC)", "0x012-0x013", "未定义")
		// 20-23(tCKAVG)、24-28(CL 掩码)、30-102(时序)由 ddr5TimingSpecs/clMaskField 给出
		add("时序组下限计数", "0x01D/0x48/0x4B/0x4E/0x51/0x54/0x57/0x5A/0x5D/0x60/0x63/0x66",
			"每组时序之后的 margin 计数字节, 不是时间值")
		add("SDRAM 其他特性(修复行等)", "0x036-0x045", "tRFC 组(0x2A-0x35)之后、tRRD_L(0x46)之前: 4 字节一组的多组条目(PPR 修复行等), 语料实证非全 0")
		add("保留(JEDEC)", "0x067-0x07F", "时序区结束、基础段 CRC(0x7E-0x7F)所在的未定义字节")
		add("保留(JEDEC)", "0x080-0x0E9", "模块组织(0x0EA)之前的未定义字节")
		add("模块组织(Rank 数/非对称)", "0x0EA", "Rank 数(位 7:5)+1 与非对称封装标志(位 6); 容量换算输入, 本工具不直接编辑")
		add("通道总线宽度", "0x0EB", "通道数(位 7:6)、扩展位宽(位 5:4)与每通道主位宽(位 4:2)")
		add("模块特性/保留", "0x0EC-0x0FF", "模块特性位与未定义字节")
		add("终端用户可编程区/保留", "0x100-0x1FD", "预留给终端用户/工具链的可编程空间; 本工具不解析(0x1FE-0x1FF 是整片 CRC)")
		add("保留(JEDEC)", "0x22B-0x22F", "身份区(0x200-0x22A)之后的未定义字节")
		add("PMIC0/PMIC1 参考设计", "0x230-0x23B", "电源管理 IC 的参考设计定义(RDIMM/LRDIMM 相关)")
		add("保留(JEDEC)", "0x23C-0x24B", "PMIC 参考设计与 RCD 信息之间")
		add("RCD(寄存时钟驱动器)信息", "0x24C-0x24E", "RCD 厂商 JEP106 ID 与修订(RDIMM/LRDIMM)")
		add("保留(JEDEC)", "0x24F-0x27F", "RCD 信息之后到 XMP 3.0 头(0x280)之前")
		add("XMP 3.0 头部与元数据", "0x280-0x2BF", "XMP 头(magic/版本/启用位)+ 槽索引与头部 CRC; 存在可编辑字段时以它们为准")
		add("XMP 3.0 Profile 1", "0x2C0-0x2FF", "profile 槽 1(首字节 = VPP 电压编码)")
		add("XMP 3.0 Profile 2", "0x300-0x33F", "profile 槽 2")
		add("XMP 3.0 Profile 3 / EXPO", "0x340-0x37F", "profile 槽 3; EXPO 存在时该槽被 EXPO 占用")
		add("XMP 3.0 Profile 4 / EXPO 扩展", "0x380-0x3BF", "profile 槽 4; EXPO 存在时该槽被 EXPO 占用")
		add("XMP 3.0 Profile 5", "0x3C0-0x3FF", "profile 槽 5(部分厂商在这里留非 profile 数据)")

	default:
		return nil
	}
	return out
}
