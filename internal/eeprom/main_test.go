package eeprom

import (
	"os"
	"testing"
)

// TestMain 关闭逐字节读间隔(真机才需要), 让协议层测试秒级跑完。
func TestMain(m *testing.M) {
	ReadDelay = 0
	os.Exit(m.Run())
}
