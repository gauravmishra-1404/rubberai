// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Gaurav Mishra

package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"rubberai/internal/collector"
	"syscall"
	"time"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	var handler http.Handler
	if configPath := os.Getenv("RUBBERAI_COLLECTOR_CONFIG"); configPath != "" {
		cfg, err := collector.LoadConfig(configPath)
		if err != nil {
			log.Fatal(err)
		}
		var connections []*collector.Collector
		handler, connections, err = collector.Connections(cfg)
		if err != nil {
			log.Fatal(err)
		}
		collector.RunAll(ctx, connections)
		log.Printf("rubberai integration hub has %d connection(s)", len(connections))
	} else {
		c, err := collector.New(os.Getenv("RUBBERAI_OUTBOX"), os.Getenv("RUBBERAI_URL"), os.Getenv("RUBBERAI_API_KEY"), os.Getenv("RUBBERAI_LOCAL_TOKEN"))
		if err != nil {
			log.Fatal(err)
		}
		handler = c.Handler()
		go c.Run(ctx)
	}
	srv := &http.Server{Addr: "127.0.0.1:4319", Handler: handler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 10 * time.Second, IdleTimeout: 30 * time.Second}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdown)
	}()
	log.Print("rubberai collector listening on 127.0.0.1:4319")
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
}
