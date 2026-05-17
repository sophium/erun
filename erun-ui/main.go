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

// defaultHeadlessPort is the listen port for `erun-app --headless` when the
// caller omits --port. 34123 is picked to stay clear of 34115, which the
// `wails dev` toolchain claims for its asset server, so a developer can run
// both side-by-side without colliding.
const defaultHeadlessPort = 34123

func main() {
	setAppIdentity("ERun")

	headless, port, leftover := parseHeadlessFlags(os.Args[1:])
	// Strip recognized flags from os.Args before Wails parses them, so the
	// Wails CLI machinery (which scans os.Args directly) does not reject
	// our additions in normal mode.
	os.Args = append([]string{os.Args[0]}, leftover...)

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

// parseHeadlessFlags strips --headless and --port from args without
// disturbing any other flags the Wails toolchain might want to see. Both long
// (`--headless`) and short (`-headless`) prefixes are accepted, as are the
// `--flag=value` and `--flag value` forms. Anything else is returned in
// leftover untouched.
func parseHeadlessFlags(args []string) (headless bool, port int, leftover []string) {
	port = defaultHeadlessPort
	leftover = make([]string, 0, len(args))
	i := 0
	for i < len(args) {
		a := args[i]
		switch {
		case a == "--headless" || a == "-headless":
			headless = true
		case strings.HasPrefix(a, "--headless=") || strings.HasPrefix(a, "-headless="):
			headless = parseBoolFlag(stripFlagPrefix(a, "headless="))
		case a == "--port" || a == "-port":
			if i+1 < len(args) {
				if p, err := strconv.Atoi(args[i+1]); err == nil && p > 0 {
					port = p
					i++
				}
			}
		case strings.HasPrefix(a, "--port=") || strings.HasPrefix(a, "-port="):
			if p, err := strconv.Atoi(stripFlagPrefix(a, "port=")); err == nil && p > 0 {
				port = p
			}
		default:
			leftover = append(leftover, a)
		}
		i++
	}
	return headless, port, leftover
}

func stripFlagPrefix(arg, name string) string {
	// Accept both --name=value and -name=value.
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
	// Replace the Wails event emitter with the SSE fan-out so the existing
	// runtime.EventsEmit call sites reach the headless browser without any
	// Wails dependency.
	app.SetEmitter(func(name string, args ...any) {
		server.Emit(name, args...)
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Match the Wails lifecycle: startup runs once with the long-lived
	// context, beforeClose + shutdown run with the same context on
	// teardown. The desktop module's PTY/session code assumes a non-nil
	// a.ctx, so we wire it through manually here.
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
