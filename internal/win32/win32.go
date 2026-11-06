package win32

import (
	"image"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"github.com/lxn/win"
	"golang.org/x/sys/windows"
)

const (
	MOUSEEVENTF_LEFTDOWN  = 0x0002
	MOUSEEVENTF_LEFTUP    = 0x0004
	MOUSEEVENTF_RIGHTDOWN = 0x0008
	MOUSEEVENTF_RIGHTUP   = 0x0010
	MOUSEEVENTF_WHEEL     = 0x0800

	KEYEVENTF_KEYUP = 0x0002

	VK_CONTROL = 0x11
	VK_MENU    = 0x12
	VK_W       = 0x57
	VK_ESCAPE  = 0x1B
)

var (
	user32                  = windows.NewLazySystemDLL("user32.dll")
	procMouseEvent          = user32.NewProc("mouse_event")
	procKeybdEvent          = user32.NewProc("keybd_event")
	procGetWindowTextW      = user32.NewProc("GetWindowTextW")
	procGetWindowTextLength = user32.NewProc("GetWindowTextLengthW")
	procSetProcessDPIAware  = user32.NewProc("SetProcessDPIAware")
	procGetDpiForWindow     = user32.NewProc("GetDpiForWindow")
)

// DPI 返回窗口所在显示器的 DPI（96 为 100% 缩放）。
func DPI(hwnd win.HWND) int {
	if dpi, _, _ := procGetDpiForWindow.Call(uintptr(hwnd)); dpi != 0 {
		return int(dpi)
	}
	return 96
}

type Window struct {
	HWND    win.HWND
	Class   string
	Title   string
	PID     uint32
	Process string
	Rect    image.Rectangle
	Visible bool
}

func InitDPI() {
	procSetProcessDPIAware.Call()
}

func SetCursorPos(x, y int) {
	win.SetCursorPos(int32(x), int32(y))
}

func Click(x, y int) {
	SetCursorPos(x, y)
	time.Sleep(90 * time.Millisecond)
	procMouseEvent.Call(MOUSEEVENTF_LEFTDOWN, 0, 0, 0, 0)
	time.Sleep(60 * time.Millisecond)
	procMouseEvent.Call(MOUSEEVENTF_LEFTUP, 0, 0, 0, 0)
}

func RightClick(x, y int) {
	SetCursorPos(x, y)
	time.Sleep(120 * time.Millisecond)
	procMouseEvent.Call(MOUSEEVENTF_RIGHTDOWN, 0, 0, 0, 0)
	time.Sleep(70 * time.Millisecond)
	procMouseEvent.Call(MOUSEEVENTF_RIGHTUP, 0, 0, 0, 0)
}

func WheelAt(x, y, delta int) {
	SetCursorPos(x, y)
	time.Sleep(40 * time.Millisecond)
	procMouseEvent.Call(MOUSEEVENTF_WHEEL, 0, 0, uintptr(uint32(int32(delta))), 0)
}

func key(vk byte, up bool) {
	flags := uintptr(0)
	if up {
		flags = KEYEVENTF_KEYUP
	}
	procKeybdEvent.Call(uintptr(vk), 0, flags, 0)
}

func Esc() {
	key(VK_ESCAPE, false)
	time.Sleep(30 * time.Millisecond)
	key(VK_ESCAPE, true)
}

func ToggleWeChat() {
	key(VK_CONTROL, false)
	key(VK_MENU, false)
	time.Sleep(30 * time.Millisecond)
	key(VK_W, false)
	time.Sleep(30 * time.Millisecond)
	key(VK_W, true)
	key(VK_MENU, true)
	key(VK_CONTROL, true)
}

func EnsureVisible(hwnd win.HWND) {
	for i := 0; i < 2; i++ {
		if win.IsWindowVisible(hwnd) {
			break
		}
		ToggleWeChat()
		time.Sleep(900 * time.Millisecond)
	}
	if win.IsIconic(hwnd) {
		win.ShowWindow(hwnd, win.SW_RESTORE)
		time.Sleep(300 * time.Millisecond)
	}
}

