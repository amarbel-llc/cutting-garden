// Package serve wires the `serve` subcommand: run a long-lived
// LocalSend (https://github.com/localsend/protocol) receiver and turn
// every incoming transfer into a capture.
//
// Each LocalSend session — one file, several files, or a whole directory
// tree — is written into a madder blob store and folded into a single
// cutting_garden-capture_receipt-fs-v1 receipt, the same wire format the
// `capture` command produces, so `restore` and `diff` consume
// serve-produced receipts unchanged.
//
// The listener binds to the host's Tailscale address by default (so the
// receiver is reachable over the tailnet, not the public internet or the
// broadcast LAN); -bind overrides with an explicit host.
//
// Normative behavior: FDR 0011 (docs/features/0011-localsend-serve.md).
package serve

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/amarbel-llc/cutting-garden/internal/capture_log"
	"github.com/amarbel-llc/cutting-garden/internal/command"
	"github.com/amarbel-llc/cutting-garden/internal/command_components"
	"github.com/amarbel-llc/madder/go/pkgs/blob_stores"
	"github.com/amarbel-llc/purse-first/libs/dewey/pkgs/errors"
	"github.com/amarbel-llc/purse-first/libs/dewey/pkgs/interfaces"
)

// defaultPort is the LocalSend protocol's well-known port.
const defaultPort = 53317

// shutdownGrace bounds how long an in-flight transfer has to drain when
// the context is cancelled before the listener is force-closed.
const shutdownGrace = 5 * time.Second

// Serve is the value registered for the `serve` subcommand. Fields are
// bound to CLI flags by SetFlagDefinitions; a pointer receiver on Run is
// required so parsed flag values reach dispatch.
type Serve struct {
	Bind  string
	Port  int
	Store string
	Alias string
}

var (
	_ command.Cmd                       = (*Serve)(nil)
	_ interfaces.CommandComponentWriter = (*Serve)(nil)
)

// New constructs a Serve with flag defaults.
func New() *Serve {
	return &Serve{Port: defaultPort}
}

func (*Serve) GetDescription() command.Description {
	return command.Description{
		Short: "receive files over the LocalSend protocol as captures",
		Long: "Runs a long-lived LocalSend receiver bound to the host's " +
			"Tailscale address. Every incoming transfer \\(em a single " +
			"file, several files, or a whole directory tree \\(em is " +
			"written into a blob store and folded into one capture " +
			"receipt, the same wire format `capture` produces, so " +
			"`restore` and `diff` work against serve-produced receipts " +
			"unchanged.\n" +
			".PP\n" +
			"Discovery by LAN multicast is out of scope (Tailscale has no " +
			"multicast); senders reach the receiver by its tailnet " +
			"address via LocalSend's manual-IP / favorites path. The " +
			"server runs until interrupted (SIGINT/SIGTERM/SIGHUP), " +
			"draining any in-flight transfer before exiting.",
	}
}

func (cmd *Serve) SetFlagDefinitions(flagSet interfaces.CLIFlagDefinitions) {
	flagSet.StringVar(&cmd.Bind, "bind", "",
		"explicit listen host (IP or hostname). Overrides Tailscale "+
			"auto-detection. Use 0.0.0.0 to bind every interface "+
			"(exposes the receiver on the LAN).")
	flagSet.IntVar(&cmd.Port, "port", defaultPort,
		"listen port (LocalSend's well-known port is 53317).")
	flagSet.StringVar(&cmd.Store, "store", "",
		"destination blob-store-id for received captures (default store "+
			"when omitted).")
	flagSet.StringVar(&cmd.Alias, "alias", "",
		"device alias advertised to senders (default: hostname).")
}

