package launcher

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"golang.org/x/term"
)

type CLIOptions struct {
	Help          bool
	GUI           bool
	Debug         bool
	Verify        bool
	DryRun        bool
	Quick         bool
	Full          bool
	Status        bool
	GameSlug      string
	ClientDir     string
	ListAddons    bool
	CheckAddons   bool
	SelectAddons  bool
	RemoveAddons  bool
	RemoveNames   string
	Addons        string
	Quiet         bool
	Relogin       bool
	ForgetLogin   bool
	Language      string
	HDCharacters  bool
	HDCreatures   bool
	OptionSlugs   []string
	GameArguments []string
}

func ParseArgs(args []string) (CLIOptions, error) {
	var opts CLIOptions
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			opts.GameArguments = append(opts.GameArguments, args[i+1:]...)
			break
		}
		if !strings.HasPrefix(arg, "-") {
			opts.GameArguments = append(opts.GameArguments, args[i:]...)
			break
		}
		switch {
		case arg == "--help" || arg == "-h":
			opts.Help = true
			return opts, nil
		case arg == "--gui":
			opts.GUI = true
		case arg == "--debug":
			opts.Debug = true
		case arg == "--verify":
			opts.Verify = true
		case arg == "--dry-run":
			opts.DryRun = true
		case arg == "--quick":
			opts.Quick = true
		case arg == "--full":
			opts.Full = true
		case arg == "--status":
			opts.Status = true
		case strings.HasPrefix(arg, "--game="):
			opts.GameSlug = strings.TrimPrefix(arg, "--game=")
			if opts.GameSlug == "" {
				return opts, errors.New("--game requires a non-empty slug")
			}
		case strings.HasPrefix(arg, "--client-dir="):
			opts.ClientDir = strings.TrimPrefix(arg, "--client-dir=")
			if opts.ClientDir == "" {
				return opts, errors.New("--client-dir requires a non-empty path")
			}
		case arg == "--list-addons":
			opts.ListAddons = true
		case arg == "--check-addons":
			opts.CheckAddons = true
		case arg == "--select-addons":
			opts.SelectAddons = true
		case arg == "--remove-addons":
			opts.RemoveAddons = true
		case strings.HasPrefix(arg, "--remove-addons="):
			opts.RemoveAddons = true
			opts.RemoveNames = strings.TrimPrefix(arg, "--remove-addons=")
		case strings.HasPrefix(arg, "--addons="):
			opts.Addons = strings.TrimPrefix(arg, "--addons=")
			if opts.Addons == "" {
				return opts, errors.New("--addons requires a comma-separated list")
			}
		case strings.HasPrefix(arg, "--language="):
			opts.Language = strings.TrimPrefix(arg, "--language=")
			if opts.Language == "" {
				return opts, errors.New("--language requires a locale code")
			}
		case arg == "--hd-characters":
			opts.HDCharacters = true
		case arg == "--hd-creatures":
			opts.HDCreatures = true
		case strings.HasPrefix(arg, "--option="):
			slug := strings.TrimPrefix(arg, "--option=")
			if slug == "" {
				return opts, errors.New("--option requires a manifest option slug")
			}
			opts.OptionSlugs = append(opts.OptionSlugs, slug)
		case arg == "--quiet":
			opts.Quiet = true
		case arg == "--relogin":
			opts.Relogin = true
		case arg == "--forget-login":
			opts.ForgetLogin = true
		default:
			return opts, fmt.Errorf("unknown launcher option %q; use -- before game arguments", arg)
		}
	}
	if err := validateCLIOptions(opts); err != nil {
		return opts, err
	}
	return opts, nil
}

