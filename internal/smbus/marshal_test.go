package smbus

import (
	"errors"
	"strings"
	"testing"
)

func TestMarshalXfer(t *testing.T) {
	cases := []struct {
		name  string
		addr  byte
		write bool
		cmd   byte
		proto byte
		data  []byte
		want  [][2]uint64 // {索引, 期望值} 对
	}{
		{"quick-read", 0x50, false, 0, ProtoQuick, nil, [][2]uint64{{0, 0x50}, {1, uint64(FlagRead)}}},
		{"quick-write-spa0", 0x36, true, 0, ProtoQuick, nil, [][2]uint64{{0, 0x36}, {1, uint64(FlagWrite)}}},
		{"bytedata-read", 0x50, false, 0x12, ProtoByteData, nil, [][2]uint64{{0, 0x50}, {2, 0x12}, {3, uint64(ProtoByteData)}, {1, uint64(FlagRead)}}},
		{"bytedata-write", 0x50, true, 0x34, ProtoByteData, []byte{0xAB}, [][2]uint64{{0, 0x50}, {2, 0x34}, {3, uint64(ProtoByteData)}, {4, 0xAB}, {1, uint64(FlagWrite)}}},
		{"worddata-read", 0x51, false, 0x07, ProtoWordData, nil, [][2]uint64{{0, 0x51}, {2, 0x07}, {3, uint64(ProtoWordData)}, {1, uint64(FlagRead)}}},
		{"worddata-write", 0x51, true, 0x07, ProtoWordData, []byte{0xCD, 0xAB}, [][2]uint64{{0, 0x51}, {2, 0x07}, {3, uint64(ProtoWordData)}, {4, 0xABCD}, {1, uint64(FlagWrite)}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := MarshalXfer(c.addr, c.write, c.cmd, c.proto, c.data)
			if err != nil {
				t.Fatalf("MarshalXfer: %v", err)
			}
			if len(got) != XferInSize {
				t.Fatalf("in_size = %d, want %d", len(got), XferInSize)
			}
			for _, kv := range c.want {
				if got[kv[0]] != kv[1] {
					t.Errorf("in[%d] = %#x, want %#x (full: %v)", kv[0], got[kv[0]], kv[1], got)
				}
			}
		})
	}
}

// 块写入参不允许被静默截断: 模块只认前 33 字节, 多出来的必须报错,
// 否则调用方会以为 40 字节都写进去了。
func TestMarshalXferRejectsOverlongBlockWrite(t *testing.T) {
	payload := make([]byte, 0, 34)
	payload = append(payload, 33) // 长度字节声称 33 > 协议上限 32
	payload = append(payload, make([]byte, 33)...)
	if _, err := MarshalXfer(0x50, true, 0, ProtoBlockData, payload); err == nil {
		t.Fatal("长度字节 33 应被拒绝(协议上限 32)")
	}
	over := append([]byte{40}, make([]byte, 40)...) // 41 字节, 远超 33
	if _, err := MarshalXfer(0x50, true, 0, ProtoBlockData, over); err == nil ||
		!strings.Contains(err.Error(), "超出上限") {
		t.Fatalf("超长块写应报错, got %v", err)
	}
	// 长度字节与实际数据不符也要拒绝(会让器件按错误字节数读)
	if _, err := MarshalXfer(0x50, true, 0, ProtoBlockData, []byte{4, 1, 2}); err == nil {
		t.Fatal("长度字节与实际数据不符应被拒绝")
	}
	// 未知协议不能静默跳过
	if _, err := MarshalXfer(0x50, true, 0, 0x7F, nil); err == nil {
		t.Fatal("未知协议应报错")
	}
	// 上限内的 32 字节块写要能正确铺进 5 个 cell
	ok := make([]byte, 0, 33)
	ok = append(ok, 32)
	for i := 0; i < 32; i++ {
		ok = append(ok, byte(i+1))
	}
	got, err := MarshalXfer(0x50, true, 0, ProtoBlockData, ok)
	if err != nil {
		t.Fatalf("32 字节块写应通过: %v", err)
	}
	if byte(got[4]) != 32 || byte(got[4]>>8) != 1 || byte(got[8]>>(8*0)) != 32 {
		t.Fatalf("块写铺字节错误: %v", got)
	}
}

func TestUnmarshalOut(t *testing.T) {
	b, err := UnmarshalOut([]uint64{0x5A}, ProtoByteData)
	if err != nil || len(b) != 1 || b[0] != 0x5A {
		t.Fatalf("byte out: %v %v", b, err)
	}
	w, err := UnmarshalOut([]uint64{0xABCD}, ProtoWordData)
	if err != nil || len(w) != 2 || w[0] != 0xCD || w[1] != 0xAB {
		t.Fatalf("word out: %v %v", w, err)
	}
	if _, err := UnmarshalOut(nil, ProtoByteData); err == nil {
		t.Fatal("空输出应报错")
	}
}

func TestStatusToError(t *testing.T) {
	if err := StatusToError(0); err != nil {
		t.Fatalf("0 应为成功, got %v", err)
	}
	if err := StatusToError(ntStatusNoSuchDevice); !strings.Contains(err.Error(), "NACK") {
		t.Fatalf("NACK 映射错误: %v", err)
	}
	if err := StatusToError(0x80070005); !strings.Contains(err.Error(), "管理员") {
		t.Fatalf("ACCESS_DENIED 映射错误: %v", err)
	}
	if err := StatusToError(0x80070002); !strings.Contains(err.Error(), "PawnIO") {
		t.Fatalf("FILE_NOT_FOUND 映射错误: %v", err)
	}
	if err := StatusToError(0xDEADBEEF); err == nil || errors.Is(err, nil) {
		t.Fatal("未知码应返回非 nil 错误")
	}
}
