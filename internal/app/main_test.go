package app

import (
	"os"
	"testing"

	"spdrw/internal/eeprom"
)

// TestMain 把逐字节读间隔清零: 那是真机需要的东西(DDR5 HUB 对背靠背事务敏感),
// 在纯内存 Fake 上只是白等。语料级端到端测试因此从 5 分钟降到几十毫秒。
func TestMain(m *testing.M) {
	eeprom.ReadDelay = 0
	os.Exit(m.Run())
}
