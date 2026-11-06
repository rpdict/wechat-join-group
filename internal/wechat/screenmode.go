package wechat

import (
	"encoding/hex"
	"errors"
	"fmt"
	"hash/fnv"
	"image"
	"image/color"
	"log"
	"strings"
	"time"

	"github.com/kbinani/screenshot"
	"github.com/lxn/win"

	"wechatJoinGroup/internal/win32"
)

// 微信 4.2 起不再向 UIAutomation 暴露界面树，此时使用基于截图的屏幕模式。
// 屏幕模式只能按“卡片外观指纹”区分邀请，无法读取时间分隔和群名。
type ScreenApp struct {
	Main     win.HWND
	Chat     string
	DebugDir string
	Log      *log.Logger
	Report   *Report
	Pages    int
	NoBottom bool
	origRect image.Rectangle
}

// 搜索框和首条搜索结果是固定尺寸的左上角控件，用像素偏移（200% DPI）而不是比例。
const (
	searchBoxOffsetX = 410
	searchBoxOffsetY = 118
	firstResultOffX  = 350
	firstResultOffY  = 270

	messageAreaX = 0.600
	messageAreaY = 0.500
)

var cardBackColor = color.RGBA{R: 238, G: 238, B: 240, A: 255}

// 卡片缩略图签名的匹配阈值（0-255 灰度平均绝对差）。
const signatureMaxDistance = 8

func NewScreen(chat, debugDir string, pages int, noBottom bool, logger *log.Logger) (*ScreenApp, error) {
	main, err := findScreenWindow()
	if err != nil {
		return nil, err
	}
	if pages < 1 {
		pages = 1
	}
	app := &ScreenApp{
		Main:     main,
		Chat:     chat,
		DebugDir: debugDir,
		Log:      logger,
		Report:   LoadReport(debugDir),
		Pages:    pages,
		NoBottom: noBottom,
		origRect: win32.Rect(main),
	}
	if !noBottom {
		win32.Show(main)
		win32.Foreground(main)
		time.Sleep(500 * time.Millisecond)
	}
	return app, nil
}

func findScreenWindow() (win.HWND, error) {
	var best win.HWND
	bestArea := 0
	for _, w := range win32.EnumWindows() {
		if !strings.EqualFold(w.Process, "Weixin.exe") || !strings.Contains(w.Class, "QWindowIcon") {
			continue
		}
		if w.Title != "微信" {
			continue
		}
		area := w.Rect.Dx() * w.Rect.Dy()
		if area > bestArea {
			bestArea = area
			best = w.HWND
		}
	}
	if best == 0 {
		return 0, errors.New("未找到微信主窗口（Weixin.exe / QWindowIcon），请确认微信已登录并显示")
	}
	return best, nil
}

func (a *ScreenApp) Close() {
	if a.origRect.Dx() > 0 && a.origRect.Dy() > 0 {
		win32.SetRect(a.Main, a.origRect)
	}
}

func (a *ScreenApp) ShouldSkip(c Card) (Result, bool) {
	if prev, ok := a.Report.ShouldSkip(c.Group); ok {
		return prev, true
	}
	if prev, ok := a.Report.FindBySignature(c.Fingerprint, signatureMaxDistance); ok {
		return prev, true
	}
	return Result{}, false
}

func (a *ScreenApp) AddResult(res Result) error { return a.Report.Add(res) }

func (a *ScreenApp) WriteSummary() error { return a.Report.WriteSummary() }

func (a *ScreenApp) windowRect() image.Rectangle { return win32.Rect(a.Main) }

