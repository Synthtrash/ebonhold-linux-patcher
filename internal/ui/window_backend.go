package ui

import (
	"fmt"
	"runtime"

	"fyne.io/fyne/v2"
	"github.com/go-gl/glfw/v3.3/glfw"
)

func prepareWindowBackend() (unlock func(), err error) {
	runtime.LockOSThread()
	defer func() {
		if raised := recover(); raised != nil {
			backendErr, ok := raised.(*glfw.Error)
			if !ok || backendErr.Code.String() != "NotInitialized" {
				panic(raised)
			}
			err = backendErr
		}
		if err != nil {
			runtime.UnlockOSThread()
			err = fmt.Errorf("could not open a %s display: %w", displayBackend, err)
		}
	}()
	if err = glfw.Init(); err != nil {
		return nil, err
	}
	// This GLFW binding may log an initialization error and return nil. The
	// hint checks that initialization succeeded before Fyne creates cursors.
	focus := glfw.True
	if nativeWayland {
		focus = glfw.False
	}
	glfw.WindowHint(glfw.FocusOnShow, focus)
	return runtime.UnlockOSThread, nil
}

const compactDisplayEdge = 800

func launcherWorkArea() fyne.Size {
	if monitor := glfw.GetPrimaryMonitor(); monitor != nil {
		_, _, width, height := monitor.GetWorkarea()
		if nativeWayland && width < height && width <= compactDisplayEdge {
			// GLFW reports the raw size of a portrait panel rotated to landscape.
			height = width
		}
		return fyne.NewSize(float32(width), float32(height))
	}
	return fyne.Size{}
}

func fitLauncherWindow(w fyne.Window, workArea fyne.Size) {
	// Read the actual pixel scale once the window exists. Leave space for
	// decorations and panels, which GLFW may not subtract on Wayland.
	x, y := w.Canvas().PixelCoordinateForPosition(fyne.NewPos(100, 100))
	if workArea.Width <= 16 || workArea.Height <= 64 || x <= 0 || y <= 0 {
		return
	}
	current := w.Canvas().Size()
	size := current.Min(fyne.NewSize(
		(workArea.Width-16)*100/float32(x),
		(workArea.Height-64)*100/float32(y),
	))
	if size != current {
		w.Resize(size)
	}
}
