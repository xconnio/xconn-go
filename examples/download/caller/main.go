package main

import (
	"context"
	"log"

	"github.com/xconnio/xconn-go"
)

const procedureProgressDownload = "io.xconn.progress.download"

func main() {
	caller, err := xconn.ConnectAnonymous(context.Background(), "ws://localhost:8080/ws", "realm1")
	if err != nil {
		log.Fatalf("Failed to connect to server: %s", err)
	}
	defer func() { _ = caller.Leave() }()

	callResponse := caller.Call(procedureProgressDownload).Arg("/home/muzzammil/nxt/.nxt/config.yaml").Download()
	if callResponse.Err != nil {
		log.Fatalf("Failed to download config.yaml: %s", callResponse.Err)
	}
}
