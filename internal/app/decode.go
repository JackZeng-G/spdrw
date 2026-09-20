package app

import (
	"fmt"

	"spdrw/internal/spd"
)

// TimingView 是时序的展示结构。
type TimingView struct {
	NS     float64 `json:"ns"`
	Cycles int     `json:"cycles"`
}

// DecodeResult 是信息面板的完整载荷(JSON 直出)。
type DecodeResult struct {
	Valid      bool   `json:"valid"`
	RamType    string `json:"ramType"`
	Size       int    `json:"size"`
	ModuleType string `json:"moduleType"`

	// Path 是来源文件路径(DecodeFileDialog 填充; 在线解码为空)。
	Path string `json:"path,omitempty"`

	// 容量
	TotalMib    uint64 `json:"totalMib"`
	TotalHuman  string `json:"totalHuman"`
	Ranks       byte   `json:"ranks"`
	DeviceWidth byte   `json:"deviceWidth"`
	BusWidth    byte   `json:"busWidth"`

	// 身份
	Manufacturer string `json:"manufacturer"`
	PartNumber   string `json:"partNumber"`
	DateYear     int    `json:"dateYear"`
	DateWeek     int    `json:"dateWeek"`
	SerialHex    string `json:"serialHex"`

	// 时序(DDR4 详尽; DDR5 原版不含时序字段)
	HasTimings bool        `json:"hasTimings"`
	TCK        *TimingView `json:"tck,omitempty"`
	TAA        *TimingView `json:"taa,omitempty"`
	TRCD       *TimingView `json:"trcd,omitempty"`
	TRP        *TimingView `json:"trp,omitempty"`
	TRAS       *TimingView `json:"tras,omitempty"`
	TRC        *TimingView `json:"trc,omitempty"`
	TRFC1      *TimingView `json:"trfc1,omitempty"`
	TRFC2      *TimingView `json:"trfc2,omitempty"`
	TRFC4      *TimingView `json:"trfc4,omitempty"`
	TFAW       *TimingView `json:"tfaw,omitempty"`
	TRRDS      *TimingView `json:"trrdS,omitempty"`
	TRRDL      *TimingView `json:"trrdL,omitempty"`
	TCCDL      *TimingView `json:"tccdL,omitempty"`
	TWR        *TimingView `json:"twr,omitempty"`
	CasLat     string      `json:"casLatencies,omitempty"`

	// CRC
	CRCOK bool `json:"crcOk"`

	// Profile
	HasXMP  bool       `json:"hasXmp"`
	HasEXPO bool       `json:"hasExpo"`
	XMP     []XMPEntry `json:"xmp,omitempty"`

	// 旧代基本信息
	Basic *spd.Basic `json:"basic,omitempty"`

	// DDR5 JEDEC 时序(JEDEC DDR5 SPD byte 20-102)
	DDR5Timings []TimingEntry `json:"ddr5Timings,omitempty"`
}

// TimingEntry 是一条已换算的时序项(ns + 相对 tCK 的周期数)。
type TimingEntry struct {
	Name   string  `json:"name"`
	NS     float64 `json:"ns"`
	PS     int     `json:"ps,omitempty"` // 原始皮秒值(仅 ps 单位项)
	Cycles int     `json:"cycles"`
	Lower  int     `json:"lower,omitempty"` // lower limit 计数(0 = 无)
}

// XMPEntry 是一份 profile 的展示数据。
type XMPEntry struct {
	Number  byte   `json:"number"`
	Enabled bool   `json:"enabled"`
	Version byte   `json:"version"`
	Summary string `json:"summary"` // "1600 MHz 9-9-9-24 1.65V"
	CasLat  string `json:"casLatencies"`
}

