//go:build !windows

package smbus

import "fmt"

// DiscoverBackends 非 Windows 平台无 PawnIO 支持。
func DiscoverBackends() ([]Transport, error) {
	return nil, fmt.Errorf("当前平台不支持直连 SMBus; 本工具需在 Windows 上运行并安装 PawnIO (https://pawnio.eu/)")
}
