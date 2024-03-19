package main

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/IBM/sarama"
	"github.com/gorilla/websocket"
)

type playerState int

const (
	stopped playerState = iota
	playing
	paused
)

func (p *playerState) Play() {
	switch *p {
	case stopped:
		*p = playing
	case playing:
		return
	case paused:
		*p = playing
	}
}

func (p *playerState) Pause() {
	switch *p {
	case stopped:
		*p = paused
	case playing:
		*p = paused
	case paused:
		*p = stopped
	}
}

func (p *playerState) Stop() {
	switch *p {
	case stopped:
		return
	case playing:
		*p = stopped
	case paused:
		*p = stopped
	}
}

func (p playerState) State() string {
	switch p {
	case stopped:
		return "stop"
	case playing:
		return "play"
	case paused:
		return "pause"
	}
	return "unknown"
}

type kafkaState int

const (
	waiting kafkaState = iota
	ready
	producing
)

type Server struct {
	ps            playerState
	ks            kafkaState
	kafkaProducer sarama.SyncProducer
	flightData    []map[string]float64
	clients       map[*websocket.Conn]bool
	broadcast     chan string
	upgrader      websocket.Upgrader
}

func (s *Server) startKafkaProducer() {
	kafkaConfig := sarama.NewConfig()
	kafkaConfig.Producer.Return.Successes = true
	kafkaConfig.Producer.Return.Errors = true

	var err error
	for {
		s.kafkaProducer, err = sarama.NewSyncProducer([]string{"kafka:9092"}, kafkaConfig)
		if err == nil {
			time.Sleep(5 * time.Second)
			s.broadcast <- fmt.Sprintf("Waiting for the Kafka broker to be ready...\n")
			break
		}
	}

}

func (s *Server) runServer() {
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
	s.ps.Play()
	s.broadcast <- fmt.Sprintf("Playing...")
	fmt.Fprint(w, s.ps.State())
}

func (s *Server) handlePause(w http.ResponseWriter, r *http.Request) {
	s.ps.Pause()
	s.broadcast <- fmt.Sprintf("Paused.")
	fmt.Fprint(w, s.ps.State())
}

func (s *Server) handleStop(w http.ResponseWriter, r *http.Request) {
	s.ps.Stop()
	s.broadcast <- fmt.Sprintf("Stopped.")
	fmt.Fprint(w, s.ps.State())
}

func (s *Server) handleBack(w http.ResponseWriter, r *http.Request) {
	s.ps.Stop()
	s.broadcast <- fmt.Sprintf("Reset.")
	fmt.Fprint(w, s.ps.State())
}

func (s *Server) handleConnections(w http.ResponseWriter, r *http.Request) {
	// Upgrade initial GET request to a websocket
	ws, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Fatal(err)
	}
	// Make sure we close the connection when the function returns
	defer ws.Close()

	// Register our new client
	s.clients[ws] = true

	for {
		_, _, err := ws.ReadMessage()
		if err != nil {
			delete(s.clients, ws)
			break
		}
	}
}

func (s *Server) runBroadcast() {
	for msg := range s.broadcast {
		for client := range s.clients {
			err := client.WriteMessage(websocket.TextMessage, []byte(msg))
			if err != nil {
				log.Printf("Websocket error: %v", err)
				client.Close()
				delete(s.clients, client)
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
		s.broadcast <- fmt.Sprintf("Error parsing multipart form: %v", err)
		http.Error(w, "Error parsing multipart form", http.StatusBadRequest)
		return
	}

	// Retrieve the file from form data
	file, _, err := r.FormFile("file") // "file" is the key of the input field of the form
	if err != nil {
		s.broadcast <- fmt.Sprintf("Error retrieving the file: %v", err)
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
	s.ks = ready
	s.broadcast <- fmt.Sprintf("Kafka broker is ready.\n")
}

func (s *Server) runKafka() {
	for {
		if s.ks == ready {
			s.ks = producing
			for i, record := range s.flightData {
				for s.ps == paused {
					time.Sleep(50 * time.Millisecond)
				}
				if s.ps == stopped {
					break
				}

				// Marshal the record to a JSON string
				jsonRecord, err := json.Marshal(record)
				if err != nil {
					s.broadcast <- fmt.Sprintf("Error marshaling record to JSON: %v", err)
					continue
				}

				// Send the JSON string to the Kafka broker
				msg := &sarama.ProducerMessage{
					Topic: "test",
					Value: sarama.StringEncoder(jsonRecord),
				}
				partition, offset, err := s.kafkaProducer.SendMessage(msg)
				if err != nil {
					s.broadcast <- fmt.Sprintf("Error sending message to Kafka: %v", err)
				} else {
					s.broadcast <- fmt.Sprintf("id:%d(p%d:o%d) %s", i, partition, offset, jsonRecord)
				}
				time.Sleep(50 * time.Millisecond)
			}
			s.ks = ready
		}
	}
}

func csvToMap(file multipart.File) ([]map[string]float64, error) {

	// Create a new reader
	reader := csv.NewReader(file)

	// Read the first line (column headers)
	headers, err := reader.Read()
	if err != nil {
		return nil, err
	}
	var data []map[string]float64
	// Read the rest of the file
	for {
		record, err := reader.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}

		d := make(map[string]float64)
		// Convert each string in the record to a float64 and store it in the map
		for i, str := range record {
			num, err := strconv.ParseFloat(str, 64)
			if err != nil {
				return nil, err
			}
			d[headers[i]] = num
		}
		data = append(data, d)
	}

	return data, nil
}

func main() {
	server := &Server{
		ps:        stopped,
		ks:        waiting,
		clients:   make(map[*websocket.Conn]bool),
		broadcast: make(chan string),
		upgrader: websocket.Upgrader{
			CheckOrigin: func(r *http.Request) bool {
				return true
			},
		},
	}

	go server.runServer()
	go server.runBroadcast()

	server.startKafkaProducer()
	go server.runKafka()

	c := make(chan os.Signal, 1)
	<-c
}
