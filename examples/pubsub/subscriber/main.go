package main

import (
	"context"
	"log"
	"os"
	"os/signal"

	"github.com/xconnio/xconn-go"
)

const testTopic = "io.xconn.test"

func main() {
	// Create and connect a subscriber client to server
	ctx := context.Background()
	subscriber, err := xconn.ConnectAnonymous(ctx, "ws://localhost:8080/ws", "realm1")
	if err != nil {
		log.Fatalf("Failed to connect to server: %s", err)
	}

	// Define function to handle received events
	eventHandler := func(event *xconn.Event) {
		log.Printf("Received event: args=%s, kwargs=%s, details=%s", event.Args(), event.Kwargs(), event.Details())
	}

	subscribeResponse := subscriber.Subscribe(testTopic, eventHandler).Do()
	if err != nil {
		log.Fatalf("Failed to subscribe: %s", err)
	}
	log.Printf("Subscribed to topic: %s", testTopic)

	// Define a signal handler to catch the interrupt signal (Ctrl+C)
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt)
	select {
	case <-sigChan:
	case <-ctx.Done():
		return
	}

	// Unsubscribe from topic
	err = subscribeResponse.Unsubscribe()
	if err != nil {
		log.Fatalf("Failed to unsubscribe: %s", err)
	}

	// Close connection to the server
	err = subscriber.Leave()
	if err != nil {
		log.Fatalf("Failed to leave server: %s", err)
	}
}
