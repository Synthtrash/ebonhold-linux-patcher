package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"

	"github.com/synthtrash/ebonhold-linux-patcher/internal/launcher"
	"github.com/synthtrash/ebonhold-linux-patcher/internal/ui"
	"golang.org/x/term"
)

func main() {
	root, err := launcher.ResolveLauncherRoot(os.Args[0])
	if err != nil {
		fail(err)
	}
	args := os.Args[1:]
	graphical := os.Getenv("DISPLAY") != "" || os.Getenv("WAYLAND_DISPLAY") != ""
	if len(args) == 0 && graphical || len(args) == 1 && args[0] == "--gui" {
		if err := ui.Run(launcher.NewDefaultAPI(), root); err != nil {
			fail(err)
		}
		return
	}
	console := launcher.NewConsole(os.Stdin, os.Stdout, os.Stderr, term.IsTerminal(int(os.Stdin.Fd())), graphical)
	if err := launcher.RunCLI(context.Background(), launcher.NewDefaultAPI(), root, args, console); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && exitErr.ExitCode() >= 0 {
			os.Exit(exitErr.ExitCode())
		}
		fail(err)
	}
}

func fail(err error) {
	fmt.Fprintf(os.Stderr, "[ERROR] %v\n", err)
	os.Exit(1)
}
