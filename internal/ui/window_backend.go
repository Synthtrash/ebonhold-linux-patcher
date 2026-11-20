package ui

import (
	"fmt"
	"runtime"

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
