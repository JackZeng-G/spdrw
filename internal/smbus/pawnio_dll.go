//go:build windows

package smbus

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"syscall"
	"unsafe"

	"spdrw/internal/assets"

	"golang.org/x/sys/windows"
)

// PawnIOLib.dll 导出函数(pawnio_*, 见 PawnIOLib.h / third_party/pawnio/README.md)。
var (
	modPawnIO         *windows.LazyDLL
	procPawnIOVersion *windows.LazyProc
	procPawnIOOpen    *windows.LazyProc
	procPawnIOLoad    *windows.LazyProc
	procPawnIOExecute *windows.LazyProc
	procPawnIOClose   *windows.LazyProc

	pawnIOLoadOnce sync.Once
	pawnIOLoadErr  error
)

const dllName = "PawnIOLib.dll"

// ensureDLL 加载 PawnIOLib.dll: 优先系统副本,否则释放内嵌副本到用户目录再加载。
func ensureDLL() error {
	pawnIOLoadOnce.Do(func() {
		if err := findSystemDLL(); err == nil {
			modPawnIO = windows.NewLazyDLL(dllName)
		} else {
			path, err2 := extractDLL()
			if err2 != nil {
				pawnIOLoadErr = err2
				return
			}
			modPawnIO = windows.NewLazyDLL(path)
		}
		procPawnIOVersion = modPawnIO.NewProc("pawnio_version")
		procPawnIOOpen = modPawnIO.NewProc("pawnio_open")
		procPawnIOLoad = modPawnIO.NewProc("pawnio_load")
		procPawnIOExecute = modPawnIO.NewProc("pawnio_execute")
		procPawnIOClose = modPawnIO.NewProc("pawnio_close")
		if err := procPawnIOVersion.Find(); err != nil {
			pawnIOLoadErr = fmt.Errorf("未找到 PawnIO 用户态库: %w (请安装 PawnIO: https://pawnio.eu/)", err)
			return
		}
		var ver uint32
		r1, _, _ := procPawnIOVersion.Call(uintptr(unsafe.Pointer(&ver)))
		if r1 != 0 {
			pawnIOLoadErr = StatusToError(uint32(r1))
			return
		}
	})
	return pawnIOLoadErr
}

func findSystemDLL() error {
	sys := os.Getenv("SystemRoot")
	if sys == "" {
		sys = `C:\Windows`
	}
	for _, dir := range []string{filepath.Join(sys, "System32"), "."} {
		if _, err := os.Stat(filepath.Join(dir, dllName)); err == nil {
			return nil
		}
	}
	return fmt.Errorf("%s 不在系统目录", dllName)
}

func extractDLL() (string, error) {
	local, err := windows.KnownFolderPath(windows.FOLDERID_LocalAppData, windows.KF_FLAG_DEFAULT)
	if err != nil {
		local = os.TempDir()
	}
	dir := filepath.Join(local, "spdrw")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("无法创建 %s: %w", dir, err)
	}
	path := filepath.Join(dir, dllName)
	if err := os.WriteFile(path, assets.MustBytes(dllName), 0o644); err != nil {
		return "", fmt.Errorf("释放 %s 失败: %w", path, err)
	}
	return path, nil
}

// pawnioSession 是一个已加载某模块的 PawnIO 执行器句柄。
type pawnioSession struct {
	handle windows.Handle
}

func pawnioOpen() (*pawnioSession, error) {
	if err := ensureDLL(); err != nil {
		return nil, err
	}
	var h windows.Handle
	r1, _, _ := procPawnIOOpen.Call(uintptr(unsafe.Pointer(&h)))
	if r1 != 0 {
		return nil, StatusToError(uint32(r1))
	}
	return &pawnioSession{handle: h}, nil
}

func (s *pawnioSession) load(blob []byte) error {
	if len(blob) == 0 {
		return fmt.Errorf("模块数据为空")
	}
	r1, _, _ := procPawnIOLoad.Call(
		uintptr(s.handle),
		uintptr(unsafe.Pointer(&blob[0])),
		uintptr(len(blob)))
	if r1 != 0 {
		return StatusToError(uint32(r1))
	}
	return nil
}

// execute 调用模块内公开函数; in/out 为 ULONG64 cell 数组,返回写入 out 的 cell 数。
func (s *pawnioSession) execute(name string, in []uint64, out []uint64) (uint64, error) {
	namePtr, err := syscall.BytePtrFromString(name)
	if err != nil {
		return 0, err
	}
	var inPtr, outPtr unsafe.Pointer
	var inSize, outSize uintptr
	if len(in) > 0 {
		inPtr = unsafe.Pointer(&in[0])
		inSize = uintptr(len(in))
	}
	if len(out) > 0 {
		outPtr = unsafe.Pointer(&out[0])
		outSize = uintptr(len(out))
	}
	var ret uintptr
	r1, _, _ := procPawnIOExecute.Call(
		uintptr(s.handle),
		uintptr(unsafe.Pointer(namePtr)),
		uintptr(unsafe.Pointer(inPtr)), inSize,
		uintptr(unsafe.Pointer(outPtr)), outSize,
		uintptr(unsafe.Pointer(&ret)))
	if r1 != 0 {
		return 0, StatusToError(uint32(r1))
	}
	return uint64(ret), nil
}

func (s *pawnioSession) close() {
	_, _, _ = procPawnIOClose.Call(uintptr(s.handle))
}

// 全局 SMBus 互斥体(与 Thaiphoon/OpenRGB 仲裁同一总线)。
var (
	smbusMutex     windows.Handle
	smbusMutexOnce sync.Once
)

// lockSMBus 获取跨进程 SMBus 仲裁互斥。
// 关键: WaitForSingleObject 与 ReleaseMutex 必须在同一 OS 线程上执行
// (Windows mutex 的所有权属于线程), 否则 Go 调度器在两次 syscall 之间
// 把 goroutine 换到别的线程时 ReleaseMutex 报 ERROR_NOT_OWNER,
// 互斥被遗弃, 之后所有等待都误报"被占用"。故 LockOSThread 包住临界区。
func lockSMBus() func() {
	smbusMutexOnce.Do(func() {
		name, _ := syscall.UTF16PtrFromString(`Global\Access_SMBUS.HTP.Method`)
		h, err := windows.CreateMutex(nil, false, name)
		if err == nil {
			smbusMutex = h
		}
	})
	if smbusMutex == 0 {
		return func() {}
	}
	runtime.LockOSThread()
	const lockTimeoutMs = 2000
	ev, err := windows.WaitForSingleObject(smbusMutex, lockTimeoutMs)
	switch {
	case err != nil:
		runtime.UnlockOSThread()
		return nil
	case ev == windows.WAIT_OBJECT_0, ev == windows.WAIT_ABANDONED:
		// WAIT_ABANDONED: 前任 owner 线程已死, 系统把所有权让渡给本线程;
		// SMBus 事务是短临界区, 视为获取成功。
		return func() {
			_ = windows.ReleaseMutex(smbusMutex)
			runtime.UnlockOSThread()
		}
	default: // WAIT_TIMEOUT: 被其他工具长期占用
		runtime.UnlockOSThread()
		return nil
	}
}
