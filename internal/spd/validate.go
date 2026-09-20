package spd

import "fmt"

// 校验层: 按世代校验 dump 的 CRC。纯函数, 无 I/O。
// 写入前预检与编辑器都依赖它 —— 写一份 CRC 不符的 SPD 会让主板拒绝该条或
// 按错误的时序启动。

// CRCOK 校验 dump 的 CRC。返回 (是否通过, 错误)。
// 长度与类型不匹配、或该世代无 CRC 定义时返回错误。
//
// 覆盖范围:
//   - DDR4/LPDDR4 系(512B): 两段各 128B, CRC 在每段末 2 字节
//   - DDR5/LPDDR5 系(1024B): 基础段 0-509(CRC 510/511) + XMP 3.0 header/各槽 + EXPO
//   - DDR3(256B): 单段, 覆盖 126 或 117 字节(byte0 bit7 决定), CRC 在 126/127
//   - DDR2(256B): 8 位校验和, byte63 = sum(bytes 0..62)
func CRCOK(dump []byte) (bool, error) {
	rt, size, err := Identify(dump)
	if err != nil {
		return false, err
	}
	if len(dump) != size {
		return false, fmt.Errorf("长度 %d 字节与 %v 的 SPD 大小 %d 字节不一致", len(dump), rt, size)
	}
	switch rt {
	case DDR4, DDR4E, LPDDR3, LPDDR4, LPDDR4X:
		d, err := NewDDR4(dump)
		if err != nil {
			return false, err
		}
		return d.CRCOK(), nil
	case DDR5, LPDDR5, DDR5NVDIMMP, LPDDR5X:
		d, err := NewDDR5(dump)
		if err != nil {
			return false, err
		}
		return d.CRCOK(), nil
	case DDR3:
		coverage := 126
		if getBit(dump[0], 7) {
			coverage = 117
		}
		return Crc16(dump[:coverage]) == uint16(dump[126])|uint16(dump[127])<<8, nil
	case DDR2, DDR2FBDIMM, DDR2FBDIMMP:
		// DDR2 没有 CRC16, 而是 byte63 = sum(bytes 0..62) & 0xFF
		return ddr2Checksum(dump) == dump[63], nil
	default:
		return false, fmt.Errorf("%v 无 CRC 定义, 无法校验", rt)
	}
}

// CRCOffsets 返回该 dump 中各 CRC 字节的偏移(写入时应最后写这些字节)。
// 只返回"对应区域非空白"的 CRC —— 未使用的扩展区不写。
func CRCOffsets(dump []byte) []int {
	rt, size, err := Identify(dump)
	if err != nil || len(dump) != size {
		return nil
	}
	var out []int
	switch rt {
	case DDR2, DDR2FBDIMM, DDR2FBDIMMP:
		out = append(out, 63)
	case DDR3:
		out = append(out, 126, 127)
	case DDR4, DDR4E, LPDDR3, LPDDR4, LPDDR4X:
		out = append(out, 126, 127, 254, 255)
	case DDR5, LPDDR5, DDR5NVDIMMP, LPDDR5X:
		out = append(out, 510, 511)
		if len(dump) > xmp30Offset+1 && dump[xmp30Offset] == 0x0C && dump[xmp30Offset+1] == 0x4A {
			out = append(out, xmp30Offset+62, xmp30Offset+63)
		}
		expo := len(dump) >= expoOffset+4 && string(dump[expoOffset:expoOffset+4]) == "EXPO"
		for i := range XMP30ProfileOffsets {
			if expo && (i == 2 || i == 3) {
				continue
			}
			if XMP30SlotPresent(dump, i) {
				out = append(out, XMP30ProfileOffsets[i]+62, XMP30ProfileOffsets[i]+63)
			}
		}
		if expo {
			out = append(out, expoOffset+126, expoOffset+127)
		}
	}
	return out
}

// FixCRC 重算并写回 dump 中所有 CRC 字节(CRC 不符时用于修复)。
// 只改 CRC 字节, 返回被修改的偏移列表。
func FixCRC(dump []byte) ([]int, error) {
	rt, size, err := Identify(dump)
	if err != nil {
		return nil, err
	}
	if len(dump) != size {
		return nil, fmt.Errorf("长度 %d 字节与 %v 的 SPD 大小 %d 字节不一致", len(dump), rt, size)
	}
	switch rt {
	case DDR4, DDR4E, LPDDR3, LPDDR4, LPDDR4X:
		d, err := NewDDR4(dump)
		if err != nil {
			return nil, err
		}
		if !d.FixCRC() {
			return nil, fmt.Errorf("DDR4 CRC 修复后仍校验失败")
		}
		return []int{126, 127, 254, 255}, nil
	case DDR3:
		coverage := 126
		if getBit(dump[0], 7) {
			coverage = 117
		}
		crc := Crc16(dump[:coverage])
		dump[126], dump[127] = byte(crc), byte(crc>>8)
		return []int{126, 127}, nil
	case DDR2, DDR2FBDIMM, DDR2FBDIMMP:
		dump[63] = ddr2Checksum(dump)
		return []int{63}, nil
	case DDR5, LPDDR5, DDR5NVDIMMP, LPDDR5X:
		var touched []int
		base := Crc16(dump[:510])
		dump[510], dump[511] = byte(base), byte(base>>8)
		touched = append(touched, 510, 511)
		if len(dump) > xmp30Offset+63 && dump[xmp30Offset] == 0x0C && dump[xmp30Offset+1] == 0x4A {
			sec := dump[xmp30Offset : xmp30Offset+xmp30HeaderLen]
			h := Crc16(sec[:62])
			sec[62], sec[63] = byte(h), byte(h>>8)
			touched = append(touched, xmp30Offset+62, xmp30Offset+63)
		}
		expo := len(dump) >= expoOffset+4 && string(dump[expoOffset:expoOffset+4]) == "EXPO"
		for i, off := range XMP30ProfileOffsets {
			if expo && (i == 2 || i == 3) {
				continue
			}
			if !XMP30SlotPresent(dump, i) {
				continue
			}
			s := dump[off : off+64]
			c := Crc16(s[:62])
			s[62], s[63] = byte(c), byte(c>>8)
			touched = append(touched, off+62, off+63)
		}
		if expo {
			sec := dump[expoOffset : expoOffset+expoLen]
			c := Crc16(sec[:126])
			sec[126], sec[127] = byte(c), byte(c>>8)
			touched = append(touched, expoOffset+126, expoOffset+127)
		}
		return touched, nil
	default:
		return nil, fmt.Errorf("%v 无 CRC 定义", rt)
	}
}

// ddr2Checksum 计算 DDR2 SPD 校验和: byte63 = sum(bytes 0..62) & 0xFF。
func ddr2Checksum(dump []byte) byte {
	sum := byte(0)
	for _, v := range dump[:63] {
		sum += v
	}
	return sum
}