func validateCLIOptions(o CLIOptions) error {
	if o.Quick && o.Full {
		return errors.New("--quick and --full cannot be used together")
	}
	if o.Verify && o.DryRun {
		return errors.New("--verify and --dry-run cannot be used together")
	}
	if o.SelectAddons && o.Addons != "" {
		return errors.New("--select-addons and --addons cannot be used together")
	}
	if o.Verify && (o.SelectAddons || o.Addons != "") {
		return errors.New("--verify cannot be combined with addon installation")
	}
	if o.Status && (o.Verify || o.DryRun || o.Quick || o.Full || o.ListAddons || o.CheckAddons || o.SelectAddons || o.RemoveAddons || o.Addons != "" || len(o.GameArguments) > 0) {
		return errors.New("--status cannot be combined with update, addon, or game-launch options")
	}
	if (o.ListAddons || o.CheckAddons) && (o.Status || o.Verify || o.DryRun || o.Quick || o.Full || o.ListAddons && o.CheckAddons || o.SelectAddons || o.RemoveAddons || o.Addons != "" || len(o.GameArguments) > 0) {
		return errors.New("addon report modes cannot be combined with update, addon, or game-launch options")
	}
	if o.RemoveAddons && (o.Status || o.Verify || o.DryRun || o.Quick || o.Full || o.ListAddons || o.CheckAddons || o.SelectAddons || o.Addons != "" || len(o.GameArguments) > 0) {
		return errors.New("addon removal cannot be combined with update, addon, or game-launch options")
	}
	if o.ForgetLogin && (o.Relogin || o.Verify || o.DryRun || o.Status || o.Quick || o.Full || o.ListAddons || o.CheckAddons || o.SelectAddons || o.RemoveAddons || o.Addons != "" || len(o.GameArguments) > 0) {
		return errors.New("--forget-login cannot be combined with another mode or game arguments")
	}
	if o.Relogin && (o.ForgetLogin || o.Verify || o.DryRun) {
		return errors.New("--relogin cannot be combined with --forget-login, --verify, or --dry-run")
	}
	if o.GUI && (o.Help || o.Debug || o.Verify || o.DryRun || o.Quick || o.Full || o.Status || o.GameSlug != "" || o.ClientDir != "" || o.ListAddons || o.CheckAddons || o.SelectAddons || o.RemoveAddons || o.Addons != "" || o.Quiet || o.Relogin || o.ForgetLogin || len(o.GameArguments) > 0) {
		return errors.New("--gui must be used by itself")
	}
	if o.Language != "" || o.HDCharacters || o.HDCreatures || len(o.OptionSlugs) > 0 {
		if o.Status || o.ListAddons || o.CheckAddons || o.RemoveAddons || o.ForgetLogin || o.GUI || o.Full {
			return errors.New("game options require an update or verification without --full")
		}
	}
	return nil
}

func WriteHelp(out io.Writer, program string) {
	fmt.Fprintf(out, `Usage: %s [OPTIONS] [--] [game arguments]

Options:
  --gui             Open the desktop launcher
  --debug           Enable verbose diagnostics
  --verify          Check manifest files without changing client files or login state
  --dry-run         Preview files that would be downloaded without changing state
  --quick           Standalone check of required game patch files (never limits an update before launch)
  --full            Include optional common and game files
  --status          Show realm status and exit
  --game=SLUG       Select a manifest game (default: roguelike-prod)
  --client-dir=PATH  Use a game client folder different from the launcher folder
  --list-addons     List the official add-on catalog and exit
  --check-addons    Check launcher-managed add-ons for updates
  --select-addons   Choose add-ons to install interactively
  --remove-addons   Choose launcher-managed add-ons to remove
  --remove-addons=LIST  Remove comma-separated add-on names or IDs
  --language=LOCALE  Select a game language (enUS, frFR, esES, deDE)
  --hd-characters    Enable manifest-provided character HD files
  --hd-creatures     Enable manifest-provided creature HD files
  --option=SLUG      Select an exact manifest option slug
  --quiet            Suppress routine output; warnings and errors remain
  --relogin         Ignore cached login and request fresh credentials
  --forget-login    Remove this installation's token and saved login locally
  --help            Show this help

Put -- before the game-manager command. Example:
  %s --quiet -- %%command%%
`, program, program)
}

