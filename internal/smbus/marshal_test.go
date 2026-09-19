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
			got := MarshalXfer(c.addr, c.write, c.cmd, c.proto, c.data)
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
