package app

import (
	"os"
	"testing"

	"spdrw/internal/eeprom"
)

// TestMain 两件事:
//
//  1. 把逐字节读间隔清零: 那是真机需要的东西(DDR5 HUB 对背靠背事务敏感),
//     在纯内存 Fake 上只是白等。语料级端到端测试因此从 5 分钟降到几十毫秒。
//  2. 把 HOME/USERPROFILE 指到系统临时目录: 历史保护 —— 旧版备份目录在
//     `~/.spdrw/backups`(os.UserHomeDir), 不隔离就会往开发者的真实家目录里灌测试 dump
//     —— 更糟的是 HOME 被外部设成相对路径时, 仓库里会冒出 `.tmphome/` 并被误提交
//     (真发生过)。v1.0.1 起备份目录改到测试二进制同级的 backup\(系统临时目录),
//     此隔离保留作兜底(遥测等仍可能写家目录)。
func TestMain(m *testing.M) {
	eeprom.ReadDelay = 0
	home, err := os.MkdirTemp("", "spdrw-test-home-")
	if err == nil {
		_ = os.Setenv("HOME", home)
		_ = os.Setenv("USERPROFILE", home) // Windows 的 UserHomeDir 走这个
	}
	code := m.Run() // 注意: os.Exit 不执行 defer, 清理要显式写
	if err == nil {
		_ = os.RemoveAll(home)
	}
	os.Exit(code)
}