type Console struct {
	In        io.Reader
	Out       io.Writer
	Err       io.Writer
	Terminal  bool
	Graphical bool
	reader    *bufio.Reader
}

func NewConsole(in io.Reader, out, errOut io.Writer, terminal, graphical bool) *Console {
	return &Console{In: in, Out: out, Err: errOut, Terminal: terminal, Graphical: graphical, reader: bufio.NewReader(in)}
}

func (c *Console) PromptLogin(ctx context.Context, reason string, allowRemember bool) (string, string, bool, error) {
	if !c.Terminal && c.Graphical {
		return c.promptZenity(ctx, reason, allowRemember)
	}
	if !c.Terminal {
		return "", "", false, errors.New("no interactive sign-in is available; run the launcher once from a terminal or desktop")
	}
	if reason != "" {
		fmt.Fprintln(c.Err, reason)
	}
	fmt.Fprint(c.Err, "Ebonhold username: ")
	username, err := c.readLine()
	if err != nil || strings.TrimSpace(username) == "" {
		return "", "", false, errors.New("username was not provided")
	}
	fmt.Fprint(c.Err, "Ebonhold password: ")
	var password string
	if file, ok := c.In.(*os.File); ok && term.IsTerminal(int(file.Fd())) {
		secret, err := term.ReadPassword(int(file.Fd()))
		fmt.Fprintln(c.Err)
		if err != nil {
			return "", "", false, errors.New("could not read password")
		}
		password = string(secret)
	} else {
		password, err = c.readLine()
		if err != nil {
			return "", "", false, errors.New("could not read password")
		}
	}
	if password == "" {
		return "", "", false, errors.New("password was not provided")
	}
	remember := false
	if allowRemember {
		fmt.Fprint(c.Err, "Remember this login unencrypted on this computer? [y/N] ")
		answer, _ := c.readLine()
		remember = strings.EqualFold(answer, "y") || strings.EqualFold(answer, "yes")
	}
	return username, password, remember, nil
}

func (c *Console) promptZenity(ctx context.Context, reason string, allowRemember bool) (string, string, bool, error) {
	if _, err := exec.LookPath("zenity"); err != nil {
		return "", "", false, errors.New("graphical sign-in needs Zenity; install it or run the launcher from a terminal")
	}
	message := "Sign in to Project Ebonhold"
	if reason != "" {
		message += "\n" + reason
	}
	username, err := exec.CommandContext(ctx, "zenity", "--entry", "--title=Ebonhold Login", "--text="+message).Output()
	if err != nil {
		return "", "", false, errors.New("sign-in was cancelled")
	}
	password, err := exec.CommandContext(ctx, "zenity", "--password", "--title=Ebonhold Login", "--text=Enter your password").Output()
	if err != nil {
		return "", "", false, errors.New("sign-in was cancelled")
	}
	remember := false
	if allowRemember {
		result, err := exec.CommandContext(ctx, "zenity", "--list", "--checklist", "--title=Ebonhold Login", "--text=Remember me stores your username and password unencrypted in a private file. Other programs running as you can read it.", "--column=Remember", "--column=Choice", "--separator=|", "FALSE", "Remember me on this computer").Output()
		remember = err == nil && len(strings.TrimSpace(string(result))) > 0
	}
	if strings.TrimSpace(string(username)) == "" || len(password) == 0 {
		return "", "", false, errors.New("username and password are required")
	}
	return strings.TrimSpace(string(username)), strings.TrimRight(string(password), "\r\n"), remember, nil
}

func (c *Console) readLine() (string, error) {
	line, err := c.reader.ReadString('\n')
	if err != nil && len(line) == 0 {
		return "", err
	}
	return strings.TrimRight(line, "\r\n"), nil
}