func (a *ScreenApp) OpenChat(name string) error {
	r := a.windowRect()
	if r.Dx() <= 0 || r.Dy() <= 0 {
		return errors.New("读取微信窗口位置失败")
	}
	win32.Foreground(a.Main)
	time.Sleep(400 * time.Millisecond)

	scale := float64(win32.DPI(a.Main)) / 192.0
	sx := r.Min.X + int(float64(searchBoxOffsetX)*scale)
	sy := r.Min.Y + int(float64(searchBoxOffsetY)*scale)
	win32.Click(sx, sy)
	time.Sleep(350 * time.Millisecond)

	if err := win32.SetClipboardText(name); err != nil {
		return fmt.Errorf("写入剪贴板失败: %w", err)
	}
	win32.CtrlKey(win32.VK_A)
	time.Sleep(120 * time.Millisecond)
	win32.CtrlKey(win32.VK_V)
	time.Sleep(1300 * time.Millisecond)

	rx := r.Min.X + int(float64(firstResultOffX)*scale)
	ry := r.Min.Y + int(float64(firstResultOffY)*scale)
	win32.Click(rx, ry)
	time.Sleep(1800 * time.Millisecond)
	return nil
}

func (a *ScreenApp) ScrollToBottom() {
	r := a.windowRect()
	cx := r.Min.X + int(messageAreaX*float64(r.Dx()))
	cy := r.Min.Y + int(messageAreaY*float64(r.Dy()))
	// 微信会截断过大的 delta，这里用大量标准滚轮事件滚到最底部。
	for i := 0; i < 320; i++ {
		win32.WheelAt(cx, cy, -120)
		time.Sleep(4 * time.Millisecond)
	}
	time.Sleep(300 * time.Millisecond)
	for i := 0; i < 20; i++ {
		win32.WheelAt(cx, cy, -120)
		time.Sleep(10 * time.Millisecond)
	}
	time.Sleep(900 * time.Millisecond)
}

func (a *ScreenApp) CollectCards() ([]Card, error) {
	if !a.NoBottom {
		if err := a.OpenChat(a.Chat); err != nil {
			return nil, err
		}
		a.ScrollToBottom()
	}

	cards := make([]Card, 0, 32)
	noNew := 0
	scrolled := 0
	for page := 0; page < a.Pages; page++ {
		found, err := a.detectCards()
		if err != nil {
			return nil, err
		}
		added := 0
		for _, c := range found {
			d, ok := nearestCardDistance(cards, c.Fingerprint)
			if ok && d <= signatureMaxDistance {
				continue
			}
			c.Index = len(cards) + 1
			cards = append(cards, c)
			added++
		}
		if added == 0 {
			noNew++
		} else {
			noNew = 0
		}
		if len(cards) > 0 && noNew >= 3 {
			break
		}
		a.scrollUpPage()
		scrolled++
	}
	for i := 0; i < scrolled; i++ {
		a.scrollDownPage()
	}
	return cards, nil
}

func (a *ScreenApp) scrollDownPage() {
	r := a.windowRect()
	cx := r.Min.X + int(messageAreaX*float64(r.Dx()))
	cy := r.Min.Y + int(messageAreaY*float64(r.Dy()))
	for i := 0; i < 10; i++ {
		win32.WheelAt(cx, cy, -120)
		time.Sleep(40 * time.Millisecond)
	}
	time.Sleep(500 * time.Millisecond)
}

func (a *ScreenApp) scrollUpPage() {
	r := a.windowRect()
	cx := r.Min.X + int(messageAreaX*float64(r.Dx()))
	cy := r.Min.Y + int(messageAreaY*float64(r.Dy()))
	for i := 0; i < 10; i++ {
		win32.WheelAt(cx, cy, 120)
		time.Sleep(40 * time.Millisecond)
	}
	time.Sleep(500 * time.Millisecond)
}

