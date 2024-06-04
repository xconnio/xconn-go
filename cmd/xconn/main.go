package main

import (
	"log"

	"github.com/xconnio/xconn-go"
)

func main() {
	router := xconn.NewRouter()
	router.AddRealm("realm1")

	server := xconn.NewServer(router, nil)
	log.Fatal(server.Start("0.0.0.0", 8080))
}