// DecodeDump 解析 dump 生成 DecodeResult。
func DecodeDump(dump []byte) (*DecodeResult, error) {
	rt, size, err := spd.Identify(dump)
	if err != nil {
		return nil, err
	}
	r := &DecodeResult{Valid: spd.ValidateSpd(dump), RamType: rt.String(), Size: size}

	switch {
	case rt == spd.DDR4 || rt == spd.DDR4E || rt == spd.LPDDR3 || rt == spd.LPDDR4 || rt == spd.LPDDR4X:
		decodeDDR4(dump, r)
	case rt == spd.DDR5 || rt == spd.LPDDR5 || rt == spd.LPDDR5X || rt == spd.DDR5NVDIMMP:
		decodeDDR5(dump, r)
	case rt == spd.DDR3 || rt == spd.DDR2 || rt == spd.DDR || rt == spd.DDR2FBDIMM:
		b, err := spd.ParseBasic(dump)
		if err != nil {
			return nil, err
		}
		r.Basic = b
		r.ModuleType = b.ModuleType
		r.TotalMib = b.BytesMib
		r.TotalHuman = humanMib(b.BytesMib)
		r.Ranks = b.Ranks
		r.DeviceWidth = b.DeviceWidth
		r.BusWidth = b.BusWidthBits
		r.Manufacturer = b.Manufacturer
		r.PartNumber = b.PartNumber
		r.DateYear, r.DateWeek = b.DateYear, b.DateWeek
		r.SerialHex = b.SerialHex
		r.CRCOK = b.CRCOK
		if b.TCKminNS > 0 {
			r.HasTimings = true
			r.TCK = &TimingView{NS: b.TCKminNS}
		}
	default:
		return nil, fmt.Errorf("不支持的世代 %v", rt)
	}
	return r, nil
}

func decodeDDR4(dump []byte, r *DecodeResult) {
	d, err := spd.NewDDR4(dump)
	if err != nil {
		return
	}
	r.ModuleType = d.ModuleType()
	_, ranks, width := d.Organization()
	_, bus := d.BusWidth()
	r.Ranks = ranks
	r.DeviceWidth = width
	r.BusWidth = bus
	r.TotalMib = d.TotalCapacityBytes() / 1024 / 1024
	r.TotalHuman = humanMib(r.TotalMib)
	r.Manufacturer, _, _ = d.Manufacturer()
	r.PartNumber = d.PartNumber()
	r.DateYear, r.DateWeek = d.DateCode()
	sn := d.SerialNumber()
	r.SerialHex = fmt.Sprintf("%02X%02X%02X%02X", sn[0], sn[1], sn[2], sn[3])
	r.CRCOK = d.CRCOK()

	tb := d.Timebase()
	tck := d.TCKAVGmin()
	r.HasTimings = true
	r.TCK = timingView(tck, tb, nil)
	r.TAA = timingView(d.TAAmin(), tb, &tck)
	r.TRCD = timingView(d.TRCDmin(), tb, &tck)
	r.TRP = timingView(d.TRPmin(), tb, &tck)
	r.TRAS = timingView(d.TRASmin(), tb, &tck)
	r.TRC = timingView(d.TRCmin(), tb, &tck)
	r.TRFC1 = timingView(d.TRFC1(), tb, nil)
	r.TRFC2 = timingView(d.TRFC2(), tb, nil)
	r.TRFC4 = timingView(d.TRFC4(), tb, nil)
	r.TFAW = timingView(d.TFAW(), tb, nil)
	r.TRRDS = timingView(d.TRRDS(), tb, nil)
	r.TRRDL = timingView(d.TRRDL(), tb, nil)
	r.TCCDL = timingView(d.TCCDL(), tb, nil)
	r.TWR = timingView(d.TWR(), tb, nil)
	r.CasLat = d.CasLatencies().String()

	if d.XMPPresence() {
		r.HasXMP = true
		for _, p := range d.XMPProfiles() {
			r.XMP = append(r.XMP, XMPEntry{
				Number: p.Number, Enabled: p.Enabled, Version: p.Version,
				Summary: spd.XMPString(p, tb),
				CasLat:  p.CasLat.String(),
			})
		}
	}
}

