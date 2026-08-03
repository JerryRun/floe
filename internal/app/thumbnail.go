package app

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"image"
	"image/color"
	_ "image/gif"
	_ "image/jpeg"
	"image/png"
	"net/http"
	"strconv"
)

func (s *Server) thumbnail(w http.ResponseWriter, r *http.Request) {
	provider, ok := s.provider(w, r.URL.Query().Get("provider"))
	if !ok {
		return
	}
	data, err := provider.ReadFile(r.URL.Query().Get("path"), 25<<20)
	if err != nil {
		http.Error(w, "thumbnail unavailable", http.StatusUnsupportedMediaType)
		return
	}
	source, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		http.Error(w, "unsupported image", http.StatusUnsupportedMediaType)
		return
	}
	size, _ := strconv.Atoi(r.URL.Query().Get("size"))
	if size < 64 {
		size = 128
	}
	if size > 512 {
		size = 512
	}
	thumb := containThumbnail(source, size, size)
	digest := sha256.Sum256(data)
	etag := `"` + hex.EncodeToString(digest[:8]) + `"`
	if r.Header.Get("If-None-Match") == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "private, max-age=300")
	w.Header().Set("ETag", etag)
	_ = png.Encode(w, thumb)
}

func containThumbnail(source image.Image, maxWidth, maxHeight int) *image.NRGBA {
	bounds := source.Bounds()
	sourceWidth, sourceHeight := bounds.Dx(), bounds.Dy()
	width, height := maxWidth, maxHeight
	if sourceWidth*maxHeight > sourceHeight*maxWidth {
		height = max(1, sourceHeight*maxWidth/sourceWidth)
	} else {
		width = max(1, sourceWidth*maxHeight/sourceHeight)
	}
	destination := image.NewNRGBA(image.Rect(0, 0, maxWidth, maxHeight))
	for index := range destination.Pix {
		destination.Pix[index] = 0
	}
	offsetX, offsetY := (maxWidth-width)/2, (maxHeight-height)/2
	for y := 0; y < height; y++ {
		sourceY := bounds.Min.Y + y*sourceHeight/height
		for x := 0; x < width; x++ {
			sourceX := bounds.Min.X + x*sourceWidth/width
			r, g, b, a := source.At(sourceX, sourceY).RGBA()
			if a == 0 {
				continue
			}
			straight := func(value uint32) uint8 {
				result := uint64(value) * 65535 / uint64(a)
				if result > 65535 {
					result = 65535
				}
				return uint8(result / 257)
			}
			destination.SetNRGBA(offsetX+x, offsetY+y, color.NRGBA{R: straight(r), G: straight(g), B: straight(b), A: uint8(a / 257)})
		}
	}
	return destination
}
