// SPDX-License-Identifier: MIT

// Command sunspec-explorerd is the headless daemon of the OT Fabric SunSpec
// Explorer: it runs the read-only engine on a gateway and serves the HTTP
// API and the web UI to a browser.
//
// Remote use needs nothing installed on the workstation:
//
//	gateway$      sunspec-explorerd
//	workstation$  ssh -N -L 8765:127.0.0.1:8765 user@gateway
//	browser       the URL printed by "sunspec-explorerd url"
//
// The daemon is a static binary without cgo and must never import Wails.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/otfabric/sunspec-explorer/frontend"
	"github.com/otfabric/sunspec-explorer/internal/api"
	"github.com/otfabric/sunspec-explorer/internal/buildinfo"
	"github.com/otfabric/sunspec-explorer/internal/daemon"
	"github.com/otfabric/sunspec-explorer/internal/engine"
	"github.com/otfabric/sunspec-explorer/internal/web"
)

const usage = `sunspec-explorerd runs the SunSpec Explorer engine and serves its web UI.

Usage:
  sunspec-explorerd [flags]        run the daemon
  sunspec-explorerd url [flags]    print the URL of the running daemon, token included
  sunspec-explorerd token rotate [flags]
                                   replace the token; the old one stops working
  sunspec-explorerd version        print the version

Remote use: run the daemon on the gateway, forward its port from your
workstation with "ssh -N -L 8765:127.0.0.1:8765 user@gateway", and open the
URL that "sunspec-explorerd url" prints in a browser.

Flags of the daemon:
`

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	os.Exit(run(ctx, os.Args[1:], os.Stdout, os.Stderr))
}

// run is main without the process: it returns the exit status.
func run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	var err error
	switch {
	case len(args) > 0 && args[0] == "url":
		err = runURL(args[1:], stdout, stderr)
	case len(args) > 1 && args[0] == "token" && args[1] == "rotate":
		err = runRotate(args[2:], stdout, stderr)
	case len(args) > 0 && args[0] == "token":
		err = errors.New(`unknown token command: use "token rotate"`)
	case len(args) > 0 && args[0] == "version":
		_, _ = fmt.Fprintf(stdout, "sunspec-explorerd %s (go-sunspec %s)\n", buildinfo.Version, buildinfo.GoSunspecVersion())
	default:
		err = runServe(ctx, args, stderr, nil)
	}
	switch {
	case err == nil:
		return 0
	case errors.Is(err, flag.ErrHelp):
		return 0
	}
	_, _ = fmt.Fprintln(stderr, "sunspec-explorerd:", err)
	return 1
}

// paths are the flags every command shares: where the daemon keeps its
// files.
type paths struct {
	dataDir   string
	tokenFile string
}

func (p *paths) register(flags *flag.FlagSet) {
	flags.StringVar(&p.dataDir, "data-dir", daemon.DefaultDataDir(),
		"directory for the token, state and recordings; the default is $"+daemon.DataDirEnv+" or temporary storage")
	flags.StringVar(&p.tokenFile, "token-file", "", "file that holds the bearer token (default <data-dir>/token)")
}

func (p *paths) token() string {
	if p.tokenFile != "" {
		return p.tokenFile
	}
	return filepath.Join(p.dataDir, daemon.TokenFileName)
}

type repeated []string

func (r *repeated) String() string     { return fmt.Sprint([]string(*r)) }
func (r *repeated) Set(v string) error { *r = append(*r, v); return nil }

