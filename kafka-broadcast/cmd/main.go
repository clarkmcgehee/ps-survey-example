package main

import (
	"net/http"
	"os"

	"kafka-broadcast/pkg"

	"github.com/gorilla/websocket"
)

func main() {
	server := &pkg.Server{
		PlayerState: pkg.Stopped,
		KafkaState:  pkg.Waiting,
		Clients:     make(map[*websocket.Conn]bool),
		Broadcast:   make(chan string),
		Upgrader: websocket.Upgrader{
			CheckOrigin: func(r *http.Request) bool {
				return true
			},
		},
	}

	go server.RunServer()
	go server.RunBroadcast()

	server.StartKafkaProducer()
	go server.RunKafka()

	c := make(chan os.Signal, 1)
	<-c
}
