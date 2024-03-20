# ps-survey-example
Example implementation of a real-time survey method calibration technique.

Run `docker-compose up --build` to build the project and start the Kafka broker, Zookeeper, producer, and consumer. The producer will be available at `localhost:8080` and the consumer at `localhost:8081`.

You can use either of the `sample_data_{1,2}.csv` .csv files to test the producer. The `sample_data` files are in the `sample_data` directory.

Use the `weather_balloon_data.csv` file in the consumer (serves as the truth source for the survey method).


