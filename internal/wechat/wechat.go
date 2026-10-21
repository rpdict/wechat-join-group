package wechat

import (
	"errors"
	"fmt"
	"image"
	"log"
	"regexp"
	"strings"
	"time"

	w32uia "github.com/hnakamur/w32uiautomation"
	"github.com/lxn/win"

	"wechatJoinGroup/internal/uia"
	"wechatJoinGroup/internal/win32"
)

var inviteRe = regexp.MustCompile(`邀请你加入群聊["“]([^"”]+)["”]`)

type App struct {
	Auto     *uia.Automation
	Main     win.HWND
	MainElem *w32uia.IUIAutomationElement
	Chat     string
	Since    time.Time
	DebugDir string
	Log      *log.Logger
	Report   *Report
}

type Message struct {
	Class string
	Name  string
	Rect  image.Rectangle
}

type Card struct {
	Index int
	Name  string
	Group string
	Rect  image.Rectangle
}

type Result struct {
	Group  string    `json:"group"`
	Status string    `json:"status"`
	Reason string    `json:"reason"`
	Card   string    `json:"card"`
	At     time.Time `json:"at"`
}

func New(chat, sinceText, debugDir string, logger *log.Logger) (*App, error) {
	since, err := time.ParseInLocation("2006-01-02 15:04", sinceText, time.Local)
	if err != nil {
		return nil, fmt.Errorf("解析 -since 失败: %w", err)
	}

	auto, err := uia.New()
	if err != nil {
		return nil, fmt.Errorf("初始化 UI Automation 失败: %w", err)
	}

	main, err := findMainWindow(auto)
	if err != nil {
		return nil, err
	}

	mainElem, err := auto.ElementFromHandle(uintptr(main))
	if err != nil {
		return nil, fmt.Errorf("读取微信主窗口失败: %w", err)
	}

	return &App{
		Auto:     auto,
		Main:     main,
		MainElem: mainElem,
		Chat:     chat,
		Since:    since,
		DebugDir: debugDir,
		Log:      logger,
		Report:   LoadReport(debugDir),
	}, nil
}

func (a *App) Close() {
	if a.MainElem != nil {
		a.MainElem.Release()
	}
}

func findMainWindow(auto *uia.Automation) (win.HWND, error) {
	var candidates []win32.Window
	for _, w := range win32.EnumWindows() {
		if win32.IsWeChatWindow(w) {
			candidates = append(candidates, w)
		}
	}

	for _, w := range candidates {
		elem, err := auto.ElementFromHandle(uintptr(w.HWND))
		if err != nil {
			continue
		}
		cls := uia.ClassName(elem)
		elem.Release()
		if cls == "mmui::MainWindow" {
			return w.HWND, nil
		}
	}

	return 0, errors.New("未找到微信主窗口，请确认微信已登录并处于显示状态")
}

func (a *App) findByID(id string) (*w32uia.IUIAutomationElement, error) {
	cond, err := a.Auto.AutomationIDCondition(id)
	if err != nil {
		return nil, err
	}
	defer cond.Release()
	return a.Auto.FindFirst(a.MainElem, w32uia.TreeScope_Descendants, cond)
}

func (a *App) findSearchEdit() (*w32uia.IUIAutomationElement, error) {
	cond, err := a.Auto.NameCondition("搜索")
	if err != nil {
		return nil, err
	}
	defer cond.Release()

	elems, err := a.Auto.FindAll(a.MainElem, w32uia.TreeScope_Descendants, cond)
	if err != nil {
		return nil, err
	}
	var fallback *w32uia.IUIAutomationElement
	for _, e := range elems {
		if e == nil {
			continue
		}
		if uia.ClassName(e) == "mmui::XValidatorTextEdit" {
			for _, other := range elems {
				if other != nil && other != e {
					other.Release()
				}
			}
			return e, nil
		}
		if fallback == nil {
			fallback = e
		} else {
			e.Release()
		}
	}
	if fallback != nil {
		return fallback, nil
	}
	return nil, errors.New("未找到搜索框")
}

