package main

import (
	"context"
	"log"

	"github.com/xconnio/xconn-go"
)

const testTopic = "io.xconn.test"

func main() {
	// Create and connect a subscriber client to server
	ctx := context.Background()
	client := xconn.Client{}
	publisher, err := client.Connect(ctx, "ws://localhost:8080/ws", "realm1")
	if err != nil {
		log.Fatalf("Failed to connect to server: %s", err)
	}

	// Publish event to topic
	err = publisher.Publish(testTopic, []any{}, map[string]any{}, map[string]any{})
	if err != nil {
		log.Fatalf("Failed to publish: %s", err)
	}

	// Publish event with args
	err = publisher.Publish(testTopic, []any{"Hello", "World"}, map[string]any{}, map[string]any{})
	if err != nil {
		log.Fatalf("Failed to publish: %s", err)
	}

	// Publish event with kwargs
	err = publisher.Publish(testTopic, []any{}, map[string]any{"Hello World!": "I love WAMP"}, map[string]any{})
	if err != nil {
		log.Fatalf("Failed to publish: %s", err)
	}

	// Publish event with args and kwargs
	err = publisher.Publish(testTopic, []any{"Hello", "World"}, map[string]any{"Hello World!": "I love WAMP"},
		map[string]any{})
	if err != nil {
		log.Fatalf("Failed to publish: %s", err)
	}

	log.Printf("Published events to %s", testTopic)

	// leave the server
	err = publisher.Leave()
	if err != nil {
		log.Fatalf("Failed to leave server: %s", err)
	}
}
