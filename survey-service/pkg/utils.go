package pkg

import (
	"encoding/csv"
	"io"
	"math"
	"mime/multipart"
	"strconv"
)

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