// detectCards 通过气泡底色定位邀请卡片。卡片底色为 (238,238,240)，
// 聊天背景为 (250,250,250)，两者差异稳定，因此按行统计像素即可分带。
func (a *ScreenApp) detectCards() ([]Card, error) {
	r := a.windowRect()
	x0 := r.Min.X + int(0.36*float64(r.Dx()))
	x1 := r.Min.X + int(0.98*float64(r.Dx()))
	y0 := r.Min.Y + int(0.10*float64(r.Dy()))
	y1 := r.Min.Y + int(0.84*float64(r.Dy()))
	if x1-x0 < 200 || y1-y0 < 200 {
		return nil, errors.New("微信窗口过小，无法识别邀请卡片")
	}

	img, err := screenshot.CaptureRect(image.Rect(x0, y0, x1, y1))
	if err != nil {
		return nil, fmt.Errorf("截取聊天区域失败: %w", err)
	}

	w, h := img.Bounds().Dx(), img.Bounds().Dy()
	counts := make([]int, h)
	minXs := make([]int, h)
	maxXs := make([]int, h)
	for y := 0; y < h; y++ {
		count := 0
		minX, maxX := w, -1
		for x := 0; x < w; x++ {
			if colorMatch(img.RGBAAt(x, y), cardBackColor, 6) {
				count++
				if x < minX {
					minX = x
				}
				if x > maxX {
					maxX = x
				}
			}
		}
		counts[y] = count
		minXs[y] = minX
		maxXs[y] = maxX
	}

	threshold := w / 8
	if threshold < 200 {
		threshold = 200
	}

	type band struct{ top, bottom int }
	var bands []band
	inBand := false
	start, lastHit := 0, 0
	for y := 0; y < h; y++ {
		if counts[y] >= threshold {
			if !inBand {
				inBand = true
				start = y
			}
			lastHit = y
			continue
		}
		if inBand && y-lastHit > 6 {
			bands = append(bands, band{top: start, bottom: lastHit})
			inBand = false
		}
	}
	if inBand {
		bands = append(bands, band{top: start, bottom: lastHit})
	}

	cards := make([]Card, 0, len(bands))
	for _, b := range bands {
		height := b.bottom - b.top + 1
		if height < 150 || height > 420 {
			continue
		}
		// 跳过被视口上下边缘截断的残缺卡片，翻页重叠时会重新完整识别。
		if b.top <= 2 || b.bottom >= h-3 {
			continue
		}
		minX, maxX := w, -1
		for y := b.top; y <= b.bottom; y++ {
			if maxXs[y] < 0 {
				continue
			}
			if minXs[y] < minX {
				minX = minXs[y]
			}
			if maxXs[y] > maxX {
				maxX = maxXs[y]
			}
		}
		if maxX <= minX {
			continue
		}
		// 邀请卡片右侧有 4 张彩色缩略图；普通文字气泡几乎不含彩色像素。
		if !hasThumbnails(img, image.Rect(minX, b.top, maxX+1, b.bottom+1)) {
			continue
		}
		rect := image.Rect(x0+minX, y0+b.top, x0+maxX+1, y0+b.bottom+1)
		sig := signatureCard(img, image.Rect(minX, b.top, maxX+1, b.bottom+1))
		cards = append(cards, Card{
			Index:       len(cards) + 1,
			Name:        "屏幕识别卡片",
			Group:       cardGroupID(sig),
			Rect:        rect,
			Fingerprint: sig,
		})
	}
	return cards, nil
}

func hasThumbnails(img *image.RGBA, r image.Rectangle) bool {
	colorful := 0
	for y := r.Min.Y; y < r.Max.Y; y += 2 {
		for x := r.Min.X; x < r.Max.X; x += 2 {
			c := img.RGBAAt(x, y)
			mx, mn := int(c.R), int(c.R)
			if int(c.G) > mx {
				mx = int(c.G)
			}
			if int(c.B) > mx {
				mx = int(c.B)
			}
			if int(c.G) < mn {
				mn = int(c.G)
			}
			if int(c.B) < mn {
				mn = int(c.B)
			}
			if mx-mn > 40 && mx > 60 {
				colorful++
			}
		}
	}
	return colorful >= 500
}

func cardGroupID(sig string) string {
	h := fnv.New64a()
	h.Write([]byte(sig))
	return fmt.Sprintf("卡片#%012x", h.Sum64())
}

