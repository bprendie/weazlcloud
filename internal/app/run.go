package app

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/bprendie/weazlcloud/internal/buildinfo"
	"github.com/bprendie/weazlcloud/internal/capsule"
	"github.com/bprendie/weazlcloud/internal/config"
	"github.com/bprendie/weazlcloud/internal/desk"
	"github.com/bprendie/weazlcloud/internal/drive"
	"github.com/bprendie/weazlcloud/internal/filesvc"
	"github.com/bprendie/weazlcloud/internal/idle"
	"github.com/bprendie/weazlcloud/internal/library"
	"github.com/bprendie/weazlcloud/internal/quota"
	"github.com/bprendie/weazlcloud/internal/share"
	"github.com/bprendie/weazlcloud/internal/users"
	"github.com/bprendie/weazlcloud/internal/vault"
)

type Node struct {
	cfg          config.Config
	desk         net.Listener
	share        net.Listener
	drive        net.Listener
	svcs         []*http.Server
	vault        *vault.Vault
	lib          *library.Library
	caps         *capsule.Store
	uploads      func(context.Context)
	uploadCancel context.CancelFunc
	activity     *idle.Coordinator
	idleCancel   context.CancelFunc
}

func Run(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("weazlcloud", flag.ContinueOnError)
	fs.SetOutput(stderr)
	showVersion := fs.Bool("version", false, "print version")
	check := fs.Bool("check", false, "validate configuration")
	ready := fs.Bool("ready", false, "probe local desk /ready")
	data := fs.String("data", "", "data directory")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *showVersion {
		fmt.Fprintf(stdout, "weazlcloud %s (%s)\n", buildinfo.Version, buildinfo.Commit)
		return nil
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if *data != "" {
		cfg.DataDir = *data
		if err := cfg.Validate(); err != nil {
			return err
		}
	}
	if err := cfg.EnsureData(); err != nil {
		return err
	}
	if err := ensureTempDir(); err != nil {
		return err
	}
	if *check {
		fmt.Fprintln(stdout, "weazlcloud: configuration valid")
		return nil
	}
	if *ready {
		return probeReady(cfg.DeskAddr)
	}
	n, err := listen(cfg)
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "weazlcloud desk %s share %s drive %s\n", n.desk.Addr(), n.share.Addr(), n.drive.Addr())
	return n.serve(ctx)
}

func (n *Node) DeskAddr() string  { return n.desk.Addr().String() }
func (n *Node) ShareAddr() string { return n.share.Addr().String() }
func (n *Node) DriveAddr() string { return n.drive.Addr().String() }

func ensureTempDir() error {
	return os.MkdirAll(os.TempDir(), 0o700)
}

func Start(cfg config.Config) (*Node, error) {
	if err := cfg.EnsureData(); err != nil {
		return nil, err
	}
	if err := ensureTempDir(); err != nil {
		return nil, err
	}
	n, err := listen(cfg)
	if err != nil {
		return nil, err
	}
	if err := n.bind(); err != nil {
		return nil, err
	}
	for i, ln := range []net.Listener{n.desk, n.share, n.drive} {
		go n.svcs[i].Serve(ln)
	}
	n.startUploadExpiry(context.Background())
	n.startIdleMaintenance(context.Background())
	return n, nil
}

func (n *Node) Close() error { return n.shutdown() }

func listen(cfg config.Config) (*Node, error) {
	d, err := net.Listen("tcp", cfg.DeskAddr)
	if err != nil {
		return nil, err
	}
	s, err := net.Listen("tcp", cfg.ShareAddr)
	if err != nil {
		d.Close()
		return nil, err
	}
	v, err := net.Listen("tcp", cfg.DriveAddr)
	if err != nil {
		d.Close()
		s.Close()
		return nil, err
	}
	return &Node{cfg: cfg, desk: d, share: s, drive: v}, nil
}

func (n *Node) serve(ctx context.Context) error {
	if err := n.bind(); err != nil {
		return err
	}
	n.startUploadExpiry(ctx)
	n.startIdleMaintenance(ctx)
	errc := make(chan error, 3)
	for i, ln := range []net.Listener{n.desk, n.share, n.drive} {
		go func(srv *http.Server, ln net.Listener) {
			err := srv.Serve(ln)
			if err != nil && !errors.Is(err, http.ErrServerClosed) {
				errc <- err
			}
		}(n.svcs[i], ln)
	}
	select {
	case <-ctx.Done():
		return n.shutdown()
	case err := <-errc:
		_ = n.shutdown()
		return err
	}
}

func (n *Node) bind() error {
	vp, np := vault.Paths(n.cfg.DataDir)
	n.vault = vault.New(vp, np)
	n.lib = library.New(n.cfg.DataDir+"/library", n.cfg.DataDir+"/catalog.enc", n.vault)
	n.caps = capsule.New(n.cfg.DataDir + "/capsules")
	us, err := users.New(n.cfg.DataDir+"/users.json", n.cfg.DataDir+"/users")
	if err != nil {
		return err
	}
	us.SetSecureCookies(n.cfg.SecureCookies)
	q := quota.New(n.cfg.DataDir)
	registry := filesvc.NewRegistry(us, q)
	n.activity = idle.New(n.cfg.MaintenanceQuiet, nil)
	registry.SetActivityTracker(n.activity.Track)
	n.activity.Register(registry.CleanupExpiredTrash)
	deskHandler := desk.NewMulti(us, n.caps, q, n.cfg.PublicBase, n.cfg.DriveBase, n.cfg.DataDir, registry)
	n.uploads = deskHandler.RunUploads
	n.svcs = []*http.Server{
		server(n.desk, deskHandler, n.cfg.DataDir, n.activity),
		server(n.share, share.New(n.caps), n.cfg.DataDir, n.activity),
		server(n.drive, drive.NewMultiWith(us, q, registry), n.cfg.DataDir, n.activity),
	}
	return nil
}

func server(ln net.Listener, h http.Handler, dataDir string, activity *idle.Coordinator) *http.Server {
	return &http.Server{
		Addr:              ln.Addr().String(),
		Handler:           observed(health(trackRequests(h, activity), dataDir)),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
}

func (n *Node) shutdown() error {
	if n.uploadCancel != nil {
		n.uploadCancel()
	}
	if n.idleCancel != nil {
		n.idleCancel()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var first error
	for _, srv := range n.svcs {
		if err := srv.Shutdown(ctx); err != nil && first == nil {
			first = err
		}
	}
	return first
}

func (n *Node) startIdleMaintenance(parent context.Context) {
	if n.activity == nil || n.idleCancel != nil {
		return
	}
	ctx, cancel := context.WithCancel(parent)
	n.idleCancel = cancel
	go n.activity.Run(ctx)
}

func (n *Node) startUploadExpiry(parent context.Context) {
	if n.uploads == nil || n.uploadCancel != nil {
		return
	}
	ctx, cancel := context.WithCancel(parent)
	n.uploadCancel = cancel
	go n.uploads(ctx)
}

func probeReady(addr string) error {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return err
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	resp, err := http.Get("http://" + net.JoinHostPort(host, port) + "/ready")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("desk /ready status %d", resp.StatusCode)
	}
	return nil
}
