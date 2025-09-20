package main

import (
	"context"
	"log"
	"os"
	"os/signal"

	"github.com/xconnio/xconn-go"
)

const procedureProgressDownload = "io.xconn.progress.download"

func main() {
	callee, err := xconn.ConnectAnonymous(context.Background(), "ws://localhost:8080/ws", "realm1")
	if err != nil {
		log.Fatalf("Failed to connect to server: %s", err)
	}
	defer func() { _ = callee.Leave() }()

	registerResponse := callee.Register(procedureProgressDownload, xconn.DownloadInvocationHandler).Do()
	if registerResponse.Err != nil {
		log.Fatalf("Failed to register method: %s", registerResponse.Err)
	}
	defer func() { _ = registerResponse.Unregister() }()

	// Wait for interrupt signal
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt)
	<-sigChan
}