func Foreground(hwnd win.HWND) {
	EnsureVisible(hwnd)
	for i := 0; i < 3; i++ {
		if win.GetForegroundWindow() == hwnd {
			return
		}
		if win.SetForegroundWindow(hwnd) {
			time.Sleep(250 * time.Millisecond)
			return
		}
		key(VK_MENU, false)
		key(VK_MENU, true)
		time.Sleep(150 * time.Millisecond)
	}
	time.Sleep(200 * time.Millisecond)
}

// Show 显示窗口。
func Show(hwnd win.HWND) {
	win.ShowWindow(hwnd, win.SW_SHOW)
}

// Maximize 最大化窗口。
func Maximize(hwnd win.HWND) {
	win.ShowWindow(hwnd, win.SW_MAXIMIZE)
}

// SetRect 移动并调整窗口大小。
func SetRect(hwnd win.HWND, r image.Rectangle) {
	win.SetWindowPos(hwnd, 0, int32(r.Min.X), int32(r.Min.Y), int32(r.Dx()), int32(r.Dy()), win.SWP_NOZORDER|win.SWP_NOACTIVATE)
}

func ClassName(hwnd win.HWND) string {
	var buf [512]uint16
	win.GetClassName(hwnd, &buf[0], len(buf))
	return windows.UTF16ToString(buf[:])
}

func Title(hwnd win.HWND) string {
	n, _, _ := procGetWindowTextLength.Call(uintptr(hwnd))
	if n == 0 {
		return ""
	}
	buf := make([]uint16, n+1)
	procGetWindowTextW.Call(uintptr(hwnd), uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	return windows.UTF16ToString(buf)
}

func Rect(hwnd win.HWND) image.Rectangle {
	var r win.RECT
	if !win.GetWindowRect(hwnd, &r) {
		return image.Rectangle{}
	}
	return image.Rect(int(r.Left), int(r.Top), int(r.Right), int(r.Bottom))
}

func ProcessName(pid uint32) string {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return ""
	}
	defer windows.CloseHandle(h)
	buf := make([]uint16, windows.MAX_PATH)
	size := uint32(len(buf))
	if err := windows.QueryFullProcessImageName(h, 0, &buf[0], &size); err != nil {
		return ""
	}
	return filepath.Base(windows.UTF16ToString(buf[:size]))
}

func describe(hwnd win.HWND) Window {
	var pid uint32
	win.GetWindowThreadProcessId(hwnd, &pid)
	return Window{
		HWND:    hwnd,
		Class:   ClassName(hwnd),
		Title:   Title(hwnd),
		PID:     pid,
		Process: ProcessName(pid),
		Rect:    Rect(hwnd),
		Visible: win.IsWindowVisible(hwnd),
	}
}

func EnumWindows() []Window {
	var out []Window
	cb := syscall.NewCallback(func(hwnd uintptr, lParam uintptr) uintptr {
		out = append(out, describe(win.HWND(hwnd)))
		return 1
	})
	windows.EnumWindows(cb, nil)
	return out
}

func ChildWindows(parent win.HWND) []Window {
	var out []Window
	cb := syscall.NewCallback(func(hwnd uintptr, lParam uintptr) uintptr {
		out = append(out, describe(win.HWND(hwnd)))
		return 1
	})
	win.EnumChildWindows(parent, cb, 0)
	return out
}

func IsWeChatWindow(w Window) bool {
	if !w.Visible {
		return false
	}
	if !strings.EqualFold(w.Process, "Weixin.exe") {
		return false
	}
	if !strings.Contains(w.Class, "QWindowIcon") {
		return false
	}
	if w.Rect.Dx() < 1000 || w.Rect.Dy() < 800 {
		return false
	}
	return true
}
