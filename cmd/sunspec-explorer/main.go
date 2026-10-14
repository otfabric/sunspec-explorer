// SPDX-License-Identifier: MIT

// Command sunspec-explorer is the OT Fabric SunSpec Explorer desktop
// application: a read-only tool to discover, browse, poll and export the
// models of a SunSpec device.
//
// The engine runs in this process (Local mode); there is no other mode. To
// reach a device behind a gateway, run sunspec-explorerd there and open its
// web UI in a browser through an SSH port forward.
package main

import (
	_ "embed"
	"flag"
	"fmt"
	"log/slog"
	"os"

	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/otfabric/sunspec-explorer/frontend"
	"github.com/otfabric/sunspec-explorer/internal/buildinfo"
	"github.com/otfabric/sunspec-explorer/internal/daemon"
	"github.com/otfabric/sunspec-explorer/internal/engine"
	"github.com/otfabric/sunspec-explorer/internal/gui"
)

const appTitle = "SunSpec Explorer"

// appIcon is the SunSpec icon of the OT Fabric website (docs/BRANDING.md,
// section 4): each OT Fabric GUI carries the icon of its protocol. It is the
// window and dock icon; the macOS bundle derives its .icns from the same
// file.
//
//go:embed appicon.png
var appIcon []byte

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "sunspec-explorer:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	flags := flag.NewFlagSet("sunspec-explorer", flag.ContinueOnError)
	version := flags.Bool("version", false, "print the version and exit")
	debug := flags.Bool("debug", false, "log at debug level")
	dataDir := flags.String("data-dir", daemon.DefaultDataDir(),
		"directory for recordings; the default is temporary storage")
	tlsCert := flags.String("tls-cert", "", "certificate (PEM) presented to tcp+tls:// devices")
	tlsKey := flags.String("tls-key", "", "private key (PEM) of --tls-cert")
	tlsCA := flags.String("tls-ca", "", "CA or device certificates (PEM) a tcp+tls:// device must present")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *version {
		fmt.Printf("sunspec-explorer %s (go-sunspec %s)\n", buildinfo.Version, buildinfo.GoSunspecVersion())
		return nil
	}

	assets := frontend.Wails()
	if !frontend.Built(assets) {
		return fmt.Errorf("this binary was built without the user interface: run \"make frontend\" and build again")
	}

	level := slog.LevelInfo
	if *debug {
		level = slog.LevelDebug
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level}))

	// Without a usable data directory the application still explores; it
	// only cannot record.
	// The desktop application runs as its user and may open any serial
	// device that user may.
	opts := engine.Options{Logger: log, SerialPorts: daemon.LocalSerialPorts}
	if *tlsCert != "" || *tlsKey != "" || *tlsCA != "" {
		tlsConfig, err := engine.LoadTLS(*tlsCert, *tlsKey, *tlsCA)
		if err != nil {
			return err
		}
		opts.TLS = tlsConfig
	}
	if err := daemon.EnsureDataDir(*dataDir); err != nil {
		log.Warn("recording is not available", "error", err)
	} else {
		opts.DataDir, opts.DataDirTemporary = *dataDir, daemon.IsTemporary(*dataDir)
	}
	eng := engine.New(opts)
	defer eng.Close()

	app := application.New(application.Options{
		Name:        appTitle,
		Description: "Read-only explorer for SunSpec devices",
		Icon:        appIcon,
		Logger:      log,
		Services: []application.Service{
			application.NewServiceWithOptions(gui.New(eng),
				application.ServiceOptions{MarshalError: gui.MarshalError}),
		},
		Assets: application.AssetOptions{
			Handler: application.BundledAssetFileServer(assets),
		},
		Mac: application.MacOptions{
			ApplicationShouldTerminateAfterLastWindowClosed: true,
		},
	})
	app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title:     appTitle,
		Width:     1360,
		Height:    860,
		MinWidth:  960,
		MinHeight: 600,
		URL:       "/",
		// --ot-bg of the dark theme, so the window does not flash white.
		BackgroundColour: application.NewRGB(0x11, 0x18, 0x1f),
	})
	return app.Run()
}