// signatureCard 将卡片缩略为 48x24 灰度网格。与逐像素哈希不同，
// 这个签名对滚动造成的 1-2 像素偏移不敏感，可用距离比较。
func signatureCard(img *image.RGBA, r image.Rectangle) string {
	const gw, gh = 48, 24
	vals := make([]byte, 0, gw*gh)
	for gy := 0; gy < gh; gy++ {
		for gx := 0; gx < gw; gx++ {
			x0 := r.Min.X + gx*r.Dx()/gw
			x1 := r.Min.X + (gx+1)*r.Dx()/gw
			y0 := r.Min.Y + gy*r.Dy()/gh
			y1 := r.Min.Y + (gy+1)*r.Dy()/gh
			if x1 <= x0 {
				x1 = x0 + 1
			}
			if y1 <= y0 {
				y1 = y0 + 1
			}
			sum, n := 0, 0
			for y := y0; y < y1; y += 2 {
				for x := x0; x < x1; x += 2 {
					c := img.RGBAAt(x, y)
					gray := (int(c.R)*299 + int(c.G)*587 + int(c.B)*114) / 1000
					sum += gray
					n++
				}
			}
			if n == 0 {
				n = 1
			}
			vals = append(vals, byte(sum/n))
		}
	}
	return hex.EncodeToString(vals)
}

// Run 是屏幕模式的完整流程：逐页向上扫描，并就地处理当前可见的邀请卡片。
// 这样无需反复滚回底部重新定位，卡片坐标始终有效。
func (a *ScreenApp) Run(force bool, limit int, oldest bool, dryRun bool) (joined, already, failed, skipped int, err error) {
	if !a.NoBottom {
		if err := a.OpenChat(a.Chat); err != nil {
			return 0, 0, 0, 0, err
		}
		a.ScrollToBottom()
	}

	seen := make([]string, 0, 64)
	processed := 0
	noNew := 0
	for page := 0; page < a.Pages; page++ {
		cards, derr := a.detectCards()
		if derr != nil {
			return joined, already, failed, skipped, derr
		}
		fresh := 0
		if !oldest {
			for i, j := 0, len(cards)-1; i < j; i, j = i+1, j-1 {
				cards[i], cards[j] = cards[j], cards[i]
			}
		}
		for _, c := range cards {
			if cardSeen(seen, c.Fingerprint) {
				continue
			}
			seen = append(seen, c.Fingerprint)
			fresh++

			if dryRun {
				a.Log.Printf("  %2d. %s", len(seen), c.Group)
				continue
			}
			if !force {
				if prev, ok := a.ShouldSkip(c); ok {
					a.Log.Printf("[%s] 已有记录（%s），跳过", c.Group, prev.Status)
					skipped++
					continue
				}
			}

			if freshCards, derr := a.detectCards(); derr == nil {
				if t := findCardByFingerprint(freshCards, c.Fingerprint); t != nil {
					c.Rect = t.Rect
				}
			}
			a.Log.Printf("处理: %s", c.Group)
			res := a.processVisible(c)
			if err := a.AddResult(res); err != nil {
				a.Log.Printf("  保存结果失败: %v", err)
			}
			switch res.Status {
			case "joined":
				joined++
				a.Log.Printf("  -> 已加入")
			case "already_joined":
				already++
				a.Log.Printf("  -> 之前已加入：%s", res.Reason)
			default:
				failed++
				a.Log.Printf("  -> 未加入: %s", res.Reason)
			}
			processed++
			if limit > 0 && processed >= limit {
				a.Log.Printf("已达到 -limit %d，停止", limit)
				_ = a.WriteSummary()
				return joined, already, failed, skipped, nil
			}
		}
		if fresh == 0 {
			noNew++
		} else {
			noNew = 0
		}
		if len(seen) > 0 && noNew >= 3 {
			break
		}
		a.scrollUpPage()
	}
	return joined, already, failed, skipped, nil
}

func cardSeen(seen []string, sig string) bool {
	for _, s := range seen {
		if d, ok := signatureDistance(s, sig); ok && d <= signatureMaxDistance {
			return true
		}
	}
	return false
}