func (a *App) OpenChat(name string) error {
	win32.Foreground(a.Main)

	edit, err := a.findSearchEdit()
	if err != nil {
		return err
	}
	defer edit.Release()

	if r, err := uia.Rect(edit); err == nil {
		win32.Click(r.Min.X+r.Dx()/2, r.Min.Y+r.Dy()/2)
		time.Sleep(200 * time.Millisecond)
	}

	if err := uia.SetValue(edit, name); err != nil {
		return fmt.Errorf("写入搜索框失败: %w", err)
	}
	time.Sleep(1500 * time.Millisecond)

	result, err := a.findSearchResult(name)
	if err != nil {
		return err
	}
	defer result.Release()

	r, err := uia.Rect(result)
	if err != nil {
		return fmt.Errorf("读取搜索结果位置失败: %w", err)
	}
	win32.Click(r.Min.X+r.Dx()/2, r.Min.Y+r.Dy()/2)
	time.Sleep(2000 * time.Millisecond)

	if _, err := a.findByID("chat_message_list"); err != nil {
		time.Sleep(1500 * time.Millisecond)
		if _, err2 := a.findByID("chat_message_list"); err2 != nil {
			return fmt.Errorf("打开聊天 %q 后未找到消息列表", name)
		}
	}
	return nil
}

func (a *App) findSearchResult(name string) (*w32uia.IUIAutomationElement, error) {
	list, err := a.findByID("search_list")
	if err == nil && list != nil {
		defer list.Release()
		children, err := a.Auto.FindAllChildren(list)
		if err == nil {
			defer uia.ReleaseAll(children)
			for _, e := range children {
				if uia.ClassName(e) == "mmui::SearchContentCellView" && uia.Name(e) == name {
					e.AddRef()
					return e, nil
				}
			}
		}
	}

	cond, err := a.Auto.NameCondition(name)
	if err != nil {
		return nil, err
	}
	defer cond.Release()
	elems, err := a.Auto.FindAll(a.MainElem, w32uia.TreeScope_Descendants, cond)
	if err != nil {
		return nil, err
	}
	for _, e := range elems {
		if uia.ClassName(e) == "mmui::SearchContentCellView" {
			for _, other := range elems {
				if other != nil && other != e {
					other.Release()
				}
			}
			return e, nil
		}
	}
	uia.ReleaseAll(elems)
	return nil, fmt.Errorf("未找到搜索结果 %q", name)
}

func (a *App) messageListRect() (image.Rectangle, error) {
	ml, err := a.findByID("chat_message_list")
	if err != nil {
		return image.Rectangle{}, err
	}
	defer ml.Release()
	return uia.Rect(ml)
}

func (a *App) wheel(delta, times int) {
	r, err := a.messageListRect()
	if err != nil {
		return
	}
	cx := r.Min.X + r.Dx()/2
	cy := r.Min.Y + r.Dy()/2
	for i := 0; i < times; i++ {
		win32.WheelAt(cx, cy, delta)
		time.Sleep(35 * time.Millisecond)
	}
}

func (a *App) ScrollToBottom() {
	a.wheel(-120, 70)
	time.Sleep(800 * time.Millisecond)
}

func (a *App) scrollUp(times int) {
	a.wheel(120, times)
	time.Sleep(70 * time.Millisecond)
}

func (a *App) scrollDown(times int) {
	a.wheel(-120, times)
	time.Sleep(70 * time.Millisecond)
}

func (a *App) Messages() ([]Message, error) {
	ml, err := a.findByID("chat_message_list")
	if err != nil {
		return nil, err
	}
	defer ml.Release()

	elems, err := a.Auto.FindAllChildren(ml)
	if err != nil {
		return nil, err
	}
	defer uia.ReleaseAll(elems)

	out := make([]Message, 0, len(elems))
	for _, e := range elems {
		r, err := uia.Rect(e)
		if err != nil {
			continue
		}
		out = append(out, Message{
			Class: uia.ClassName(e),
			Name:  strings.ReplaceAll(uia.Name(e), "\r", ""),
			Rect:  r,
		})
	}
	return out, nil
}

