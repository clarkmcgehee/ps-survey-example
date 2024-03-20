package main

import (
	"net/http"
	"os"

	"github.com/gorilla/websocket"

	"survey-service/pkg"
)

func main() {
	server := &pkg.Server{
		PlayerState: pkg.Stopped,
		KafkaState:  pkg.Disconnected,
		Messages:    make(chan map[string]float64),
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

	server.StartKafkaConsumer()
	go server.RunKafka()
	go server.ProcessMessages()

	c := make(chan os.Signal, 1)
	<-c
}
