package main

import (
	"fmt"
	"log"

	"github.com/xconnio/wampproto-go"
)

func main() {
	session := wampproto.NewSession(nil)
	fmt.Println(session)
	log.Println("HELLO WORLD!")
}
