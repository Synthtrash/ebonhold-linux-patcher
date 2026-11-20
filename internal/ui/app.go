package ui

import (
	"context"
	"errors"
	"fmt"
	"image/color"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/storage"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	"github.com/synthtrash/ebonhold-linux-patcher/internal/launcher"
)

const (
	colorBG     = "#080f19"
	colorRail   = "#0c1724"
	colorCard   = "#102032"
	colorButton = "#1b3247"
	colorRaised = "#26465f"
	colorBorder = "#304a63"
	colorText   = "#eaf4ff"
	colorMuted  = "#afc4d9"
	colorBlue   = "#91dcff"
	colorGreen  = "#8be2ad"
	colorAmber  = "#ffcc91"
	colorRed    = "#ff9fa8"
)

var terminalEscape = regexp.MustCompile(`\x1b\[[0-?]*[ -/]*[@-~]`)

var frostColors = map[fyne.ThemeColorName]color.Color{
	theme.ColorNameBackground:          hexColor(colorBG),
	theme.ColorNameButton:              hexColor(colorButton),
	theme.ColorNameDisabledButton:      hexColor(colorRail),
	theme.ColorNameDisabled:            hexColor(colorMuted),
	theme.ColorNameError:               hexColor(colorRed),
	theme.ColorNameFocus:               color.NRGBA{R: 70, G: 127, B: 168, A: 199},
	theme.ColorNameForeground:          hexColor(colorText),
	theme.ColorNameHyperlink:           hexColor(colorBlue),
	theme.ColorNameForegroundOnError:   hexColor(colorBG),
	theme.ColorNameForegroundOnPrimary: hexColor(colorBG),
	theme.ColorNameHover:               hexColor(colorRaised),
	theme.ColorNameInputBackground:     hexColor(colorRail),
	theme.ColorNameInputBorder:         hexColor(colorBorder),
	theme.ColorNameOverlayBackground:   hexColor(colorCard),
	theme.ColorNamePlaceHolder:         hexColor(colorMuted),
	theme.ColorNamePrimary:             hexColor(colorBlue),
	theme.ColorNameScrollBar:           hexColor(colorBorder),
	theme.ColorNameSeparator:           hexColor(colorBorder),
	theme.ColorNameSelection:           hexColor(colorRaised),
	theme.ColorNameSuccess:             hexColor(colorGreen),
	theme.ColorNameWarning:             hexColor(colorAmber),
}

type frostTheme struct {
	fyne.Theme
	compact bool
}

func (t frostTheme) Color(n fyne.ThemeColorName, v fyne.ThemeVariant) color.Color {
	if c, ok := frostColors[n]; ok {
		return c
	}
	return t.Theme.Color(n, v)
}
func (t frostTheme) Size(n fyne.ThemeSizeName) float32 {
	size := t.Theme.Size(n)
	if n == theme.SizeNameText && !t.compact {
		return size + 2
	}
	return size
}

func hexColor(s string) color.Color {
	var r, g, b uint8
	_, _ = fmt.Sscanf(s, "#%02x%02x%02x", &r, &g, &b)
	return color.NRGBA{R: r, G: g, B: b, A: 255}
}

type loginPrompt struct {
	win fyne.Window
	tr  translate
}

func (p loginPrompt) PromptLogin(ctx context.Context) (string, string, error) {
	type answer struct {
		user, pass string
		ok         bool
	}
	done := make(chan answer, 1)
	fyne.Do(func() {
		if ctx.Err() != nil {
			return
		}
		user, pass := widget.NewEntry(), widget.NewPasswordEntry()
		user.SetPlaceHolder(p.tr("Username"))
		pass.SetPlaceHolder(p.tr("Password"))
		note := wrappedLabel(p.tr("This signs you in for updates and add-ons. In WoW, select Remember Account Name to save only the name. Anyone who can read that game folder can see it. Leave this off on shared computers."))
		items := []*widget.FormItem{
			widget.NewFormItem(p.tr("Username"), user),
			widget.NewFormItem(p.tr("Password"), pass),
			widget.NewFormItem("", note),
		}
		d := dialog.NewForm(p.tr("Sign in"), p.tr("Sign in"), p.tr("Cancel"), items, func(ok bool) {
			done <- answer{user: user.Text, pass: pass.Text, ok: ok}
		}, p.win)
		size := p.win.Canvas().Size()
		d.Resize(fyne.NewSize(min(520, size.Width-32), min(310, size.Height-32)))
		d.Show()
	})
	select {
	case result := <-done:
		if !result.ok {
			return "", "", context.Canceled
		}
		return result.user, result.pass, nil
	case <-ctx.Done():
		return "", "", ctx.Err()
	}
}

func queryServerStatus(ctx context.Context, api launcher.API, root string) (launcher.ServerStatus, bool, error) {
	token, err := (&launcher.CredentialStore{Root: root}).Token()
	if err != nil {
		return launcher.ServerStatus{}, false, err
	}
	if token == "" {
		return launcher.ServerStatus{}, false, errors.New("no cached sign-in")
	}
	status, err := api.Status(ctx, token)
	var httpErr *launcher.HTTPError
	if errors.As(err, &httpErr) && (httpErr.StatusCode == 401 || httpErr.StatusCode == 403) {
		return launcher.ServerStatus{}, false, err
	}
	return status, true, err
}

func setEnabled(control fyne.Disableable, enabled bool) {
	if enabled {
		control.Enable()
	} else {
		control.Disable()
	}
}

func Run(api *launcher.HTTPAPI, launcherRoot string) error {
	if api == nil {
		return errors.New("launcher API is unavailable")
	}
	launcherPath, err := os.Executable()
	if err != nil || launcherPath == "" {
		launcherPath = filepath.Join(launcherRoot, filepath.Base(os.Args[0]))
	}
	launcherPath, err = filepath.Abs(launcherPath)
	if err != nil {
		return errors.New("could not resolve launcher executable path")
	}
	prefsStore, err := launcher.NewPreferencesStore()
	if err != nil {
		return err
	}
	prefs, err := prefsStore.Load()
	if err != nil {
		return err
	}
	if prefs.GameSlug != "roguelike" && prefs.GameSlug != "roguelike-prod" {
		prefs.GameSlug = "roguelike-prod"
	}
	tr := launcherText(prefs.LauncherLanguage)
	a := app.NewWithID("com.project-ebonhold.launcher")
	unlockThread, err := prepareWindowBackend()
	if err != nil {
		return fmt.Errorf("initialize window backend: %w", err)
	}
	defer unlockThread()
	workArea := launcherWorkArea()
	shortEdge := min(workArea.Width, workArea.Height)
	compact := shortEdge > 0 && shortEdge <= compactDisplayEdge
	if compact && !nativeWayland && os.Getenv("FYNE_DISABLE_DPI_DETECTION") == "" {
		// A small panel can have high physical DPI even at 100% desktop scale.
		if err := os.Setenv("FYNE_DISABLE_DPI_DETECTION", "1"); err != nil {
			return err
		}
	}
	a.Settings().SetTheme(frostTheme{Theme: theme.DefaultTheme(), compact: compact})
	a.SetIcon(launcherIcon)
	w := a.NewWindow(tr("Ebonhold Launcher"))
	w.SetIcon(launcherIcon)
	statusTicker := time.NewTicker(30 * time.Second)
	defer statusTicker.Stop()
	stop := setupWindow(w, api, launcherPath, prefsStore, prefs, statusTicker.C, api.News)
	defer stop()
	w.Show()
	fitLauncherWindow(w, workArea)
	a.Run()
	return nil
}

func setupWindow(w fyne.Window, api launcher.API, launcherPath string, prefsStore *launcher.PreferencesStore, prefs launcher.Preferences, statusTicks <-chan time.Time, fetchNews func(context.Context) ([]launcher.NewsArticle, error)) func() {
	w.SetPadded(false)
	w.Resize(fyne.NewSize(1024, 710))
	var stop func()
	var render func(launcher.Preferences, string)
	render = func(prefs launcher.Preferences, notice string) {
		if stop != nil {
			stop()
			w.Canvas().Unfocus()
			w.SetTitle(launcherText(prefs.LauncherLanguage)("Ebonhold Launcher"))
		}
		stop = renderWindow(w, api, launcherPath, prefsStore, prefs, statusTicks, fetchNews, render, notice)
	}
	render(prefs, "")
	return func() { stop() }
}