// processVisible 直接使用当前视图中检测到的卡片坐标，不再滚动重新定位。
func (a *ScreenApp) processVisible(c Card) Result {
	res := Result{Group: c.Group, Card: "屏幕识别卡片 " + c.Group, Signature: c.Fingerprint, At: time.Now()}

	if !a.NoBottom {
		win32.Foreground(a.Main)
		time.Sleep(200 * time.Millisecond)
	}

	cx := c.Rect.Min.X + c.Rect.Dx()*30/100
	cy := c.Rect.Min.Y + c.Rect.Dy()/2
	a.Log.Printf("  卡片位置 %v，右键点 (%d,%d)", c.Rect, cx, cy)
	if err := a.openInviteMenu(cx, cy); err != nil {
		res.Status = "failed"
		res.Reason = err.Error()
		return res
	}

	green, gray, hasWebview := a.waitPageState(9 * time.Second)
	a.Log.Printf("  邀请页: webview=%v green=%v gray=%v title=%q", hasWebview, green != nil, gray != nil, a.webviewTitle())
	if green == nil {
		if gray != nil {
			clickRect(*gray)
			time.Sleep(1200 * time.Millisecond)
			res.Status = "already_joined"
			res.Reason = "页面显示你已接受该邀请"
			return res
		}
		if hasWebview {
			res.Status = "failed"
			res.Reason = "未找到加入按钮，可能邀请已失效"
		} else {
			res.Status = "already_joined"
			res.Reason = "未打开邀请页，可能此前已加入"
		}
		a.saveScreenDebug(c.Group, "no_button")
		return res
	}

	clickRect(*green)
	time.Sleep(2500 * time.Millisecond)

	if g2, _ := a.findGreen(); g2 != nil {
		clickRect(*g2)
		time.Sleep(2000 * time.Millisecond)
		if g3, _ := a.findGreen(); g3 != nil {
			res.Status = "failed"
			res.Reason = "点击加入后按钮仍然存在"
			a.saveScreenDebug(c.Group, "join_failed")
			return res
		}
	}

	res.Status = "joined"
	res.Reason = "已点击加入群聊（屏幕模式）"
	if title := a.webviewTitle(); title != "" {
		res.Card = title
		res.Reason += "：" + title
	}
	return res
}

func (a *ScreenApp) ProcessCard(c Card) Result {
	res := Result{Group: c.Group, Card: "屏幕识别卡片 " + c.Group, Signature: c.Fingerprint, At: time.Now()}

	target, err := a.findCard(c.Fingerprint)
	if err != nil {
		res.Status = "failed"
		res.Reason = err.Error()
		return res
	}

	cx := target.Rect.Min.X + target.Rect.Dx()*30/100
	cy := target.Rect.Min.Y + target.Rect.Dy()/2
	a.Log.Printf("  卡片位置 %v，右键点 (%d,%d)", target.Rect, cx, cy)
	if err := a.openInviteMenu(cx, cy); err != nil {
		res.Status = "failed"
		res.Reason = err.Error()
		return res
	}

	green, gray, hasWebview := a.waitPageState(9 * time.Second)
	a.Log.Printf("  邀请页: webview=%v green=%v gray=%v title=%q", hasWebview, green != nil, gray != nil, a.webviewTitle())
	if green == nil {
		if gray != nil {
			clickRect(*gray)
			time.Sleep(1200 * time.Millisecond)
			res.Status = "already_joined"
			res.Reason = "页面显示你已接受该邀请"
			return res
		}
		if hasWebview {
			res.Status = "failed"
			res.Reason = "未找到加入按钮，可能邀请已失效"
		} else {
			res.Status = "already_joined"
			res.Reason = "未打开邀请页，可能此前已加入"
		}
		a.saveScreenDebug(c.Group, "no_button")
		return res
	}

	clickRect(*green)
	time.Sleep(2500 * time.Millisecond)

	if g2, _ := a.findGreen(); g2 != nil {
		clickRect(*g2)
		time.Sleep(2000 * time.Millisecond)
		if g3, _ := a.findGreen(); g3 != nil {
			res.Status = "failed"
			res.Reason = "点击加入后按钮仍然存在"
			a.saveScreenDebug(c.Group, "join_failed")
			return res
		}
	}

	res.Status = "joined"
	res.Reason = "已点击加入群聊（屏幕模式）"
	if title := a.webviewTitle(); title != "" {
		res.Card = title
		res.Reason += "：" + title
	}
	return res
}

