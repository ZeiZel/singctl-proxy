package main

import (
	"context"
	"errors"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"singctl/internal/licensesrv"
)

func runServe(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	addr := fs.String("addr", envOr("LICENSE_ADDR", ":8080"), "listen address")
	if err := fs.Parse(args); err != nil {
		return err
	}

	signer, err := loadSigner()
	if err != nil {
		return err
	}
	store, err := licensesrv.NewFileStore(dbPath())
	if err != nil {
		return err
	}

	var payment licensesrv.PaymentProvider
	if secret := os.Getenv("LICENSE_WEBHOOK_SECRET"); secret != "" {
		payment = licensesrv.GenericHMAC{Secret: secret}
	}

	ttl := 365 * 24 * time.Hour
	if v := os.Getenv("LICENSE_DEFAULT_TTL_DAYS"); v != "" {
		if days, err := strconv.Atoi(v); err == nil {
			ttl = time.Duration(days) * 24 * time.Hour // 0 → perpetual
		}
	}

	srv, err := licensesrv.New(licensesrv.Config{
		Store:      store,
		Signer:     signer,
		AdminToken: os.Getenv("LICENSE_ADMIN_TOKEN"),
		Payment:    payment,
		DefaultTTL: ttl,
		Logger:     log.New(os.Stdout, "license ", log.LstdFlags),
	})
	if err != nil {
		return err
	}

	httpSrv := &http.Server{
		Addr:              *addr,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	errc := make(chan error, 1)
	go func() {
		log.Println("license server listening on", *addr)
		errc <- httpSrv.ListenAndServe()
	}()

	select {
	case err := <-errc:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		shutCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return httpSrv.Shutdown(shutCtx)
	}
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
