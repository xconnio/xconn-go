package main

import (
	"flag"
	"log"
	"os"
	"os/signal"

	"github.com/xconnio/xconn-go"
)

func main() {
	host := flag.String("host", "127.0.0.1", "host to listen on")
	port := flag.Int("port", 8080, "port to listen on")
	realm := flag.String("realm", "realm1", "realm to use")
	help := flag.Bool("help", false, "print help")

	flag.Parse()

	if *help {
		flag.Usage()
		return
	}

	r := xconn.NewRouter()
	r.AddRealm(*realm)
	defer r.Close()

	server := xconn.NewServer(r, nil, nil)
	closer, err := server.StartRawSocket(*host, *port)
	if err != nil {
		log.Fatal("Failed to start server:", err)
	}
	defer closer.Close()

	// Close server if SIGINT (CTRL-c) received.
	closeChan := make(chan os.Signal, 1)
	signal.Notify(closeChan, os.Interrupt)
	<-closeChan
}
