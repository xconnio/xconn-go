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

	// Publish event to topic
	publishRequest := xconn.NewPublishRequest(testTopic)
	err = publisher.Publish(publishRequest)
	if err != nil {
		log.Fatalf("Failed to publish: %s", err)
	}

	// Publish event with args
	publishRequestWithArgs := xconn.NewPublishRequest(testTopic).Args("Hello", "World")
	err = publisher.Publish(publishRequestWithArgs)
	if err != nil {
		log.Fatalf("Failed to publish: %s", err)
	}

	// Publish event with kwargs
	publishRequestWithKwArgs := xconn.NewPublishRequest(testTopic).KWArg("Hello World!", "I love WAMP")
	err = publisher.Publish(publishRequestWithKwArgs)
	if err != nil {
		log.Fatalf("Failed to publish: %s", err)
	}

	// Publish event with args and kwargs
	publishRequestWithArgsKwArgs := xconn.NewPublishRequest(testTopic).
		Args("Hello", "World").
		KWArg("Hello World!", "I love WAMP")
	err = publisher.Publish(publishRequestWithArgsKwArgs)
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