func (a *ScreenApp) findCard(fp string) (*Card, error) {
	if !a.NoBottom {
		if err := a.OpenChat(a.Chat); err != nil {
			return nil, err
		}
		a.ScrollToBottom()
	}
	for page := 0; page < a.Pages; page++ {
		cards, err := a.detectCards()
		if err != nil {
			return nil, err
		}
		if target := findCardByFingerprint(cards, fp); target != nil {
			return target, nil
		}
		a.scrollUpPage()
	}
	return nil, errors.New("重新定位邀请卡片失败")
}

func nearestCardDistance(cards []Card, sig string) (int, bool) {
	best := -1
	for i := range cards {
		d, ok := signatureDistance(cards[i].Fingerprint, sig)
		if !ok {
			continue
		}
		if best < 0 || d < best {
			best = d
		}
	}
	if best < 0 {
		return 0, false
	}
	return best, true
}

func findCardByFingerprint(cards []Card, fp string) *Card {
	bestIdx := -1
	bestDist := signatureMaxDistance + 1
	for i := range cards {
		d, ok := signatureDistance(cards[i].Fingerprint, fp)
		if !ok || d > signatureMaxDistance {
			continue
		}
		if d < bestDist {
			bestDist = d
			bestIdx = i
		}
	}
	if bestIdx >= 0 {
		return &cards[bestIdx]
	}
	return nil
}

func clickRect(r image.Rectangle) {
	win32.Click(r.Min.X+r.Dx()/2, r.Min.Y+r.Dy()/2)
}

// 右键菜单相对光标的位置（200% DPI 下的像素），用于点击“在微信中打开”。
// 实测菜单向下弹出时该行约在光标下方 375px，向上弹出时约在光标上方 163px。
const (
	menuItemDownOffset = 375
	menuItemUpOffset   = 163
	menuWidth          = 320
)

func (a *ScreenApp) openInviteMenu(cx, cy int) error {
	scale := float64(win32.DPI(a.Main)) / 192.0
	for attempt := 0; attempt < 3; attempt++ {
		win32.RightClick(cx, cy)
		time.Sleep(900 * time.Millisecond)

		open, coverage := a.menuOpen(cx, cy)
		if !open {
			a.Log.Printf("  右键菜单未弹出（覆盖率 %.0f%%），重试", coverage*100)
			time.Sleep(300 * time.Millisecond)
			continue
		}

		r := a.windowRect()
		itemY := cy + int(float64(menuItemDownOffset)*scale)
		if cy+int(600*scale) > r.Max.Y {
			itemY = cy - int(float64(menuItemUpOffset)*scale)
		}
		clickX := cx + int(80*scale)
		a.Log.Printf("  右键菜单已弹出，点击 (%d,%d)", clickX, itemY)
		win32.Click(clickX, itemY)
		time.Sleep(2200 * time.Millisecond)
		return nil
	}
	return errors.New("右键菜单中未找到“在微信中打开”")
}

// menuOpen 通过菜单底色（约 246,247,247，比聊天背景 250 略暗）判断菜单是否弹出。
func (a *ScreenApp) menuOpen(cx, cy int) (bool, float64) {
	r := a.windowRect()
	x0 := cx + 6
	y0 := cy + 6
	x1 := x0 + menuWidth
	y1 := y0 + 420
	if x1 > r.Max.X {
		x1 = r.Max.X
	}
	if y1 > r.Max.Y {
		y1 = r.Max.Y
	}
	if x1-x0 < 100 || y1-y0 < 100 {
		return false, 0
	}
	img, err := screenshot.CaptureRect(image.Rect(x0, y0, x1, y1))
	if err != nil {
		return false, 0
	}
	total, matched := 0, 0
	b := img.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y += 2 {
		for x := b.Min.X; x < b.Max.X; x += 2 {
			total++
			c := img.RGBAAt(x, y)
			if c.R >= 244 && c.R <= 249 && c.G >= 244 && c.G <= 249 && c.B >= 244 && c.B <= 250 {
				matched++
			}
		}
	}
	if total == 0 {
		return false, 0
	}
	cov := float64(matched) / float64(total)
	return cov >= 0.55, cov
}

