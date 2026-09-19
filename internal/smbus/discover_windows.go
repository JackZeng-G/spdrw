//go:build windows

package smbus

// DiscoverBackends 是 PawnIO 枚举的对外名称。
func DiscoverBackends() ([]Transport, error) { return Discover() }
