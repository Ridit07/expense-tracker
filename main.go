package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"expense-tracker/config"
	"expense-tracker/db"
	"expense-tracker/model"
	"expense-tracker/transport_http"
)

func main() {
	cfg, err := config.LoadConfig()
	if err != nil {
		log.Fatalf("failed to load config: %v", err)
	}

	if err := db.InitDB(cfg.DBReadURL, cfg.DBWriteURL); err != nil {
		log.Fatalf("failed to init db: %v", err)
	}

	// Auto-migrate the ingestion schema. As new pipeline tables are added they
	// get appended here.
	if err := db.WriteConnection().AutoMigrate(&model.User{}, &model.RawMessage{}); err != nil {
		log.Fatalf("failed to migrate db: %v", err)
	}

	srv := transport_http.NewServer(cfg)

	// Start server.
	go func() {
		log.Printf("expense-tracker listening on %s (env=%s)", srv.Addr, cfg.Env)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("server error: %v", err)
		}
	}()

	// Graceful shutdown on SIGINT/SIGTERM.
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop

	log.Println("shutting down...")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := srv.Shutdown(ctx); err != nil {
		log.Fatalf("graceful shutdown failed: %v", err)
	}
	log.Println("stopped")
}
