package ui

import (
	_ "embed"
	"image"
	"image/color"
	"image/jpeg"
	"io"
	"math"
	"os"
	"path/filepath"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

//go:embed assets/launcher.png
var launcherMark []byte

//go:embed assets/ell-background.jpg
var ellBackground []byte

var launcherIcon = fyne.NewStaticResource("ebonhold.png", launcherMark)

func launcherBackdrop() fyne.CanvasObject {
	var art *canvas.Image
	if local := readPersonalWallpaper(); local != nil {
		art = canvas.NewImageFromImage(local)
	} else {
		art = canvas.NewImageFromResource(fyne.NewStaticResource("ell-background.jpg", ellBackground))
	}
	shade := image.NewNRGBA(image.Rect(0, 0, 128, 1))
	for x := range shade.Bounds().Dx() {
		shade.SetNRGBA(x, 0, color.NRGBA{R: 7, G: 14, B: 23, A: uint8(12 + 176*x/127)})
	}
	return container.NewStack(container.New(wallpaperCover{aspect: art.Aspect()}, art), canvas.NewImageFromImage(shade))
}

func readPersonalWallpaper() image.Image {
	base, err := os.UserConfigDir()
	if err != nil {
		return nil
	}
	path := filepath.Join(base, "ebonhold-launcher", "background.jpg")
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() > 16<<20 {
		return nil
	}
	file, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer file.Close()
	cfg, err := jpeg.DecodeConfig(file)
	if err != nil || cfg.Width < 1 || cfg.Height < 1 || int64(cfg.Width)*int64(cfg.Height) > 16_000_000 {
		return nil
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return nil
	}
	art, _ := jpeg.Decode(file)
	return art
}

// wallpaperCover fills the window without stretching the art or setting a layout minimum.
type wallpaperCover struct{ aspect float32 }

func (wallpaperCover) MinSize([]fyne.CanvasObject) fyne.Size { return fyne.Size{} }

func (c wallpaperCover) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	width, height := size.Width, size.Height
	if c.aspect > 0 {
		width = max(width, height*c.aspect)
		height = max(height, width/c.aspect)
	}
	for _, art := range objects {
		art.Resize(fyne.NewSize(width, height))
		art.Move(fyne.NewPos((size.Width-width)/2, (size.Height-height)/2))
	}
}

// Align card copy to the content edge and keep half of Fyne's vertical inset.
func flushText(text fyne.CanvasObject) *fyne.Container {
	pad := theme.InnerPadding()
	return container.New(layout.NewCustomPaddedLayout(-pad/2, -pad/2, -pad, -pad), text)
}

// Keep the desktop column centered without making it a minimum window width.
type centeredSetupColumn struct{}

func (centeredSetupColumn) MinSize(objects []fyne.CanvasObject) fyne.Size {
	return objects[0].MinSize().AddWidthHeight(theme.ScrollBarSize()*2, 0)
}

func (centeredSetupColumn) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	column := objects[0]
	width := min(max(738, column.MinSize().Width), size.Width-2*theme.ScrollBarSize())
	column.Move(fyne.NewPos((size.Width-width)/2, 0))
	column.Resize(fyne.NewSize(width, size.Height))
}

type homeLayout struct{ viewport *container.Scroll }

const (
	homeNewsWidth = 324
	homeRailWidth = 440
)

func (h homeLayout) MinSize(objects []fyne.CanvasObject) fyne.Size {
	news, rail := objects[0].MinSize(), objects[1].MinSize()
	gutter, gap := theme.ScrollBarSize(), theme.Padding()
	height := max(news.Height, rail.Height)
	// The scroll's minimum height depends on whether its viewport fits both columns.
	if h.viewport.Size().Width-gutter < max(homeNewsWidth, news.Width)+gap+max(homeRailWidth, rail.Width) {
		height = rail.Height + gap + news.Height
	}
	return fyne.NewSize(max(news.Width, rail.Width)+gutter, height)
}

func (h homeLayout) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	news, rail := objects[0], objects[1]
	newsMin, railMin := news.MinSize(), rail.MinSize()
	newsWidth, railWidth := max(homeNewsWidth, newsMin.Width), max(homeRailWidth, railMin.Width)
	width := size.Width - theme.ScrollBarSize()
	gap := theme.Padding()
	if width < newsWidth+gap+railWidth {
		rail.Move(fyne.NewPos(0, 0))
		rail.Resize(fyne.NewSize(width, railMin.Height))
		news.Move(fyne.NewPos(0, railMin.Height+gap))
		news.Resize(fyne.NewSize(width, newsMin.Height))
		return
	}
	news.Move(fyne.NewPos(0, size.Height-newsMin.Height))
	news.Resize(fyne.NewSize(newsWidth, newsMin.Height))
	rail.Move(fyne.NewPos(width-railWidth, 0))
	rail.Resize(fyne.NewSize(railWidth, size.Height))
}

type headerLayout struct {
	status     *widget.Label
	brandWidth float32
}

const (
	headerGap      = 8
	minStatusWidth = 100
	statusDotSize  = 9
)

func (h headerLayout) MinSize(objects []fyne.CanvasObject) fyne.Size {
	brand, nav := objects[0].MinSize(), objects[1].MinSize()
	return fyne.NewSize(brand.Width+nav.Width+statusDotSize+theme.Padding()+2*headerGap, max(brand.Height, nav.Height, h.status.MinSize().Height))
}

func (h headerLayout) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	brand, nav := objects[0].MinSize(), objects[1].MinSize()
	reserved := min(minStatusWidth, size.Width-brand.Width-nav.Width-2*headerGap)
	reserved = max(statusDotSize+theme.Padding(), reserved)
	brand.Width = max(brand.Width, min(h.brandWidth, size.Width-nav.Width-reserved-2*headerGap))
	// Keep the tabs centered unless the window needs their space for the brand or status.
	navX := min(max((size.Width-nav.Width)/2, brand.Width+headerGap), size.Width-nav.Width-reserved-headerGap)
	gap := theme.Padding()
	pad := theme.InnerPadding()
	textWidth := fyne.MeasureText(h.status.Text, theme.TextSize(), h.status.TextStyle).Width
	// Fyne rounds down the ellipsis width limit. Give full strings a whole-pixel slot.
	textWidth = max(0, min(float32(math.Ceil(float64(textWidth))), size.Width-navX-nav.Width-headerGap-statusDotSize-gap))
	textLeft := size.Width - textWidth

	place := func(obj fyne.CanvasObject, bounds fyne.Size, x float32) {
		obj.Resize(bounds)
		obj.Move(fyne.NewPos(x, (size.Height-bounds.Height)/2))
	}
	place(objects[0], brand, 0)
	place(objects[1], nav, navX)
	// Match the visible glyphs' center rather than the label's padded box.
	objects[2].Resize(fyne.NewSquareSize(statusDotSize))
	objects[2].Move(fyne.NewPos(textLeft-gap-statusDotSize, (size.Height-statusDotSize)/2+2))
	place(h.status, fyne.NewSize(textWidth+2*pad, h.status.MinSize().Height), textLeft-pad)
}
