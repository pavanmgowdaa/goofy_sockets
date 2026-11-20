package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"time"

	"github.com/fairpass/backend/internal/config"
	"github.com/fairpass/backend/internal/db"
	"github.com/fairpass/backend/internal/httpapi"
	"github.com/fairpass/backend/internal/maintenance"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatal(err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	pool, err := db.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Fatal(err)
	}
	defer pool.Close()
	go maintenance.Run(ctx, pool, cfg)
	srv := &http.Server{Addr: cfg.HTTPAddr, Handler: httpapi.New(cfg, pool), ReadHeaderTimeout: 5 * time.Second}
	go func() {
		<-ctx.Done()
		c, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = srv.Shutdown(c)
	}()
	log.Printf("FAIRPASS API listening on %s", cfg.HTTPAddr)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
}