func RunCLI(ctx context.Context, api API, root string, args []string, console *Console) error {
	opts, err := ParseArgs(args)
	if err != nil {
		return err
	}
	if opts.Help {
		WriteHelp(console.Out, "ebonhold-launcher")
		return nil
	}
	if opts.GUI {
		return errors.New("desktop UI is not available in CLI execution")
	}
	if opts.ClientDir != "" {
		clientRoot, err := filepath.Abs(opts.ClientDir)
		if err != nil {
			return errors.New("could not resolve the client folder")
		}
		info, err := os.Lstat(clientRoot)
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return errors.New("client folder must be an existing, non-symlink directory")
		}
		root = clientRoot
	}
	store := &CredentialStore{Root: root}
	auth := &Authenticator{API: api, Store: store, Prompt: console, ReadOnly: opts.Verify || opts.DryRun}
	if opts.ForgetLogin {
		if err := auth.Forget(); err != nil {
			return fmt.Errorf("could not forget login: %w", err)
		}
		if !opts.Quiet {
			fmt.Fprintln(console.Out, "Forgot the cached updater token and saved local login for this installation.")
		}
		return nil
	}
	if opts.RemoveAddons {
		addons := NewAddonManager(api, root)
		requested := opts.RemoveNames
		if requested == "" {
			installed, err := addons.Installed()
			if err != nil {
				return err
			}
			if len(installed) == 0 {
				fmt.Fprintln(console.Out, "No addons installed via the launcher are recorded.")
				return nil
			}
			requested, err = console.selectAddonNames(ctx, installed, "Select addons to remove")
			if err != nil || requested == "" {
				return err
			}
		}
		lines, err := addons.Remove(requested)
		for _, line := range lines {
			fmt.Fprintln(console.Out, line)
		}
		return err
	}

	if opts.GameSlug == "" {
		opts.GameSlug = "roguelike-prod"
	}
	var prefs Preferences
	if !opts.Status && !opts.ListAddons && !opts.CheckAddons {
		if prefsStore, storeErr := NewPreferencesStore(); storeErr == nil {
			prefs, err = prefsStore.Load()
			if err != nil {
				return err
			}
			for _, saved := range [...]struct{ slug, name string }{
				{"roguelike-prod", "Production"},
				{"roguelike", "PTR"},
			} {
				if opts.GameSlug == saved.slug {
					continue
				}
				roots := Preferences{ProductionRoot: root, PTRRoot: prefs.RootForProfile(saved.slug)}
				if err := roots.ValidateRoots(); err != nil {
					return fmt.Errorf("client folder conflicts with the saved %s folder; use --game=%s with that folder or choose a separate --client-dir: %w", saved.name, saved.slug, err)
				}
			}
		}
	}

	token, manifest, err := auth.Authenticate(ctx, opts.Relogin)
	if err != nil {
		return err
	}
	if auth.Warning != "" {
		fmt.Fprintf(console.Err, "[WARN] %s\n", auth.Warning)
		auth.Warning = ""
	}
	status, statusErr := api.Status(ctx, token)
	if statusErr != nil {
		if opts.Status {
			return fmt.Errorf("could not retrieve server status: %w", statusErr)
		}
		fmt.Fprintf(console.Err, "[WARN] Could not retrieve server status: %v\n", statusErr)
	} else if status.Online {
		if !opts.Quiet {
			name := status.ServerName
			if name == "" {
				name = "unknown"
			}
			fmt.Fprintf(console.Out, "[INFO] Server %s is online.\n", name)
		}
	} else {
		fmt.Fprintf(console.Err, "[WARN] Server %s appears offline; updates may still work.\n", status.ServerName)
	}
	if opts.Status {
		return nil
	}
	game, err := selectGame(manifest, opts.GameSlug)
	if err != nil {
		return err
	}
	if opts.Debug {
		fmt.Fprintf(console.Err, "[DEBUG] installation=%q game=%q\n", root, game.Slug)
	}

	addons := NewAddonManager(api, root)
	if opts.ListAddons {
		catalog, err := addons.Catalog(ctx, token)
		if err != nil {
			return err
		}
		if len(catalog) == 0 {
			fmt.Fprintln(console.Out, "No addons are currently available.")
			return nil
		}
		fmt.Fprintln(console.Out, "Available addons:")
		for _, addon := range catalog {
			fmt.Fprintf(console.Out, "  [%d] %s\n      %s\n      %v bytes, updated %s\n", addon.ID, addon.Name, addon.Description, addon.FileSizeBytes, addon.UpdatedAt)
		}
		return nil
	}
	if opts.CheckAddons {
		states, err := addons.Check(ctx, token)
		if err != nil {
			return err
		}
		for _, state := range states {
			fmt.Fprintf(console.Out, "[%s] %s", state.State, state.Addon.Name)
			if state.Detail != "" {
				fmt.Fprintf(console.Out, " — %s", state.Detail)
			}
			fmt.Fprintln(console.Out)
		}
		return nil
	}

	var addonIDs []int64
	if opts.SelectAddons || opts.Addons != "" {
		catalog, err := addons.Catalog(ctx, token)
		if err != nil {
			return err
		}
		requested := opts.Addons
		if opts.SelectAddons {
			requested, err = console.selectCatalogAddons(ctx, catalog)
			if err != nil || requested == "" {
				return err
			}
		}
		addonIDs, err = addons.Resolve(catalog, requested)
		if err != nil {
			return err
		}
	}

	updater := NewUpdater(api, auth, root)
	updateOptions := UpdateOptions{GameSlug: game.Slug, Quick: opts.Quick && len(opts.GameArguments) == 0, Full: opts.Full, Verify: opts.Verify, DryRun: opts.DryRun, Quiet: opts.Quiet, Language: opts.Language, HDCharacters: opts.HDCharacters, HDCreatures: opts.HDCreatures, OptionSlugs: opts.OptionSlugs}
	if len(opts.OptionSlugs) == 0 && !updateOptions.Quick && !opts.Full {
		if configuredRoot := prefs.RootForProfile(game.Slug); configuredRoot != "" {
			actual, actualErr := resolvedRoot(root)
			configured, configuredErr := resolvedRoot(configuredRoot)
			if actualErr == nil && configuredErr == nil && actual == configured {
				settings := prefs.GameSettingsForProfile(game.Slug)
				if updateOptions.Language == "" {
					updateOptions.Language = settings.Language
				}
				updateOptions.ManageHD = settings.ManageHD
				updateOptions.HDCharacters = updateOptions.HDCharacters || settings.HDCharacters
				updateOptions.HDCreatures = updateOptions.HDCreatures || settings.HDCreatures
			}
		}
	}
	result, err := updater.Run(ctx, manifest, token, updateOptions, func(progress Progress) {
		if opts.Quiet {
			return
		}
		if progress.Total > 0 {
			fmt.Fprintf(console.Out, "[%s] %d/%d %s\n", strings.ToUpper(progress.Stage), progress.Current, progress.Total, progress.Path)
		} else if progress.Message != "" {
			fmt.Fprintf(console.Out, "[%s] %s\n", strings.ToUpper(progress.Stage), progress.Message)
		}
	})
	if auth.Warning != "" {
		fmt.Fprintf(console.Err, "[WARN] %s\n", auth.Warning)
	}
	if err != nil {
		return err
	}
	token = result.Token
	if opts.DryRun && !opts.Quiet {
		if len(result.PlannedPaths) == 0 {
			fmt.Fprintln(console.Out, "[DRY RUN] All selected files are current.")
		} else {
			fmt.Fprintln(console.Out, "[DRY RUN] Would download:")
			for _, path := range result.PlannedPaths {
				fmt.Fprintln(console.Out, "  "+path)
			}
		}
	}
	if len(addonIDs) > 0 {
		if opts.DryRun {
			if !opts.Quiet {
				fmt.Fprintf(console.Out, "[DRY RUN] Would install %d selected addons.\n", len(addonIDs))
			}
		} else if !opts.Verify {
			if err := addons.Install(ctx, token, addonIDs); err != nil {
				return err
			}
		}
	}

	if len(opts.GameArguments) == 0 && console.Terminal && !opts.Quiet && !opts.DryRun && !opts.Verify && opts.Addons == "" && !opts.SelectAddons {
		states, err := addons.Check(ctx, token)
		if err == nil {
			var available []AddonStatus
			for _, state := range states {
				if state.State == "UPDATE AVAILABLE" {
					available = append(available, state)
					fmt.Fprintf(console.Out, "[UPDATE AVAILABLE] %s\n", state.Addon.Name)
				}
			}
			if len(available) > 0 {
				requested, err := console.selectAddonNames(ctx, available, "Enter add-on names or IDs to update, comma-separated (blank cancels):")
				if err != nil {
					return err
				}
				if requested != "" {
					ids, err := addons.Resolve(statesToAddons(available), requested)
					if err != nil {
						return err
					}
					if err := addons.Install(ctx, token, ids); err != nil {
						return err
					}
				}
			}
		}
	}
	if len(opts.GameArguments) > 0 && !opts.Verify && !opts.DryRun {
		return runGame(ctx, opts.GameArguments, console)
	}
	return nil
}

