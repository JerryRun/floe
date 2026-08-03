package main

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
)

var iconSizes = []int{16, 20, 24, 32, 40, 48, 64, 128, 256}

type iconEntry struct {
	width, height byte
	colorCount    byte
	reserved      byte
	planes        uint16
	bits          uint16
	size          uint32
	offset        uint32
}

func main() {
	if len(os.Args) != 4 {
		fmt.Fprintln(os.Stderr, "usage: makeicon source.png output.ico favicon.png")
		os.Exit(2)
	}
	source, err := loadPNG(os.Args[1])
	check(err)

	images := make([][]byte, 0, len(iconSizes))
	for _, size := range iconSizes {
		resized := boxResize(source, size, size)
		var encoded bytes.Buffer
		check(png.Encode(&encoded, resized))
		images = append(images, encoded.Bytes())
		if size == 64 {
			check(os.MkdirAll(filepath.Dir(os.Args[3]), 0o755))
			check(os.WriteFile(os.Args[3], encoded.Bytes(), 0o644))
		}
	}
	check(writeICO(os.Args[2], images))
}

func loadPNG(path string) (image.Image, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return png.Decode(f)
}

// boxResize averages premultiplied source pixels. It is deliberately small and
// dependency-free because icon generation runs as part of the release build.
func boxResize(source image.Image, width, height int) *image.NRGBA {
	bounds := source.Bounds()
	dst := image.NewNRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		sy0 := bounds.Min.Y + y*bounds.Dy()/height
		sy1 := bounds.Min.Y + (y+1)*bounds.Dy()/height
		if sy1 <= sy0 {
			sy1 = sy0 + 1
		}
		for x := 0; x < width; x++ {
			sx0 := bounds.Min.X + x*bounds.Dx()/width
			sx1 := bounds.Min.X + (x+1)*bounds.Dx()/width
			if sx1 <= sx0 {
				sx1 = sx0 + 1
			}
			var sr, sg, sb, sa uint64
			var count uint64
			for sy := sy0; sy < sy1; sy++ {
				for sx := sx0; sx < sx1; sx++ {
					r, g, b, a := source.At(sx, sy).RGBA()
					sr += uint64(r)
					sg += uint64(g)
					sb += uint64(b)
					sa += uint64(a)
					count++
				}
			}
			a := sa / count
			if a == 0 {
				dst.SetNRGBA(x, y, color.NRGBA{})
				continue
			}
			toStraight := func(sum uint64) uint8 {
				premultiplied := sum / count
				straight := premultiplied * 65535 / a
				if straight > 65535 {
					straight = 65535
				}
				return uint8(straight / 257)
			}
			dst.SetNRGBA(x, y, color.NRGBA{
				R: toStraight(sr), G: toStraight(sg), B: toStraight(sb), A: uint8(a / 257),
			})
		}
	}
	return dst
}

func writeICO(path string, images [][]byte) error {
	var output bytes.Buffer
	check(binary.Write(&output, binary.LittleEndian, uint16(0)))
	check(binary.Write(&output, binary.LittleEndian, uint16(1)))
	check(binary.Write(&output, binary.LittleEndian, uint16(len(images))))
	offset := uint32(6 + 16*len(images))
	for index, data := range images {
		size := iconSizes[index]
		encodedSize := byte(size)
		if size == 256 {
			encodedSize = 0
		}
		entry := iconEntry{
			width: encodedSize, height: encodedSize, planes: 1, bits: 32,
			size: uint32(len(data)), offset: offset,
		}
		check(binary.Write(&output, binary.LittleEndian, entry))
		offset += uint32(len(data))
	}
	for _, data := range images {
		_, _ = output.Write(data)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, output.Bytes(), 0o644)
}

func check(err error) {
	if err != nil {
		panic(err)
	}
}
