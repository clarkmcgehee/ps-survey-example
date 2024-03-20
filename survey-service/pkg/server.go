package pkg

import (
	"encoding/json"
	"fmt"
	"log"
	"math"
	"mime/multipart"
	"net/http"
	"time"

	"github.com/IBM/sarama"
	"github.com/gorilla/websocket"
)

type Server struct {
	PlayerState   PlayerState
	KafkaState    KafkaState
	KafkaConsumer sarama.Consumer
	Messages      chan map[string]float64
	Clients       map[*websocket.Conn]bool
	Broadcast     chan string
	Upgrader      websocket.Upgrader
	WeatherData   map[string][]float64
	AtmosZ        []float64
	AtmosPa       []float64
}

func (s *Server) handleConnections(w http.ResponseWriter, r *http.Request) {
	// Upgrade initial GET request to a websocket
	ws, err := s.Upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Fatal(err)
	}
	// Make sure we close the connection when the function returns
	defer func(ws *websocket.Conn) {
		err := ws.Close()
		if err != nil {
			log.Println("Error closing the websocket connection:", err)
		}
	}(ws)

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
				err := client.Close()
				if err != nil {
					log.Println("Error closing the websocket connection:", err)
				}
				delete(s.Clients, client)
			}
		}
	}
	log.Println("Broadcast channel closed.")
}

func (s *Server) handleRoot(writer http.ResponseWriter, request *http.Request) {
	http.FileServer(http.Dir("./web")).ServeHTTP(writer, request)
}

func (s *Server) handlePlay(w http.ResponseWriter, _ *http.Request) {
	s.PlayerState.Play()
	_, err := fmt.Fprint(w, s.PlayerState.State())
	if err != nil {
		log.Println("Error writing to the response writer: ", err)
	}
}

func (s *Server) handlePause(w http.ResponseWriter, _ *http.Request) {
	s.PlayerState.Pause()
	_, err := fmt.Fprint(w, s.PlayerState.State())
	if err != nil {
		log.Println("Error writing to the response writer: ", err)
	}
}

func (s *Server) RunServer() {
	http.HandleFunc("/", s.handleRoot)
	http.HandleFunc("/upload", s.handleFileUpload)
	http.HandleFunc("/ws", s.handleConnections)
	http.HandleFunc("/play", s.handlePlay)
	http.HandleFunc("/pause", s.handlePause)
	log.Println("Server started on localhost:8081")
	err := http.ListenAndServe(":8081", nil)
	if err != nil {
		log.Fatalf("Error starting the server: %v", err)
	}
}

func (s *Server) StartKafkaConsumer() {
	config := sarama.NewConfig()
	config.Consumer.Return.Errors = true

	brokers := []string{"kafka:9092"}

	var err error
	for {
		s.KafkaConsumer, err = sarama.NewConsumer(brokers, config)
		if err == nil {
			break
		}
		log.Println("Failed to start consumer:", err)
		time.Sleep(5 * time.Second)
	}

	s.KafkaState = Waiting
}

func (s *Server) RunKafka() {
	for s.KafkaState == Waiting || s.KafkaState == Disconnected {
		s.Broadcast <- "Waiting for Kafka to be ready...be sure to upload the weather data."
		time.Sleep(5 * time.Second)
	}

	partitionConsumer, err := s.KafkaConsumer.ConsumePartition("test", 0, sarama.OffsetNewest)
	if err != nil {
		log.Fatalln("Failed to start partition consumer:", err)
	}
	s.KafkaState = Consuming

	for {
		for s.PlayerState == Playing {
			if s.PlayerState == Paused {
				continue
			}
			select {
			case msg := <-partitionConsumer.Messages():
				var message map[string]float64
				if err := json.Unmarshal(msg.Value, &message); err != nil {
					log.Println("Failed to unmarshal message:", err)
					continue
				}
				s.Messages <- message
			case err := <-partitionConsumer.Errors():
				log.Println("Failed to receive message:", err)
			}
		}
	}
}

func (s *Server) ProcessMessages() {
	for message := range s.Messages {
		z := message["height_ft"]
		pstatic := message["static_pres_psi"]
		ptotal := message["total_pres_psi"]
		qcicPs := (ptotal - pstatic) / pstatic
		mach := calculateMach(qcicPs)
		pa := s.interpolateAtmosphere(z)
		dppPs := (pstatic - pa) / pstatic
		j, err := json.Marshal(map[string]float64{
			"mach":  mach,
			"dppPs": dppPs,
		})
		if err != nil {
			log.Println("Failed to marshal message:", err)
			continue
		}
		s.Broadcast <- string(j)
	}
}

func (s *Server) interpolateAtmosphere(z float64) float64 {
	if s.AtmosZ == nil || s.AtmosPa == nil || len(s.AtmosZ) == 0 || len(s.AtmosPa) == 0 {
		log.Println("s.AtmosZ or s.s.AtmosPa is not initialized")
		return math.NaN()
	}
	if z < minValue(s.AtmosZ) || z > maxValue(s.AtmosZ) {
		return math.NaN()
	}
	for i := 0; i < len(s.AtmosZ); i++ {
		if s.AtmosZ[i] > z {
			return s.AtmosPa[i-1] + (s.AtmosPa[i]-s.AtmosPa[i-1])/(s.AtmosZ[i]-s.AtmosZ[i-1])*(z-s.AtmosZ[i-1])
		}
	}
	return math.NaN() // return NaN if no value is found
}

func (s *Server) handleFileUpload(w http.ResponseWriter, r *http.Request) {
	// Parse the multipart form in the request
	err := r.ParseMultipartForm(10 << 20) // 10 MB
	if err != nil {
		http.Error(w, "Error parsing multipart form", http.StatusBadRequest)
		return
	}

	// Retrieve the file from form data
	file, _, err := r.FormFile("file") // "file" is the key of the input field of the form
	if err != nil {
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
	s.WeatherData, err = csvToMap(file)
	if err != nil {
		http.Error(w, "Error reading the file", http.StatusBadRequest)
		return
	}

	s.AtmosZ = s.WeatherData["ALT"]
	s.AtmosPa = s.WeatherData["PRESS"]
	for i := 0; i < len(s.AtmosPa); i++ {
		s.AtmosPa[i] *= 0.0145038
	}

	log.Println("s.AtmosZ is: ", s.AtmosZ)
	log.Println("s.AtmosPa is: ", s.AtmosPa)

	_, err = fmt.Fprint(w, "File parsed successfully.")
	if err != nil {
		log.Println("Error writing to the response writer: ", err)
	}
	s.KafkaState = Ready
}
