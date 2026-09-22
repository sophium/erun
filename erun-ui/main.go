package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"

	eruncommon "github.com/sophium/erun/erun-common"
	"github.com/sophium/erun/erun-ui/headlessserver"
	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/mac"
)

// defaultHeadlessPort stays clear of 34115, which `wails dev` claims for its
// asset server, so the headless build and `wails dev` can run side-by-side.
const defaultHeadlessPort = 34123

// cliUsage is printed for --help/-h and ahead of an unrecognized-flag error.
// Keep it in sync with the flags parseHeadlessFlags actually recognizes.
const cliUsage = `Usage: erun-app [flags]

erun-app is the ERun desktop application. Run with no flags to launch it.

Flags:
  -h, --help          Show this help message and exit.
      --headless      Run without a native window, serving the UI over
                       HTTP+SSE instead (see erun-ui/playwright/AGENTS.md).
      --port <n>      Port for --headless mode to listen on (default 34123).
`

func main() {
	// This must run before any other work (app identity, durable logging,
	// Wails startup): --help/-h and an unrecognized flag must produce no
	// side effects at all, not just skip launching the app.
	parsed := parseHeadlessFlags(os.Args[1:])
	if parsed.Help {
		_, _ = fmt.Fprint(os.Stdout, cliUsage)
		return
	}
	if parsed.Unknown != "" {
		_, _ = fmt.Fprintf(os.Stderr, "erun-app: unrecognized flag %q\n\n", parsed.Unknown)
		_, _ = fmt.Fprint(os.Stderr, cliUsage)
		os.Exit(1)
	}

	setAppIdentity("ERun")
	defer initDurableAppLog()()

	headless, port := parsed.Headless, parsed.Port
	// Strip recognized flags from os.Args before Wails parses them, so the
	// Wails CLI machinery (which scans os.Args directly) does not reject
	// our additions in normal mode.
	os.Args = append([]string{os.Args[0]}, parsed.Leftover...)

	deps := erunUIDeps{
		store:           eruncommon.ConfigStore{},
		findProjectRoot: eruncommon.FindProjectRoot,
		resolveCLIPath:  resolveCLIExecutable,
		windowStatePath: defaultAppWindowStatePath(),
	}
	if headless {
		// Wails' runtime.WindowIsMaximised panics without a hosting
		// window, so the headless build supplies a stub. Window state
		// is irrelevant when there is no window to restore.
		deps.windowMaximised = func(context.Context) bool { return false }
	}
	app := NewApp(deps)

	if headless {
		if err := runHeadless(app, port); err != nil {
			log.Fatal(err)
		}
		return
	}

	runWails(app)
}

// parsedCLIFlags is the outcome of parsing erun-app's own command-line
// flags. Help and Unknown are mutually exclusive with a normal run: main
// checks them before doing anything else that has a side effect.
type parsedCLIFlags struct {
	Headless bool
	Port     int
	Leftover []string
	Help     bool
	// Unknown holds the first unrecognized flag encountered, if any.
	Unknown string
}

func parseHeadlessFlags(args []string) parsedCLIFlags {
	result := parsedCLIFlags{Port: defaultHeadlessPort, Leftover: make([]string, 0, len(args))}
	i := 0
	for i < len(args) {
		a := args[i]
		switch {
		case isHelpFlag(a):
			result.Help = true
			return result
		case matchHeadlessFlag(a, &result.Headless):
		case isPortFlag(a):
			if consumePortFlagValue(args, i, &result.Port) {
				i++
			}
		case matchPortAssignFlag(a, &result.Port):
		case strings.HasPrefix(a, "-"):
			result.Unknown = a
			return result
		default:
			result.Leftover = append(result.Leftover, a)
		}
		i++
	}
	return result
}

func isHelpFlag(arg string) bool {
	return arg == "--help" || arg == "-help" || arg == "-h"
}

func matchHeadlessFlag(arg string, headless *bool) bool {
	switch {
	case arg == "--headless" || arg == "-headless":
		*headless = true
		return true
	case strings.HasPrefix(arg, "--headless=") || strings.HasPrefix(arg, "-headless="):
		*headless = parseBoolFlag(stripFlagPrefix(arg, "headless="))
		return true
	default:
		return false
	}
}

func isPortFlag(arg string) bool {
	return arg == "--port" || arg == "-port"
}

func consumePortFlagValue(args []string, i int, port *int) bool {
	if i+1 >= len(args) {
		return false
	}
	p, err := strconv.Atoi(args[i+1])
	if err != nil || p <= 0 {
		return false
	}
	*port = p
	return true
}

func matchPortAssignFlag(arg string, port *int) bool {
	if !strings.HasPrefix(arg, "--port=") && !strings.HasPrefix(arg, "-port=") {
		return false
	}
	if p, err := strconv.Atoi(stripFlagPrefix(arg, "port=")); err == nil && p > 0 {
		*port = p
	}
	return true
}

func stripFlagPrefix(arg, name string) string {
	for _, prefix := range []string{"--" + name, "-" + name} {
		if strings.HasPrefix(arg, prefix) {
			return strings.TrimPrefix(arg, prefix)
		}
	}
	return arg
}

func parseBoolFlag(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "1", "true", "yes":
		return true
	default:
		return false
	}
}

func runWails(app *App) {
	windowStatePath := defaultAppWindowStatePath()
	windowStartState := options.Normal
	if loadAppWindowState(windowStatePath).Maximised {
		windowStartState = options.Maximised
	}
	err := wails.Run(&options.App{
		Title:            "ERun",
		Width:            1320,
		Height:           860,
		MinWidth:         960,
		MinHeight:        640,
		WindowStartState: windowStartState,
		AssetServer: &assetserver.Options{
			Assets: assets,
		},
		BackgroundColour: options.NewRGB(245, 245, 247),
		OnStartup:        app.startup,
		OnBeforeClose:    app.beforeClose,
		OnShutdown:       app.shutdown,
		Mac: &mac.Options{
			TitleBar: mac.TitleBarHiddenInset(),
		},
		Bind: []interface{}{
			app,
		},
	})
	if err != nil {
		log.Fatal(err)
	}
}

func runHeadless(app *App, port int) error {
	server := headlessserver.New(app, frontendDistFS())
	// Headless has no Wails, so the app's existing event-emit call sites reach
	// the browser through the SSE fan-out instead.
	app.SetEmitter(func(name string, args ...any) {
		server.Emit(name, args...)
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// wailsruntime.Quit requires a context Wails itself constructed (it looks
	// up a "frontend" value that is never set here) and calls log.Fatal
	// otherwise, so ConfirmWindowClose's quit — and any other caller of
	// deps.quitApp — must not reach it in headless mode. Cancelling this
	// context instead unblocks the select below the same way a SIGTERM does,
	// which server.Listen already turns into a graceful HTTP shutdown.
	app.deps.quitApp = cancel

	// The desktop PTY/session code assumes a non-nil a.ctx, which Wails would
	// normally supply, so mirror its startup/teardown lifecycle by hand here.
	app.startup(ctx)

	addr := fmt.Sprintf("127.0.0.1:%d", port)
	log.Printf("erun-app headless: listening on http://%s/", addr)

	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)

	errCh := make(chan error, 1)
	go func() {
		errCh <- server.Listen(ctx, addr)
	}()

	var listenErr error
	select {
	case <-signals:
		log.Printf("erun-app headless: shutting down")
		cancel()
		<-errCh
	case listenErr = <-errCh:
		cancel()
	}

	_ = app.beforeClose(ctx)
	app.shutdown(ctx)
	return listenErr
}
