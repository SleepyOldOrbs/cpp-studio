package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"cpp-studio/internal/config"
	"cpp-studio/internal/gateway"
	"cpp-studio/internal/lifecycle"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		log.Fatal(err)
	}
}

func run(args []string) error {
	flags := flag.NewFlagSet("cpp-studio", flag.ContinueOnError)
	configPath := flags.String("config", "config.example.json", "path to gateway config JSON")
	checkOnly := flags.Bool("check", false, "validate config and engine commands, then exit")
	runSeconds := flags.Int("run-seconds", 0, "optional smoke duration before shutdown")
	if err := flags.Parse(args); err != nil {
		return err
	}

	cfg, err := config.LoadChecked(*configPath)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	if *checkOnly {
		fmt.Println("config ok")
		return nil
	}

	ctx, stopSignals := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stopSignals()

	manager := lifecycle.NewManager(cfg)
	if err := manager.StartAll(ctx); err != nil {
		_ = manager.StopAll(context.Background())
		return fmt.Errorf("start engines: %w", err)
	}

	addr := fmt.Sprintf("%s:%d", cfg.Gateway.Host, cfg.Gateway.Port)
	server := &http.Server{Addr: addr, Handler: gateway.NewRouter(cfg, manager)}
	serverErr := make(chan error, 1)
	go func() {
		log.Printf("cpp-studio gateway listening on http://%s", addr)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			serverErr <- err
		}
		close(serverErr)
	}()

	var serveErr error
	if *runSeconds > 0 {
		timer := time.NewTimer(time.Duration(*runSeconds) * time.Second)
		select {
		case <-ctx.Done():
		case <-timer.C:
		case err := <-serverErr:
			serveErr = err
		}
		timer.Stop()
	} else {
		select {
		case <-ctx.Done():
		case err := <-serverErr:
			serveErr = err
		}
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	shutdownErr := server.Shutdown(shutdownCtx)
	cancel()

	stopCtx, stopCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer stopCancel()
	if err := manager.StopAll(stopCtx); err != nil {
		log.Printf("stop engines: %v", err)
	}
	if serveErr != nil {
		return fmt.Errorf("server error: %w", serveErr)
	}
	if shutdownErr != nil {
		return fmt.Errorf("server shutdown: %w", shutdownErr)
	}
	return nil
}
