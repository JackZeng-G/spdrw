package smbus

import (
	"sync"
)

// CountingTransport 统计经过它的总线事务, 供"干跑零写入"这类断言与真机验证使用。
//
// 为什么需要它: 光是"代码里没写"不算证据 —— 真机验证时要能从程序里读到
// "本次操作下发了多少读、多少页选择、多少字节写", 并区分出**对 SPD NVM 的写**。
// 只做计数与转发, 不改变任何语义。
type CountingTransport struct {
	Inner Transport

	mu       sync.Mutex
	reads    int
	quickW   int
	byteData int
	byteNoD  int
	// writeLog 记录最近若干次写事务(地址/cmd), 用于事后排查"到底写了哪些字节"。
	writeLog []WriteOp
	// nvmWrites 是**独立累计**的 NVM 字节写计数。
	//
	// 不能只靠 writeLog 去数: 日志有上限(countingWriteLogLimit), force 模式写
	// 1024 字节时末尾会被截掉, 于是"NVM 写"这个证据会少报(M7 审计项)。
	nvmWrites int
	// ddr5 决定 NVM 判据(cmd bit7); SetDDR5 由设备层同步。
	ddr5 bool
}

const countingWriteLogLimit = 512

// NewCounting 包装一个 Transport 并开始计数。
func NewCounting(inner Transport) *CountingTransport {
	return &CountingTransport{Inner: inner}
}

// BusStats 是累计的事务计数。
type BusStats struct {
	Reads          int `json:"reads"`
	QuickWrites    int `json:"quickWrites"`
	ByteDataWrites int `json:"byteDataWrites"`
	ByteWrites     int `json:"byteWrites"`
}

// Stats 返回累计计数。
func (c *CountingTransport) Stats() BusStats {
	c.mu.Lock()
	defer c.mu.Unlock()
	return BusStats{
		Reads: c.reads, QuickWrites: c.quickW,
		ByteDataWrites: c.byteData, ByteWrites: c.byteNoD,
	}
}

// Reset 清零计数与写日志。
func (c *CountingTransport) Reset() {
	c.mu.Lock()
	c.reads, c.quickW, c.byteData, c.byteNoD = 0, 0, 0, 0
	c.nvmWrites = 0
	c.writeLog = nil
	c.mu.Unlock()
}

// SetDDR5 同步"当前设备是不是 DDR5": 既用于按正确判据累计 NVM 写,
// 也透传给内层传输(写周期判定要用)。
func (c *CountingTransport) SetDDR5(on bool) {
	c.mu.Lock()
	c.ddr5 = on
	c.mu.Unlock()
	SetTransportDDR5(c.Inner, on)
}

// NVMWriteCount 返回**精确累计**的 NVM 字节写次数(不受日志截断影响)。
func (c *CountingTransport) NVMWriteCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.nvmWrites
}

// noteNVM 按世代判据在写事务发生时累计 NVM 写。
func (c *CountingTransport) noteNVM(cmd byte) {
	c.mu.Lock()
	if c.ddr5 {
		if cmd&0x80 != 0 {
			c.nvmWrites++
		}
	} else {
		c.nvmWrites++ // 调用点只对 0x50-0x57 的 byte-data 写调用
	}
	c.mu.Unlock()
}

// WriteLog 返回最近的写事务(地址/cmd/值)。
func (c *CountingTransport) WriteLog() []WriteOp {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]WriteOp, len(c.writeLog))
	copy(out, c.writeLog)
	return out
}

func (c *CountingTransport) logWrite(addr, cmd, val byte) {
	c.mu.Lock()
	c.writeLog = append(c.writeLog, WriteOp{Addr: addr, Cmd: cmd, Val: val})
	if len(c.writeLog) > countingWriteLogLimit {
		c.writeLog = c.writeLog[len(c.writeLog)-countingWriteLogLimit:]
	}
	c.mu.Unlock()
}

