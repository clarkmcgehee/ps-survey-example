package main

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math"
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
	disconnected kafkaState = iota
	waiting
	ready
	consuming
)

var (
	ps playerState = stopped
	ks kafkaState  = disconnected

	kafkaConsumer sarama.Consumer
	messages      = make(chan map[string]float64)

	clients   = make(map[*websocket.Conn]bool) // connected clients
	broadcast = make(chan string)              // broadcast channel
	upgrader  = websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool {
			return true
		},
	}

	weatherData map[string][]float64
	atmosZ      []float64
	atmosPa     []float64
)

func main() {
	go runServer()
	go runBroadcast()

	startKafkaConsumer()
	go runKafka()
	go processMessages()

	c := make(chan os.Signal, 1)
	<-c
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

func handlePlay(w http.ResponseWriter, r *http.Request) {
	ps.Play()
	fmt.Fprint(w, ps.State())
}

func handlePause(w http.ResponseWriter, r *http.Request) {
	ps.Pause()
	fmt.Fprint(w, ps.State())
}

func runServer() {
	http.HandleFunc("/", handleRoot)
	http.HandleFunc("/upload", handleFileUpload)
	http.HandleFunc("/ws", handleConnections)
	http.HandleFunc("/play", handlePlay)
	http.HandleFunc("/pause", handlePause)
	log.Println("Server started on localhost:8081")
	err := http.ListenAndServe(":8081", nil)
	if err != nil {
		log.Fatalf("Error starting the server: %v", err)
	}
}

func startKafkaConsumer() {
	config := sarama.NewConfig()
	config.Consumer.Return.Errors = true

	brokers := []string{"kafka:9092"}

	var err error
	for {
		kafkaConsumer, err = sarama.NewConsumer(brokers, config)
		if err == nil {
			break
		}
		log.Println("Failed to start consumer:", err)
		time.Sleep(5 * time.Second)
	}

	ks = waiting
}

func runKafka() {
	for ks == waiting || ks == disconnected {
		broadcast <- "Waiting for Kafka to be ready...be sure to upload the weather data."
		time.Sleep(5 * time.Second)
	}

	partitionConsumer, err := kafkaConsumer.ConsumePartition("test", 0, sarama.OffsetNewest)
	if err != nil {
		log.Fatalln("Failed to start partition consumer:", err)
	}
	ks = consuming

	for {
		for ps == playing {
			if ps == paused {
				continue
			}
			select {
			case msg := <-partitionConsumer.Messages():
				var message map[string]float64
				if err := json.Unmarshal(msg.Value, &message); err != nil {
					log.Println("Failed to unmarshal message:", err)
					continue
				}
				messages <- message
			case err := <-partitionConsumer.Errors():
				log.Println("Failed to receive message:", err)
			}
		}
	}
}

func processMessages() {
	for message := range messages {
		z := message["height_ft"]
		pstatic := message["static_pres_psi"]
		ptotal := message["total_pres_psi"]
		qcicPs := (ptotal - pstatic) / pstatic
		mach := calculateMach(qcicPs)
		pa := interpolateAtmosphere(z)
		dppPs := (pstatic - pa) / pstatic
		j, err := json.Marshal(map[string]float64{
			"mach":  mach,
			"dppPs": dppPs,
		})
		if err != nil {
			log.Println("Failed to marshal message:", err)
			continue
		}
		broadcast <- string(j)
	}
}

func minValue(slice []float64) float64 {
	m := slice[0]
	for _, value := range slice {
		if m > value {
			m = value
		}
	}
	return m
}

func maxValue(slice []float64) float64 {
	m := slice[0]
	for _, value := range slice {
		if m < value {
			m = value
		}
	}
	return m
}

func interpolateAtmosphere(z float64) float64 {
	if atmosZ == nil || atmosPa == nil || len(atmosZ) == 0 || len(atmosPa) == 0 {
		log.Println("atmosZ or atmosPa is not initialized")
		return math.NaN()
	}
	if z < minValue(atmosZ) || z > maxValue(atmosZ) {
		return math.NaN()
	}
	for i := 0; i < len(atmosZ); i++ {
		if atmosZ[i] > z {
			return atmosPa[i-1] + (atmosPa[i]-atmosPa[i-1])/(atmosZ[i]-atmosZ[i-1])*(z-atmosZ[i-1])
		}
	}
	return math.NaN() // return NaN if no value is found
}

func calculateMach(qcPa float64) float64 {
	var mach float64
	if qcPa < 0.0 {
		mach = 0.0
	} else if qcPa <= 0.89293 {
		mach = math.Sqrt(5.0 * (math.Pow(qcPa+1.0, 2.0/7.0) - 1.0))
	} else {
		f := func(m float64) float64 {
			return m - 0.881284*math.Sqrt((qcPa+1)*math.Pow(1.0-(1.0/(7.0*m*m)), 2.5))
		}
		machLower := 0.0
		machUpper := 10.0
		for i := 0; i < 100; i++ {
			machMid := (machLower + machUpper) / 2.0
			if f(machMid)*f(machLower) < 0 {
				machUpper = machMid
			} else {
				machLower = machMid
			}
			if math.Abs(machUpper-machLower) < 1e-5 {
				break
			}
		}
		mach = (machLower + machUpper) / 2.0
	}
	return mach
}

func csvToMap(file multipart.File) (map[string][]float64, error) {

	// Create a new reader
	reader := csv.NewReader(file)

	// Read the first line (column headers)
	headers, err := reader.Read()
	if err != nil {
		return nil, err
	}

	data := make(map[string][]float64)
	// Read the rest of the file
	for {
		record, err := reader.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}

		// Convert each string in the record to a float64 and store it in the map
		for i, str := range record {
			num, err := strconv.ParseFloat(str, 64)
			if err != nil {
				return nil, err
			}
			data[headers[i]] = append(data[headers[i]], num)
		}
	}

	return data, nil
}

func handleFileUpload(w http.ResponseWriter, r *http.Request) {
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
	weatherData, err = csvToMap(file)
	if err != nil {
		http.Error(w, "Error reading the file", http.StatusBadRequest)
		return
	}

	atmosZ = weatherData["ALT"]
	atmosPa = weatherData["PRESS"]
	for i := 0; i < len(atmosPa); i++ {
		atmosPa[i] *= 0.0145038
	}

	log.Println("AtmosZ is: ", atmosZ)
	log.Println("AtmosPa is: ", atmosPa)

	_, err = fmt.Fprint(w, "File parsed successfully.")
	if err != nil {
		log.Println("Error writing to the response writer: ", err)
	}
	ks = ready
}
