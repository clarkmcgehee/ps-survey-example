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

var (
	ps playerState = stopped
	ks kafkaState  = waiting

	kafkaProducer sarama.SyncProducer
	flightData    []map[string]float64

	clients   = make(map[*websocket.Conn]bool) // connected clients
	broadcast = make(chan string)              // broadcast channel
	upgrader  = websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool {
			return true
		},
	}
)

func main() {

	go runServer()
	go runBroadcast()

	startKafkaProducer()
	go runKafka()

	c := make(chan os.Signal, 1)
	<-c
}

func startKafkaProducer() {
	kafkaConfig := sarama.NewConfig()
	kafkaConfig.Producer.Return.Successes = true
	kafkaConfig.Producer.Return.Errors = true

	// Wait for the Kafka broker to be ready
	var err error
	for {
		kafkaProducer, err = sarama.NewSyncProducer([]string{"kafka:9092"}, kafkaConfig)
		if err == nil {
			time.Sleep(5 * time.Second)
			broadcast <- fmt.Sprintf("Waiting for the Kafka broker to be ready...\n")
			break
		}
	}
	ks = ready
	broadcast <- fmt.Sprintf("Kafka broker is ready.\n")

}

func runServer() {
	http.HandleFunc("/", handleRoot)
	http.HandleFunc("/upload", handleFileUpload)
	http.HandleFunc("/ws", handleConnections)
	http.HandleFunc("/play", handlePlay)
	http.HandleFunc("/pause", handlePause)
	http.HandleFunc("/stop", handleStop)
	http.HandleFunc("/back", handleBack)
	log.Println("Server started on localhost:8080")
	err := http.ListenAndServe(":8080", nil)
	if err != nil {
		log.Fatalf("Error starting the server: %v", err)
	}
}

func handlePlay(w http.ResponseWriter, r *http.Request) {
	ps.Play()
	broadcast <- fmt.Sprintf("Playing...")
	fmt.Fprint(w, ps.State())
}

func handlePause(w http.ResponseWriter, r *http.Request) {
	ps.Pause()
	broadcast <- fmt.Sprintf("Paused.")
	fmt.Fprint(w, ps.State())
}

func handleStop(w http.ResponseWriter, r *http.Request) {
	ps.Stop()
	broadcast <- fmt.Sprintf("Stopped.")
	fmt.Fprint(w, ps.State())
}

func handleBack(w http.ResponseWriter, r *http.Request) {
	ps.Stop()
	broadcast <- fmt.Sprintf("Reset.")
	fmt.Fprint(w, ps.State())
}

func handleConnections(w http.ResponseWriter, r *http.Request) {
	// Upgrade initial GET request to a websocket
	ws, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Fatal(err)
	}
	// Make sure we close the connection when the function returns
	defer ws.Close()

	// Register our new client
	clients[ws] = true

	for {
		_, _, err := ws.ReadMessage()
		if err != nil {
			delete(clients, ws)
			break
		}
	}
}

func runBroadcast() {
	for msg := range broadcast {
		for client := range clients {
			err := client.WriteMessage(websocket.TextMessage, []byte(msg))
			if err != nil {
				log.Printf("Websocket error: %v", err)
				client.Close()
				delete(clients, client)
			}
		}
	}
	log.Println("Broadcast channel closed.")
}

func handleRoot(writer http.ResponseWriter, request *http.Request) {
	http.FileServer(http.Dir("./web")).ServeHTTP(writer, request)
}

func handleFileUpload(w http.ResponseWriter, r *http.Request) {
	// Parse the multipart form in the request
	err := r.ParseMultipartForm(10 << 20) // 10 MB
	if err != nil {
		broadcast <- fmt.Sprintf("Error parsing multipart form: %v", err)
		http.Error(w, "Error parsing multipart form", http.StatusBadRequest)
		return
	}

	// Retrieve the file from form data
	file, _, err := r.FormFile("file") // "file" is the key of the input field of the form
	if err != nil {
		broadcast <- fmt.Sprintf("Error retrieving the file: %v", err)
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
	flightData, err = csvToMap(file)
	if err != nil {
		http.Error(w, "Error reading the file", http.StatusBadRequest)
		return
	}

	_, err = fmt.Fprint(w, "File parsed successfully.")
	if err != nil {
		log.Println("Error writing to the response writer: ", err)
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

func runKafka() {
	for {
		if ks == ready {
			ks = producing
			for i, record := range flightData {
				for ps == paused {
					time.Sleep(50 * time.Millisecond)
				}
				if ps == stopped {
					break
				}

				// Marshal the record to a JSON string
				jsonRecord, err := json.Marshal(record)
				if err != nil {
					broadcast <- fmt.Sprintf("Error marshaling record to JSON: %v", err)
					continue
				}

				// Send the JSON string to the Kafka broker
				msg := &sarama.ProducerMessage{
					Topic: "test",
					Value: sarama.StringEncoder(jsonRecord),
				}
				partition, offset, err := kafkaProducer.SendMessage(msg)
				if err != nil {
					broadcast <- fmt.Sprintf("Error sending message to Kafka: %v", err)
				} else {
					broadcast <- fmt.Sprintf("id:%d(p%d:o%d) %s", i, partition, offset, jsonRecord)
				}
				time.Sleep(50 * time.Millisecond)
			}
			ks = ready
		}
	}
}