func (c *CountingTransport) Identity() (Controller, error) { return c.Inner.Identity() }

func (c *CountingTransport) Quick(addr byte, write bool) error {
	if write {
		c.mu.Lock()
		c.quickW++
		c.mu.Unlock()
		c.logWrite(addr, 0, 0)
	}
	return c.Inner.Quick(addr, write)
}

func (c *CountingTransport) ReadByteData(addr byte, cmd byte) (byte, error) {
	c.mu.Lock()
	c.reads++
	c.mu.Unlock()
	return c.Inner.ReadByteData(addr, cmd)
}

func (c *CountingTransport) WriteByteData(addr byte, cmd byte, val byte) error {
	c.mu.Lock()
	c.byteData++
	c.mu.Unlock()
	c.logWrite(addr, cmd, val)
	if addr >= 0x50 && addr <= 0x57 {
		c.noteNVM(cmd)
	}
	return c.Inner.WriteByteData(addr, cmd, val)
}

func (c *CountingTransport) WriteByteNoData(addr byte) error {
	c.mu.Lock()
	c.byteNoD++
	c.mu.Unlock()
	c.logWrite(addr, 0, 0)
	return c.Inner.WriteByteNoData(addr)
}

// ReadBlockData 计入读事务(块读一次顶 32 次字节读, 计数上仍算 1 次)。
func (c *CountingTransport) ReadBlockData(addr byte, cmd byte) ([]byte, error) {
	c.mu.Lock()
	c.reads++
	c.mu.Unlock()
	return c.Inner.ReadBlockData(addr, cmd)
}

// WriteBlockData 计入字节写事务(块写一次顶多次字节写, 计数上仍算 1 次)。
func (c *CountingTransport) WriteBlockData(addr byte, cmd byte, data []byte) error {
	c.mu.Lock()
	c.byteData++
	c.mu.Unlock()
	c.logWrite(addr, cmd, byte(len(data)))
	if addr >= 0x50 && addr <= 0x57 {
		c.noteNVM(cmd)
	}
	return c.Inner.WriteBlockData(addr, cmd, data)
}

func (c *CountingTransport) ReadWordData(addr byte, cmd byte) (uint16, error) {
	c.mu.Lock()
	c.reads++
	c.mu.Unlock()
	return c.Inner.ReadWordData(addr, cmd)
}

func (c *CountingTransport) Close() error { return c.Inner.Close() }

// InnerTuner 把"总线调优"能力从内层透出来(包装器自己不实现 Tuner)。
func (c *CountingTransport) InnerTuner() (Tuner, bool) { return TunerOf(c.Inner) }

// NVMWrites 从写日志里挑出"真正写到 SPD NVM 窗口"的字节写, 依据该世代的语义:
//   - DDR5: 写 MR 寄存器/切页时 cmd bit7=0(寄存器区), 访问 NVM 时 cmd bit7=1
//   - DDR4 及更早: byte-data 写的 cmd 就是页内偏移(NVM), 但 quick 命令(页选择/保护命令)
//     与 byte 无数据写(0x30-0x37 PSWP/SPA 探测)不算 NVM 写
//
// ddr5 由调用方按当前设备世代传入。
func NVMWrites(ops []WriteOp, ddr5 bool) []WriteOp {
	var out []WriteOp
	for _, op := range ops {
		if ddr5 {
			if op.Cmd&0x80 != 0 {
				out = append(out, op)
			}
			continue
		}
		// DDR4/更早: 只有 byte-data / 块写才是 NVM 数据写。WriteLog 里 quick 与
		// byte 无数据写都记成 Cmd=0/Val=0, 排除它们即可 —— 否则会虚报 NVM 写
		// (审计指出的假阳性: "干跑零 NVM 写"这类证据会被污染)。
		if op.Addr < 0x50 || op.Addr > 0x57 {
			continue
		}
		if op.Cmd == 0 && op.Val == 0 {
			continue
		}
		out = append(out, op)
	}
	return out
}
