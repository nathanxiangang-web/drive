package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path"
	"regexp"
	"strings"
	"syscall"
	"time"

	"github.com/nathanxiangang-web/drive/internal/cloud"
	"github.com/nathanxiangang-web/drive/internal/download"
	"github.com/nathanxiangang-web/drive/internal/site"
	"github.com/nathanxiangang-web/drive/internal/store"
)

func main() {
	if err := run(); err != nil {
		log.Print(err)
		os.Exit(1)
	}
}

func secret(filename string) (string, error) {
	b, err := os.ReadFile(filename)
	if err != nil || len(b) > 65536 {
		return "", errors.New("cannot read a configured secret file")
	}
	defer clear(b)
	s := strings.TrimSpace(string(b))
	if s == "" {
		return "", errors.New("configured secret file is empty")
	}
	return s, nil
}

func variable(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

func run() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	dsn, err := secret(variable("DRIVE_DSN_FILE", "/run/drive/database-url"))
	if err != nil {
		return err
	}
	init, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	db, err := store.Open(init, dsn)
	if err != nil {
		return errors.New("drive database initialization failed")
	}
	defer db.Close()
	if len(os.Args) > 1 {
		if len(os.Args) != 4 || os.Args[1] != "provision" {
			return errors.New("usage: drive provision <login> <password-file>")
		}
		password, err := secret(os.Args[3])
		if err != nil {
			return err
		}
		if err := db.Provision(init, os.Args[2], []byte(password)); err != nil {
			return errors.New("drive account provisioning failed")
		}
		fmt.Println("Drive account provisioned")
		return nil
	}
	if file := os.Getenv("DRIVE_BOOTSTRAP_FILE"); file != "" {
		password, err := secret(file)
		if err != nil {
			return err
		}
		if err := db.Provision(init, variable("DRIVE_BOOTSTRAP_LOGIN", "nathan"), []byte(password)); err != nil {
			return errors.New("bootstrap account provisioning failed")
		}
	}
	base := variable("DRIVE_OPENLIST_BASE", "http://openlist:5244")
	origin := os.Getenv("DRIVE_ORIGIN")
	u, err := url.Parse(origin)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
		return errors.New("invalid HTTPS drive origin")
	}
	public := variable("DRIVE_PUBLIC_ROOT", "/CloudSite")
	private := path.Join(public, variable("DRIVE_PRIVATE_FOLDER", "DriveSpaces"))
	if path.Clean(public) != public || !strings.HasPrefix(public, "/") || public == "/" || path.Dir(private) != public || !cloud.ValidName(path.Base(private)) {
		return errors.New("invalid storage roots")
	}
	hosts := map[string]bool{}
	validHost := regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?(\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)+$`)
	for _, host := range strings.Split(variable("DRIVE_CDN_HOSTS", "cdnfhnfile.115cdn.net"), ",") {
		host = strings.TrimSpace(host)
		if !validHost.MatchString(host) || net.ParseIP(host) != nil || strings.HasSuffix(host, ".local") || strings.HasSuffix(host, ".internal") {
			return errors.New("invalid CDN allowlist")
		}
		hosts[host] = true
	}
	native, err := cloud.OpenNative(variable("DRIVE_115_CONFIG", "/run/drive/115.toml"))
	if err != nil {
		return errors.New("115 client configuration failed")
	}
	s := &site.Site{Store: db, Drive: native, Links: cloud.Links{Base: base, TokenFile: variable("DRIVE_OPENLIST_TOKEN_FILE", "/run/drive/openlist-token"), Hosts: hosts}, Origin: origin, PublicRoot: public, PrivateRoot: private, OpenListPrefix: os.Getenv("DRIVE_OPENLIST_PREFIX"), DownloadEnabled: variable("DRIVE_DOWNLOADS", "true") == "true"}
	workerDone := make(chan struct{})
	if s.DownloadEnabled {
		if err := db.Recover(init); err != nil {
			return errors.New("could not restore task state")
		}
		go func() {
			defer close(workerDone)
			(download.Worker{Store: db, Drive: native, PrivateRoot: private}).Run(ctx)
		}()
	} else {
		close(workerDone)
	}
	httpServer := &http.Server{Addr: variable("DRIVE_LISTEN", ":8098"), Handler: s.Routes(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 28 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 << 10}
	go func() {
		<-ctx.Done()
		deadline, done := context.WithTimeout(context.Background(), 10*time.Second)
		defer done()
		_ = httpServer.Shutdown(deadline)
	}()
	log.Print("CloudDrive ready")
	if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		stop()
		<-workerDone
		return errors.New("drive HTTP listener stopped unexpectedly")
	}
	stop()
	<-workerDone
	return nil
}
