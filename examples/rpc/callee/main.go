package main

import (
	"context"
	"log"
	"os"
	"os/signal"

	"github.com/xconnio/wampproto-go/util"
	"github.com/xconnio/xconn-go"
)

const testProcedureEcho = "io.xconn.echo"
const testProcedureSum = "io.xconn.sum"

// Function to handle received Invocation for "io.xconn.sum".
func sumHandler(_ context.Context, inv *xconn.Invocation) *xconn.InvocationResult {
	log.Printf("Received invocation: args=%s, kwargs=%s, details=%s", inv.Args(), inv.Kwargs(), inv.Details())
	sum := uint64(0)
	for _, i := range inv.Args() {
		arg, ok := util.AsUInt64(i)
		if ok {
			sum = sum + arg
		}
	}

	return xconn.NewInvocationResult(sum)
}

func main() {
	// Create and connect a callee client to server
	ctx := context.Background()
	callee, err := xconn.ConnectAnonymous(ctx, "ws://localhost:8080/ws", "realm1")
	if err != nil {
		log.Fatalf("Failed to connect to server: %s", err)
	}

	// Define function to handle received Invocation for "io.xconn.echo"
	echoHandler := func(_ context.Context, inv *xconn.Invocation) *xconn.InvocationResult {
		log.Printf("Received invocation: args=%s, kwargs=%s, details=%s", inv.Args(), inv.Kwargs(), inv.Details())

		return &xconn.InvocationResult{Args: inv.Args(), Kwargs: inv.Kwargs(), Details: inv.Details()}
	}

	// RegisterWithRequest procedure "io.xconn.echo"
	echoRegisterResponse := callee.Register(testProcedureEcho, echoHandler).Do()
	if echoRegisterResponse.Err != nil {
		log.Fatalf("Failed to register: %s", echoRegisterResponse.Err)
	}
	log.Printf("Registered procedure: %s", testProcedureEcho)

	// RegisterWithRequest procedure "io.xconn.sum"
	sumRegisterResponse := callee.Register(testProcedureSum, sumHandler).Do()
	if sumRegisterResponse.Err != nil {
		log.Fatalf("Failed to register: %s", sumRegisterResponse.Err)
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
	err = echoRegisterResponse.Unregister()
	if err != nil {
		log.Fatalf("Failed to unregister: %s", err)
	}

	// Unregister procedure "io.xconn.sum"
	err = sumRegisterResponse.Unregister()
	if err != nil {
		log.Fatalf("Failed to unregister: %s", err)
	}

	// Close connection to the server
	err = callee.Leave()
	if err != nil {
		log.Fatalf("Failed to leave server: %s", err)
	}
}
