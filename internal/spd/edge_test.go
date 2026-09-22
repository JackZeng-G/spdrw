package spd

import "testing"

// 审计补测: DDR/SDRAM 现在必须在 NewEditor 就被拒(不给半成品编辑器)
func TestNewEditorRejectsDDRandSDRAM(t *testing.T) {
	for _, b2 := range []byte{0x04, 0x07, 0x08, 0x09, 0x0A, 0x00} {
		d := make([]byte, 256)
		d[2] = b2
		if _, err := NewEditor(d); err == nil {
			t.Errorf("byte2=%#02x: NewEditor 应拒绝, 却成功了", b2)
		}
	}
	// DDR3 仍可正常打开
	d := make([]byte, 256)
	d[2] = 0x0B
	if _, err := NewEditor(d); err != nil {
		t.Fatalf("DDR3 应可打开: %v", err)
	}
}
