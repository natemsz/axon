// Package icon draws Axon's icon (EveSynapse's mark) in the formats
// each system wants: PNG for pages and Linux, ICO for Windows shortcuts
// and the tray, ICNS for a Mac app.
package icon

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
)

// PNG draws EveSynapse's mark at size pixels square: the ember
// tile and its two dark shards, from the site's favicon.svg (a 64-unit
// box). Drawn here so the program carries no image file.
func PNG(size int) []byte {
	img := image.NewNRGBA(image.Rect(0, 0, size, size))
	shards := [][3][2]float64{
		{{40.65, 7.79}, {14.91, 33.53}, {27.39, 33.53}},
		{{36.61, 30.47}, {49.09, 30.47}, {23.35, 56.21}},
	}
	const sub = 4 // samples per pixel side, for smooth edges
	for py := 0; py < size; py++ {
		for px := 0; px < size; px++ {
			var r, g, b, a float64
			for sy := 0; sy < sub; sy++ {
				for sx := 0; sx < sub; sx++ {
					x := (float64(px) + (float64(sx)+0.5)/sub) * 64 / float64(size)
					y := (float64(py) + (float64(sy)+0.5)/sub) * 64 / float64(size)
					if !inRoundRect(x, y, 64, 12) {
						continue
					}
					c := tile((x + y) / 128)
					for _, t := range shards {
						if inTriangle(x, y, t) {
							c = [3]float64{0x1a, 0x09, 0x04}
						}
					}
					r, g, b, a = r+c[0], g+c[1], b+c[2], a+1
				}
			}
			if a == 0 {
				continue
			}
			img.SetNRGBA(px, py, color.NRGBA{uint8(r / a), uint8(g / a), uint8(b / a), uint8(a * 255 / (sub * sub))})
		}
	}
	var out bytes.Buffer
	_ = png.Encode(&out, img)
	return out.Bytes()
}

// tile is the favicon's gradient, #ffc56a to #ff6a1a at .55 to #d23a12.
func tile(t float64) [3]float64 {
	mix := func(a, b [3]float64, f float64) [3]float64 {
		return [3]float64{a[0] + (b[0]-a[0])*f, a[1] + (b[1]-a[1])*f, a[2] + (b[2]-a[2])*f}
	}
	gold, ember, red := [3]float64{0xff, 0xc5, 0x6a}, [3]float64{0xff, 0x6a, 0x1a}, [3]float64{0xd2, 0x3a, 0x12}
	if t < 0.55 {
		return mix(gold, ember, t/0.55)
	}
	return mix(ember, red, (t-0.55)/0.45)
}

func inRoundRect(x, y, side, r float64) bool {
	cx, cy := min(max(x, r), side-r), min(max(y, r), side-r)
	return (x-cx)*(x-cx)+(y-cy)*(y-cy) <= r*r
}

func inTriangle(x, y float64, t [3][2]float64) bool {
	side := func(a, b [2]float64) float64 { return (b[0]-a[0])*(y-a[1]) - (b[1]-a[1])*(x-a[0]) }
	d1, d2, d3 := side(t[0], t[1]), side(t[1], t[2]), side(t[2], t[0])
	neg, pos := d1 < 0 || d2 < 0 || d3 < 0, d1 > 0 || d2 > 0 || d3 > 0
	return !(neg && pos)
}

// ICO is a Windows icon file holding the mark at the sizes Explorer
// asks for, each a PNG (which ICO allows since Windows Vista).
func ICO() []byte {
	sizes := []int{16, 24, 32, 48, 64, 256}
	var head, body bytes.Buffer
	le16 := func(b *bytes.Buffer, v int) { b.WriteByte(byte(v)); b.WriteByte(byte(v >> 8)) }
	le32 := func(b *bytes.Buffer, v int) { le16(b, v); le16(b, v>>16) }
	le16(&head, 0)
	le16(&head, 1) // icon
	le16(&head, len(sizes))
	offset := 6 + 16*len(sizes)
	for _, s := range sizes {
		p := PNG(s)
		dim := s
		if s >= 256 {
			dim = 0 // 0 means 256
		}
		head.WriteByte(byte(dim))
		head.WriteByte(byte(dim))
		head.WriteByte(0) // no palette
		head.WriteByte(0)
		le16(&head, 1)  // planes
		le16(&head, 32) // bits per pixel
		le32(&head, len(p))
		le32(&head, offset)
		offset += len(p)
		body.Write(p)
	}
	return append(head.Bytes(), body.Bytes()...)
}

// ICNS is a macOS icon file: the mark as PNGs at 128, 256 and 512
// pixels (types ic07, ic08, ic09).
func ICNS() []byte {
	var body bytes.Buffer
	be32 := func(b *bytes.Buffer, v int) { b.Write([]byte{byte(v >> 24), byte(v >> 16), byte(v >> 8), byte(v)}) }
	for _, e := range []struct {
		kind string
		size int
	}{{"ic07", 128}, {"ic08", 256}, {"ic09", 512}} {
		p := PNG(e.size)
		body.WriteString(e.kind)
		be32(&body, 8+len(p))
		body.Write(p)
	}
	var out bytes.Buffer
	out.WriteString("icns")
	be32(&out, 8+body.Len())
	out.Write(body.Bytes())
	return out.Bytes()
}