func decodeDDR5(dump []byte, r *DecodeResult) {
	d, err := spd.NewDDR5(dump)
	if err != nil {
		return
	}
	r.ModuleType = d.ModuleType()
	_, ranks := d.Organization()
	ch, ext, primary := d.ChannelBusWidth()
	_ = ext
	r.Ranks = ranks
	r.DeviceWidth = d.DeviceWidth()
	r.TotalMib = d.TotalCapacityBytes() * 1024 // 公式单位为 GiB
	r.TotalHuman = humanMib(r.TotalMib)
	r.Manufacturer, _, _ = d.Manufacturer()
	r.PartNumber = d.PartNumber()
	r.DateYear, r.DateWeek = d.DateCode()
	sn := d.SerialNumber()
	r.SerialHex = fmt.Sprintf("%02X%02X%02X%02X", sn[0], sn[1], sn[2], sn[3])
	r.CRCOK = d.CRCOK()
	r.HasXMP = d.XMPPresence()
	r.HasEXPO = d.EXPOPresence()
	if ch > 0 {
		r.BusWidth = primary
	}

	// JEDEC 时序(旧版完全不解析 DDR5 时序)
	t := d.Timings()
	if t.TCKMinPS > 0 {
		r.HasTimings = true
		tck := t.TCKMinPS
		ps := func(name string, v int) TimingEntry {
			e := TimingEntry{Name: name, PS: v, NS: float64(v) / 1000}
			if tck > 0 {
				e.Cycles = int((e.NS + float64(tck)/1000 - 1e-9) / (float64(tck) / 1000))
			}
			return e
		}
		ns := func(name string, v int) TimingEntry {
			e := TimingEntry{Name: name, NS: float64(v)}
			if tck > 0 {
				e.Cycles = int((e.NS + float64(tck)/1000 - 1e-9) / (float64(tck) / 1000))
			}
			return e
		}
		r.DDR5Timings = []TimingEntry{
			ps("tCKAVGmin", t.TCKMinPS),
			ps("tAA", t.TAA),
			ps("tRCD", t.TRCD),
			ps("tRP", t.TRP),
			ps("tRAS", t.TRAS),
			ps("tRC", t.TRC),
			ps("tWR", t.TWR),
			ns("tRFC1(SLR)", t.RFC1SLR),
			ns("tRFC2(SLR)", t.RFC2SLR),
			ns("tRFCsb(SLR)", t.RFCSbSLR),
			ns("tRFC1(DLR)", t.RFC1DLR),
			ns("tRFC2(DLR)", t.RFC2DLR),
			ns("tRFCsb(DLR)", t.RFCSbDLR),
			withLower(ps("tRRD_L", t.TRRDL), t.Limits["tRRD_L"]),
			withLower(ps("tCCD_L", t.TCCDL), t.Limits["tCCD_L"]),
			withLower(ps("tCCD_L_WR", t.TCCDLWR), t.Limits["tCCD_L_WR"]),
			withLower(ps("tCCD_L_WR2", t.TCCDLWR2), t.Limits["tCCD_L_WR2"]),
			withLower(ps("tFAW", t.TFAW), t.Limits["tFAW"]),
			withLower(ps("tCCD_L_WTR", t.TCCDLWTR), t.Limits["tCCD_L_WTR"]),
			withLower(ps("tCCD_S_WTR", t.TCCDSWTR), t.Limits["tCCD_S_WTR"]),
			withLower(ps("tRTP", t.TRTP), t.Limits["tRTP"]),
		}
		r.CasLat = ""
		for i, cl := range t.CL {
			if i > 0 {
				r.CasLat += ","
			}
			r.CasLat += fmt.Sprintf("%d", cl)
		}
	}
}

func withLower(e TimingEntry, lower int) TimingEntry {
	e.Lower = lower
	return e
}

func timingView(t spd.Timing, tb spd.Timebase, ref *spd.Timing) *TimingView {
	v := &TimingView{NS: t.NanoSeconds(tb)}
	if ref != nil {
		v.Cycles = t.ClockCycles(tb, *ref)
	}
	return v
}

func humanMib(mib uint64) string {
	switch {
	case mib >= 1024:
		gib := mib / 1024
		if mib%1024 != 0 {
			return fmt.Sprintf("%d.%d GiB", gib, (mib%1024)*10/1024)
		}
		return fmt.Sprintf("%d GiB", gib)
	default:
		return fmt.Sprintf("%d MiB", mib)
	}
}
