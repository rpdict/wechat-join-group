package wechat

import (
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"

	"github.com/kbinani/screenshot"
)

type WebviewState struct {
	Rect  image.Rectangle
	Green *image.Rectangle
	Gray  *image.Rectangle
	Image *image.RGBA
}

func AnalyzeWebview(rect image.Rectangle) (*WebviewState, error) {
	img, err := screenshot.CaptureRect(rect)
	if err != nil {
		return nil, err
	}
	green := findColorBand(img, color.RGBA{R: 7, G: 193, B: 96, A: 255}, 0.50, 0.99, 60, 14)
	gray := findColorBand(img, color.RGBA{R: 242, G: 242, B: 242, A: 255}, 0.65, 0.99, 80, 3)

	st := &WebviewState{Rect: rect, Image: img}
	if green != nil {
		r := green.Add(rect.Min)
		st.Green = &r
	}
	if gray != nil {
		r := gray.Add(rect.Min)
		st.Gray = &r
	}
	return st, nil
}

func findColorBand(img *image.RGBA, target color.RGBA, minYFrac, maxYFrac float64, minPixels, tolerance int) *image.Rectangle {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	y0 := int(float64(h) * minYFrac)
	y1 := int(float64(h) * maxYFrac)
	if y0 < 0 {
		y0 = 0
	}
	if y1 > h {
		y1 = h
	}
	if y1 <= y0 {
		return nil
	}

	type rowInfo struct {
		count int
		minX  int
		maxX  int
	}
	rows := make([]rowInfo, y1-y0)
	bestIdx := -1
	bestCount := 0

	for y := y0; y < y1; y++ {
		count := 0
		minX := w
		maxX := -1
		for x := 0; x < w; x++ {
			c := img.RGBAAt(x, y)
			if colorMatch(c, target, tolerance) {
				count++
				if x < minX {
					minX = x
				}
				if x > maxX {
					maxX = x
				}
			}
		}
		rows[y-y0] = rowInfo{count: count, minX: minX, maxX: maxX}
		if count > bestCount {
			bestCount = count
			bestIdx = y - y0
		}
	}

	if bestIdx < 0 || bestCount < minPixels {
		return nil
	}

	threshold := bestCount / 3
	if threshold < minPixels {
		threshold = minPixels
	}

	up := bestIdx
	for up > 0 && rows[up-1].count >= threshold {
		up--
	}
	down := bestIdx
	for down+1 < len(rows) && rows[down+1].count >= threshold {
		down++
	}

	minX := w
	maxX := -1
	for i := up; i <= down; i++ {
		if rows[i].count == 0 {
			continue
		}
		if rows[i].minX < minX {
			minX = rows[i].minX
		}
		if rows[i].maxX > maxX {
			maxX = rows[i].maxX
		}
	}
	if maxX <= minX {
		return nil
	}

	topY := y0 + up
	bottomY := y0 + down + 1
	bw := maxX - minX + 1
	bh := bottomY - topY
	if bw < 80 || bh < 20 || bw > w || bh > h/2 {
		return nil
	}

	r := image.Rect(minX, topY, maxX+1, bottomY)
	return &r
}

func colorMatch(c, target color.RGBA, tolerance int) bool {
	dr := int(c.R) - int(target.R)
	dg := int(c.G) - int(target.G)
	db := int(c.B) - int(target.B)
	return dr*dr+dg*dg+db*db <= tolerance*tolerance
}

func SavePNG(img image.Image, path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return png.Encode(f, img)
}
