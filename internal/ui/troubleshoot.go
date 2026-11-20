package ui

import (
	"context"
	"errors"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"
	"github.com/synthtrash/ebonhold-linux-patcher/internal/launcher"
)

type launchIssue string

const (
	issueMissingLauncher launchIssue = "missing-launcher"
	issueMissingApp      launchIssue = "missing-app"
	issueFlatpakAccess   launchIssue = "flatpak-access"
	issueWineDisplay     launchIssue = "wine-display"
	issueVCRuntime       launchIssue = "vc-runtime"
	issueClientExt       launchIssue = "clientext"
	issueGeneric         launchIssue = "generic"
)

func classifyLaunchIssue(manager string, err error, output string) launchIssue {
	var issue launchIssue
	for line := range strings.SplitSeq(strings.ToLower(output), "\n") {
		switch {
		case strings.Contains(line, "nodrv_createwindow") && (strings.Contains(line, "$display") || strings.Contains(line, "x server")),
			strings.Contains(line, "err:winediag") && strings.Contains(line, "could not open display"):
			return issueWineDisplay
		case (strings.Contains(line, "vcruntime140") || strings.Contains(line, "msvcp140")) &&
			(strings.Contains(line, "not found") || strings.Contains(line, "missing") || strings.Contains(line, "cannot find")):
			issue = issueVCRuntime
		case issue != issueVCRuntime && strings.Contains(line, "clientext.dll") &&
			(strings.Contains(line, "failed") || strings.Contains(line, "not found") || strings.Contains(line, "could not load") || strings.Contains(line, "missing")):
			issue = issueClientExt
		case issue == "" && (strings.Contains(line, "no application associated") || strings.Contains(line, "no handler") ||
			strings.Contains(line, "application not found") || (manager == "bottles-flatpak" &&
			strings.Contains(line, "com.usebottles.bottles") && strings.Contains(line, "not installed"))):
			issue = issueMissingApp
		}
	}
	if issue != "" {
		return issue
	}
	if err == nil {
		return ""
	}
	if errors.Is(err, exec.ErrNotFound) {
		return issueMissingLauncher
	}
	lowerErr := strings.ToLower(err.Error())
	if strings.Contains(lowerErr, "no application associated") || strings.Contains(lowerErr, "no handler") || strings.Contains(lowerErr, "application not found") {
		return issueMissingApp
	}
	return issueGeneric
}

func checkFlatpakLaunch(ctx context.Context, manager, executable string) launchIssue {
	if manager != "bottles-flatpak" || executable == "" {
		return ""
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "flatpak", "info", "--file-access="+executable, "com.usebottles.bottles")
	cmd.Env = append(cmd.Environ(), "LC_ALL=C")
	output, err := cmd.CombinedOutput()
	if err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return issueMissingLauncher
		}
		text := strings.ToLower(string(output))
		if strings.Contains(text, "not installed") || strings.Contains(text, "no such ref") || strings.Contains(text, "no installed ref") {
			return issueMissingApp
		}
		return ""
	}
	if strings.EqualFold(strings.TrimSpace(string(output)), "hidden") {
		return issueFlatpakAccess
	}
	return ""
}