// runServe runs the daemon until ctx ends. ready, when not nil, receives the
// address the daemon bound.
func runServe(ctx context.Context, args []string, stderr io.Writer, ready chan<- string) error {
	flags := flag.NewFlagSet("sunspec-explorerd", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.Usage = func() {
		_, _ = fmt.Fprint(stderr, usage)
		flags.PrintDefaults()
	}
	var (
		p            paths
		allowTargets repeated
		allowHosts   repeated
	)
	p.register(flags)
	listen := flags.String("listen", daemon.DefaultListen, "address to listen on; loopback unless --allow-remote-bind")
	allowRemote := flags.Bool("allow-remote-bind", false,
		"allow --listen on an address other than loopback (unencrypted; the token and the Host check still apply)")
	flags.Var(&allowHosts, "allow-host", "additional host name the daemon answers to (repeatable; for --allow-remote-bind)")
	flags.Var(&allowTargets, "allow-target",
		"CIDR or address of devices the daemon may connect to (repeatable; default: private and link-local ranges)")
	maxSessions := flags.Int("max-sessions", engine.DefaultMaxSessions, "maximum number of open sessions")
	maxRecording := flags.Int64("max-recording-mb", engine.DefaultMaxRecordingBytes>>20, "size at which a recording is stopped, in MiB")
	maxRecordings := flags.Int64("max-recordings-mb", engine.DefaultMaxRecordingsBytes>>20, "size of all recordings together at which recording stops, in MiB")
	rotate := flags.Int64("rotate-recording-mb", engine.DefaultRotateBytes>>20, "size at which a recording continues in a new file, in MiB")
	noUI := flags.Bool("no-ui", false, "serve the API only, not the web UI")
	debug := flags.Bool("debug", false, "log at debug level")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() > 0 {
		return fmt.Errorf("unexpected argument %q (see --help)", flags.Arg(0))
	}

	level := slog.LevelInfo
	if *debug {
		level = slog.LevelDebug
	}
	log := slog.New(slog.NewTextHandler(stderr, &slog.HandlerOptions{Level: level}))

	warning, err := daemon.CheckListen(*listen, *allowRemote)
	if err != nil {
		return err
	}
	if err := daemon.EnsureDataDir(p.dataDir); err != nil {
		return err
	}
	tokens, created, err := daemon.NewTokenSource(p.token())
	if err != nil {
		return err
	}
	allow, err := daemon.NewTargetAllowList(allowTargets)
	if err != nil {
		return err
	}

	if *maxRecording < 1 || *maxRecordings < 1 || *rotate < 1 {
		return errors.New("recording sizes must be at least 1 MiB")
	}
	eng := engine.New(engine.Options{
		Logger: log, MaxSessions: *maxSessions, AllowTarget: allow.Allow,
		DataDir: p.dataDir, DataDirTemporary: daemon.IsTemporary(p.dataDir),
		Recording: engine.RecordingLimits{
			MaxBytes: *maxRecording << 20, MaxTotalBytes: *maxRecordings << 20, RotateBytes: *rotate << 20,
		},
	})
	defer eng.Close()

	var ui http.Handler
	switch assets := frontend.Web(); {
	case *noUI:
	case !frontend.Built(assets):
		log.Warn("this binary was built without the web UI: serving the API only")
	default:
		ui = web.Handler(assets)
	}

	listener, err := net.Listen("tcp", *listen)
	if err != nil {
		return err
	}
	bound := listener.Addr().String()
	if err := daemon.WriteState(p.dataDir, daemon.State{Listen: bound, PID: os.Getpid(), StartedAt: time.Now().UTC()}); err != nil {
		_ = listener.Close()
		return fmt.Errorf("state file: %w", err)
	}
	defer daemon.RemoveState(p.dataDir)

	// Requests end with the daemon: an event stream would otherwise keep the
	// shutdown waiting.
	reqCtx, cancelRequests := context.WithCancel(context.Background())
	defer cancelRequests()
	srv := &http.Server{
		Handler: api.New(eng, api.Options{
			Token:  tokens.Token,
			Hosts:  daemon.Hosts(bound, allowHosts),
			UI:     ui,
			Logger: log,
		}),
		ReadHeaderTimeout: 10 * time.Second,
		BaseContext:       func(net.Listener) context.Context { return reqCtx },
		ErrorLog:          slog.NewLogLogger(log.Handler(), slog.LevelWarn),
	}

	log.Info("sunspec-explorerd started", "version", buildinfo.Version, "listen", bound,
		"data_dir", p.dataDir, "new_token", created, "ui", ui != nil)
	if warning != "" {
		log.Warn(warning)
	}
	// The one place the token is shown at startup. It is printed, not logged
	// through the logger, and nowhere else; "sunspec-explorerd url" shows it
	// again.
	hostname, _ := os.Hostname()
	if access, err := daemon.AccessFor(bound, tokens.Token(), hostname); err == nil {
		_, _ = fmt.Fprintf(stderr, "Open %s\n", access.URL)
		if access.SSH != "" {
			_, _ = fmt.Fprintf(stderr, "From a workstation, first run: %s\n", access.SSH)
		}
	}
	if ready != nil {
		ready <- bound
	}

	served := make(chan error, 1)
	go func() { served <- srv.Serve(listener) }()
	select {
	case err := <-served:
		return err
	case <-ctx.Done():
	}
	log.Info("shutting down")
	cancelRequests()
	shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdown); err != nil {
		_ = srv.Close()
	}
	return nil
}

// runURL prints how to reach the running daemon. The URL goes to standard
// output, alone on its line, so that it can be captured; everything else
// goes to standard error.
func runURL(args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("sunspec-explorerd url", flag.ContinueOnError)
	flags.SetOutput(stderr)
	var p paths
	p.register(flags)
	if err := flags.Parse(args); err != nil {
		return err
	}
	st, err := daemon.ReadState(p.dataDir)
	if err != nil {
		return err
	}
	conn, err := net.DialTimeout("tcp", st.Listen, time.Second)
	if err != nil {
		return fmt.Errorf("no daemon answers on %s (the state file in %s is left over from an earlier run)", st.Listen, p.dataDir)
	}
	_ = conn.Close()
	token, err := daemon.ReadToken(p.token())
	if err != nil {
		return err
	}
	hostname, _ := os.Hostname()
	access, err := daemon.AccessFor(st.Listen, token, hostname)
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintln(stdout, access.URL)
	if access.SSH != "" {
		_, _ = fmt.Fprintf(stderr, "From a workstation, first run: %s\n", access.SSH)
	}
	_, _ = fmt.Fprintln(stderr, "The part after # is the access token: treat this URL like a password.")
	return nil
}

// runRotate replaces the token. A running daemon follows within a second.
func runRotate(args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("sunspec-explorerd token rotate", flag.ContinueOnError)
	flags.SetOutput(stderr)
	var p paths
	p.register(flags)
	if err := flags.Parse(args); err != nil {
		return err
	}
	if err := daemon.EnsureDataDir(p.dataDir); err != nil {
		return err
	}
	if _, err := daemon.RotateToken(p.token()); err != nil {
		return err
	}
	_, _ = fmt.Fprintln(stdout, `Token rotated: the old token no longer works. Run "sunspec-explorerd url" for the new URL.`)
	return nil
}
