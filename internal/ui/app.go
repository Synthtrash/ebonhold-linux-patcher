package ui

import (
	"context"
	"errors"
	"fmt"
	"image/color"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	"github.com/synthtrash/ebonhold-linux-patcher/internal/launcher"
)

const (
	colorBG     = "#1e1e2e"
	colorRail   = "#181825"
	colorCard   = "#313244"
	colorRaised = "#3b3d51"
	colorBorder = "#7f849c"
	colorText   = "#cdd6f4"
	colorMuted  = "#a6adc8"
	colorBlue   = "#89b4fa"
	colorRed    = "#f38ba8"
)

var catThemeColors = map[fyne.ThemeColorName]color.Color{
	theme.ColorNameBackground:      hexColor(colorBG),
	theme.ColorNameButton:          hexColor(colorCard),
	theme.ColorNameDisabled:        hexColor(colorMuted),
	theme.ColorNameError:           hexColor(colorRed),
	theme.ColorNameFocus:           hexColor(colorBlue),
	theme.ColorNameForeground:      hexColor(colorText),
	theme.ColorNameHover:           hexColor(colorRaised),
	theme.ColorNameInputBackground: hexColor(colorRail),
	theme.ColorNamePlaceHolder:     hexColor(colorMuted),
	theme.ColorNamePrimary:         hexColor(colorBlue),
	theme.ColorNameScrollBar:       hexColor(colorBorder),
	theme.ColorNameSelection:       hexColor(colorRaised),
}

type catTheme struct{ fyne.Theme }

func (t catTheme) Color(n fyne.ThemeColorName, v fyne.ThemeVariant) color.Color {
	if c, ok := catThemeColors[n]; ok {
		return c
	}
	return t.Theme.Color(n, v)
}

func hexColor(s string) color.Color {
	var r, g, b uint8
	_, _ = fmt.Sscanf(s, "#%02x%02x%02x", &r, &g, &b)
	return color.NRGBA{R: r, G: g, B: b, A: 255}
}

type loginPrompt struct{ win fyne.Window }

func (p loginPrompt) PromptLogin(_ context.Context, _ string, allowRemember bool) (string, string, bool, error) {
	user, pass := widget.NewEntry(), widget.NewPasswordEntry()
	user.SetPlaceHolder("Username")
	pass.SetPlaceHolder("Password")
	remember := widget.NewCheck("Remember sign-in on this device", nil)
	items := []*widget.FormItem{widget.NewFormItem("Username", user), widget.NewFormItem("Password", pass)}
	if allowRemember {
		items = append(items, widget.NewFormItem("", remember))
		items = append(items, widget.NewFormItem("Storage warning", widget.NewLabel("Remembering stores the username and password unencrypted in a private local file.")))
	}
	type answer struct {
		user, pass   string
		remember, ok bool
	}
	done := make(chan answer, 1)
	d := dialog.NewForm("Sign in", "Sign in", "Cancel", items, func(ok bool) {
		done <- answer{user: user.Text, pass: pass.Text, remember: allowRemember && remember.Checked, ok: ok}
	}, p.win)
	d.Resize(fyne.NewSize(440, 260))
	fyne.Do(func() { d.Show() })
	result := <-done
	if !result.ok {
		return "", "", false, errors.New("sign-in cancelled")
	}
	return result.user, result.pass, result.remember, nil
}

