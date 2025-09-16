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
	publisher, err := xconn.ConnectAnonymous(ctx, "ws://localhost:8080/ws", "realm1")
	if err != nil {
		log.Fatalf("Failed to connect to server: %s", err)
	}

	publishResponse := publisher.Publish(testTopic).Do()
	if publishResponse.Err != nil {
		log.Fatalf("Failed to publish: %s", err)
	}

	publishResponse = publisher.Publish(testTopic).Args("Hello", "World").Do()
	if publishResponse.Err != nil {
		log.Fatalf("Failed to publish: %s", err)
	}

	publishResponse = publisher.Publish(testTopic).Kwarg("Hello World!", "I love WAMP").Do()
	if publishResponse.Err != nil {
		log.Fatalf("Failed to publish: %s", err)
	}

	publishResponse = publisher.Publish(testTopic).
		Args("Hello", "World").
		Kwarg("Hello World!", "I love WAMP").
		Do()
	if publishResponse.Err != nil {
		log.Fatalf("Failed to publish: %s", publishResponse.Err)
	}

	log.Printf("Published events to %s", testTopic)

	// leave the server
	err = publisher.Leave()
	if err != nil {
		log.Fatalf("Failed to leave server: %s", err)
	}
}
