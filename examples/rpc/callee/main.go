package main

import (
	"context"
	"log"
	"os"
	"os/signal"

	"github.com/xconnio/wampproto-go/messages"

	"github.com/xconnio/xconn-go"
)

const testProcedureEcho = "io.xconn.echo"
const testProcedureSum = "io.xconn.sum"

// Function to handle received Invocation for "io.xconn.sum"
func sumHandler(_ context.Context, inv *xconn.Invocation) *xconn.Result {
	log.Printf("Received invocation: args=%s, kwargs=%s, details=%s", inv.Arguments, inv.KwArguments, inv.Details)
	sum := int64(0)
	for _, i := range inv.Arguments {
		arg, ok := messages.AsInt64(i)
		if ok {
			sum = sum + arg
		}
	}
	return &xconn.Result{Arguments: []any{sum}}
}

func main() {
	// Create and connect a callee client to server
	ctx := context.Background()
	client := xconn.Client{}
	callee, err := client.Connect(ctx, "ws://localhost:8080/ws", "realm1")
	if err != nil {
		log.Fatalf("Failed to connect to server: %s", err)
	}

	// Define function to handle received Invocation for "io.xconn.echo"
	echoHandler := func(_ context.Context, inv *xconn.Invocation) *xconn.Result {
		log.Printf("Received invocation: args=%s, kwargs=%s, details=%s", inv.Arguments, inv.KwArguments, inv.Details)

		return &xconn.Result{Arguments: inv.Arguments, KwArguments: inv.KwArguments, Details: inv.Details}
	}

	// Register procedure "io.xconn.echo"
	echoRegistration, err := callee.Register(testProcedureEcho, echoHandler, map[string]any{})
	if err != nil {
		log.Fatalf("Failed to register: %s", err)
	}
	log.Printf("Registered procedure: %s", testProcedureEcho)

	// Register procedure "io.xconn.sum"
	sumRegistration, err := callee.Register(testProcedureSum, sumHandler, map[string]any{})
	if err != nil {
		log.Fatalf("Failed to register: %s", err)
	}
	log.Printf("Registered procedure: %s", testProcedureSum)

	// Define a signal handler to catch the interrupt signal (Ctrl+C)
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt)
	select {
	case <-sigChan:
	case <-ctx.Done():
		return
	}

	// Unregister procedure "io.xconn.echo"
	err = callee.Unregister(echoRegistration.ID)
	if err != nil {
		log.Fatalf("Failed to unregister: %s", err)
	}

	// Unregister procedure "io.xconn.sum"
	err = callee.Unregister(sumRegistration.ID)
	if err != nil {
		log.Fatalf("Failed to unregister: %s", err)
	}

	// Close connection to the server
	err = callee.Leave()
	if err != nil {
		log.Fatalf("Failed to leave server: %s", err)
	}
}