func Run(api launcher.API, launcherRoot string) error {
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
	a := app.NewWithID("com.project-ebonhold.launcher")
	a.Settings().SetTheme(catTheme{Theme: theme.DefaultTheme()})
	w := a.NewWindow("Ebonhold Launcher")
	w.Resize(fyne.NewSize(1000, 650))
	w.SetFixedSize(false)
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
	if prefs.Manager == "" {
		prefs.Manager = "unknown"
	}
	var mu sync.Mutex
	var status *widget.Label
	save := func() {
		mu.Lock()
		err := prefsStore.Save(prefs)
		mu.Unlock()
		if err != nil && status != nil {
			status.SetText("Preferences not saved: " + err.Error())
		}
	}
	root := widget.NewEntry()
	root.SetText(prefs.ClientRoot)
	root.SetPlaceHolder("Select your game client folder")
	game := widget.NewSelect([]string{"Production", "PTR"}, nil)
	if prefs.GameSlug == "roguelike" {
		game.SetSelected("PTR")
	} else {
		game.SetSelected("Production")
	}
	folderText := "No client folder selected"
	if prefs.ClientRoot != "" {
		folderText = "Selected folder: " + prefs.ClientRoot
	}
	folderLabel := widget.NewLabel(folderText)
	folderLabel.Wrapping = fyne.TextWrapBreak
	managerSelect := widget.NewSelect([]string{"Not sure yet", "Steam", "Lutris", "Bottles (native)", "Bottles (Flatpak)"}, nil)
	managerIDs := map[string]string{"Not sure yet": "unknown", "Steam": "steam", "Lutris": "lutris", "Bottles (native)": "bottles-native", "Bottles (Flatpak)": "bottles-flatpak"}
	managerNames := map[string]string{"unknown": "Not sure yet", "steam": "Steam", "lutris": "Lutris", "bottles-native": "Bottles (native)", "bottles-flatpak": "Bottles (Flatpak)"}
	managerSelect.SetSelected(managerNames[prefs.Manager])
	if managerSelect.Selected == "" {
		managerSelect.SetSelected("Not sure yet")
	}
	var refreshSetup func()
	root.OnChanged = func(s string) {
		mu.Lock()
		prefs.ClientRoot = s
		mu.Unlock()
		folderLabel.SetText("No client folder selected")
		if strings.TrimSpace(s) != "" {
			folderLabel.SetText("Selected folder: " + s)
		}
		save()
		if refreshSetup != nil {
			refreshSetup()
		}
	}
	game.OnChanged = func(s string) {
		mu.Lock()
		if s == "PTR" {
			prefs.GameSlug = "roguelike"
		} else {
			prefs.GameSlug = "roguelike-prod"
		}
		mu.Unlock()
		save()
	}
	status = widget.NewLabel("Ready · sign in to check server status")
	status.Wrapping = fyne.TextWrapWord
	progressText := widget.NewLabel("No update running")
	progress := widget.NewProgressBar()
	progress.Hide()
	files := []string{}
	newAuthenticator := func(root string, readOnly bool) *launcher.Authenticator {
		return &launcher.Authenticator{API: api, Store: &launcher.CredentialStore{Root: root}, Prompt: loginPrompt{win: w}, ReadOnly: readOnly}
	}
	var catalog []launcher.Addon
	async := func(task func(context.Context)) { go task(context.Background()) }
	showDetails := func(title, details string) {
		entry := widget.NewMultiLineEntry()
		entry.SetText(details)
		entry.Disable()
		copy := widget.NewButton("Copy diagnostics", func() { w.Clipboard().SetContent(details) })
		dialog.ShowCustom(title, "Close", container.NewBorder(nil, copy, nil, nil, entry), w)
	}
	showAuthWarning := func(auth *launcher.Authenticator) {
		if auth.Warning == "" {
			return
		}
		warning := auth.Warning
		auth.Warning = ""
		fyne.Do(func() { showDetails("Sign-in warning", warning) })
	}
	showErr := func(title string, e error) {
		if e == nil {
			return
		}
		fyne.Do(func() { status.SetText(title + ": " + e.Error()); showDetails(title, e.Error()) })
	}
	selectedRoot := func() string { mu.Lock(); defer mu.Unlock(); return prefs.ClientRoot }
	selectedGame := func() string { mu.Lock(); defer mu.Unlock(); return prefs.GameSlug }
	clientRoot := func() (string, error) {
		root := selectedRoot()
		if strings.TrimSpace(root) == "" {
			return "", errors.New("choose a game client folder first")
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
	showFiles := func() {
		text := strings.Join(files, "\n")
		if text == "" {
			text = "No files listed."
		}
		entry := widget.NewMultiLineEntry()
		entry.SetText(text)
		entry.Disable()
		dialog.ShowCustom("Update files", "Close", entry, w)
	}
	var tabs *container.AppTabs
	runUpdate := func(verify bool) {
		async(func(ctx context.Context) {
			rootPath, err := clientRoot()
			if err != nil {
				showErr("Choose a client folder", err)
				return
			}
			fyne.Do(func() {
				status.SetText("Signing in and loading game manifest…")
				progressText.SetText("Starting")
				progress.SetValue(0)
				progress.Show()
			})
			updateAuth := newAuthenticator(rootPath, verify)
			token, manifest, e := updateAuth.Authenticate(ctx, false)
			if e != nil {
				showErr("Sign-in failed", e)
				fyne.Do(func() { progress.Hide() })
				return
			}
			showAuthWarning(updateAuth)
			server, serverErr := api.Status(ctx, token)
			if serverErr == nil {
				fyne.Do(func() {
					if server.Online {
						status.SetText("Server online · " + server.ServerName)
					} else {
						status.SetText("Server offline · " + server.ServerName)
					}
				})
			}
			up := launcher.NewUpdater(api, updateAuth, rootPath)
			var pathsMu sync.Mutex
			var downloadedPaths []string
			result, e := up.Run(ctx, manifest, token, launcher.UpdateOptions{GameSlug: selectedGame(), Full: false, Verify: verify}, func(p launcher.Progress) {
				if p.Stage == "download" {
					pathsMu.Lock()
					downloadedPaths = append(downloadedPaths, p.Path)
					pathsMu.Unlock()
				}
				message := p.Message
				if message == "" {
					message = strings.ToUpper(p.Stage)
					if p.Path != "" {
						message += " · " + p.Path
					}
				}
				fyne.Do(func() {
					progressText.SetText(message)
					if p.Total > 0 {
						progress.SetValue(float64(p.Current) / float64(p.Total))
					}
				})
			})
			showAuthWarning(updateAuth)
			pathsMu.Lock()
			downloaded := append([]string(nil), downloadedPaths...)
			pathsMu.Unlock()
			fyne.Do(func() {
				files = append([]string(nil), result.PlannedPaths...)
				if len(files) == 0 && verify {
					for _, f := range result.Files {
						files = append(files, f.Path)
					}
				} else if len(files) == 0 {
					files = append(files, downloaded...)
				}
				progress.Hide()
				if e != nil {
					status.SetText("Update failed: " + e.Error())
					showDetails("Update failed", fmt.Sprintf("%s\nChecked: %d\nDownloaded: %d\nMissing: %d\nMismatched: %d\nFailed paths:\n%s", e.Error(), result.Checked, result.Downloaded, result.Missing, result.Mismatched, strings.Join(result.FailedPaths, "\n")))
					return
				}
				if verify {
					status.SetText(fmt.Sprintf("Verification complete · checked %d, missing %d, mismatched %d", result.Checked, result.Missing, result.Mismatched))
				} else {
					status.SetText(fmt.Sprintf("Update complete · downloaded %d files", result.Downloaded))
				}
			})
		})
	}
	updateBtn := widget.NewButton("Update", func() { runUpdate(false) })
	verifyBtn := widget.NewButton("Verify", func() { runUpdate(true) })
	guideBtn := widget.NewButton("Setup guide", func() { tabs.SelectIndex(2) })
	filesBtn := widget.NewButton("View files", showFiles)
	browse := widget.NewButton("Choose folder…", func() {
		dialog.NewFolderOpen(func(uri fyne.ListableURI, e error) {
			if e == nil && uri != nil {
				root.SetText(uri.Path())
			}
		}, w).Show()
	})
	folderRow := container.NewBorder(nil, nil, nil, browse, root)
	welcome := widget.NewLabel("To launch Ebonhold, start the game from your selected manager after applying its preset. This app cannot safely guess an app ID, bottle, or prefix.")
	welcome.Wrapping = fyne.TextWrapWord
	home := container.NewVBox(widget.NewLabelWithStyle("Ebonhold Launcher", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}), status, welcome, widget.NewSeparator(), widget.NewLabel("Client folder"), folderRow, folderLabel, widget.NewLabel("Game profile"), game, container.NewGridWithColumns(3, updateBtn, verifyBtn, guideBtn))
	updateTab := container.NewVBox(widget.NewLabel("Update and verify"), container.NewGridWithColumns(2, widget.NewButton("Update", func() { runUpdate(false) }), widget.NewButton("Verify", func() { runUpdate(true) })), progressText, progress, filesBtn)
	addonNames := widget.NewEntry()
	addonNames.SetPlaceHolder("Addon names or IDs, comma-separated")
	addonStatus := widget.NewLabel("Catalog not loaded")
	addonList := widget.NewMultiLineEntry()
	addonList.Disable()
	loadCatalog := widget.NewButton("Load catalog", func() {
		rootPath, err := clientRoot()
		if err != nil {
			showErr("Choose a client folder", err)
			return
		}
		manager := launcher.NewAddonManager(api, rootPath)
		async(func(ctx context.Context) {
			auth := newAuthenticator(rootPath, false)
			token, _, err := auth.Authenticate(ctx, false)
			if err != nil {
				showErr("Add-on sign-in failed", err)
				return
			}
			showAuthWarning(auth)
			items, err := manager.Catalog(ctx, token)
			if err != nil {
				showErr("Catalog failed", err)
				return
			}
			mu.Lock()
			catalog = append([]launcher.Addon(nil), items...)
			mu.Unlock()
			fyne.Do(func() {
				lines := make([]string, 0, len(items))
				for _, item := range items {
					lines = append(lines, fmt.Sprintf("%s · ID %d", item.Name, item.ID))
				}
				addonList.SetText(strings.Join(lines, "\n"))
				addonStatus.SetText(fmt.Sprintf("%d catalog add-ons", len(items)))
			})
		})
	})
	checkAddons := widget.NewButton("Check installed", func() {
		rootPath, err := clientRoot()
		if err != nil {
			showErr("Choose a client folder", err)
			return
		}
		manager := launcher.NewAddonManager(api, rootPath)
		async(func(context.Context) {
			items, err := manager.Installed()
			if err != nil {
				showErr("Add-on check failed", err)
				return
			}
			lines := make([]string, 0, len(items))
			for _, item := range items {
				lines = append(lines, item.Addon.Name+" · "+item.State)
			}
			fyne.Do(func() {
				addonList.SetText(strings.Join(lines, "\n"))
				addonStatus.SetText(fmt.Sprintf("%d launcher-managed add-ons installed", len(items)))
			})
		})
	})
	installAddon := widget.NewButton("Install", func() {
		rootPath, err := clientRoot()
		if err != nil {
			showErr("Choose a client folder", err)
			return
		}
		manager := launcher.NewAddonManager(api, rootPath)
		requested := addonNames.Text
		async(func(ctx context.Context) {
			mu.Lock()
			available := append([]launcher.Addon(nil), catalog...)
			mu.Unlock()
			ids, err := manager.Resolve(available, requested)
			if err != nil {
				showErr("Add-on selection failed", err)
				return
			}
			auth := newAuthenticator(rootPath, false)
			token, _, err := auth.Authenticate(ctx, false)
			if err != nil {
				showErr("Add-on sign-in failed", err)
				return
			}
			showAuthWarning(auth)
			if err := manager.Install(ctx, token, ids); err != nil {
				showErr("Add-on install failed", err)
				return
			}
			fyne.Do(func() { addonStatus.SetText("Add-ons installed") })
		})
	})
	removeAddon := widget.NewButton("Remove", func() {
		rootPath, err := clientRoot()
		if err != nil {
			showErr("Choose a client folder", err)
			return
		}
		manager := launcher.NewAddonManager(api, rootPath)
		requested := addonNames.Text
		async(func(context.Context) {
			lines, err := manager.Remove(requested)
			if err != nil {
				showErr("Add-on removal failed", err)
				return
			}
			fyne.Do(func() { addonStatus.SetText("Add-ons removed · " + strings.Join(lines, "; ")) })
		})
	})
	addonsBox := container.NewVBox(addonStatus, addonNames, container.NewGridWithColumns(4, loadCatalog, checkAddons, installAddon, removeAddon), addonList)
	updateTab.Add(addonsBox)
	managerInfo := widget.NewLabel("")
	managerInfo.Wrapping = fyne.TextWrapWord
	preset := widget.NewMultiLineEntry()
	preset.Disable()
	detectedManagerText := "Detecting installed game managers…"
	refreshSetup = func() {
		selected := managerSelect.Selected
		key := managerIDs[selected]
		if selected == "Not sure yet" || key == "" {
			key = "unknown"
		}
		if key == "unknown" {
			preset.SetText("Choose a manager only if you know which one starts Ebonhold.")
		} else {
			rootPath, err := clientRoot()
			if err != nil {
				preset.SetText(err.Error())
			} else {
				preset.SetText(launcher.ManagerPreset(key, launcherPath, rootPath))
			}
		}
		message := detectedManagerText + " This only detects installed applications; it cannot identify which one starts Ebonhold."
		if strings.HasPrefix(key, "bottles-") {
			message += " Replace the bottle name and executable path in the Bottles wrapper before using it."
		}
		managerInfo.SetText(message)
	}
	managerSelect.OnChanged = func(s string) {
		mu.Lock()
		prefs.Manager = managerIDs[s]
		mu.Unlock()
		save()
		refreshSetup()
	}
	refreshSetup()
	go func() {
		managers := launcher.DetectManagers(context.Background())
		var names []string
		for _, manager := range managers {
			if manager.Installed && manager.ID != "unknown" {
				names = append(names, manager.Name)
			}
		}
		if len(names) == 0 {
			names = append(names, "none")
		}
		fyne.Do(func() {
			detectedManagerText = "Detected as installed: " + strings.Join(names, ", ") + "."
			refreshSetup()
		})
	}()
	copyPreset := widget.NewButton("Copy preset", func() {
		manager := managerIDs[managerSelect.Selected]
		if manager == "" || manager == "unknown" {
			showErr("Choose a manager", errors.New("select Steam, Lutris, or Bottles before copying a preset"))
			return
		}
		rootPath, err := clientRoot()
		if err != nil {
			showErr("Choose a client folder", err)
			return
		}
		w.Clipboard().SetContent(launcher.ManagerPreset(manager, launcherPath, rootPath))
		status.SetText("Preset copied · apply it yourself in the selected manager")
	})
	setup := container.NewVBox(widget.NewLabel("Manager setup · instructions only"), managerInfo, widget.NewLabel("Selected manager"), managerSelect, preset, copyPreset, widget.NewLabel("Do not paste a Bottles preset without replacing its bottle name and executable path. The launcher never changes manager configuration, installs packages, alters sandbox permissions, or starts the game."))
	tabs = container.NewAppTabs(container.NewTabItem("Home", home), container.NewTabItem("Update", container.NewVScroll(updateTab)), container.NewTabItem("Setup", container.NewVScroll(setup)))
	tabs.SetTabLocation(container.TabLocationTop)
	w.SetContent(tabs)
	go func() {
		server, err := api.Status(context.Background(), "")
		if err == nil {
			fyne.Do(func() {
				if server.Online {
					status.SetText("Server online · " + server.ServerName)
				} else {
					status.SetText("Server offline · " + server.ServerName)
				}
			})
		}
	}()
	w.ShowAndRun()
	return nil
}
