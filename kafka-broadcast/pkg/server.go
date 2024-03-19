package pkg

import (
	"encoding/json"
	"fmt"
	"log"
	"mime/multipart"
	"net/http"
	"time"

	"github.com/IBM/sarama"
	"github.com/gorilla/websocket"
)

type Server struct {
	PlayerState   playerState
	KafkaState    kafkaState
	kafkaProducer sarama.SyncProducer
	flightData    []map[string]float64
	Clients       map[*websocket.Conn]bool
	Broadcast     chan string
	Upgrader      websocket.Upgrader
}

func (s *Server) StartKafkaProducer() {
	kafkaConfig := sarama.NewConfig()
	kafkaConfig.Producer.Return.Successes = true
	kafkaConfig.Producer.Return.Errors = true

	var err error
	for {
		s.kafkaProducer, err = sarama.NewSyncProducer([]string{"kafka:9092"}, kafkaConfig)
		if err == nil {
			time.Sleep(5 * time.Second)
			s.Broadcast <- fmt.Sprintf("Waiting for the Kafka broker to be Ready...\n")
			break
		}
	}
}

func (s *Server) RunServer() {
	http.HandleFunc("/", s.handleRoot)
	http.HandleFunc("/upload", s.handleFileUpload)
	http.HandleFunc("/ws", s.handleConnections)
	http.HandleFunc("/play", s.handlePlay)
	http.HandleFunc("/pause", s.handlePause)
	http.HandleFunc("/stop", s.handleStop)
	http.HandleFunc("/back", s.handleBack)
	log.Println("Server started on localhost:8080")
	err := http.ListenAndServe(":8080", nil)
	if err != nil {
		log.Fatalf("Error starting the server: %v", err)
	}
}

func (s *Server) handlePlay(w http.ResponseWriter, r *http.Request) {
	s.PlayerState.Play()
	s.Broadcast <- fmt.Sprintf("Playing...")
	fmt.Fprint(w, s.PlayerState.State())
}

func (s *Server) handlePause(w http.ResponseWriter, r *http.Request) {
	s.PlayerState.Pause()
	s.Broadcast <- fmt.Sprintf("Paused.")
	fmt.Fprint(w, s.PlayerState.State())
}

func (s *Server) handleStop(w http.ResponseWriter, r *http.Request) {
	s.PlayerState.Stop()
	s.Broadcast <- fmt.Sprintf("Stopped.")
	fmt.Fprint(w, s.PlayerState.State())
}

func (s *Server) handleBack(w http.ResponseWriter, r *http.Request) {
	s.PlayerState.Stop()
	s.Broadcast <- fmt.Sprintf("Reset.")
	fmt.Fprint(w, s.PlayerState.State())
}

func (s *Server) handleConnections(w http.ResponseWriter, r *http.Request) {
	// Upgrade initial GET request to a websocket
	ws, err := s.Upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Fatal(err)
	}
	// Make sure we close the connection when the function returns
	defer ws.Close()

	// Register our new client
	s.Clients[ws] = true

	for {
		_, _, err := ws.ReadMessage()
		if err != nil {
			delete(s.Clients, ws)
			break
		}
	}
}

func (s *Server) RunBroadcast() {
	for msg := range s.Broadcast {
		for client := range s.Clients {
			err := client.WriteMessage(websocket.TextMessage, []byte(msg))
			if err != nil {
				log.Printf("Websocket error: %v", err)
				client.Close()
				delete(s.Clients, client)
			}
		}
	}
	log.Println("Broadcast channel closed.")
}

func (s *Server) handleRoot(writer http.ResponseWriter, request *http.Request) {
	http.FileServer(http.Dir("./web")).ServeHTTP(writer, request)
}

func (s *Server) handleFileUpload(w http.ResponseWriter, r *http.Request) {
	// Parse the multipart form in the request
	err := r.ParseMultipartForm(10 << 20) // 10 MB
	if err != nil {
		s.Broadcast <- fmt.Sprintf("Error parsing multipart form: %v", err)
		http.Error(w, "Error parsing multipart form", http.StatusBadRequest)
		return
	}

	// Retrieve the file from form data
	file, _, err := r.FormFile("file") // "file" is the key of the input field of the form
	if err != nil {
		s.Broadcast <- fmt.Sprintf("Error retrieving the file: %v", err)
		http.Error(w, "Error retrieving the file", http.StatusBadRequest)
		return
	}
	defer func(file multipart.File) {
		err := file.Close()
		if err != nil {
			log.Println("Error closing the file")
		}
	}(file)

	// Convert the file to a map
	s.flightData, err = csvToMap(file)
	if err != nil {
		http.Error(w, "Error reading the file", http.StatusBadRequest)
		return
	}

	_, err = fmt.Fprint(w, "File parsed successfully.")
	if err != nil {
		log.Println("Error writing to the response writer: ", err)
	}

	s.KafkaState = Ready
	s.Broadcast <- fmt.Sprintf("Kafka broker is Ready.\n")

}

func (s *Server) RunKafka() {
	for {
		if s.KafkaState == Ready {
			s.KafkaState = Producing
			for i, record := range s.flightData {
				for s.PlayerState == Paused {
					time.Sleep(50 * time.Millisecond)
				}
				if s.PlayerState == Stopped {
					break
				}

				// Marshal the record to a JSON string
				jsonRecord, err := json.Marshal(record)
				if err != nil {
					s.Broadcast <- fmt.Sprintf("Error marshaling record to JSON: %v", err)
					continue
				}

				// Send the JSON string to the Kafka broker
				msg := &sarama.ProducerMessage{
					Topic: "test",
					Value: sarama.StringEncoder(jsonRecord),
				}
				partition, offset, err := s.kafkaProducer.SendMessage(msg)
				if err != nil {
					s.Broadcast <- fmt.Sprintf("Error sending message to Kafka: %v", err)
				} else {
					s.Broadcast <- fmt.Sprintf("id:%d(p%d:o%d) %s", i, partition, offset, jsonRecord)
				}
				time.Sleep(50 * time.Millisecond)
			}
			s.KafkaState = Ready
		}
	}
}
