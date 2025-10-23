package main

import (
	"context"
	"time"

	log "github.com/sirupsen/logrus"

	"github.com/xconnio/xconn-go"
)

func main() {
	retryDelay := 1 * time.Second
	maxDelay := 30 * time.Second

	for {
		session, err := xconn.ConnectAnonymous(context.Background(), "ws://localhost:8080/ws", "realm1")
		if err != nil {
			log.Printf("failed to connect: %v", err)
			log.Printf("retrying in %v...", retryDelay)
			time.Sleep(retryDelay)

			// exponential backoff
			retryDelay *= 2
			if retryDelay > maxDelay {
				retryDelay = maxDelay
			}
			continue
		}

		log.Println("connected successfully")

		// reset backoff after successful connection
		retryDelay = 1 * time.Second

		// wait for session to disconnect
		<-session.Done()

		log.Println("disconnected, retrying...")
	}

}