func (a *App) findBoundary() ([]Message, Message, string, error) {
	boundary := a.Since.Format("15:04")
	fallbackLabel := a.Since.Add(time.Minute).Format("15:04")

	var fallbackSnap []Message
	var fallbackMsg Message

	for i := 0; i < 140; i++ {
		msgs, err := a.Messages()
		if err == nil {
			for _, m := range msgs {
				if m.Class != "mmui::ChatItemView" {
					continue
				}
				if m.Name == boundary {
					return msgs, m, boundary, nil
				}
				if m.Name == fallbackLabel && fallbackSnap == nil {
					fallbackSnap = msgs
					fallbackMsg = m
				}
			}
		}
		a.scrollUp(1)
	}

	if fallbackSnap != nil {
		a.Log.Printf("警告: 未找到 %s 时间分隔，使用 %s 作为起点", boundary, fallbackLabel)
		return fallbackSnap, fallbackMsg, fallbackLabel, nil
	}
	return nil, Message{}, "", fmt.Errorf("未找到时间分隔 %s，请确认聊天记录中存在该时间点", boundary)
}

func (a *App) CollectCards() ([]Card, error) {
	if err := a.OpenChat(a.Chat); err != nil {
		return nil, err
	}
	a.ScrollToBottom()

	snap, ts, label, err := a.findBoundary()
	if err != nil {
		return nil, err
	}
	a.Log.Printf("已定位时间分隔 %s，开始收集其后的群邀请", label)

	cards := make([]Card, 0, 64)
	seen := make(map[string]bool)

	add := func(msgs []Message, filterY bool) int {
		added := 0
		for _, m := range msgs {
			if m.Class != "mmui::ChatBubbleItemView" {
				continue
			}
			if !strings.Contains(m.Name, "邀请你加入群聊") {
				continue
			}
			if filterY && m.Rect.Min.Y <= ts.Rect.Min.Y {
				continue
			}
			group := extractGroup(m.Name)
			if group == "" || seen[group] {
				continue
			}
			seen[group] = true
			added++
			cards = append(cards, Card{
				Index: len(cards) + 1,
				Name:  m.Name,
				Group: group,
				Rect:  m.Rect,
			})
		}
		return added
	}

	add(snap, true)
	noNew := 0
	for i := 0; i < 45; i++ {
		a.scrollDown(2)
		time.Sleep(350 * time.Millisecond)
		msgs, err := a.Messages()
		if err != nil {
			break
		}
		if add(msgs, false) == 0 {
			noNew++
		} else {
			noNew = 0
		}
		if noNew >= 15 {
			break
		}
	}

	return cards, nil
}

func extractGroup(cardName string) string {
	m := inviteRe.FindStringSubmatch(cardName)
	if len(m) < 2 {
		return ""
	}
	return strings.TrimSpace(m[1])
}

func (a *App) findCard(group string) (image.Rectangle, error) {
	for i := 0; i < 140; i++ {
		msgs, err := a.Messages()
		if err == nil {
			for _, m := range msgs {
				if m.Class != "mmui::ChatBubbleItemView" {
					continue
				}
				if extractGroup(m.Name) == group {
					return m.Rect, nil
				}
			}
		}
		a.scrollUp(1)
	}
	return image.Rectangle{}, fmt.Errorf("未找到群邀请卡片: %s", group)
}

func (a *App) findMenuItemIn(root *w32uia.IUIAutomationElement, cond *w32uia.IUIAutomationCondition) *w32uia.IUIAutomationElement {
	el, err := a.Auto.FindFirst(root, w32uia.TreeScope_Descendants, cond)
	if err != nil || el == nil {
		return nil
	}
	r, rerr := uia.Rect(el)
	if rerr == nil && r.Dx() > 20 && r.Dy() > 10 {
		if uia.ClassName(el) == "mmui::XMenuView" || uia.AutomationID(el) == "XMenuItem" {
			return el
		}
	}
	el.Release()
	return nil
}

