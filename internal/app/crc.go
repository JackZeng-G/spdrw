package app

import (
	"fmt"

	"spdrw/internal/spd"
)

// 校验状态查询: 前端把"左侧 hex 视图当前显示的字节"传进来, 后端据此给出
// 该内容是否通过校验、哪几段被校验覆盖、哪些区域改了不用重算。
//
// 为什么参数是 dump 而不是"读后端状态": 左侧视图可能来自设备、也可能来自
// 打开的文件, 还可能是编辑器的工作副本 —— 让调用方把正在显示的字节传进来,
// 显示与判定就永远一致, 不会出现"面板说 CRC 通过但屏幕上已经不是那份数据"。

// CRCStatusResult 是"给定内容"的校验状态。
type CRCStatusResult struct {
	Generation string         `json:"generation"`
	Size       int            `json:"size"`
	Known      bool           `json:"known"` // 该世代是否定义了校验(未知类型时 false)
	OK         bool           `json:"ok"`
	Covered    int            `json:"covered"`  // 参与校验的字节数
	CRCBytes   []int          `json:"crcBytes"` // 校验值所在的字节(hex 视图里标出来)
	Ranges     []spd.CRCRange `json:"ranges"`
	// FreeAreas 是身份区里不参与校验的字段(改了不用重算 CRC), 用于界面提示。
	FreeAreas []spd.Area `json:"freeAreas"`
	Note      string     `json:"note,omitempty"`
}

// CRCStatus 是**纯函数**(不碰设备/编辑器状态, 也不持操作锁), 因此不参与
// 操作串行化; 它只依赖调用方传入的字节。
func (a *App) CRCStatus(dump []int) (*CRCStatusResult, error) {
	if len(dump) == 0 {
		return nil, fmt.Errorf("内容为空, 无法校验")
	}
	buf := make([]byte, len(dump))
	for i, v := range dump {
		if v < 0 || v > 255 {
			return nil, fmt.Errorf("第 %d 个字节值 %d 超出 0-255", i, v)
		}
		buf[i] = byte(v)
	}
	res := &CRCStatusResult{Size: len(buf)}
	rt, size, err := spd.Identify(buf)
	if err != nil || len(buf) != size {
		res.Note = "无法识别 SPD 世代(长度或类型不符), 未做校验"
		return res, nil
	}
	res.Generation = rt.String()
	res.Known = true
	res.Ranges = spd.CRCRanges(buf)
	res.CRCBytes = spd.CRCBytes(res.Ranges)
	for _, r := range res.Ranges {
		res.Covered += r.End - r.Start
	}
	ok, err := spd.CRCOK(buf)
	res.OK = err == nil && ok
	res.FreeAreas = spd.UnprotectedIdentityAreas(rt, res.Ranges)
	if !res.OK {
		res.Note = "校验不通过: 数据区改动后必须重算校验(或该 dump 本身已损坏)"
	}
	return res, nil
}
