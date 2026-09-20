// Package handler is the Vercel serverless entrypoint. Vercel builds files under
// api/ into functions; vercel.json rewrites every path to /api/index so our
// existing router handles all routes.
package handler

import (
	"net/http"
	"sync"

	"expense-tracker/config"
	"expense-tracker/db"
	"expense-tracker/transport_http"
)

var (
	initOnce sync.Once
	router   http.Handler
	initErr  error
)

// setup runs once per warm instance: load config + open the DB pool. Schema is
// NOT migrated here (no startup on serverless) — migrations are applied via
// supabase/migrations.
func setup() {
	cfg, err := config.LoadConfig()
	if err != nil {
		initErr = err
		return
	}
	if err := db.InitDB(cfg.DBReadURL, cfg.DBWriteURL); err != nil {
		initErr = err
		return
	}
	router = transport_http.NewServer(cfg).Handler
}

func Handler(w http.ResponseWriter, r *http.Request) {
	initOnce.Do(setup)
	if initErr != nil {
		http.Error(w, "init failed: "+initErr.Error(), http.StatusInternalServerError)
		return
	}
	router.ServeHTTP(w, r)
}