func (a *App) findMenuItem(name string) (*w32uia.IUIAutomationElement, error) {
	cond, err := a.Auto.NameCondition(name)
	if err != nil {
		return nil, err
	}
	defer cond.Release()

	for i := 0; i < 12; i++ {
		if elem, err := a.Auto.ElementFromHandle(uintptr(a.Main)); err == nil {
			if item := a.findMenuItemIn(elem, cond); item != nil {
				elem.Release()
				return item, nil
			}
			elem.Release()
		}

		for _, w := range win32.EnumWindows() {
			if !w.Visible || !strings.Contains(w.Class, "QWindowToolSaveBits") {
				continue
			}
			elem, err := a.Auto.ElementFromHandle(uintptr(w.HWND))
			if err != nil {
				continue
			}
			item := a.findMenuItemIn(elem, cond)
			elem.Release()
			if item != nil {
				return item, nil
			}
		}
		time.Sleep(150 * time.Millisecond)
	}
	return nil, fmt.Errorf("未找到菜单项 %q", name)
}

func (a *App) visibleCard(group string) (image.Rectangle, bool) {
	msgs, err := a.Messages()
	if err != nil {
		return image.Rectangle{}, false
	}
	for _, m := range msgs {
		if m.Class == "mmui::ChatBubbleItemView" && extractGroup(m.Name) == group {
			return m.Rect, true
		}
	}
	return image.Rectangle{}, false
}

func (a *App) centerCard(group string, rect image.Rectangle) image.Rectangle {
	for i := 0; i < 4; i++ {
		list, err := a.messageListRect()
		if err != nil {
			return rect
		}
		cy := rect.Min.Y + rect.Dy()/2
		lcy := list.Min.Y + list.Dy()/2
		switch {
		case cy < lcy-120:
			a.scrollUp(1)
		case cy > lcy+120:
			a.scrollDown(1)
		default:
			return rect
		}
		time.Sleep(300 * time.Millisecond)
		if r, ok := a.visibleCard(group); ok {
			rect = r
		} else if r, err := a.findCard(group); err == nil {
			rect = r
		} else {
			return rect
		}
	}
	return rect
}

func (a *App) openInvite(group string, initial image.Rectangle) error {
	rect := initial
	for attempt, pct := range []int{27, 20, 33} {
		if r, ok := a.visibleCard(group); ok {
			rect = r
		} else if r, err := a.findCard(group); err == nil {
			rect = r
		}
		rect = a.centerCard(group, rect)
		if r, ok := a.visibleCard(group); ok {
			rect = r
		}

		x := rect.Min.X + rect.Dx()*pct/100
		y := rect.Min.Y + rect.Dy()/2
		a.Log.Printf("  右键卡片[%d]: %s rect=%v click=(%d,%d)", attempt+1, group, rect, x, y)
		win32.RightClick(x, y)
		time.Sleep(900 * time.Millisecond)

		item, err := a.findMenuItem("在微信中打开")
		if err == nil && item != nil {
			r, rerr := uia.Rect(item)
			if rerr == nil {
				a.Log.Printf("  点击菜单: 在微信中打开 at (%d,%d)", r.Min.X+r.Dx()/2, r.Min.Y+r.Dy()/2)
				win32.Click(r.Min.X+r.Dx()/2, r.Min.Y+r.Dy()/2)
				item.Release()
				time.Sleep(2500 * time.Millisecond)
				return nil
			}
			item.Release()
		}
		win32.Esc()
		time.Sleep(200 * time.Millisecond)
	}
	return errors.New("右键菜单中未找到“在微信中打开”")
}