func selectGame(manifest Manifest, slug string) (Game, error) {
	for _, game := range manifest.Data.Games {
		if game.Slug == slug {
			return game, nil
		}
	}
	available := make([]string, 0, len(manifest.Data.Games))
	for _, game := range manifest.Data.Games {
		available = append(available, game.Slug)
	}
	return Game{}, fmt.Errorf("game slug %q not found in manifest; available: %s", slug, strings.Join(available, ", "))
}

func statesToAddons(states []AddonStatus) []Addon {
	addons := make([]Addon, len(states))
	for i, state := range states {
		addons[i] = state.Addon
	}
	return addons
}

func (c *Console) selectCatalogAddons(ctx context.Context, catalog []Addon) (string, error) {
	if !c.Terminal && c.Graphical {
		if _, err := exec.LookPath("zenity"); err == nil {
			args := []string{"--list", "--checklist", "--title=Ebonhold Add-ons", "--text=Select add-ons to install", "--column=Install", "--column=Name", "--column=Description", "--separator=,"}
			for _, addon := range catalog {
				args = append(args, "FALSE", strconv.FormatInt(addon.ID, 10), addon.Name+": "+addon.Description)
			}
			out, err := exec.CommandContext(ctx, "zenity", args...).Output()
			return strings.TrimSpace(string(out)), err
		}
	}
	if !c.Terminal {
		return "", errors.New("add-on selection needs a terminal or Zenity")
	}
	for _, addon := range catalog {
		fmt.Fprintf(c.Out, "  [%d] %s — %s\n", addon.ID, addon.Name, addon.Description)
	}
	fmt.Fprint(c.Err, "Add-on names or IDs, comma-separated (blank cancels): ")
	return c.readLine()
}

