package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/romtenma/monasync/internal/app"
	"github.com/romtenma/monasync/internal/config"
	"github.com/romtenma/monasync/internal/store"
)

func main() {
	dumpXML := flag.Bool("dump-xml", false, "log sync request/response XML")
	flag.Parse()

	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("load config: %v", err)
	}
	cfg.DumpXML = *dumpXML

	st, err := store.Open(cfg.DBPath)
	if err != nil {
		log.Fatalf("open store: %v", err)
	}
	defer st.Close()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	server := app.New(cfg, st)
	if err := server.ListenAndServe(ctx); err != nil {
		log.Fatalf("server error: %v", err)
	}
}
