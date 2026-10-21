package icon

import (
	"bytes"
	"encoding/binary"
	"image/png"
	"testing"
)

func TestIcon(t *testing.T) {
	img, err := png.Decode(bytes.NewReader(PNG(32)))
	if err != nil {
		t.Fatal(err)
	}
	if b := img.Bounds(); b.Dx() != 32 || b.Dy() != 32 {
		t.Errorf("icon is %v", b)
	}
	if _, _, _, a := img.At(0, 0).RGBA(); a != 0 {
		t.Error("the rounded corner is not clear")
	}
	if _, _, _, a := img.At(16, 2).RGBA(); a == 0 {
		t.Error("the tile is not drawn")
	}
}

func TestICO(t *testing.T) {
	b := ICO()
	if binary.LittleEndian.Uint16(b[2:]) != 1 || binary.LittleEndian.Uint16(b[4:]) != 6 {
		t.Fatalf("header % x", b[:6])
	}
	// Each entry points at a PNG of its size.
	for i := 0; i < 6; i++ {
		e := b[6+16*i:]
		size, off := binary.LittleEndian.Uint32(e[8:]), binary.LittleEndian.Uint32(e[12:])
		img, err := png.Decode(bytes.NewReader(b[off : off+size]))
		if err != nil {
			t.Fatalf("entry %d: %v", i, err)
		}
		want := int(e[0])
		if want == 0 {
			want = 256
		}
		if img.Bounds().Dx() != want {
			t.Errorf("entry %d is %d wide, says %d", i, img.Bounds().Dx(), want)
		}
	}
}

func TestICNS(t *testing.T) {
	b := ICNS()
	if string(b[:4]) != "icns" || int(binary.BigEndian.Uint32(b[4:])) != len(b) {
		t.Fatalf("header % x, %d bytes", b[:8], len(b))
	}
	if string(b[8:12]) != "ic07" {
		t.Errorf("first entry %q", b[8:12])
	}
}
