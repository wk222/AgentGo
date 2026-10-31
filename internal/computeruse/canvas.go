package computeruse

import (
	"bytes"
	"fmt"
	"image"
	"image/png"
	"math"

	xdraw "golang.org/x/image/draw"
)

const (
	// MaxImageW/H bound the screenshot sent to the model (same canvas Cursor's
	// sidecar uses). A smaller display is never upscaled.
	MaxImageW = 1456
	MaxImageH = 840
	// MaxZoomEdge bounds the long edge of a zoom image.
	MaxZoomEdge = 1280
)

// frame is what the model last saw: the physical screen and the scaled image
// derived from it. Model coordinates are pixels of that image.
type frame struct {
	screenW, screenH int
	imgW, imgH       int
}

func newFrame(screenW, screenH int) frame {
	s := math.Min(float64(MaxImageW)/float64(screenW), float64(MaxImageH)/float64(screenH))
	if s > 1 {
		s = 1
	}
	return frame{
		screenW: screenW, screenH: screenH,
		imgW: max(1, int(math.Round(float64(screenW)*s))),
		imgH: max(1, int(math.Round(float64(screenH)*s))),
	}
}

func (f frame) sameScreen(w, h int) bool { return f.screenW == w && f.screenH == h }

// toScreen maps image pixels to physical screen pixels (pixel centers).
func (f frame) toScreen(x, y float64) (float64, float64, *Error) {
	if x < 0 || y < 0 || x >= float64(f.imgW) || y >= float64(f.imgH) {
		return 0, 0, NewError(CodeInvalidArguments,
			"(%.0f, %.0f) is outside the screenshot, which is %dx%d pixels with origin top-left", x, y, f.imgW, f.imgH)
	}
	sx := (x + 0.5) * float64(f.screenW) / float64(f.imgW)
	sy := (y + 0.5) * float64(f.screenH) / float64(f.imgH)
	return math.Min(sx, float64(f.screenW-1)), math.Min(sy, float64(f.screenH-1)), nil
}

// rectToScreen maps an image-space rectangle to an integer screen rectangle.
func (f frame) rectToScreen(x1, y1, x2, y2 float64) (image.Rectangle, *Error) {
	if x1 > x2 {
		x1, x2 = x2, x1
	}
	if y1 > y2 {
		y1, y2 = y2, y1
	}
	if x1 < 0 || y1 < 0 || x2 > float64(f.imgW) || y2 > float64(f.imgH) {
		return image.Rectangle{}, NewError(CodeInvalidArguments,
			"zoom region corners must lie within the %dx%d screenshot, got (%.0f,%.0f)-(%.0f,%.0f)", f.imgW, f.imgH, x1, y1, x2, y2)
	}
	kx, ky := float64(f.screenW)/float64(f.imgW), float64(f.screenH)/float64(f.imgH)
	r := image.Rect(int(math.Floor(x1*kx)), int(math.Floor(y1*ky)), int(math.Ceil(x2*kx)), int(math.Ceil(y2*ky)))
	if r.Dx() < 8 || r.Dy() < 8 {
		return image.Rectangle{}, NewError(CodeInvalidArguments,
			"zoom region is too thin to render (%dx%d screen pixels); pick at least about 8 pixels each way", r.Dx(), r.Dy())
	}
	return r.Intersect(image.Rect(0, 0, f.screenW, f.screenH)), nil
}

// zoom is one region the model asked to see closer. Its pixels map linearly
// onto rect (physical screen pixels).
type zoom struct {
	rect       image.Rectangle
	imgW, imgH int
}

func (z zoom) toScreen(x, y float64) (float64, float64, *Error) {
	if x < 0 || y < 0 || x >= float64(z.imgW) || y >= float64(z.imgH) {
		return 0, 0, NewError(CodeInvalidArguments,
			"(%.0f, %.0f) is outside the zoom image, which is %dx%d pixels", x, y, z.imgW, z.imgH)
	}
	sx := float64(z.rect.Min.X) + (x+0.5)*float64(z.rect.Dx())/float64(z.imgW)
	sy := float64(z.rect.Min.Y) + (y+0.5)*float64(z.rect.Dy())/float64(z.imgH)
	return sx, sy, nil
}

// scaleTo resizes src to w x h (no-op copy semantics when sizes match).
func scaleTo(src image.Image, w, h int) *image.RGBA {
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	xdraw.BiLinear.Scale(dst, dst.Bounds(), src, src.Bounds(), xdraw.Src, nil)
	return dst
}

func encodePNG(img image.Image) ([]byte, error) {
	var buf bytes.Buffer
	enc := png.Encoder{CompressionLevel: png.BestSpeed}
	if err := enc.Encode(&buf, img); err != nil {
		return nil, fmt.Errorf("png encode: %w", err)
	}
	return buf.Bytes(), nil
}

// renderFrame returns the model-sized PNG of a full-screen capture.
func renderFrame(full *image.RGBA, f frame) ([]byte, error) {
	if f.imgW == full.Bounds().Dx() && f.imgH == full.Bounds().Dy() {
		return encodePNG(full)
	}
	return encodePNG(scaleTo(full, f.imgW, f.imgH))
}

// renderZoom crops rect out of the capture at native resolution, shrinking
// only when the long edge exceeds MaxZoomEdge.
func renderZoom(full *image.RGBA, rect image.Rectangle) (png []byte, w, h int, err error) {
	crop := full.SubImage(rect)
	w, h = rect.Dx(), rect.Dy()
	if long := max(w, h); long > MaxZoomEdge {
		s := float64(MaxZoomEdge) / float64(long)
		w, h = max(1, int(math.Round(float64(w)*s))), max(1, int(math.Round(float64(h)*s)))
		png, err = encodePNG(scaleTo(crop, w, h))
		return
	}
	png, err = encodePNG(crop)
	return
}
