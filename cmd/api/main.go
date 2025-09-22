package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/example/accounting/internal/app"
	staticrules "github.com/example/accounting/internal/infrastructure/rule/static"
	"github.com/example/accounting/internal/infrastructure/voucher/inmemory"
	httpiface "github.com/example/accounting/internal/interfaces/http"
)

func main() {
	ruleRepo := staticrules.NewRepository()
	voucherRepo := inmemory.NewVoucherRepository()
	messageStore := inmemory.NewMessageStore()

	service := app.NewService(ruleRepo, voucherRepo, messageStore)
	server := httpiface.NewServer(service)

	srv := &http.Server{
		Addr:         ":8080",
		Handler:      server.Routes(),
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 5 * time.Second,
	}

	go func() {
		log.Printf("accounting demo API listening on %s", srv.Addr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("server failed: %v", err)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := srv.Shutdown(ctx); err != nil {
		log.Printf("graceful shutdown failed: %v", err)
	}
}
