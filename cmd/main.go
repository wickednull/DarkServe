package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"darkserve/internal/auth"
	"darkserve/internal/config"
	"darkserve/internal/daemon"
	"darkserve/internal/db"
	"darkserve/internal/scanner"
	"darkserve/internal/server"
	"darkserve/internal/usb"
)

func main() {
	configPath := flag.String("config", "", "path to config.yaml")
	daemonize := flag.Bool("daemon", false, "run detached in the background")
	pidFile := flag.String("pidfile", "", "write the process id to this file")
	addrFlag := flag.String("addr", "", "listen address, e.g. :7676")
	flag.Parse()

	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	// The port may also be given as a plain argument ("darkserve 7676").
	// An explicit -addr wins over the argument, the argument wins over the
	// config file.
	if len(flag.Args()) > 0 {
		if port, err := strconv.Atoi(flag.Args()[0]); err == nil && port > 0 && port < 65536 {
			cfg.Addr = ":" + strconv.Itoa(port)
		}
	}
	if *addrFlag != "" {
		cfg.Addr = *addrFlag
	}

	_ = os.MkdirAll(cfg.DataDir, 0o755)
	_ = os.MkdirAll(cfg.CacheDir, 0o755)

	// -daemon starts a detached copy of this binary and waits until it is
	// serving. The child then does the real work below.
	if *daemonize && !daemon.IsChild() {
		startDetached(cfg, *configPath, *pidFile, *addrFlag)
		return
	}
	if daemon.IsChild() {
		// The child outlives the launcher, so it must not die when the
		// terminal or the desktop session that started it goes away.
		signal.Ignore(syscall.SIGHUP)
	}

	database, err := db.Open(filepath.Join(cfg.DataDir, "darkserve.db"))
	if err != nil {
		log.Fatalf("db: %v", err)
	}
	defer database.Close()

	// Settings saved in-app (settings menu) win over any config file so the
	// user never has to edit files. Applies on every boot.
	if rows, err := database.Query("SELECT key, value FROM settings"); err == nil {
		for rows.Next() {
			var k, v string
			if rows.Scan(&k, &v) == nil {
				cfg.ApplySetting(k, v)
			}
		}
		rows.Close()
		// Libraries saved before audio support keep only their video formats;
		// extend the stored setting so uploads and scans accept music too.
		if exts, changed := config.EnsureAudioExts(cfg.MediaExts); changed {
			cfg.MediaExts = exts
			database.Exec("INSERT INTO settings(key, value) VALUES('media_exts', ?) "+
				"ON CONFLICT(key) DO UPDATE SET value=excluded.value",
				strings.Join(exts, ","))
		}
	}

	detector := usb.NewDetector(cfg.USBPaths)
	scanner := scanner.New(database, cfg, detector)
	authManager := auth.NewManager(database.DB)
	srv := server.New(cfg, database, scanner, detector, authManager)
	srv.StartJobs(context.Background())

	// The web UI can ask the server to stop (admin only). It is how the
	// AppImage gets shut down when it was started from a file manager.
	stop := make(chan struct{})
	srv.SetShutdownHook(func() { close(stop) })

	// Initial sync: USB volumes + local media roots. The drives are found
	// right away, the indexing runs in the background so the server answers
	// immediately — the UI shows the scan progress while it works through
	// the libraries.
	drives := detector.Scan()
	go scanner.SyncAll(context.Background(), drives)

	// Keep watching for drive changes.
	go detector.Watch(context.Background(), func(drives []usb.Drive) {
		log.Printf("USB change: %d drives detected", len(drives))
		scanner.SyncAll(context.Background(), drives)
	})

	ln, err := net.Listen("tcp", cfg.Addr)
	if err != nil {
		if listenErr, ok := err.(*net.OpError); ok && listenErr.Err == syscall.EADDRINUSE {
			log.Printf("port %s already in use — is DarkServe already running?", cfg.Addr)
		}
		log.Fatalf("listen: %v", err)
	}

	// Record the pid so the launcher can stop the server later without
	// guessing which process owns the port.
	if *pidFile != "" {
		if err := daemon.WritePID(*pidFile); err != nil {
			log.Printf("pidfile: %v", err)
		} else {
			defer daemon.RemovePID(*pidFile)
		}
	}

	// The port is ours. Tell the parent, so it knows the server is really
	// up and not some other program that holds the same port.
	daemon.SignalReady()

	httpSrv := &http.Server{Handler: srv.Router()}
	go func() {
		log.Printf("DarkServe listening on http://localhost%s", cfg.Addr)
		if err := httpSrv.Serve(ln); err != nil && err != http.ErrServerClosed {
			log.Printf("http: %v", err)
		}
	}()

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	select {
	case <-sig:
	case <-stop:
		log.Printf("shutdown requested")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	httpSrv.Shutdown(ctx)
	fmt.Println("DarkServe stopped")
}

// startDetached runs this binary in the background and returns once the
// server answers on its port. The server keeps running after this process
// exits, which is what the AppImage launcher and start.sh need.
func startDetached(cfg *config.Config, configPath, pidFile, addr string) {
	args := []string{}
	if configPath != "" {
		args = append(args, "-config", configPath)
	}
	if pidFile != "" {
		args = append(args, "-pidfile", pidFile)
	}
	if addr != "" {
		args = append(args, "-addr", addr)
	}
	args = append(args, flag.Args()...)

	logPath := filepath.Join(cfg.DataDir, "run.log")
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		log.Fatalf("log: %v", err)
	}
	defer logFile.Close()

	self, err := os.Executable()
	if err != nil {
		log.Fatalf("executable: %v", err)
	}

	if _, err := daemon.Spawn(self, args, logFile, 90*time.Second); err != nil {
		log.Printf("could not start DarkServe: %v", err)
		// Show the tail of the log, that is where the reason lives.
		if data, rerr := os.ReadFile(logPath); rerr == nil {
			lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
			if len(lines) > 15 {
				lines = lines[len(lines)-15:]
			}
			for _, l := range lines {
				log.Printf("  %s", l)
			}
		}
		os.Exit(1)
	}
	fmt.Printf("DarkServe up → http://localhost%s\n", cfg.Addr)
}
