package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"

	"minerdash/internal/agent"
)

func main() {
	configPath := flag.String("config", "/etc/minerdash/agent.json", "agent configuration file")
	flag.Parse()
	logger := log.New(os.Stdout, "minerdash-agent: ", log.LstdFlags|log.LUTC)
	config, err := agent.LoadConfig(*configPath)
	if err != nil {
		logger.Fatal(err)
	}
	client, err := agent.NewClient(config, logger)
	if err != nil {
		logger.Fatal(err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := client.Run(ctx); err != nil {
		logger.Fatal(err)
	}
}
