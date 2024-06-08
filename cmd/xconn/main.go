package main

import (
	"log"

	"github.com/xconnio/wampproto-protobuf/go"
	"github.com/xconnio/xconn-go"
)

func main() {
	router := xconn.NewRouter()
	router.AddRealm("realm1")

	server := xconn.NewServer(router, nil)

	serializer := &wampprotobuf.ProtobufSerializer{}
	protobufSpec := xconn.NewWSSerializerSpec("wamp.2.protobuf", serializer)

	if err := server.RegisterSpec(protobufSpec); err != nil {
		log.Fatal(err)
	}

	log.Fatal(server.Start("0.0.0.0", 8080))
}