func (a *App) waitWebview(timeout time.Duration) (win32.Window, error) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		var best win32.Window
		for _, w := range win32.ChildWindows(a.Main) {
			if !w.Visible || w.Class != "Chrome_WidgetWin_0" {
				continue
			}
			if w.Rect.Dx() < 500 || w.Rect.Dy() < 500 {
				continue
			}
			if best.HWND == 0 || w.Rect.Dx()*w.Rect.Dy() > best.Rect.Dx()*best.Rect.Dy() {
				best = w
			}
		}
		if best.HWND != 0 {
			return best, nil
		}
		time.Sleep(300 * time.Millisecond)
	}
	return win32.Window{}, errors.New("未找到微信内嵌网页窗口")
}

func (a *App) waitWebviewState(rect image.Rectangle) (*WebviewState, error) {
	time.Sleep(3 * time.Second)
	var last *WebviewState
	for i := 0; i < 16; i++ {
		st, err := AnalyzeWebview(rect)
		if err == nil {
			last = st
			if st.Green != nil || st.Gray != nil {
				return st, nil
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	if last != nil {
		return last, nil
	}
	return nil, errors.New("无法读取内嵌网页内容")
}

func (a *App) ProcessCard(c Card) Result {
	res := Result{Group: c.Group, Card: c.Name, At: time.Now()}

	if err := a.OpenChat(a.Chat); err != nil {
		res.Status = "failed"
		res.Reason = err.Error()
		return res
	}
	a.ScrollToBottom()

	rect, err := a.findCard(c.Group)
	if err != nil {
		res.Status = "failed"
		res.Reason = err.Error()
		return res
	}

	if err := a.openInvite(c.Group, rect); err != nil {
		res.Status = "failed"
		res.Reason = err.Error()
		return res
	}

	wv, err := a.waitWebview(10 * time.Second)
	if err != nil {
		res.Status = "failed"
		res.Reason = err.Error()
		return res
	}

	st, err := a.waitWebviewState(wv.Rect)
	if err != nil {
		res.Status = "failed"
		res.Reason = err.Error()
		return res
	}

	switch {
	case st.Green != nil:
		clickButton(st.Green)
		time.Sleep(2500 * time.Millisecond)

		st2, err2 := AnalyzeWebview(wv.Rect)
		if err2 == nil && st2.Green != nil {
			clickButton(st2.Green)
			time.Sleep(2000 * time.Millisecond)
			st2, err2 = AnalyzeWebview(wv.Rect)
		}

		if err2 == nil && st2.Green == nil {
			res.Status = "joined"
			res.Reason = "已点击加入群聊"
		} else {
			res.Status = "failed"
			res.Reason = "点击加入后按钮仍然存在"
			if st2 != nil {
				a.saveDebug(st2, c.Group, "join_failed")
			}
		}
	case st.Gray != nil:
		res.Status = "already_joined"
		res.Reason = "页面显示你已接受该邀请"
	default:
		res.Status = "failed"
		res.Reason = "未找到加入按钮，可能邀请已失效"
		a.saveDebug(st, c.Group, "no_button")
	}

	return res
}

func clickButton(r *image.Rectangle) {
	win32.Click(r.Min.X+r.Dx()/2, r.Min.Y+r.Dy()/2)
}

func (a *App) saveDebug(st *WebviewState, group, reason string) {
	if st == nil || st.Image == nil {
		return
	}
	name := sanitizeFilename(group)
	path := fmt.Sprintf("%s/%s_%s_%s.png", a.DebugDir, time.Now().Format("20060102_150405"), name, reason)
	if err := SavePNG(st.Image, path); err != nil {
		a.Log.Printf("保存调试截图失败: %v", err)
	}
}

func sanitizeFilename(s string) string {
	replacer := strings.NewReplacer(
		"\\", "_", "/", "_", ":", "_", "*", "_", "?", "_",
		"\"", "_", "<", "_", ">", "_", "|", "_", "\n", "_", "\r", "_",
	)
	out := strings.TrimSpace(replacer.Replace(s))
	if len(out) > 80 {
		out = out[:80]
	}
	if out == "" {
		out = "group"
	}
	return out
}