func (c *Console) selectAddonNames(ctx context.Context, states []AddonStatus, prompt string) (string, error) {
	if !c.Terminal && c.Graphical {
		if _, err := exec.LookPath("zenity"); err == nil {
			args := []string{"--list", "--checklist", "--title=Ebonhold Add-ons", "--text=" + prompt, "--column=Select", "--column=Name", "--separator=,"}
			for _, state := range states {
				args = append(args, "FALSE", state.Addon.Name)
			}
			out, err := exec.CommandContext(ctx, "zenity", args...).Output()
			return strings.TrimSpace(string(out)), err
		}
	}
	if !c.Terminal {
		return "", errors.New("add-on selection needs a terminal or Zenity")
	}
	for _, state := range states {
		fmt.Fprintf(c.Out, "  [%d] %s\n", state.Addon.ID, state.Addon.Name)
	}
	fmt.Fprint(c.Err, prompt+" ")
	return c.readLine()
}

func runGame(ctx context.Context, args []string, console *Console) error {
	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	cmd.Stdin = console.In
	cmd.Stdout = console.Out
	cmd.Stderr = console.Err
	cmd.Env = gameEnvironment(cmd.Environ())
	if err := cmd.Run(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return exitErr
		}
		return fmt.Errorf("could not launch game command: %w", err)
	}
	return nil
}
