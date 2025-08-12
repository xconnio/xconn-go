package main

import (
	"context"
	"crypto/rand"
	"fmt"
	"log"
	"time"

	"github.com/xconnio/xconn-go"
)

const testProcedureEcho = "io.xconn.echo"
const testProcedureSum = "io.xconn.sum"

func main() {
	// Create and connect a caller client to server
	client := xconn.Client{
		SerializerSpec: xconn.CapnprotoSplitSerializerSpec,
	}
	caller, err := client.Connect(context.Background(), "ws://localhost:8080/ws", "realm1")
	if err != nil {
		log.Fatalf("Failed to connect to server: %s", err)
	}

	options := map[string]any{
		"x_payload_serializer": uint64(0),
	}

	const size = 1024 * 1024 * 100 // 100 MB
	data := make([]byte, size)

	_, err = rand.Read(data)
	if err != nil {
		panic(err)
	}

	start := time.Now()
	// Call procedure "io.xconn.echo"
	echoResponse := caller.Call(testProcedureEcho).Arg(data).Options(options).Do()
	if echoResponse.Err != nil {
		log.Fatalf("Failed to call %s: %s", testProcedureEcho, echoResponse.Err)
	}
	elapsed := time.Since(start)
	fmt.Printf("Time taken: %s\n", elapsed)
	//log.Printf("Result of procedure %s: args=%s, kwargs=%s, details=%s", testProcedureEcho, echoResponse.Args,
	//	echoResponse.Kwargs, echoResponse.Details)

	// Call procedure "io.xconn.sum"
	//sumResponse := caller.Call(testProcedureSum).Arg(1).Arg(2).Options(options).Do()
	//if sumResponse.Err != nil {
	//	log.Fatalf("Failed to call %s: %s", testProcedureSum, sumResponse.Err)
	//}
	//log.Printf("Sum=%s", sumResponse.Args[0])

	// Close connection to the server
	err = caller.Leave()
	if err != nil {
		log.Fatalf("Failed to leave server: %s", err)
	}
}
