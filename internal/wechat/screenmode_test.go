package wechat

import (
	"image"
	"image/color"
	"testing"
)

func TestSignatureDistance(t *testing.T) {
	if d, ok := signatureDistance("0001020304", "0001020304"); !ok || d != 0 {
		t.Fatalf("相同签名距离应为 0，实际 %d (ok=%v)", d, ok)
	}
	if d, ok := signatureDistance("0000000000", "0a0a0a0a0a"); !ok || d != 10 {
		t.Fatalf("距离应为 10，实际 %d (ok=%v)", d, ok)
	}
	if _, ok := signatureDistance("00", "0001"); ok {
		t.Fatal("长度不同应返回 false")
	}
	if _, ok := signatureDistance("zz", "00"); ok {
		t.Fatal("非法十六进制应返回 false")
	}
}

func TestCardGroupID(t *testing.T) {
	id1 := cardGroupID("deadbeef")
	id2 := cardGroupID("deadbeef")
	id3 := cardGroupID("deadbeee")
	if id1 != id2 {
		t.Fatalf("相同签名应产生相同编号: %s != %s", id1, id2)
	}
	if id1 == id3 {
		t.Fatalf("不同签名不应产生相同编号: %s", id1)
	}
}

func TestHasThumbnails(t *testing.T) {
	gray := image.NewRGBA(image.Rect(0, 0, 120, 120))
	for y := 0; y < 120; y++ {
		for x := 0; x < 120; x++ {
			gray.SetRGBA(x, y, color.RGBA{R: 238, G: 238, B: 240, A: 255})
		}
	}
	if hasThumbnails(gray, gray.Bounds()) {
		t.Fatal("纯灰色区域不应判定为含缩略图")
	}

	colorful := image.NewRGBA(image.Rect(0, 0, 120, 120))
	for y := 0; y < 120; y++ {
		for x := 0; x < 120; x++ {
			colorful.SetRGBA(x, y, color.RGBA{R: 238, G: 238, B: 240, A: 255})
		}
	}
	for y := 10; y < 90; y++ {
		for x := 10; x < 90; x++ {
			colorful.SetRGBA(x, y, color.RGBA{R: 220, G: 40, B: 40, A: 255})
		}
	}
	if !hasThumbnails(colorful, colorful.Bounds()) {
		t.Fatal("包含彩色缩略图区域应被识别")
	}
}
