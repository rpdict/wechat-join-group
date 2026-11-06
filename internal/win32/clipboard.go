package win32

import (
	"errors"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	CF_UNICODETEXT = 13
	GMEM_MOVEABLE  = 0x0002

	VK_A      = 0x41
	VK_V      = 0x56
	VK_RETURN = 0x0D
)

var (
	procOpenClipboard    = user32.NewProc("OpenClipboard")
	procEmptyClipboard   = user32.NewProc("EmptyClipboard")
	procSetClipboardData = user32.NewProc("SetClipboardData")
	procCloseClipboard   = user32.NewProc("CloseClipboard")

	kernel32          = windows.NewLazySystemDLL("kernel32.dll")
	procGlobalAlloc   = kernel32.NewProc("GlobalAlloc")
	procGlobalLock    = kernel32.NewProc("GlobalLock")
	procGlobalUnlock  = kernel32.NewProc("GlobalUnlock")
	procGlobalFree    = kernel32.NewProc("GlobalFree")
	procRtlMoveMemory = kernel32.NewProc("RtlMoveMemory")
)

// SetClipboardText 将文本写入 Windows 剪贴板（CF_UNICODETEXT）。
func SetClipboardText(text string) error {
	utf16, err := windows.UTF16FromString(text)
	if err != nil {
		return err
	}

	opened := false
	for i := 0; i < 20; i++ {
		if r, _, _ := procOpenClipboard.Call(0); r == 0 {
			time.Sleep(40 * time.Millisecond)
			continue
		}
		if r, _, _ := procEmptyClipboard.Call(); r != 0 {
			opened = true
			break
		}
		procCloseClipboard.Call()
		time.Sleep(40 * time.Millisecond)
	}
	if !opened {
		return errors.New("打开或清空剪贴板失败")
	}
	defer procCloseClipboard.Call()

	size := uintptr(len(utf16) * 2)
	h, _, _ := procGlobalAlloc.Call(GMEM_MOVEABLE, size)
	if h == 0 {
		return errors.New("分配剪贴板内存失败")
	}
	p, _, _ := procGlobalLock.Call(h)
	if p == 0 {
		procGlobalFree.Call(h)
		return errors.New("锁定剪贴板内存失败")
	}
	procRtlMoveMemory.Call(p, uintptr(unsafe.Pointer(&utf16[0])), size)
	procGlobalUnlock.Call(h)

	if r, _, _ := procSetClipboardData.Call(CF_UNICODETEXT, h); r == 0 {
		procGlobalFree.Call(h)
		return errors.New("写入剪贴板失败")
	}
	return nil
}

// CtrlKey 发送 Ctrl+指定按键。
func CtrlKey(vk byte) {
	key(VK_CONTROL, false)
	time.Sleep(25 * time.Millisecond)
	key(vk, false)
	time.Sleep(25 * time.Millisecond)
	key(vk, true)
	time.Sleep(25 * time.Millisecond)
	key(VK_CONTROL, true)
}

// PressKey 发送一次按键（按下并释放）。
func PressKey(vk byte) {
	key(vk, false)
	time.Sleep(25 * time.Millisecond)
	key(vk, true)
}