func renderWindow(w fyne.Window, api launcher.API, launcherPath string, prefsStore *launcher.PreferencesStore, prefs launcher.Preferences, statusTicks <-chan time.Time, fetchNews func(context.Context) ([]launcher.NewsArticle, error), reload func(launcher.Preferences, string), notice string) func() {
	runningLanguage := prefs.LauncherLanguage
	tr := launcherText(runningLanguage)
	windowCtx, cancelWindow := context.WithCancel(context.Background())
	var copyWork sync.WaitGroup
	w.SetOnClosed(cancelWindow)
	// A language change replaces this view without closing the native window.
	post := func(fn func()) {
		fyne.Do(func() {
			if windowCtx.Err() == nil {
				fn()
			}
		})
	}
	var mu sync.Mutex
	var status *widget.Label
	setupNotice := widget.NewLabel("")
	setupNotice.Wrapping = fyne.TextWrapWord
	save := func() bool {
		mu.Lock()
		err := prefsStore.Save(prefs)
		mu.Unlock()
		if err != nil && status != nil {
			status.SetText(tr("Preferences not saved: %s", err.Error()))
		}
		return err == nil
	}
	profileLabel := func(slug string) string {
		if slug == "roguelike" {
			return "PTR"
		}
		return "Production"
	}
	root := widget.NewEntry()
	root.SetText(prefs.RootForProfile(prefs.GameSlug))
	root.SetPlaceHolder(tr("Select a separate %s game folder", profileLabel(prefs.GameSlug)))
	gameChoices := []string{tr("Production realm"), tr("Public test realm (PTR)")}
	game := widget.NewSelect(gameChoices, nil)
	if prefs.GameSlug == "roguelike" {
		game.SetSelected(gameChoices[1])
	} else {
		game.SetSelected(gameChoices[0])
	}
	managerNames := map[string]string{"": tr("Start the game myself"), "steam": "Steam", "lutris": "Lutris", "bottles-native": tr("Bottles (native)"), "bottles-flatpak": "Bottles (Flatpak)"}
	launchTarget := func() launcher.LaunchTarget {
		mu.Lock()
		defer mu.Unlock()
		return prefs.LaunchTargetForProfile(prefs.GameSlug)
	}
	var refreshSetup func()
	var homeGame *widget.Select
	var profileHint *widget.Label
	profileHintText := func(slug string) string {
		mu.Lock()
		activeRoot := prefs.RootForProfile(slug)
		otherSlug := "roguelike"
		if slug == "roguelike" {
			otherSlug = "roguelike-prod"
		}
		otherRoot := prefs.RootForProfile(otherSlug)
		mu.Unlock()
		if slug == "roguelike" && setupFolderMissing(activeRoot) {
			return tr("Choose a separate PTR folder or create a copy from Production in Setup.")
		}
		if strings.TrimSpace(activeRoot) == "" {
			return tr("Choose the Production client folder in Setup.")
		}
		if slug == "roguelike-prod" && setupFolderMissing(otherRoot) {
			return tr("To try PTR, switch profiles and create a separate copy in Setup.")
		}
		return tr("Production and PTR use separate folders.")
	}
	var refreshServerStatus func()
	var refreshAddons func()
	var statusInFlight bool
	var sessionChecked, signInNeeded, signingIn bool
	var statusTarget, addonTarget uint64
	var updateFiles *updateFilePane
	var updateResult launcher.UpdateResult
	var updateHasRun, updateInProgress, updateFailed, updateVerify bool
	var copyInProgress bool
	var setCopyActivity func(string)
	var playButton, settingsButton, copyPTR, searchFolders *widget.Button
	var updateError string
	resetUpdate := func() {
		updateHasRun, updateFailed, updateVerify = false, false, false
		updateResult = launcher.UpdateResult{}
		updateError = ""
		if updateFiles != nil {
			updateFiles.reset(tr("Run Update to see files that change."))
		}
	}
	clientTargetChanged := func() {
		sessionChecked = false
		mu.Lock()
		statusTarget++
		addonTarget++
		mu.Unlock()
		if refreshSetup != nil {
			refreshSetup()
		}
		if refreshServerStatus != nil {
			refreshServerStatus()
		}
		if refreshAddons != nil {
			refreshAddons()
		}
	}
	root.OnChanged = func(s string) {
		mu.Lock()
		profile := prefs.GameSlug
		updated := prefs
		mu.Unlock()
		previousRoot := updated.RootForProfile(profile)
		if previousRoot == s {
			return
		}
		if err := updated.ValidateRootForProfile(profile, s); err != nil {
			root.SetText(updated.RootForProfile(profile))
			setupNotice.SetText(tr(err.Error()))
			if status != nil {
				status.SetText(tr(err.Error()))
			}
			return
		}
		mu.Lock()
		prefs.SetRootForProfile(profile, s)
		mu.Unlock()
		if !save() {
			mu.Lock()
			prefs = updated
			mu.Unlock()
			root.SetText(previousRoot)
			return
		}
		setupNotice.SetText("")
		resetUpdate()
		if status != nil {
			if strings.TrimSpace(s) == "" {
				status.SetText(tr("Choose a separate %s folder in Setup", profileLabel(profile)))
			} else {
				status.SetText(tr("Client folder selected · ready to update or verify"))
			}
		}
		if profileHint != nil {
			profileHint.SetText(profileHintText(profile))
		}
		clientTargetChanged()
	}
	game.OnChanged = func(s string) {
		if homeGame != nil && homeGame.Selected != s {
			homeGame.SetSelected(s)
		}
		newSlug := "roguelike-prod"
		if s == gameChoices[1] {
			newSlug = "roguelike"
		}
		mu.Lock()
		changed := prefs.GameSlug != newSlug
		prefs.GameSlug = newSlug
		newRoot := prefs.RootForProfile(newSlug)
		mu.Unlock()
		if changed {
			resetUpdate()
		}
		root.SetPlaceHolder(tr("Select a separate %s game folder", profileLabel(newSlug)))
		if root.Text != newRoot {
			root.SetText(newRoot)
		}
		if status != nil {
			if strings.TrimSpace(newRoot) == "" {
				status.SetText(tr("Choose a separate %s folder in Setup", profileLabel(newSlug)))
			} else {
				status.SetText(tr("Client folder selected · ready to update or verify"))
			}
		}
		if profileHint != nil {
			profileHint.SetText(profileHintText(newSlug))
		}
		save()
		if changed {
			clientTargetChanged()
		}
	}
	statusMessage := tr("Choose a separate %s folder in Setup", profileLabel(prefs.GameSlug))
	if strings.TrimSpace(prefs.RootForProfile(prefs.GameSlug)) != "" {
		statusMessage = tr("Client folder selected · ready to update or verify")
	}
	if notice != "" {
		statusMessage = tr(notice)
	}
	status = widget.NewLabel(statusMessage)
	status.Wrapping = fyne.TextWrapWord
	status.Importance = widget.LowImportance
	statusRow := flushText(status)
	var header *fyne.Container
	serverStatus := widget.NewLabel(tr("Checking shared server status…"))
	serverStatus.Truncation = fyne.TextTruncateEllipsis
	serverStatusDot := canvas.NewCircle(hexColor(colorAmber))
	setServerStatus := func(text string, tint color.Color) {
		serverStatus.SetText(text)
		serverStatusDot.FillColor = tint
		serverStatusDot.Refresh()
		if header != nil {
			header.Refresh()
		}
	}
	progressText := widget.NewLabel("")
	progressText.Truncation = fyne.TextTruncateEllipsis
	progress := widget.NewProgressBar()
	activityProgress := container.NewVBox(flushText(progressText), progress)
	activityProgress.Hide()
	var refreshUpdateUI func()
	var switchScreen func(string)
	newAuthenticator := func(root string, readOnly bool) *launcher.Authenticator {
		return &launcher.Authenticator{API: api, Store: &launcher.CredentialStore{Root: root}, Prompt: loginPrompt{win: w, tr: tr}, ReadOnly: readOnly}
	}
	var addonIDs map[string]int64
	async := func(task func(context.Context)) { go task(windowCtx) }
	showDetails := func(title, details string) {
		details = tr("Launcher version: %s", launcher.BuildVersion()) + "\n\n" + details
		entry := widget.NewMultiLineEntry()
		entry.Wrapping = fyne.TextWrapBreak
		entry.Scroll = fyne.ScrollVerticalOnly
		entry.TextStyle.Monospace = true
		entry.SetText(details)
		entry.Disable()
		copy := widget.NewButton(tr("Copy details"), func() { w.Clipboard().SetContent(details) })
		d := dialog.NewCustom(tr(title), tr("Close"), container.NewBorder(nil, copy, nil, nil, entry), w)
		size := w.Canvas().Size()
		d.Resize(fyne.NewSize(min(680, size.Width-32), min(420, size.Height-32)))
		d.Show()
	}
	showAuthWarning := func(auth *launcher.Authenticator) {
		if auth.Warning == "" {
			return
		}
		warning := auth.Warning
		auth.Warning = ""
		post(func() { showDetails(tr("Sign-in warning"), tr(warning)) })
	}
	authDetails := func(details string, auth *launcher.Authenticator) string {
		if auth.Warning != "" {
			return details + "\n\n" + auth.Warning
		}
		return details
	}
	showErr := func(title string, e error) {
		if e == nil {
			return
		}
		post(func() { status.SetText(tr(title)); showDetails(title, tr(e.Error())) })
	}
	selectedRoot := func() string {
		mu.Lock()
		defer mu.Unlock()
		return prefs.RootForProfile(prefs.GameSlug)
	}
	selectedGame := func() string { mu.Lock(); defer mu.Unlock(); return prefs.GameSlug }
	clientRoot := func() (string, error) {
		mu.Lock()
		current := prefs
		mu.Unlock()
		if err := current.ValidateRoots(); err != nil {
			return "", err
		}
		root := current.RootForProfile(current.GameSlug)
		if strings.TrimSpace(root) == "" {
			return "", errors.New(tr("Choose a separate %s folder in Setup", profileLabel(current.GameSlug)))
		}
		absolute, err := filepath.Abs(root)
		if err != nil {
			return "", errors.New("could not resolve the selected game folder")
		}
		info, err := os.Lstat(absolute)
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return "", errors.New("client folder must be an existing, non-symlink directory")
		}
		return absolute, nil
	}

	refreshServerStatus = func() {
		rootPath, err := clientRoot()
		if err != nil {
			setServerStatus(tr("Server status unknown"), hexColor(colorAmber))
			return
		}
		mu.Lock()
		if statusInFlight {
			mu.Unlock()
			return
		}
		statusInFlight = true
		target := statusTarget
		profile := prefs.GameSlug
		mu.Unlock()
		setServerStatus(tr("Checking shared server status…"), hexColor(colorAmber))
		go func() {
			server, authenticated, err := queryServerStatus(windowCtx, api, rootPath)
			if windowCtx.Err() != nil {
				return
			}
			post(func() {
				mu.Lock()
				currentRoot, rootErr := filepath.Abs(prefs.RootForProfile(profile))
				current := statusTarget == target && prefs.GameSlug == profile && rootErr == nil && currentRoot == rootPath
				statusInFlight = false
				mu.Unlock()
				if current {
					needed := !authenticated
					changed := !sessionChecked || signInNeeded != needed
					sessionChecked, signInNeeded = true, needed
					if changed {
						refreshUpdateUI()
					}
					switch {
					case err != nil && !authenticated:
						setServerStatus(tr("Server · sign in"), hexColor(colorAmber))
					case err != nil:
						setServerStatus(tr("Server status unavailable"), hexColor(colorAmber))
					case server.Online:
						setServerStatus(tr("Shared server online · ")+server.ServerName, hexColor(colorGreen))
					default:
						setServerStatus(tr("Shared server offline · ")+server.ServerName, hexColor(colorRed))
					}
				} else {
					refreshServerStatus()
				}
			})
		}()
	}
	refreshAfterSignIn := func() {
		mu.Lock()
		statusTarget++
		mu.Unlock()
		refreshServerStatus()
	}
	go func() {
		for {
			select {
			case <-windowCtx.Done():
				return
			case <-statusTicks:
				post(refreshServerStatus)
			}
		}
	}()
	selectedGameExecutable := func(root string) (string, error) {
		data, err := os.Lstat(filepath.Join(root, "Data"))
		if err != nil || !data.IsDir() || data.Mode()&os.ModeSymlink != 0 {
			return "", errors.New("selected folder must contain a real Data/ directory")
		}
		path := filepath.Join(root, "Wow.exe")
		info, err := os.Lstat(path)
		if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			return "", errors.New("selected folder must contain a regular, non-symlink Wow.exe")
		}
		return path, nil
	}
	migrateSavedPasswords := func(roots ...string) {
		async(func(ctx context.Context) {
			var failures []string
			for _, root := range roots {
				if ctx.Err() != nil {
					return
				}
				if root == "" {
					continue
				}
				if err := (&launcher.CredentialStore{Root: root}).RemoveLegacyPassword(); err != nil {
					failures = append(failures, root+": "+err.Error())
				}
			}
			if len(failures) != 0 {
				post(func() {
					showDetails(tr("Saved password check incomplete"), strings.Join(failures, "\n"))
				})
			}
		})
	}
	var checkingLaunch bool
	gameActionBusy := func() bool { return updateInProgress || copyInProgress || signingIn || checkingLaunch }
	gameRoot := func() (string, error) {
		rootPath, err := clientRoot()
		if err == nil {
			_, err = selectedGameExecutable(rootPath)
		}
		return rootPath, err
	}
	cvarsFor := func(profile, folder string) func(string) (bool, error) {
		return func(input string) (bool, error) {
			if gameActionBusy() || selectedGame() != profile || selectedRoot() != folder {
				return false, errors.New(tr("Wait for the current operation to finish, then reopen Game CVars for the selected folder."))
			}
			rootPath, err := gameRoot()
			if err != nil {
				return false, err
			}
			return launcher.ApplyGameCVars(rootPath, input)
		}
	}
	showLaunchHelp := func(manager string, issue launchIssue, output string) {
		var openCVars func()
		if _, err := gameRoot(); err == nil && !gameActionBusy() {
			profileName := game.Selected
			apply := cvarsFor(selectedGame(), selectedRoot())
			openCVars = func() { showGameCVars(w, tr, profileName, apply) }
		}
		showTroubleshoot(w, tr, managerNames[manager], issue, output, openCVars)
	}
	newTroubleshootButton := func() *widget.Button {
		button := widget.NewButton(tr("Troubleshoot"), func() { showLaunchHelp(launchTarget().Manager, "", "") })
		button.Importance = widget.LowImportance
		return button
	}
	launchFailed := func(manager string, issue launchIssue, output string, report func(string)) {
		report(tr("Could not launch game"))
		showLaunchHelp(manager, issue, output)
	}
	var launchAttempt uint64
	startGame := func(command *exec.Cmd, rootPath, profile, manager string, report func(string)) {
		attempt := launchAttempt
		fail := func(err error) { launchFailed(manager, classifyLaunchIssue(manager, err, ""), err.Error(), report) }
		// The manager can outlive this window. A file keeps its output open without a reader in the launcher.
		log, err := os.CreateTemp("", "ebonhold-launch-*")
		if err != nil {
			fail(err)
			return
		}
		if err := os.Remove(log.Name()); err != nil {
			log.Close()
			fail(err)
			return
		}
		command.Stdout, command.Stderr = log, log
		if err := command.Start(); err != nil {
			log.Close()
			fail(err)
			return
		}
		report(tr("Launch requested from %s.", managerNames[manager]))
		go func() {
			defer log.Close()
			waitErr := command.Wait()
			if windowCtx.Err() != nil {
				return
			}
			var output string
			const maxLaunchDetails = 16 << 10
			if info, err := log.Stat(); err == nil && info.Size() > 0 {
				size := min(info.Size(), maxLaunchDetails)
				buf := make([]byte, size)
				n, _ := log.ReadAt(buf, info.Size()-size)
				output = strings.TrimSpace(string(buf[:n]))
			}
			output = terminalEscape.ReplaceAllString(output, "")
			issue := classifyLaunchIssue(manager, waitErr, output)
			if issue == "" {
				return
			}
			if waitErr != nil {
				output = strings.TrimSpace(waitErr.Error() + "\n\n" + output)
			}
			post(func() {
				currentRoot, _ := clientRoot()
				if attempt == launchAttempt && selectedGame() == profile && currentRoot == rootPath {
					launchFailed(manager, issue, output, report)
				}
			})
		}()
	}
	runUpdate := func(rootPath, profile string, settings launcher.GameSettings, verify bool, afterUpdate func()) {
		if !verify && profile != "roguelike" && launcher.PTRNeedsInstall(rootPath) {
			showErr(tr("Update failed"), errors.New("This copy needs its first PTR install. Switch to the PTR profile before updating."))
			return
		}
		updateHasRun, updateInProgress, updateFailed, updateVerify = true, true, false, verify
		updateError = ""
		updateResult = launcher.UpdateResult{}
		updateFiles.reset(tr("Checking game files…"))
		status.SetText(tr("Signing in and loading game manifest…"))
		progressText.SetText(tr("Loading manifest…"))
		progress.SetValue(0)
		refreshSetup()
		switchScreen("update")
		async(func(ctx context.Context) {
			updateAuth := newAuthenticator(rootPath, verify)
			token, manifest, e := updateAuth.Authenticate(ctx, false)
			if e != nil {
				details := authDetails(e.Error(), updateAuth)
				post(func() {
					updateInProgress, updateFailed, updateError = false, true, details
					if verify {
						updateFiles.reset(tr("No verification results."))
					} else {
						updateFiles.reset(tr("No file changes were made."))
					}
					status.SetText(tr("Sign-in failed"))
					refreshSetup()
					showDetails(tr("Sign-in failed"), details)
				})
				return
			}
			showAuthWarning(updateAuth)
			post(refreshAfterSignIn)
			up := launcher.NewUpdater(api, updateAuth, rootPath)
			opts := launcher.UpdateOptions{GameSlug: profile, Verify: verify, Language: settings.Language, ManageHD: settings.ManageHD, HDCharacters: settings.HDCharacters, HDCreatures: settings.HDCreatures}
			result, e := up.Run(ctx, manifest, token, opts, func(p launcher.Progress) {
				post(func() {
					switch p.Stage {
					case "plan":
						updateFiles.record(p.Path, fileQueued, "")
					case "download", "update":
						updateFiles.record(p.Path, fileUpdated, "")
					case "remove":
						updateFiles.record(p.Path, fileRemoved, p.Message)
					case "stale":
						updateFiles.record(p.Path, fileDuplicate, "")
					case "verify":
						switch p.Issue {
						case "missing":
							updateFiles.record(p.Path, fileMissing, "")
						case "mismatch":
							updateFiles.record(p.Path, fileMismatch, "")
						case "unreadable":
							updateFiles.record(p.Path, fileUnreadable, p.Message)
						}
					}
					message := p.Path
					if p.Stage == "plan" {
						message = tr("Waiting for downloads…")
					}
					if p.Stage != "stale" && progressText.Text != message {
						progressText.SetText(message)
					}
					if p.Total > 0 {
						progress.SetValue(float64(p.Current) / float64(p.Total))
					}
				})
			})
			if e == nil || len(result.FailedFiles) > 0 {
				showAuthWarning(updateAuth)
			}
			post(func() {
				if len(updateFiles.rows) == 0 {
					switch {
					case verify && e != nil:
						updateFiles.showSummary(tr("No verification results."))
					case e != nil:
						updateFiles.showSummary(tr("No file changes completed."))
					case verify:
						updateFiles.showSummary(tr("No files need repair."))
					default:
						updateFiles.showSummary(tr("No files changed."))
					}
				}
				updateResult = result
				if !verify {
					for _, failure := range result.FailedFiles {
						updateFiles.record(failure.Path, fileDownloadFailed, failure.Reason)
					}
				}
				if len(result.FailedFiles) > 0 {
					updateFiles.showSummary(tr("%d files need attention", len(result.FailedFiles)))
				}
				updateInProgress, updateFailed, updateVerify = false, e != nil, verify
				updateError = ""
				if e != nil {
					title := tr("Update failed")
					if verify {
						if len(result.FailedFiles) != 0 {
							title = tr("Some files need repair")
						} else {
							title = tr("Verification failed")
						}
					}
					status.SetText(title)
					updateError = authDetails(e.Error(), updateAuth)
					refreshSetup()
					if len(result.FailedFiles) == 0 {
						showDetails(title, updateError)
					}
					return
				}
				if verify {
					status.SetText(tr("Verification complete · checked %d, missing %d, mismatched %d", result.Checked, result.Missing, result.Mismatched))
				} else {
					status.SetText(tr("Update complete · downloaded %d files", result.Downloaded))
				}
				if afterUpdate != nil && ctx.Err() == nil {
					afterUpdate()
				}
				refreshSetup()
			})
		})
	}
	updateFilesOnly := func(verify bool) {
		if gameActionBusy() {
			return
		}
		rootPath, err := gameRoot()
		if err != nil {
			showErr(tr("Choose a client folder"), err)
			return
		}
		mu.Lock()
		profile, settings := prefs.GameSlug, prefs.GameSettingsForProfile(prefs.GameSlug)
		mu.Unlock()
		runUpdate(rootPath, profile, settings, verify, nil)
	}
	updateBtn := widget.NewButton(tr("Update files"), func() { updateFilesOnly(false) })
	verifyBtn := widget.NewButton(tr("Verify files"), func() { updateFilesOnly(true) })
	launchGame := func(testOnly bool) {
		if gameActionBusy() {
			return
		}
		rootPath, err := gameRoot()
		if err != nil {
			showErr(tr("Choose a client folder"), err)
			return
		}
		mu.Lock()
		profile := prefs.GameSlug
		target, settings := prefs.LaunchTargetForProfile(profile), prefs.GameSettingsForProfile(profile)
		err = prefs.ValidateLaunchTargetForProfile(profile, target)
		mu.Unlock()
		if err != nil {
			showErr(tr("Could not launch game"), err)
			return
		}
		if profile == "roguelike" && launcher.PTRNeedsInstall(rootPath) {
			showErr(tr("Could not launch game"), errors.New(tr("Update this PTR copy before playing.")))
			return
		}
		launchAttempt++
		report := func(text string) {
			status.SetText(text)
			if testOnly {
				setupNotice.SetText(text)
			}
		}
		// Closing the launcher must not stop the game.
		var args []string
		if strings.HasPrefix(target.Manager, "bottles-") {
			args = settings.BottlesArguments()
		}
		command, err := target.Command(context.Background(), rootPath, args...)
		if err != nil {
			launchFailed(target.Manager, classifyLaunchIssue(target.Manager, err, ""), err.Error(), report)
			return
		}
		requestLaunch := func() {
			if settings.UpdateBeforePlay && !testOnly {
				runUpdate(rootPath, profile, settings, false, func() {
					if _, err := selectedGameExecutable(rootPath); err != nil {
						showErr(tr("Could not launch game"), err)
						return
					}
					startGame(command, rootPath, profile, target.Manager, report)
				})
				return
			}
			startGame(command, rootPath, profile, target.Manager, report)
		}
		if target.Manager != "bottles-flatpak" {
			requestLaunch()
			return
		}
		checkingLaunch = true
		report(tr("Checking whether Bottles can read Wow.exe…"))
		refreshSetup()
		async(func(ctx context.Context) {
			issue := checkFlatpakLaunch(ctx, target.Manager, filepath.Join(rootPath, "Wow.exe"))
			post(func() {
				checkingLaunch = false
				refreshSetup()
				currentRoot, _ := clientRoot()
				if gameActionBusy() || currentRoot != rootPath || selectedGame() != profile || launchTarget() != target {
					return
				}
				if issue != "" {
					launchFailed(target.Manager, issue, "", report)
					return
				}
				requestLaunch()
			})
		})
	}
	playButton = widget.NewButtonWithIcon(tr("Play"), theme.MediaPlayIcon(), func() { launchGame(false) })
	playButton.Importance = widget.HighImportance
	var updateActivityButton, verifyActivityButton *widget.Button
	var launchSetupButton *widget.Button
	signInButton := widget.NewButton(tr("Sign in"), func() {
		if gameActionBusy() {
			return
		}
		rootPath, err := clientRoot()
		if err != nil {
			showErr(tr("Choose a client folder"), err)
			return
		}
		target := statusTarget
		signingIn = true
		refreshSetup()
		async(func(ctx context.Context) {
			auth := newAuthenticator(rootPath, false)
			_, _, err := auth.Authenticate(ctx, false)
			if ctx.Err() != nil {
				return
			}
			post(func() {
				signingIn = false
				if target == statusTarget {
					switch {
					case err == nil:
						sessionChecked = false
						status.SetText(tr("Signed in"))
						refreshAfterSignIn()
						if auth.Warning != "" {
							showDetails(tr("Sign-in warning"), tr(auth.Warning))
						}
					case !errors.Is(err, context.Canceled):
						showDetails(tr("Sign-in failed"), authDetails(err.Error(), auth))
					case auth.Warning != "":
						showDetails(tr("Sign-in warning"), auth.Warning)
					}
				}
				refreshSetup()
			})
		})
	})
	signInButton.Importance = widget.HighImportance
	var fileActions, launchRow *fyne.Container
	var homeScroll *container.Scroll
	chooseFolder := func() {
		if gameActionBusy() {
			return
		}
		profile := selectedGame()
		picker := dialog.NewFolderOpen(func(uri fyne.ListableURI, e error) {
			if e == nil && uri != nil && !gameActionBusy() && selectedGame() == profile {
				root.SetText(uri.Path())
			}
		}, w)
		showSetupPicker(w, picker, tr("Choose game folder"))
	}
	browse := widget.NewButton(tr("Choose folder…"), chooseFolder)
	browseExecutable := widget.NewButton(tr("Choose Wow.exe…"), func() {
		if gameActionBusy() {
			return
		}
		profile := selectedGame()
		picker := dialog.NewFileOpen(func(file fyne.URIReadCloser, err error) {
			if file != nil {
				defer file.Close()
			}
			if gameActionBusy() || selectedGame() != profile {
				return
			}
			if err != nil {
				setupNotice.SetText(err.Error())
				return
			}
			if file == nil {
				return
			}
			uri := file.URI()
			if uri.Scheme() != "file" || uri.Name() != "Wow.exe" {
				setupNotice.SetText(tr("Choose Wow.exe in the Ebonhold game folder."))
				return
			}
			path := filepath.Dir(uri.Path())
			if _, err := selectedGameExecutable(path); err != nil {
				setupNotice.SetText(tr(err.Error()))
				return
			}
			root.SetText(path)
		}, w)
		picker.SetFilter(storage.NewExtensionFileFilter([]string{".exe"}))
		showSetupPicker(w, picker, tr("Choose Wow.exe…"))
	})
	findGameFolder := func() {
		if gameActionBusy() {
			return
		}
		mu.Lock()
		searchProfile := prefs.GameSlug
		searchRoot := prefs.RootForProfile(searchProfile)
		searchPrefs := prefs
		mu.Unlock()
		go func() {
			allCandidates := launcher.FindClientRoots()
			candidates := allCandidates[:0]
			for _, candidate := range allCandidates {
				if searchPrefs.ValidateRootForProfile(searchProfile, candidate) == nil {
					candidates = append(candidates, candidate)
				}
			}
			post(func() {
				if gameActionBusy() || selectedGame() != searchProfile || selectedRoot() != searchRoot {
					return
				}
				switch len(candidates) {
				case 0:
					messageText := tr("No game folder found. Choose the folder or select Wow.exe.")
					if len(allCandidates) > 0 {
						messageText = tr("The folders found are already assigned to the other profile. Choose a different folder containing Data/ and Wow.exe.")
					}
					setupNotice.SetText(messageText)
				case 1:
					message := widget.NewLabel(tr("Possible separate game folder found:\n%s\n\nUse this folder, or choose another one.", candidates[0]))
					message.Wrapping = fyne.TextWrapWord
					form := dialog.NewForm(tr("Confirm game folder"), tr("Use this folder"), tr("Choose another…"), []*widget.FormItem{widget.NewFormItem("", message)}, func(ok bool) {
						if ok && selectedGame() == searchProfile && selectedRoot() == searchRoot {
							root.SetText(candidates[0])
						} else if !ok && selectedGame() == searchProfile && selectedRoot() == searchRoot {
							chooseFolder()
						}
					}, w)
					form.Show()
				default:
					selection := widget.NewSelect(candidates, nil)
					selectedPath := widget.NewLabel(candidates[0])
					selectedPath.Wrapping = fyne.TextWrapBreak
					selection.OnChanged = func(path string) { selectedPath.SetText(path) }
					selection.SetSelected(candidates[0])
					message := widget.NewLabel(tr("Choose a separate folder containing your Ebonhold client. Nothing is selected until you confirm."))
					message.Wrapping = fyne.TextWrapWord
					form := dialog.NewForm(tr("Choose game folder"), tr("Use selected folder"), tr("Choose manually…"), []*widget.FormItem{
						widget.NewFormItem(tr("Possible folders"), selection),
						widget.NewFormItem(tr("Selected path"), selectedPath),
						widget.NewFormItem("", message),
					}, func(ok bool) {
						if ok && selection.Selected != "" && selectedGame() == searchProfile && selectedRoot() == searchRoot {
							root.SetText(selection.Selected)
						} else if !ok && selectedGame() == searchProfile && selectedRoot() == searchRoot {
							chooseFolder()
						}
					}, w)
					form.Show()
				}
			})
		}()
	}
	addonStatus := widget.NewLabel(tr("Add-on status has not been checked."))
	var homeAddonStatus *widget.Label
	setAddonStatus := func(text string) {
		addonStatus.SetText(text)
		if homeAddonStatus != nil {
			homeAddonStatus.SetText(text)
		}
	}
	var installAddon, removeAddon *widget.Button
	var addonBusy bool
	var setAddonBusy func(bool)
	addonSelection := widget.NewCheckGroup(nil, func(selected []string) {
		if installAddon == nil || removeAddon == nil || addonBusy {
			return
		}
		if len(selected) == 0 {
			installAddon.Disable()
			removeAddon.Disable()
			return
		}
		installAddon.Enable()
		removeAddon.Enable()
	})
	addonSelection.Disable()
	addonList := widget.NewMultiLineEntry()
	addonList.Disable()
	addonList.Hide()
	selectedAddonIDs := func(options []string) []int64 {
		mu.Lock()
		defer mu.Unlock()
		ids := make([]int64, 0, len(options))
		for _, option := range options {
			if id, ok := addonIDs[option]; ok {
				ids = append(ids, id)
			}
		}
		return ids
	}
	addonTargetMatches := func(profile, rootPath string, target uint64) bool {
		mu.Lock()
		defer mu.Unlock()
		currentRoot, err := filepath.Abs(prefs.RootForProfile(profile))
		return addonTarget == target && prefs.GameSlug == profile && err == nil && currentRoot == rootPath
	}
	loadAddons := func(interactive bool) {
		profile := selectedGame()
		mu.Lock()
		addonTarget++
		target := addonTarget
		addonIDs = nil
		mu.Unlock()
		addonSelection.SetSelected(nil)
		addonSelection.Options = nil
		addonSelection.Disable()
		addonList.SetText("")
		addonList.Hide()
		rootPath, err := clientRoot()
		if err != nil {
			setAddonStatus(tr("Choose a client folder to check add-ons."))
			return
		}
		manager := launcher.NewAddonManager(api, rootPath)
		setAddonStatus(tr("Checking add-ons for %s…", profileLabel(profile)))
		async(func(ctx context.Context) {
			auth := &launcher.Authenticator{API: api, Store: &launcher.CredentialStore{Root: rootPath}, ReadOnly: true}
			if interactive {
				auth = newAuthenticator(rootPath, false)
			}
			token, _, authErr := auth.Authenticate(ctx, false)
			if interactive && authErr == nil {
				showAuthWarning(auth)
				post(func() {
					if addonTargetMatches(profile, rootPath, target) {
						refreshAfterSignIn()
					}
				})
			}
			var statuses []launcher.AddonStatus
			var checkErr error
			if authErr == nil {
				statuses, checkErr = manager.Check(ctx, token)
			}
			if authErr != nil || checkErr != nil {
				installed, localErr := manager.Installed()
				lines := make([]string, 0, len(installed))
				for _, item := range installed {
					lines = append(lines, item.Addon.Name+" · "+tr("MANAGED BY LAUNCHER"))
				}
				post(func() {
					if !addonTargetMatches(profile, rootPath, target) {
						return
					}
					addonList.Show()
					if interactive && authErr != nil && !errors.Is(authErr, context.Canceled) {
						showDetails(tr("Add-on sign-in failed"), authDetails(authErr.Error(), auth))
					} else if interactive && auth.Warning != "" {
						showDetails(tr("Sign-in warning"), auth.Warning)
					}
					addonList.SetText(strings.Join(lines, "\n"))
					if localErr != nil {
						setAddonStatus(tr("Add-on status unavailable") + ": " + localErr.Error())
					} else if authErr != nil {
						setAddonStatus(tr("Could not check the catalog. Showing launcher-managed add-ons for %s.", profileLabel(profile)))
					} else {
						setAddonStatus(tr("Catalog check unavailable") + ": " + checkErr.Error())
					}
				})
				return
			}
			options := make([]string, 0, len(statuses))
			optionIDs := make(map[string]int64, len(statuses))
			lines := make([]string, 0, len(statuses))
			installed := 0
			updates := 0
			for _, item := range statuses {
				option := fmt.Sprintf("%s · ID %d", item.Addon.Name, item.Addon.ID)
				options = append(options, option)
				optionIDs[option] = item.Addon.ID
				state := item.State
				if item.Detail == "repair required" {
					state = "REPAIR REQUIRED"
				}
				lines = append(lines, item.Addon.Name+" · "+tr(state))
				if item.State != "NOT INSTALLED" {
					installed++
				}
				if item.State == "UPDATE AVAILABLE" {
					updates++
				}
			}
			post(func() {
				if !addonTargetMatches(profile, rootPath, target) {
					return
				}
				mu.Lock()
				addonIDs = optionIDs
				mu.Unlock()
				addonSelection.Options = options
				addonSelection.SetSelected(nil)
				if !addonBusy {
					addonSelection.Enable()
				}
				addonSelection.Refresh()
				addonList.Show()
				addonList.SetText(strings.Join(lines, "\n"))
				setAddonStatus(tr("%s · %d installed · %d updates or repairs", profileLabel(profile), installed, updates))
			})
		})
	}
	refreshAddons = func() { loadAddons(false) }
	loadCatalog := widget.NewButton(tr("Refresh catalog"), func() { loadAddons(true) })
	setAddonBusy = func(busy bool) {
		addonBusy = busy
		setEnabled(settingsButton, !busy && !gameActionBusy())
		if busy {
			addonSelection.Disable()
			loadCatalog.Disable()
			installAddon.Disable()
			removeAddon.Disable()
			return
		}
		if len(addonSelection.Options) == 0 {
			addonSelection.Disable()
		} else {
			addonSelection.Enable()
		}
		loadCatalog.Enable()
		if len(addonSelection.Selected) == 0 {
			installAddon.Disable()
			removeAddon.Disable()
		} else {
			installAddon.Enable()
			removeAddon.Enable()
		}
	}
	installAddon = widget.NewButton(tr("Install selected"), func() {
		rootPath, err := clientRoot()
		if err != nil {
			showErr(tr("Choose a client folder"), err)
			return
		}
		selected := append([]string(nil), addonSelection.Selected...)
		ids := selectedAddonIDs(selected)
		if len(ids) == 0 {
			showErr(tr("Choose add-ons"), errors.New("select one or more catalog add-ons first"))
			return
		}
		profile := selectedGame()
		mu.Lock()
		target := addonTarget
		mu.Unlock()
		manager := launcher.NewAddonManager(api, rootPath)
		setAddonBusy(true)
		async(func(ctx context.Context) {
			auth := newAuthenticator(rootPath, false)
			token, _, err := auth.Authenticate(ctx, false)
			showAuthWarning(auth)
			if err == nil {
				err = manager.Install(ctx, token, ids)
			}
			post(func() {
				setAddonBusy(false)
				if !addonTargetMatches(profile, rootPath, target) {
					return
				}
				if err != nil {
					setAddonStatus(tr("Add-on install failed") + " · " + err.Error())
					return
				}
				addonSelection.SetSelected(nil)
				addonList.SetText("")
				addonList.Hide()
				setAddonStatus(tr("Add-ons installed or updated: %d · refreshing status…", len(ids)))
				refreshAddons()
			})
		})
	})
	installAddon.Disable()
	removeAddon = widget.NewButton(tr("Remove selected"), func() {
		rootPath, err := clientRoot()
		if err != nil {
			showErr(tr("Choose a client folder"), err)
			return
		}
		selected := append([]string(nil), addonSelection.Selected...)
		ids := selectedAddonIDs(selected)
		if len(ids) == 0 {
			showErr(tr("Choose add-ons"), errors.New("select launcher-managed add-ons from the catalog first"))
			return
		}
		profile := selectedGame()
		mu.Lock()
		target := addonTarget
		mu.Unlock()
		manager := launcher.NewAddonManager(api, rootPath)
		setAddonBusy(true)
		async(func(context.Context) {
			requested := make([]string, len(ids))
			for i, id := range ids {
				requested[i] = fmt.Sprint(id)
			}
			lines, err := manager.Remove(strings.Join(requested, ","))
			post(func() {
				setAddonBusy(false)
				if !addonTargetMatches(profile, rootPath, target) {
					return
				}
				if err != nil {
					setAddonStatus(tr("Add-on removal failed") + " · " + err.Error())
					return
				}
				addonSelection.SetSelected(nil)
				addonList.Show()
				addonList.SetText(strings.Join(lines, "\n"))
				setAddonStatus(tr("Add-ons removed · refreshing status…"))
				refreshAddons()
			})
		})
	})
	removeAddon.Disable()
	addonHelp := widget.NewLabel(tr("Select one or more catalog add-ons. Removal only affects add-ons managed by this launcher."))
	addonHelp.Wrapping = fyne.TextWrapWord
	addonsBox := container.NewVBox(
		flushText(addonStatus),
		flushText(addonHelp),
		addonSelection,
		container.NewGridWithColumns(3, loadCatalog, installAddon, removeAddon),
		addonList,
	)
	card := func(content fyne.CanvasObject) fyne.CanvasObject {
		background := canvas.NewRectangle(hexColor(colorCard))
		background.StrokeColor = hexColor(colorBorder)
		background.StrokeWidth = 1
		background.CornerRadius = 10
		return container.NewStack(background, container.New(layout.NewCustomPaddedLayout(12, 12, 14, 14), content))
	}
	headingText := func(text string, size float32) *canvas.Text {
		label := canvas.NewText(text, hexColor(colorText))
		label.TextSize = size
		label.TextStyle = fyne.TextStyle{Bold: true}
		return label
	}

	addonIntro := widget.NewLabel(tr("Browse the official catalog, select add-ons to install or update, or remove launcher-managed add-ons."))
	addonIntro.Wrapping = fyne.TextWrapWord
	addonScreen := container.NewVScroll(container.NewVBox(
		headingText(tr("Add-ons"), 26),
		flushText(addonIntro),
		card(addonsBox),
	))
	gameExecutableInfo := widget.NewLabel(tr("Choose a folder containing Data/ and Wow.exe"))
	gameExecutableInfo.Wrapping = fyne.TextWrapWord
	managerInfo := widget.NewLabel("")
	managerInfo.Wrapping = fyne.TextWrapWord
	managerInfo.Importance = widget.LowImportance
	managerHint := widget.NewLabel("")
	managerHint.Wrapping = fyne.TextWrapWord
	preset := widget.NewMultiLineEntry()
	preset.Wrapping = fyne.TextWrapWord
	preset.Disable()
	detectedManagerText := tr("Detecting installed game managers…")
	setupStep := 1
	var setSetupStep func(int)
	var setupPanel *fyne.Container
	var setupView *fyne.Container
	var setupScroll *container.Scroll
	var reviewFolderReady, reviewLaunchReady bool
	reviewTitle := widget.NewLabelWithStyle("", fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
	reviewNext := widget.NewLabel("")
	reviewNext.Wrapping = fyne.TextWrapWord
	setupSummaryFolder := widget.NewLabel("")
	setupSummaryFolder.Wrapping = fyne.TextWrapBreak
	setupSummaryProfile := widget.NewLabel("")
	setupSummaryManager := widget.NewLabel("")
	setupSummaryManager.Wrapping = fyne.TextWrapBreak
	var launchSummary *widget.Label
	managerButtons := make(map[string]*widget.Button)
	stepOne := widget.NewLabel("1  Game folder")
	stepTwo := widget.NewLabel("")
	stepThree := widget.NewLabel("")
	stepHeaders := []*widget.Label{stepOne, stepTwo, stepThree}
	managerChoices := []string{"steam", "lutris", "bottles-native", "bottles-flatpak", ""}
	managerLaunchHint := func(manager string) string {
		switch manager {
		case "steam":
			return tr("Use a separate Steam shortcut pointing to this profile's Wow.exe.")
		case "lutris":
			return tr("Use a separate Lutris entry pointing to this profile's Wow.exe.")
		case "bottles-native", "bottles-flatpak":
			return tr("A bottle is the Windows setup Bottles uses to run the game. Choose the one you use for Ebonhold.")
		default:
			return tr("You can update here and open the game yourself.")
		}
	}
	searchFolders = widget.NewButton(tr("Search common locations"), findGameFolder)
	firstRunChoices := container.NewGridWithColumns(2,
		widget.NewButton(tr("I already have Ebonhold"), findGameFolder),
		widget.NewButton(tr("I need to install it"), func() { showInstallHelp(w, tr) }),
	)
	offerPTRCopy := func() bool {
		mu.Lock()
		profile, production, ptr := prefs.GameSlug, prefs.ProductionRoot, prefs.PTRRoot
		mu.Unlock()
		return profile == "roguelike" && strings.TrimSpace(production) != "" && setupFolderMissing(ptr)
	}
	copyPTR = widget.NewButton(tr("Create PTR copy…"), func() {
		canCopy := func() bool {
			return !gameActionBusy() && offerPTRCopy()
		}
		if !canCopy() {
			return
		}
		mu.Lock()
		original := prefs.ProductionRoot
		mu.Unlock()
		source, err := filepath.Abs(original)
		if original == "" || err != nil {
			setupNotice.SetText(tr("Choose the Production client folder in Setup."))
			return
		}
		if _, err := selectedGameExecutable(source); err != nil {
			setupNotice.SetText(tr("Choose the Production client folder in Setup."))
			return
		}
		beginCopy := func(path string) {
			if !canCopy() {
				return
			}
			setCopyActivity("Copying game files for PTR…")
			copyWork.Add(1)
			async(func(ctx context.Context) {
				defer copyWork.Done()
				err := launcher.CopyClient(ctx, source, path)
				post(func() {
					setCopyActivity("")
					if err != nil {
						setupNotice.SetText(tr("Could not create PTR copy") + ": " + tr(err.Error()))
						return
					}
					if selectedRoot() == path {
						// The path did not change, but this is a new installation.
						resetUpdate()
						clientTargetChanged()
					} else {
						root.SetText(path)
					}
					if selectedRoot() != path {
						setupNotice.SetText(tr("PTR copy created. Choose its folder to finish setup."))
						return
					}
					setupNotice.SetText(tr("PTR copy ready. Install PTR from Home before playing."))
				})
			})
		}
		destination := widget.NewEntry()
		suggested := selectedRoot()
		if !filepath.IsAbs(suggested) {
			suggested = filepath.Join(filepath.Dir(source), "Ebonhold-PTR")
		}
		destination.SetText(suggested)
		destination.Validator = func(text string) error {
			if !filepath.IsAbs(strings.TrimSpace(text)) {
				return errors.New(tr("Enter the full path to a new PTR folder."))
			}
			return nil
		}
		form := dialog.NewForm(tr("Create PTR copy"), tr("Check space"), tr("Cancel"), []*widget.FormItem{
			widget.NewFormItem("", wrappedLabel(tr("Choose a new folder for the base game files. Your existing game files and sign-in stay untouched."))),
			widget.NewFormItem(tr("PTR folder"), destination),
		}, func(ok bool) {
			if !ok || !canCopy() {
				return
			}
			path := filepath.Clean(strings.TrimSpace(destination.Text))
			setCopyActivity("Checking space for the PTR copy…")
			copyWork.Add(1)
			async(func(ctx context.Context) {
				defer copyWork.Done()
				needed, available, err := launcher.ClientCopySpace(ctx, source, path)
				post(func() {
					setCopyActivity("")
					if err != nil {
						setupNotice.SetText(tr("Could not check copy space: %s", tr(err.Error())))
						return
					}
					if !canCopy() {
						return
					}
					folder := widget.NewLabel(path)
					folder.Wrapping = fyne.TextWrapBreak
					space := container.NewVBox(wrappedLabel(tr("PTR destination")), folder,
						wrappedLabel(tr("Estimated copy: %s\nFree on this drive: %s", copySize(needed), copySize(available))))
					size := fyne.NewSize(min(580, w.Canvas().Size().Width-32), min(315, w.Canvas().Size().Height-32))
					if needed > available {
						space.Add(wrappedLabel(tr("Not enough space. Choose a new folder on a drive with more free space.")))
						d := dialog.NewCustom(tr("Create PTR copy"), tr("Close"), container.NewVScroll(space), w)
						d.Resize(size)
						d.Show()
						return
					}
					space.Add(wrappedLabel(tr("The first PTR update may need more space.")))
					d := dialog.NewCustomConfirm(tr("Create PTR copy"), tr("Create copy"), tr("Cancel"), container.NewVScroll(space), func(ok bool) {
						if ok {
							beginCopy(path)
						}
					}, w)
					d.Resize(size)
					d.Show()
				})
			})
		}, w)
		form.Resize(fyne.NewSize(580, 250))
		form.Show()
	})
	folderHint := widget.NewLabel(tr("Choose the Ebonhold folder. It contains Wow.exe and a folder named Data."))
	folderHint.Wrapping = fyne.TextWrapWord
	step1Content := container.NewVBox(
		widget.NewLabelWithStyle(tr("Choose the game folder"), fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		firstRunChoices,
		folderHint,
		widget.NewLabelWithStyle(tr("Game files folder"), fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		container.NewBorder(nil, nil, nil, container.NewHBox(browse, browseExecutable), root),
		gameExecutableInfo,
		container.NewGridWithColumns(2,
			searchFolders,
			container.NewVBox(widget.NewLabel(tr("Game profile")), game),
		),
		copyPTR,
	)
	managerOptions := make([]fyne.CanvasObject, 0, len(managerChoices)-1)
	setManager := func(manager string) {
		if gameActionBusy() {
			return
		}
		mu.Lock()
		profile := prefs.GameSlug
		old := prefs.LaunchTargetForProfile(profile)
		if old.Manager != manager {
			prefs.SetLaunchTargetForProfile(profile, launcher.LaunchTarget{Manager: manager})
		}
		mu.Unlock()
		if old.Manager != manager && !save() {
			mu.Lock()
			prefs.SetLaunchTargetForProfile(profile, old)
			mu.Unlock()
		}
		refreshSetup()
	}
	for _, manager := range managerChoices {
		button := widget.NewButton(managerNames[manager], func() { setManager(manager) })
		managerButtons[manager] = button
		if manager != "" {
			managerOptions = append(managerOptions, button)
		}
	}
	editLaunchTarget := func() {
		target := launchTarget()
		profile, path := selectedGame(), selectedRoot()
		gameFolder, err := clientRoot()
		if err != nil {
			setupNotice.SetText(tr(err.Error()))
			return
		}
		showLaunchTarget(windowCtx, w, tr, target, gameFolder, func(id string) {
			if gameActionBusy() || selectedGame() != profile || selectedRoot() != path || launchTarget() != target {
				return
			}
			next := launcher.LaunchTarget{Manager: target.Manager, ID: id}
			mu.Lock()
			err := prefs.ValidateLaunchTargetForProfile(profile, next)
			if err == nil {
				prefs.SetLaunchTargetForProfile(profile, next)
			}
			mu.Unlock()
			if err != nil {
				setupNotice.SetText(tr(err.Error()))
				return
			}
			if !save() {
				mu.Lock()
				prefs.SetLaunchTargetForProfile(profile, target)
				mu.Unlock()
				return
			}
			setupNotice.SetText("")
			refreshSetup()
		})
	}
	targetButton := widget.NewButton("", editLaunchTarget)
	managerNotice := widget.NewLabel(tr("The launcher does not edit your manager's settings."))
	managerNotice.Wrapping = fyne.TextWrapWord
	managerNotice.Importance = widget.LowImportance
	managerInstall := container.NewVBox(
		wrappedLabel(tr("New to Linux? Install Bottles and create a Gaming bottle. Use Run Executable to open Wow.exe, then select that bottle here.")),
		container.NewHBox(bottlesInstallLink(tr)),
	)
	managerInstall.Hide()
	step2Content := container.New(layout.NewCustomPaddedVBoxLayout(8),
		headingText(tr("Which app do you use to start Ebonhold?"), 16),
		flushText(managerInfo),
		managerInstall,
		container.NewGridWithColumns(2, managerOptions...),
		flushText(managerHint),
		container.NewBorder(nil, nil, targetButton, managerButtons[""]),
		flushText(managerNotice),
	)
	copyPreset := widget.NewButton(tr("Copy launch command"), func() {
		w.Clipboard().SetContent(preset.Text)
		setupNotice.SetText(tr("Launch command copied."))
	})
	advanced := widget.NewAccordion(widget.NewAccordionItem(tr("Advanced: start from your game manager"), container.NewVBox(preset, copyPreset)))
	summaryValue := func(label *widget.Label) fyne.CanvasObject {
		return container.New(layout.NewCustomPaddedLayout(0, 0, 12, 0), flushText(label))
	}
	testLaunch := widget.NewButtonWithIcon(tr("Test launch"), theme.MediaPlayIcon(), func() { launchGame(true) })
	step3Content := container.New(layout.NewCustomPaddedVBoxLayout(10),
		flushText(reviewTitle),
		flushText(reviewNext),
		widget.NewSeparator(),
		container.New(layout.NewFormLayout(),
			flushText(widget.NewLabel(tr("Game profile"))), summaryValue(setupSummaryProfile),
			flushText(widget.NewLabel(tr("Game folder"))), summaryValue(setupSummaryFolder),
			flushText(widget.NewLabel(tr("Start with"))), summaryValue(setupSummaryManager),
		),
		container.NewHBox(testLaunch),
		advanced,
	)
	setupScreens := map[int]fyne.CanvasObject{1: step1Content, 2: step2Content, 3: step3Content}
	setupPanel = container.NewMax(step1Content)
	setupBack := widget.NewButton(tr("Back"), func() {})
	setupNext := widget.NewButton(tr("Continue"), func() {})
	setupNext.Importance = widget.HighImportance
	setupBack.OnTapped = func() {
		if setupStep > 1 {
			setSetupStep(setupStep - 1)
		}
	}
	setupNext.OnTapped = func() {
		if setupStep == 1 {
			rootPath, err := clientRoot()
			if err == nil {
				_, err = selectedGameExecutable(rootPath)
			}
			if err != nil {
				setupNotice.SetText(tr("Choose a valid game folder first: %s", tr(err.Error())))
				return
			}
			migrateSavedPasswords(rootPath)
		}
		if setupStep < 3 {
			setSetupStep(setupStep + 1)
			return
		}
		if !reviewFolderReady {
			setSetupStep(1)
			return
		}
		if launchTarget().Manager != "" && !reviewLaunchReady {
			editLaunchTarget()
			return
		}
		setSetupStep(1)
		if switchScreen != nil {
			switchScreen("home")
		}
	}
	setSetupStep = func(step int) {
		if step < 1 {
			step = 1
		}
		if step > 3 {
			step = 3
		}
		setupStep = step
		setupNotice.SetText("")
		refreshSetup()
		if setupScroll != nil {
			setupScroll.ScrollToTop()
		}
	}
	refreshSetup = func() {
		target := launchTarget()
		key := target.Manager
		selected := managerNames[key]
		busy := gameActionBusy()
		if key == "" {
			targetButton.Hide()
		} else {
			targetButton.SetText(launchTargetTitle(tr, key))
			targetButton.Show()
		}
		setEnabled(targetButton, key != "" && !busy)
		mu.Lock()
		reviewLaunchReady = prefs.ValidateLaunchTargetForProfile(prefs.GameSlug, target) == nil
		mu.Unlock()
		canCopy := offerPTRCopy()
		if canCopy {
			copyPTR.Show()
			setEnabled(copyPTR, !busy)
		} else {
			copyPTR.Hide()
		}
		rootPath, rootErr := clientRoot()
		executableErr := rootErr
		if rootErr == nil {
			_, executableErr = selectedGameExecutable(rootPath)
		}
		if executableErr != nil {
			gameExecutableInfo.SetText(tr("Game files not ready: %s", tr(executableErr.Error())))
		} else {
			gameExecutableInfo.SetText(tr("Ready · Data/ and Wow.exe found"))
		}
		if selectedRoot() == "" {
			gameExecutableInfo.Hide()
		} else {
			gameExecutableInfo.Show()
		}
		reviewFolderReady = executableErr == nil
		needsPTRInstall := reviewFolderReady && selectedGame() == "roguelike" && launcher.PTRNeedsInstall(rootPath)
		if reviewFolderReady && reviewLaunchReady && !needsPTRInstall {
			testLaunch.Show()
		} else {
			testLaunch.Hide()
		}
		setEnabled(testLaunch, !busy)
		newInstall := !reviewFolderReady && (selectedGame() != "roguelike" || !canCopy)
		if newInstall {
			firstRunChoices.Show()
			searchFolders.Hide()
		} else {
			firstRunChoices.Hide()
			searchFolders.Show()
		}
		setupSummaryFolder.SetText(rootPath)
		if rootErr != nil {
			setupSummaryFolder.SetText(tr("Not selected"))
		}
		setupSummaryProfile.SetText(game.Selected)
		targetText := selected
		if target.ID != "" && strings.HasPrefix(key, "bottles-") {
			targetText += " · " + target.ID
		}
		setupSummaryManager.SetText(targetText)
		if refreshUpdateUI != nil {
			refreshUpdateUI()
		}
		managerInfo.SetText(detectedManagerText)
		managerHint.SetText(managerLaunchHint(key))
		for _, manager := range managerChoices {
			button := managerButtons[manager]
			button.Icon = nil
			button.Importance = widget.MediumImportance
			if manager == key {
				button.Icon = theme.ConfirmIcon()
				button.Importance = widget.HighImportance
			}
			if manager == "" {
				button.Importance = widget.LowImportance
			}
			setEnabled(button, !busy)
			button.Refresh()
		}
		stepNames := []string{tr("Game folder"), tr("How to launch"), tr("Next steps")}
		for i, label := range stepHeaders {
			text := fmt.Sprintf("%d  %s", i+1, stepNames[i])
			if setupStep > i+1 {
				text = "✓  " + stepNames[i]
			}
			label.SetText(text)
			label.TextStyle = fyne.TextStyle{Bold: setupStep == i+1}
			label.Refresh()
		}
		setupPanel.Objects = []fyne.CanvasObject{setupScreens[setupStep]}
		setupPanel.Refresh()
		setupBack.Hide()
		if setupStep > 1 {
			setupBack.Show()
			setupBack.Enable()
		}
		if setupStep == 3 {
			next := tr("Go to Home")
			switch {
			case !reviewFolderReady:
				reviewTitle.SetText(tr("Game folder needed"))
				reviewNext.SetText(tr("Choose the folder that contains Ebonhold's game files."))
				next = tr("Choose game folder")
			case key != "" && !reviewLaunchReady:
				reviewTitle.SetText(tr("One more step before you can play"))
				reviewNext.SetText(managerLaunchHint(key))
				next = launchTargetTitle(tr, key)
			case needsPTRInstall:
				reviewTitle.SetText(tr("Update this PTR copy before playing."))
				message := tr("On Home, sign in if asked and choose Install PTR.")
				if key == "" {
					message += " " + tr("Then start the game from your usual app.")
				}
				reviewNext.SetText(message)
			case key == "":
				reviewTitle.SetText(tr("Ready to update"))
				reviewNext.SetText(tr("On Home, sign in if asked and choose Update files.") + " " + tr("Then start the game from your usual app."))
			default:
				reviewTitle.SetText(tr("Ready to play"))
				reviewNext.SetText(tr("Use Test launch to try opening WoW without a launcher update. You can also use Play from Home."))
			}
			setupNext.SetText(next)
			setEnabled(setupNext, !busy)
			if reviewFolderReady && reviewLaunchReady {
				mu.Lock()
				settings := prefs.GameSettingsForProfile(prefs.GameSlug)
				mu.Unlock()
				var args []string
				if strings.HasPrefix(key, "bottles-") {
					args = settings.BottlesArguments()
				}
				preset.SetText(target.Preset(launcherPath, rootPath, selectedGame(), args...))
				advanced.Show()
				setEnabled(copyPreset, !busy)
			} else {
				advanced.Hide()
			}
		} else {
			setupNext.SetText(tr("Continue"))
			setEnabled(setupNext, !busy && (setupStep != 1 || reviewFolderReady))
		}
		if setupView != nil {
			setupView.Refresh()
		}
	}
	refreshSetup()
	go func() {
		managers := launcher.DetectManagers(windowCtx)
		if windowCtx.Err() != nil {
			return
		}
		var names []string
		for _, manager := range managers {
			if manager.Installed {
				names = append(names, manager.Name)
			}
		}
		post(func() {
			if len(names) == 0 {
				detectedManagerText = tr("No launch apps detected.")
				managerInstall.Show()
			} else {
				detectedManagerText = tr("Detected as installed: ") + strings.Join(names, ", ") + "."
			}
			refreshSetup()
		})
	}()
	setupStepBar := container.NewGridWithColumns(3, flushText(stepHeaders[0]), flushText(stepHeaders[1]), flushText(stepHeaders[2]))
	setupFooter := container.NewBorder(nil, nil, setupBack, container.NewHBox(newTroubleshootButton(), setupNext), setupNotice)
	setupColumn := container.New(layout.NewCustomPaddedVBoxLayout(8),
		headingText(tr("Set up your game client"), theme.TextSize()+10),
		setupStepBar,
		widget.NewSeparator(),
		card(setupPanel),
	)
	setupScroll = container.NewVScroll(container.New(centeredSetupColumn{}, setupColumn))
	setupView = container.NewBorder(nil, container.New(centeredSetupColumn{}, setupFooter), nil, nil, setupScroll)
	refreshSetup()
	updateState := canvas.NewText(tr("READY TO UPDATE"), hexColor(colorAmber))
	updateState.TextSize = 14
	updateState.TextStyle = fyne.TextStyle{Bold: true}
	homeHeading := widget.NewLabelWithStyle(tr("Update before playing"), fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
	homeHeading.SizeName = theme.SizeNameHeadingText
	homeHeading.Wrapping = fyne.TextWrapWord
	homeDescription := widget.NewLabel("")
	homeDescription.Wrapping = fyne.TextWrapWord
	activityHeading := widget.NewLabelWithStyle(tr("Update available"), fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
	activityHeading.Wrapping = fyne.TextWrapWord
	activityRealm := widget.NewLabel(game.Selected)
	activityRealm.Importance = widget.LowImportance
	activityDescription := widget.NewLabel("")
	activityDescription.Wrapping = fyne.TextWrapWord
	activityNotice := flushText(activityDescription)
	technicalButton := widget.NewButton(tr("Technical details"), func() {
		if updateError != "" {
			showDetails(tr("Technical details"), tr("%s\n\nClient folder: %s\nChecked: %d\nDownloaded: %d\nMissing: %d\nMismatched: %d",
				updateError, selectedRoot(), updateResult.Checked, updateResult.Downloaded, updateResult.Missing, updateResult.Mismatched))
		}
	})
	technicalAction := container.NewHBox(technicalButton)
	technicalAction.Hide()
	refreshUpdateUI = func() {
		rootPath, clientErr := gameRoot()
		clientReady := clientErr == nil
		mu.Lock()
		profile := prefs.GameSlug
		target := prefs.LaunchTargetForProfile(prefs.GameSlug)
		canPlay := prefs.ValidateLaunchTargetForProfile(prefs.GameSlug, target) == nil
		updateBeforePlay := prefs.GameSettingsForProfile(profile).UpdateBeforePlay
		mu.Unlock()
		ptrNeedsInstall := clientReady && profile == "roguelike" && launcher.PTRNeedsInstall(rootPath)
		waitingForSignIn := clientReady && !updateInProgress && (!sessionChecked || signInNeeded)
		switch {
		case updateInProgress && updateVerify:
			updateState.Text, updateState.Color = tr("VERIFICATION IN PROGRESS"), hexColor(colorBlue)
			homeHeading.SetText(tr("Verifying game files"))
			homeDescription.SetText(tr("Verify checks files without changing them."))
		case updateInProgress:
			updateState.Text, updateState.Color = tr("UPDATE IN PROGRESS"), hexColor(colorBlue)
			homeHeading.SetText(tr("Updating Ebonhold"))
			homeDescription.SetText(tr("Wait for the update to finish before playing."))
		case !clientReady:
			updateState.Text, updateState.Color = tr("SETUP REQUIRED"), hexColor(colorBlue)
			homeHeading.SetText(tr("Choose your game folder"))
			homeDescription.SetText(tr("Choose the Ebonhold folder. It contains Wow.exe and a folder named Data."))
		case waitingForSignIn:
			if sessionChecked {
				homeHeading.SetText(tr("Sign in to Ebonhold"))
			} else {
				homeHeading.SetText(tr("Checking sign-in…"))
			}
			homeDescription.SetText(tr("Use your Project Ebonhold account to update files."))
		case ptrNeedsInstall && !updateFailed:
			updateState.Text, updateState.Color = tr("READY TO UPDATE"), hexColor(colorAmber)
			homeHeading.SetText(tr("Update this PTR copy before playing."))
			homeDescription.SetText(tr("Choose Install PTR once to prepare this copy."))
		case !updateHasRun:
			updateState.Text, updateState.Color = tr("READY TO UPDATE"), hexColor(colorAmber)
			switch {
			case canPlay && updateBeforePlay:
				homeHeading.SetText(tr("Ready to play"))
				homeDescription.SetText(tr("Play checks for updates before starting the game."))
			case canPlay:
				homeHeading.SetText(tr("Ready to play"))
				homeDescription.SetText(tr("Play starts the game without checking for updates."))
			default:
				homeHeading.SetText(tr("Start the game from your usual app"))
				homeDescription.SetText(tr("You can update here and open the game yourself."))
			}
		case updateFailed && updateVerify:
			if len(updateResult.FailedFiles) == 0 {
				updateState.Text, updateState.Color = tr("VERIFICATION STOPPED"), hexColor(colorRed)
				homeHeading.SetText(tr("Verification failed"))
				homeDescription.SetText(tr("Open Technical details on Update for the cause."))
			} else {
				updateState.Text, updateState.Color = tr("VERIFICATION NEEDS ATTENTION"), hexColor(colorRed)
				homeHeading.SetText(tr("Some files need repair"))
				homeDescription.SetText(tr("Files checked: %d · Missing: %d · Other issues: %d. Select a file on Update for the next step.", updateResult.Checked, updateResult.Missing, updateResult.Mismatched))
			}
		case updateFailed:
			updateState.Text, updateState.Color = tr("UPDATE NEEDS A RETRY"), hexColor(colorRed)
			homeHeading.SetText(tr("Retry the update"))
			homeDescription.SetText(tr("The update did not finish. Some files may be out of date."))
		case updateVerify:
			updateState.Text, updateState.Color = tr("FILES VERIFIED"), hexColor(colorGreen)
			homeHeading.SetText(tr("Your game files are ready"))
			homeDescription.SetText(tr("Verification checked %d files; all matched the selected profile.", updateResult.Checked))
		case updateResult.Downloaded > 0 || updateResult.FilesUpdated:
			updateState.Text, updateState.Color = tr("UPDATE COMPLETE"), hexColor(colorGreen)
			homeHeading.SetText(tr("Update complete"))
			homeDescription.SetText(tr("Files downloaded: %d", updateResult.Downloaded))
		default:
			updateState.Text, updateState.Color = tr("CLIENT UP TO DATE"), hexColor(colorGreen)
			homeHeading.SetText(tr("Your game files are up to date"))
			homeDescription.SetText(tr("No files needed updating."))
		}
		if waitingForSignIn {
			updateState.Hide()
			statusRow.Hide()
		} else {
			if canPlay && !ptrNeedsInstall && !updateInProgress && !updateHasRun {
				updateState.Hide()
			} else {
				updateState.Show()
			}
			statusRow.Show()
		}
		if waitingForSignIn && sessionChecked {
			if signingIn {
				signInButton.SetText(tr("Signing in…"))
			} else {
				signInButton.SetText(tr("Sign in"))
			}
			signInButton.Show()
		} else {
			signInButton.Hide()
		}
		if fileActions != nil {
			if waitingForSignIn {
				fileActions.Hide()
			} else {
				fileActions.Show()
			}
			if (clientReady && canPlay) || waitingForSignIn {
				launchRow.Hide()
			} else {
				launchRow.Show()
			}
		}
		updateState.Refresh()
		actionLabel := "Update files"
		if ptrNeedsInstall {
			actionLabel = "Install PTR"
		}
		if updateFailed && !updateVerify {
			actionLabel = "Retry update"
		}
		updateBtn.SetText(tr(actionLabel))
		if updateActivityButton != nil {
			updateActivityButton.SetText(updateBtn.Text)
		}
		if verifyActivityButton != nil {
			verifyActivityButton.SetText(tr("Verify files"))
		}
		busy := gameActionBusy()
		setEnabled(signInButton, !busy)
		if sessionChecked && signInNeeded {
			loadCatalog.SetText(tr("Sign in"))
		} else {
			loadCatalog.SetText(tr("Refresh catalog"))
		}
		setEnabled(updateBtn, clientReady && !busy)
		setEnabled(verifyBtn, clientReady && !busy)
		if updateActivityButton != nil {
			setEnabled(updateActivityButton, clientReady && !busy)
		}
		if verifyActivityButton != nil {
			setEnabled(verifyActivityButton, clientReady && !busy)
		}
		setEnabled(game, !busy)
		setEnabled(root, !busy)
		setEnabled(browse, !busy)
		setEnabled(browseExecutable, !busy)
		setEnabled(searchFolders, !busy)
		if homeGame != nil {
			setEnabled(homeGame, !busy)
		}
		if settingsButton != nil {
			setEnabled(settingsButton, !busy && !addonBusy)
		}
		if clientReady && canPlay && !ptrNeedsInstall && (!waitingForSignIn || !updateBeforePlay) {
			playButton.Show()
		} else {
			playButton.Hide()
		}
		playButton.Importance = widget.HighImportance
		if waitingForSignIn {
			playButton.Importance = widget.MediumImportance
		}
		setEnabled(playButton, !busy)
		playButton.Refresh()
		if launchSetupButton != nil {
			action := "Set up launch"
			var summary string
			switch {
			case !clientReady:
				action = "Choose game folder"
				summary = tr("Game folder not ready")
			case target.Manager == "bottles-native", target.Manager == "bottles-flatpak":
				summary = tr("Bottle name needed")
			case target.Manager == "steam", target.Manager == "lutris":
				summary = tr("Game shortcut needed")
			default:
				summary = tr("Start the game yourself until launch is set up.")
			}
			launchSummary.SetText(summary)
			launchSetupButton.SetText(tr(action))
			importance := widget.HighImportance
			if clientReady && target.Manager == "" {
				importance = widget.LowImportance
			}
			launchSetupButton.Importance = importance
			setEnabled(launchSetupButton, !busy)
			launchSetupButton.Refresh()
		}
		updateBtn.Importance = widget.MediumImportance
		if ptrNeedsInstall || (!canPlay && target.Manager == "") {
			updateBtn.Importance = widget.HighImportance
		}
		updateBtn.Refresh()
		activityHeading.SetText(homeHeading.Text)
		activityRealm.SetText(game.Selected)
		switch {
		case updateInProgress:
			activityNotice.Hide()
		case updateError != "" && len(updateResult.FailedFiles) == 0:
			activityDescription.SetText(tr("Open Technical details on Update for the cause."))
		case updateError != "":
			activityDescription.SetText(homeDescription.Text)
		case updateHasRun:
			text := status.Text
			if !updateVerify && (updateResult.FilesUpdated || updateResult.RealmlistUpdated) {
				text += "\n" + tr("Game cache refreshed.")
			}
			activityDescription.SetText(text)
		case !clientReady:
			activityDescription.SetText(tr("Choose and confirm your game folder in Setup first."))
		case waitingForSignIn:
			activityDescription.SetText(tr("Sign in to check for updates."))
		default:
			activityDescription.SetText(tr("Verify checks files without changing them."))
		}
		if !updateInProgress {
			activityNotice.Show()
		}
		if updateError != "" {
			technicalAction.Show()
		} else {
			technicalAction.Hide()
		}
		if updateInProgress {
			activityProgress.Show()
		} else {
			activityProgress.Hide()
		}
		if homeScroll != nil {
			homeScroll.Refresh()
		}
	}
	refreshUpdateUI()
	launchSummary = widget.NewLabel("")
	launchSummary.Wrapping = fyne.TextWrapWord
	launchSummary.Importance = widget.LowImportance
	launchSetupButton = widget.NewButtonWithIcon(tr("Set up launch"), theme.NavigateNextIcon(), func() {
		rootPath, err := clientRoot()
		if err == nil {
			_, err = selectedGameExecutable(rootPath)
		}
		if err != nil {
			setSetupStep(1)
		} else {
			setSetupStep(2)
		}
		switchScreen("setup")
	})
	launchSetupButton.Importance = widget.LowImportance
	launchSetupButton.IconPlacement = widget.ButtonIconTrailingText
	homeGame = widget.NewSelect(gameChoices, func(profile string) {
		if game.Selected != profile {
			game.SetSelected(profile)
		}
	})
	homeGame.SetSelected(game.Selected)
	profileTitle := canvas.NewText(tr("GAME PROFILE"), hexColor(colorBlue))
	profileTitle.TextSize = 12
	profileTitle.TextStyle = fyne.TextStyle{Bold: true}
	profileHint = widget.NewLabel(profileHintText(selectedGame()))
	profileHint.Wrapping = fyne.TextWrapWord
	profileHint.Importance = widget.LowImportance
	profileSelect := container.NewGridWrap(fyne.NewSize(232, homeGame.MinSize().Height), homeGame)
	settingsButton = widget.NewButtonWithIcon(tr("Settings"), theme.SettingsIcon(), func() {
		mu.Lock()
		profile := prefs.GameSlug
		original := prefs
		mu.Unlock()
		before := original.GameSettingsForProfile(profile)
		addToApplications := func() error { return launcher.InstallApplicationShortcut(launcherPath, launcherMark) }
		showSettings(w, tr, game.Selected, original.LauncherLanguage, original.LaunchTargetForProfile(profile).Manager, before, cvarsFor(profile, selectedRoot()), addToApplications, func(settings launcher.GameSettings, language string) {
			mu.Lock()
			prefs.SetGameSettingsForProfile(profile, settings)
			prefs.LauncherLanguage = language
			next := prefs
			mu.Unlock()
			if !save() {
				mu.Lock()
				prefs.SetGameSettingsForProfile(profile, before)
				prefs.LauncherLanguage = original.LauncherLanguage
				mu.Unlock()
				return
			}
			message := "Settings saved."
			patchesChanged := settings.Language != before.Language || settings.ManageHD != before.ManageHD ||
				settings.HDCharacters != before.HDCharacters || settings.HDCreatures != before.HDCreatures
			if patchesChanged {
				message = "Settings saved. Update files to apply game changes."
			}
			if (runningLanguage == "fr") != (language == "fr") {
				reload(next, message)
				return
			}
			if patchesChanged {
				resetUpdate()
			}
			status.SetText(tr(message))
			refreshSetup()
		})
	})
	fileActions = container.NewGridWithColumns(2, updateBtn, verifyBtn)
	homeActions := container.NewBorder(nil, nil, nil, settingsButton, fileActions)
	profileRow := container.NewBorder(nil, nil, container.NewCenter(profileTitle), profileSelect, nil)
	launchRow = container.NewBorder(nil, nil, nil, container.NewCenter(launchSetupButton),
		container.NewVBox(layout.NewSpacer(), flushText(launchSummary), layout.NewSpacer()))
	refreshSetup()
	primaryCard := card(container.NewVBox(
		profileRow,
		flushText(profileHint),
		widget.NewSeparator(),
		updateState,
		flushText(homeHeading),
		flushText(homeDescription),
		signInButton,
		playButton,
		launchRow,
		homeActions,
		statusRow,
	))
	addonManageButton := widget.NewButton(tr("Browse add-ons"), func() { switchScreen("addons") })
	homeAddonStatus = widget.NewLabel(addonStatus.Text)
	homeAddonStatus.Truncation = fyne.TextTruncateEllipsis
	homeAddonStatus.Importance = widget.LowImportance
	addonRow := container.NewBorder(nil, nil, container.NewCenter(headingText(tr("Add-ons"), 16)), addonManageButton)
	supportCard := card(container.NewVBox(addonRow, flushText(homeAddonStatus), widget.NewSeparator(), container.NewHBox(newTroubleshootButton())))
	newsView, newsPreview, loadNews := newNewsView(windowCtx, tr, fetchNews, func() { switchScreen("news") }, func() { homeScroll.Refresh() })
	homeCards := container.New(layout.NewCustomPaddedVBoxLayout(8), primaryCard, layout.NewSpacer(), supportCard)
	homeScroll = container.NewVScroll(nil)
	homeScroll.Content = container.New(homeLayout{viewport: homeScroll}, card(newsPreview), homeCards)
	updateFiles = newUpdateFilePane(w, tr, card)
	updateActivityButton = widget.NewButton(tr("Update files"), func() { updateFilesOnly(false) })
	verifyActivityButton = widget.NewButton(tr("Verify files"), func() { updateFilesOnly(true) })
	updateActivityButton.Importance = widget.HighImportance
	activityButtons := container.NewGridWithColumns(2, updateActivityButton, verifyActivityButton)
	activityCard := card(container.New(layout.NewCustomPaddedVBoxLayout(8),
		flushText(activityHeading), activityNotice, activityProgress,
		activityButtons, technicalAction,
	))
	activityColumn := container.NewVScroll(container.NewVBox(activityCard))
	activityColumn.SetMinSize(fyne.NewSize(328, 0))
	activityLeft := container.New(layout.NewCustomPaddedLayout(0, 0, 0, 8), activityColumn)
	updateHeader := container.NewBorder(nil, nil, nil, container.NewCenter(flushText(activityRealm)), headingText(tr("Update activity"), 26))
	updateView := container.NewBorder(
		container.New(layout.NewCustomPaddedLayout(0, 8, 0, 0), updateHeader),
		nil, activityLeft, nil, updateFiles.content,
	)
	refreshUpdateUI()
	const screenInset = 22
	screenPadding := layout.NewCustomPaddedLayout(18, 18, screenInset, screenInset)
	homeView := container.New(screenPadding, homeScroll)
	updateScreen := container.New(screenPadding, updateView)
	setupScreen := container.New(screenPadding, setupView)
	addonsScreen := container.New(screenPadding, addonScreen)
	newsScreen := container.New(screenPadding, card(newsView))
	homeNav := widget.NewButton(tr("Home"), func() { switchScreen("home") })
	updateNav := widget.NewButton(tr("Update"), func() { switchScreen("update") })
	setupNav := widget.NewButton(tr("Setup"), func() { switchScreen("setup") })
	addonsNav := widget.NewButton(tr("Add-ons"), func() { switchScreen("addons") })
	newsNav := widget.NewButton(tr("News"), func() { switchScreen("news") })
	const underlineHeight = 3
	navIndicators := make(map[string]*canvas.Rectangle)
	navItem := func(name string, button *widget.Button) fyne.CanvasObject {
		button.Importance = widget.LowImportance
		indicator := canvas.NewRectangle(color.Transparent)
		indicator.SetMinSize(fyne.NewSize(0, underlineHeight))
		navIndicators[name] = indicator
		return container.NewVBox(button, indicator)
	}
	dimWallpaper := canvas.NewRectangle(color.NRGBA{R: 7, G: 14, B: 23, A: 192})
	screenStack := container.NewStack(homeView)
	screenViews := map[string]fyne.CanvasObject{"home": homeView, "update": updateScreen, "setup": setupScreen, "addons": addonsScreen, "news": newsScreen}
	dimWallpaper.Hide()
	switchScreen = func(name string) {
		view, ok := screenViews[name]
		if !ok {
			return
		}
		if name == "addons" {
			refreshAddons()
		}
		if name == "home" || name == "setup" {
			if name == "setup" && setupStep != 1 && selectedGame() == "roguelike" && setupFolderMissing(selectedRoot()) {
				setSetupStep(1)
			} else {
				refreshSetup()
			}
		}
		if name == "home" {
			dimWallpaper.Hide()
			profileHint.SetText(profileHintText(selectedGame()))
			homeScroll.ScrollToTop()
		} else {
			dimWallpaper.Show()
		}
		screenStack.Objects = []fyne.CanvasObject{view}
		screenStack.Refresh()
		for key, indicator := range navIndicators {
			indicator.FillColor = color.Transparent
			if key == name {
				indicator.FillColor = hexColor(colorBlue)
			}
			indicator.Refresh()
		}
	}
	brandLogo := canvas.NewImageFromResource(launcherIcon)
	brandLogo.FillMode = canvas.ImageFillContain
	brandLogo.SetMinSize(fyne.NewSquareSize(56))
	logoFrame := canvas.NewRectangle(hexColor(colorCard))
	logoFrame.StrokeColor = hexColor(colorBorder)
	logoFrame.StrokeWidth = 1
	brandTile := container.NewStack(logoFrame, container.New(layout.NewCustomPaddedLayout(2, 2, 2, 2), brandLogo))
	brandSubtitle := widget.NewLabel(tr("Linux game client"))
	brandSubtitle.Importance = widget.LowImportance
	brandSubtitle.Truncation = fyne.TextTruncateEllipsis
	subtitle := container.New(layout.NewCustomPaddedLayout(-theme.InnerPadding(), -theme.InnerPadding(), -theme.InnerPadding(), -theme.InnerPadding()), brandSubtitle)
	brand := container.NewBorder(nil, nil, container.NewCenter(brandTile), nil, container.NewVBox(
		layout.NewSpacer(), headingText("Ebonhold", 18), subtitle, layout.NewSpacer(),
	))
	captionWidth := fyne.MeasureText(brandSubtitle.Text, theme.TextSize(), brandSubtitle.TextStyle).Width
	brandWidth := brandTile.MinSize().Width + theme.Padding() + float32(math.Ceil(float64(captionWidth)))
	navigation := container.New(layout.NewCustomPaddedHBoxLayout(theme.Padding()/2),
		navItem("home", homeNav),
		navItem("update", updateNav),
		navItem("setup", setupNav),
		navItem("addons", addonsNav),
		navItem("news", newsNav),
	)
	// Balance the underline and its gap so the tab labels share the header's centerline.
	nav := container.New(layout.NewCustomPaddedLayout(underlineHeight+theme.Padding(), 0, 0, 0), navigation)
	header = container.New(headerLayout{status: serverStatus, brandWidth: brandWidth}, brand, nav, serverStatusDot, serverStatus)
	bar := container.New(layout.NewCustomPaddedLayout(8, 8, screenInset, screenInset), header)
	rule := canvas.NewRectangle(hexColor(colorBorder))
	rule.SetMinSize(fyne.NewSize(1, 1))
	topbar := container.NewStack(canvas.NewRectangle(hexColor(colorRail)), container.NewBorder(nil, rule, nil, nil, bar))
	background := container.NewStack(canvas.NewRectangle(hexColor(colorBG)), launcherBackdrop(), dimWallpaper)
	copyActivity := wrappedLabel("")
	copyActivity.TextStyle.Bold = true
	copyProgress := widget.NewProgressBarInfinite()
	meter := container.NewGridWrap(fyne.NewSize(152, copyProgress.MinSize().Height), copyProgress)
	copyProgress.Stop()
	copyBanner := container.NewStack(canvas.NewRectangle(hexColor(colorRail)),
		container.New(layout.NewCustomPaddedLayout(2, 6, screenInset, screenInset),
			container.NewBorder(nil, nil, nil, meter, copyActivity)))
	copyBanner.Hide()
	content := container.NewBorder(container.New(layout.NewCustomPaddedVBoxLayout(0), topbar, copyBanner), nil, nil, nil, background, screenStack)
	w.SetContent(content)
	setCopyActivity = func(message string) {
		copyInProgress = message != ""
		if copyInProgress {
			setupNotice.SetText("")
			copyActivity.SetText(tr(message))
			copyBanner.Show()
			copyProgress.Start()
		} else {
			copyProgress.Stop()
			copyBanner.Hide()
		}
		content.Refresh()
		refreshSetup()
	}
	switchScreen("home")
	migrateSavedPasswords(prefs.ProductionRoot, prefs.PTRRoot)
	loadNews()
	if notice == "" && setupFolderMissing(prefs.RootForProfile(prefs.GameSlug)) {
		switchScreen("setup")
	}
	refreshServerStatus()
	return func() {
		cancelWindow()
		copyProgress.Stop()
		copyWork.Wait()
	}
}