func (a *ScreenApp) webviewWindow() (win32.Window, bool) {
	var best win32.Window
	pick := func(w win32.Window) {
		if !w.Visible || !strings.HasPrefix(w.Class, "Chrome_WidgetWin") {
			return
		}
		if w.Rect.Dx() < 300 || w.Rect.Dy() < 300 {
			return
		}
		if best.HWND == 0 || w.Rect.Dx()*w.Rect.Dy() > best.Rect.Dx()*best.Rect.Dy() {
			best = w
		}
	}
	for _, w := range win32.ChildWindows(a.Main) {
		pick(w)
	}
	if best.HWND != 0 {
		return best, true
	}
	// 部分微信版本的内嵌页是独立顶层窗口。
	for _, w := range win32.EnumWindows() {
		if !strings.EqualFold(w.Process, "Weixin.exe") {
			continue
		}
		pick(w)
	}
	return best, best.HWND != 0
}

func (a *ScreenApp) webviewTitle() string {
	w, ok := a.webviewWindow()
	if !ok {
		return ""
	}
	t := strings.TrimSpace(w.Title)
	switch t {
	case "", "Chrome Legacy Window", "Weixin", "微信":
		return ""
	}
	return t
}

func (a *ScreenApp) findGreen() (*image.Rectangle, error) {
	var rect image.Rectangle
	if wv, ok := a.webviewWindow(); ok {
		rect = wv.Rect
	} else {
		r := a.windowRect()
		rect = image.Rect(
			r.Min.X+int(0.55*float64(r.Dx())),
			r.Min.Y+int(0.35*float64(r.Dy())),
			r.Min.X+int(0.99*float64(r.Dx())),
			r.Min.Y+int(0.98*float64(r.Dy())),
		)
	}
	if rect.Dx() < 100 || rect.Dy() < 100 {
		return nil, nil
	}
	img, err := screenshot.CaptureRect(rect)
	if err != nil {
		return nil, err
	}
	band := findColorBand(img, color.RGBA{R: 7, G: 193, B: 96, A: 255}, 0.40, 1.0, 150, 20)
	if band == nil {
		return nil, nil
	}
	abs := band.Add(rect.Min)
	return &abs, nil
}

func (a *ScreenApp) findGray() (*image.Rectangle, error) {
	wv, ok := a.webviewWindow()
	if !ok {
		return nil, nil
	}
	img, err := screenshot.CaptureRect(wv.Rect)
	if err != nil {
		return nil, err
	}
	band := findColorBand(img, color.RGBA{R: 242, G: 242, B: 242, A: 255}, 0.60, 1.0, 150, 3)
	if band == nil {
		return nil, nil
	}
	abs := band.Add(wv.Rect.Min)
	return &abs, nil
}

func (a *ScreenApp) waitPageState(timeout time.Duration) (green, gray *image.Rectangle, hasWebview bool) {
	deadline := time.Now().Add(timeout)
	for {
		if _, ok := a.webviewWindow(); ok {
			hasWebview = true
		}
		if g, err := a.findGreen(); err == nil && g != nil {
			return g, nil, hasWebview
		}
		if g, err := a.findGray(); err == nil && g != nil {
			return nil, g, hasWebview
		}
		if time.Now().After(deadline) {
			return nil, nil, hasWebview
		}
		time.Sleep(500 * time.Millisecond)
	}
}

func (a *ScreenApp) saveScreenDebug(group, reason string) {
	r := a.windowRect()
	img, err := screenshot.CaptureRect(r)
	if err != nil {
		return
	}
	name := sanitizeFilename(group)
	path := fmt.Sprintf("%s/%s_%s_%s.png", a.DebugDir, time.Now().Format("20060102_150405"), name, reason)
	if err := SavePNG(img, path); err != nil {
		a.Log.Printf("保存调试截图失败: %v", err)
	}
}
