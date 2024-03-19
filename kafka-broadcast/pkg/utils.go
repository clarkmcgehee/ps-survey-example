package pkg

import (
	"encoding/csv"
	"io"
	"mime/multipart"
	"strconv"
)

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