func showTroubleshoot(w fyne.Window, tr translate, managerName string, issue launchIssue, localOutput string, openCVars func()) {
	title := tr("Troubleshoot")
	summary := tr("Choose what went wrong.")
	if issue != "" {
		title = tr("Could not launch game")
		summary = issueMessage(tr, issue)
	}
	size := w.Canvas().Size()
	var d *dialog.CustomDialog
	content := widget.NewAccordion()
	selected := 0
	add := func(heading string, body fyne.CanvasObject, match bool) {
		if match {
			selected = len(content.Items)
		}
		content.Append(widget.NewAccordionItem(tr(heading), body))
	}
	add("Play does not open the game", wrappedLabel(tr("Try the same Wow.exe directly in Bottles, Steam, or Lutris. If it opens there, check the game app and shortcut selected in Setup. If the app itself is missing, install it or choose one you already use.")), false)
	add("Steam or Steam Deck", wrappedLabel(tr("Find the Ebonhold shortcut in Steam. Under Properties > Compatibility, enable a Steam Play compatibility tool and choose an installed Proton version. Try starting the shortcut from Steam before using Play here.")), false)
	add("Flatpak cannot find Wow.exe", wrappedLabel(tr("If your game app is a Flatpak, open it in Flatseal and add only the Ebonhold game folder under Other files. Restart the game app and try again. You can also use a folder the app can already access.")), issue == issueFlatpakAccess)
	runtimeLink := widget.NewHyperlink(tr("Visual C++ downloads from Microsoft"), &url.URL{Scheme: "https", Host: "learn.microsoft.com", Path: "/en-us/cpp/windows/latest-supported-vc-redist", RawQuery: "view=msvc-170"})
	add("ClientExt.dll or Visual C++ errors", container.NewVBox(
		wrappedLabel(tr("If ClientExt.dll is missing or won't load, run Verify files, then Update files if repair is needed.")),
		wrappedLabel(tr("If the error names a missing vcruntime140 or msvcp140 DLL, close WoW and download the Visual C++ v14 installer matching the app: x86 for 32-bit, x64 for 64-bit.")),
		container.NewHBox(runtimeLink),
		wrappedLabel(tr("In the existing Ebonhold bottle, choose Run Executable, select the installer, then choose Install or Repair. Use x64 only in a 64-bit bottle. In Steam or Lutris, use the same Wine or Proton setup as the game. Don't create a new one.")),
	), issue == issueClientExt || issue == issueVCRuntime)
	add("Wine cannot open the display", wrappedLabel(tr("Try the same game directly in your game app. On Wayland, check that XWayland is available and that your game app is running in your current desktop session. The launcher's native Wayland build doesn't change how Wine opens its windows.")), issue == issueWineDisplay)
	display := container.NewVBox(wrappedLabel(tr("Close WoW. Open Game CVars, load the windowed preset, and apply it. If you need to undo it, restore WTF/Config.wtf.before-launcher-cvars if present.")))
	if openCVars != nil {
		display.Add(container.NewHBox(widget.NewButton(tr("Open Game CVars"), func() {
			d.Hide()
			openCVars()
		})))
	}
	add("Black bars or offset clicks", display, false)
	content.Open(selected)

	copyDetails := widget.NewButton(tr("Copy details"), func() {
		fields := []string{
			tr("Launcher version: %s", launcher.BuildVersion()),
			tr("Desktop session: %s", tr(displaySession())),
		}
		if distro := distroSummary(); distro != "" {
			fields = append(fields, tr("Distribution: %s", distro))
		}
		if label := safeManagerLabel(tr, managerName); label != "" {
			fields = append(fields, tr("Game app: %s", label))
		}
		if issue != "" {
			fields = append(fields, tr("Detected issue: %s", summary))
		}
		w.Clipboard().SetContent(strings.Join(fields, "\n"))
	})
	buttons := []fyne.CanvasObject{copyDetails}
	if localOutput != "" {
		buttons = append(buttons, widget.NewButton(tr("View local output"), func() {
			entry := widget.NewMultiLineEntry()
			entry.Wrapping = fyne.TextWrapBreak
			entry.Scroll = fyne.ScrollVerticalOnly
			entry.TextStyle.Monospace = true
			entry.SetText(localOutput)
			entry.Disable()
			note := wrappedLabel(tr("This output stays out of Copy details. Check it for private information before sharing."))
			view := dialog.NewCustom(tr("Local launch output"), tr("Close"), container.NewBorder(note, nil, nil, nil, entry), w)
			view.Resize(fyne.NewSize(min(660, size.Width-32), min(420, size.Height-32)))
			view.Show()
		}))
	}
	d = dialog.NewCustomWithoutButtons(title, container.NewBorder(wrappedLabel(summary), nil, nil, nil, container.NewVScroll(content)), w)
	d.SetButtons(append(buttons, widget.NewButton(tr("Close"), d.Hide)))
	d.Resize(fyne.NewSize(min(660, size.Width-32), min(560, size.Height-32)))
	d.Show()
}

func issueMessage(tr translate, issue launchIssue) string {
	messages := map[launchIssue]string{
		issueMissingLauncher: "The selected launcher command was not found.",
		issueMissingApp:      "No installed application or handler was found for this launch.",
		issueFlatpakAccess:   "Flatpak reports that Bottles cannot access the selected game folder.",
		issueWineDisplay:     "Wine could not open the X11 display.",
		issueVCRuntime:       "The launch output names a missing Microsoft Visual C++ runtime DLL.",
		issueClientExt:       "The launch output reports a ClientExt.dll error.",
		issueGeneric:         "The game launch request failed.",
	}
	return tr(messages[issue])
}

func safeManagerLabel(tr translate, label string) string {
	for _, known := range []string{"Steam", "Lutris", "Bottles (native)", "Bottles (Flatpak)", tr("Bottles (native)"), tr("Bottles (Flatpak)"), tr("Start the game myself")} {
		if label == known {
			return label
		}
	}
	return ""
}

func displaySession() string {
	switch os.Getenv("XDG_SESSION_TYPE") {
	case "wayland":
		return "Wayland"
	case "x11":
		return "X11"
	}
	if os.Getenv("WAYLAND_DISPLAY") != "" || os.Getenv("WAYLAND_SOCKET") != "" {
		return "Wayland"
	}
	if os.Getenv("DISPLAY") != "" {
		return "X11"
	}
	return "Unknown"
}

func distroSummary() string {
	data, err := os.ReadFile("/etc/os-release")
	if errors.Is(err, os.ErrNotExist) {
		data, err = os.ReadFile("/usr/lib/os-release")
	}
	if err != nil {
		return ""
	}
	var id, version string
	for line := range strings.SplitSeq(string(data), "\n") {
		key, value, ok := strings.Cut(line, "=")
		if !ok || (key != "ID" && key != "VERSION_ID") {
			continue
		}
		value = strings.Trim(value, "\"'")
		if value == "" || len(value) > 64 || strings.ContainsFunc(value, func(r rune) bool {
			return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '.' || r == '-' || r == '_')
		}) {
			continue
		}
		if key == "ID" {
			id = value
		} else {
			version = value
		}
	}
	return strings.TrimSpace(id + " " + version)
}