func (cmd *Serve) Run(req command.Request) {
	ctx := req.Context.(errors.Context)

	if req.RemainingArgCount() > 0 {
		errors.ContextCancelWithBadRequestf(ctx,
			"serve takes no positional arguments; trailing: %v",
			req.PeekArgs())
		return
	}

	if cmd.Port < 1 || cmd.Port > 65535 {
		errors.ContextCancelWithBadRequestf(ctx,
			"invalid -port %d; expected 1-65535", cmd.Port)
		return
	}

	host, err := cmd.resolveBindHost()
	if err != nil {
		errors.ContextCancelWithBadRequestf(ctx, "%s", err.Error())
		return
	}
	addr := net.JoinHostPort(host, strconv.Itoa(cmd.Port))

	// Resolve the destination store up front so a bad -store fails fast
	// (EX_USAGE) instead of after the listener is already up.
	envBlobStore := command_components.MakeBlobStoreEnv(ctx)
	store, storeName, effectiveStoreId := resolveStore(ctx, envBlobStore, cmd.Store)

	cgEnvDir := command_components.MakeCgEnvDir(ctx)
	captureLogPath := cgEnvDir.GetXDG().State.MakePath(capture_log.FileName).String()

	logf := func(format string, args ...any) {
		fmt.Fprintf(os.Stderr, format+"\n", args...)
	}

	srv := newServer(
		store, storeName, effectiveStoreId, captureLogPath,
		cmd.makeInfo(), logf,
	)

	httpServer := &http.Server{
		Addr:    addr,
		Handler: srv.handler(),
		// Per-request context derives from ctx so an in-flight upload's
		// blob copy aborts when the server is shutting down.
		BaseContext: func(net.Listener) context.Context { return ctx },
	}

	listener, err := net.Listen("tcp", addr)
	if err != nil {
		errors.ContextCancelWithError(ctx,
			errors.Wrapf(err, "listen on %s", addr))
		return
	}

	logf("serve: LocalSend receiver listening on http://%s%s",
		addr, apiPrefix)
	logf("serve: device alias=%q store=%s; Ctrl-C to stop",
		srv.info.Alias, quoteEmpty(storeName))

	// Shut the server down when the context is cancelled (SIGINT/
	// SIGTERM/SIGHUP). Shutdown lets in-flight transfers drain up to
	// shutdownGrace before the listener is force-closed.
	go func() {
		<-ctx.Done()
		shutCtx, cancel := context.WithTimeout(
			context.Background(), shutdownGrace,
		)
		defer cancel()
		_ = httpServer.Shutdown(shutCtx)
	}()

	if serveErr := httpServer.Serve(listener); serveErr != nil &&
		!errors.Is(serveErr, http.ErrServerClosed) {
		errors.ContextCancelWithError(ctx, errors.Wrap(serveErr))
	}
}

// resolveBindHost returns the host to bind: the explicit -bind value, or
// the auto-detected Tailscale address.
func (cmd *Serve) resolveBindHost() (string, error) {
	if cmd.Bind != "" {
		return cmd.Bind, nil
	}
	return tailscaleAddr()
}

// makeInfo builds the advertised device descriptor. Alias defaults to
// the hostname; a fresh random fingerprint is minted per process.
func (cmd *Serve) makeInfo() deviceInfo {
	alias := cmd.Alias
	if alias == "" {
		if h, err := os.Hostname(); err == nil && h != "" {
			alias = h
		} else {
			alias = "cutting-garden"
		}
	}
	return deviceInfo{
		Alias:       alias,
		Version:     protocolVersion,
		DeviceModel: "cutting-garden",
		DeviceType:  "headless",
		Fingerprint: randToken(),
		Port:        cmd.Port,
		Protocol:    "http",
		Download:    false,
	}
}

// resolveStore returns the destination blob store plus its display name
// (empty for the default store) and the store-id used for the receipt's
// store-hint. Parsing and configured-store lookup delegate to
// command_components.ResolveStoreByID; a bad -store cancels ctx
// (EX_USAGE).
func resolveStore(
	ctx errors.Context,
	envBlobStore interface {
		command_components.MaterializationEnv
		GetDefaultBlobStoreId() string
	},
	storeFlag string,
) (store blob_stores.BlobStoreInitialized, storeName, effectiveStoreId string) {
	if storeFlag == "" {
		return envBlobStore.GetDefaultBlobStore(), "",
			envBlobStore.GetDefaultBlobStoreId()
	}

	store, err := command_components.ResolveStoreByID(envBlobStore, storeFlag)
	if err != nil {
		errors.ContextCancelWithBadRequestf(ctx, "%s", err.Error())
		return
	}
	return store, storeFlag, storeFlag
}
