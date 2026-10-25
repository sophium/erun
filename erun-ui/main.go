package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-ui/headlessserver"
	"github.com/sophium/erun/erun-ui/internal/terminal"
	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/mac"
)

// defaultHeadlessPort avoids 34115, which `wails dev` claims.
const defaultHeadlessPort = 34123

// Keep in sync with the flags parseHeadlessFlags recognizes.
const cliUsage = `Usage: erun-app [flags]

erun-app is the ERun desktop application. Run with no flags to launch it.

Flags:
  -h, --help          Show this help message and exit.
      --headless      Run without a native window, serving the UI over
                       HTTP+SSE instead (see erun-ui/playwright/AGENTS.md).
      --port <n>      Port for --headless mode to prefer (default 34123). A
                      port already in use falls back to an OS-assigned one;
                      the address it actually serves on is announced on
                      startup.
`

func main() {
	// Must run first: help and unknown flags must have no side effects at all.
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
	if !launch(parsed) {
		os.Exit(1)
	}
}

// A failure is logged here while the durable log is still open.
func launch(parsed parsedCLIFlags) bool {
	setAppIdentity("ERun")
	defer initDurableAppLog()()

	headless, port := parsed.Headless, parsed.Port
	// Wails scans os.Args directly and would reject our flags.
	os.Args = append([]string{os.Args[0]}, parsed.Leftover...)

	app := NewApp(desktopDeps(headless))

	if headless {
		if err := runHeadless(app, port); err != nil {
			log.Print(err)
			return false
		}
		return true
	}

	runWails(app)
	return true
}

func desktopDeps(headless bool) erunUIDeps {
	deps := erunUIDeps{
		store:           config.ConfigStore{},
		findProjectRoot: config.FindProjectRoot,
		resolveCLIPath:  terminal.ResolveCLIExecutable,
		windowStatePath: defaultAppWindowStatePath(),
	}
	if headless {
		// runtime.WindowIsMaximised panics without a window.
		deps.windowMaximised = func(context.Context) bool { return false }
		// The login shell's PATH would replace the stubs the Playwright harness
		// puts first on it.
		deps.skipLoginShellEnv = true
	}
	return deps
}

type parsedCLIFlags struct {
	Headless bool
	Port     int
	Leftover []string
	Help     bool
	Unknown  string
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
	app.SetEmitter(func(name string, args ...any) {
		server.Emit(name, args...)
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// wailsruntime.Quit log.Fatals without a Wails-built context, so headless quit
	// cancels this context instead.
	app.deps.quitApp = cancel

	// PTY/session code assumes a non-nil a.ctx, normally supplied by Wails.
	app.startup(ctx)

	// Bind here so the announcement names the address actually bound.
	listener, bindErr := listenHeadless(fmt.Sprintf("127.0.0.1:%d", port))
	if bindErr == nil {
		log.Printf("erun-app headless: listening on http://%s/", listener.Addr().String())
	}

	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)

	errCh := make(chan error, 1)
	go func() {
		if bindErr != nil {
			errCh <- bindErr
			return
		}
		errCh <- server.Serve(ctx, listener)
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

// listenHeadless treats --port as a preference: address-in-use falls back to
// an OS-assigned port; every other bind error stays fatal.
func listenHeadless(addr string) (net.Listener, error) {
	listener, err := net.Listen("tcp", addr)
	if err == nil {
		return listener, nil
	}
	if !errors.Is(err, syscall.EADDRINUSE) {
		return nil, err
	}
	fallback, fallbackErr := net.Listen("tcp", "127.0.0.1:0")
	if fallbackErr != nil {
		return nil, err
	}
	log.Printf("erun-app headless: %s is in use, listening on an OS-assigned port instead", addr)
	return fallback, nil
}
