package uia

import (
	"errors"
	"image"
	"syscall"
	"unsafe"

	ole "github.com/go-ole/go-ole"
	uia "github.com/hnakamur/w32uiautomation"
)

var ErrInvalidRect = errors.New("invalid UIA bounding rectangle")

type Automation struct {
	Auto *uia.IUIAutomation
}

func New() (*Automation, error) {
	if err := ole.CoInitializeEx(0, ole.COINIT_APARTMENTTHREADED); err != nil {
		if oleErr, ok := err.(*ole.OleError); !ok || oleErr.Code() != 1 {
			return nil, err
		}
	}
	auto, err := uia.NewUIAutomation()
	if err != nil {
		return nil, err
	}
	return &Automation{Auto: auto}, nil
}

func (a *Automation) ElementFromHandle(hwnd uintptr) (*uia.IUIAutomationElement, error) {
	var elem *uia.IUIAutomationElement
	hr, _, _ := syscall.Syscall(
		a.Auto.VTable().ElementFromHandle,
		3,
		uintptr(unsafe.Pointer(a.Auto)),
		hwnd,
		uintptr(unsafe.Pointer(&elem)),
	)
	if hr != 0 {
		return nil, ole.NewError(hr)
	}
	return elem, nil
}

type elementArray struct {
	ole.IUnknown
}

type elementArrayVtbl struct {
	ole.IUnknownVtbl
	GetLength  uintptr
	GetElement uintptr
}

func (a *elementArray) VTable() *elementArrayVtbl {
	return (*elementArrayVtbl)(unsafe.Pointer(a.RawVTable))
}

func (a *Automation) FindAll(elem *uia.IUIAutomationElement, scope uia.TreeScope, cond *uia.IUIAutomationCondition) ([]*uia.IUIAutomationElement, error) {
	if cond == nil {
		c, err := a.Auto.CreateTrueCondition()
		if err != nil {
			return nil, err
		}
		defer c.Release()
		cond = c
	}

	var arr *elementArray
	hr, _, _ := syscall.Syscall6(
		elem.VTable().FindAll,
		4,
		uintptr(unsafe.Pointer(elem)),
		uintptr(scope),
		uintptr(unsafe.Pointer(cond)),
		uintptr(unsafe.Pointer(&arr)),
		0,
		0,
	)
	if hr != 0 {
		return nil, ole.NewError(hr)
	}
	if arr == nil {
		return nil, nil
	}
	defer arr.Release()

	var length int32
	hr, _, _ = syscall.Syscall(
		arr.VTable().GetLength,
		2,
		uintptr(unsafe.Pointer(arr)),
		uintptr(unsafe.Pointer(&length)),
		0,
	)
	if hr != 0 {
		return nil, ole.NewError(hr)
	}

	out := make([]*uia.IUIAutomationElement, 0, length)
	for i := int32(0); i < length; i++ {
		var el *uia.IUIAutomationElement
		hr, _, _ = syscall.Syscall(
			arr.VTable().GetElement,
			3,
			uintptr(unsafe.Pointer(arr)),
			uintptr(i),
			uintptr(unsafe.Pointer(&el)),
		)
		if hr != 0 {
			for _, e := range out {
				e.Release()
			}
			return nil, ole.NewError(hr)
		}
		out = append(out, el)
	}
	return out, nil
}

func (a *Automation) FindAllChildren(elem *uia.IUIAutomationElement) ([]*uia.IUIAutomationElement, error) {
	return a.FindAll(elem, uia.TreeScope_Children, nil)
}

func (a *Automation) FindAllDescendants(elem *uia.IUIAutomationElement) ([]*uia.IUIAutomationElement, error) {
	return a.FindAll(elem, uia.TreeScope_Descendants, nil)
}

func (a *Automation) FindFirst(elem *uia.IUIAutomationElement, scope uia.TreeScope, cond *uia.IUIAutomationCondition) (*uia.IUIAutomationElement, error) {
	return elem.FindFirst(scope, cond)
}

func (a *Automation) NameCondition(name string) (*uia.IUIAutomationCondition, error) {
	v := uia.NewVariantString(name)
	defer ole.VariantClear(&v)
	return a.Auto.CreatePropertyCondition(uia.UIA_NamePropertyId, v)
}

func (a *Automation) AutomationIDCondition(id string) (*uia.IUIAutomationCondition, error) {
	v := uia.NewVariantString(id)
	defer ole.VariantClear(&v)
	return a.Auto.CreatePropertyCondition(uia.UIA_AutomationIdPropertyId, v)
}

func (a *Automation) ClassNameCondition(class string) (*uia.IUIAutomationCondition, error) {
	v := uia.NewVariantString(class)
	defer ole.VariantClear(&v)
	return a.Auto.CreatePropertyCondition(uia.UIA_ClassNamePropertyId, v)
}

type valuePattern struct {
	ole.IUnknown
}

type valuePatternVtbl struct {
	ole.IUnknownVtbl
	SetValue             uintptr
	GetCurrentValue      uintptr
	GetCurrentIsReadOnly uintptr
}

func (p *valuePattern) VTable() *valuePatternVtbl {
	return (*valuePatternVtbl)(unsafe.Pointer(p.RawVTable))
}

func SetValue(elem *uia.IUIAutomationElement, value string) error {
	unk, err := elem.GetCurrentPattern(uia.UIA_ValuePatternId)
	if err != nil {
		return err
	}
	defer unk.Release()

	p := (*valuePattern)(unsafe.Pointer(unk))
	bstr := ole.SysAllocStringLen(value)
	if bstr == nil {
		return errors.New("SysAllocStringLen failed")
	}
	defer ole.SysFreeString(bstr)

	hr, _, _ := syscall.Syscall(
		p.VTable().SetValue,
		2,
		uintptr(unsafe.Pointer(p)),
		uintptr(unsafe.Pointer(bstr)),
		0,
	)
	if hr != 0 {
		return ole.NewError(hr)
	}
	return nil
}

func Name(elem *uia.IUIAutomationElement) string {
	s, _ := elem.Get_CurrentName()
	return s
}

func ClassName(elem *uia.IUIAutomationElement) string {
	s, _ := elem.Get_CurrentCurrentClassName()
	return s
}

func AutomationID(elem *uia.IUIAutomationElement) string {
	s, _ := elem.Get_CurrentAutomationId()
	return s
}

func NativeWindowHandle(elem *uia.IUIAutomationElement) uintptr {
	h, _ := elem.Get_CurrentNativeWindowHandle()
	return uintptr(h)
}

func Rect(elem *uia.IUIAutomationElement) (image.Rectangle, error) {
	r, err := elem.Get_CurrentBoundingRectangle()
	if err != nil {
		return image.Rectangle{}, err
	}
	left, top, right, bottom := int(r.Left), int(r.Top), int(r.Right), int(r.Bottom)
	if right <= left || bottom <= top || right > 20000 || bottom > 20000 || left < -20000 || top < -20000 {
		return image.Rectangle{}, ErrInvalidRect
	}
	return image.Rect(left, top, right, bottom), nil
}

func ReleaseAll(elems []*uia.IUIAutomationElement) {
	for _, e := range elems {
		if e != nil {
			e.Release()
		}
	}
}
