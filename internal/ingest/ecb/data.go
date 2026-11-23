package ecb

import (
	"encoding/csv"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
)

type genericData struct {
	XMLName xml.Name `xml:"GenericData"`
	DataSet dataSet  `xml:"DataSet"`
}

func (parsed *genericData) writeOutput(w io.Writer, dataset, format string) error {
	switch format {
	case "json":
		return parsed.writeJSONOutput(w, dataset)
	case "csv":
		return parsed.writeCSVOutput(w)
	default:
		return parsed.writeTextOutput(w)
	}
}

func (parsed *genericData) writeTextOutput(w io.Writer) error {
	for i, series := range parsed.DataSet.Series {
		if i > 0 {
			if _, err := fmt.Fprintln(w); err != nil {
				return err
			}
		}

		if _, err := fmt.Fprintln(w, "Currency Pair:"); err != nil {
			return err
		}

		for _, kv := range series.SeriesKey.Values {
			if _, err := fmt.Fprintf(w, "  %s: %s\n", kv.ID, kv.Value); err != nil {
				return err
			}
		}

		if _, err := fmt.Fprintln(w, "Observations:"); err != nil {
			return err
		}

		for _, obs := range series.Obs {
			if _, err := fmt.Fprintf(w, "  %s = %.4f\n", observationTime(obs), obs.ObsValue.Value); err != nil {
				return err
			}
		}
	}

	return nil
}

func (parsed *genericData) writeJSONOutput(w io.Writer, dataset string) error {
	output := jsonOutput{
		Dataset: dataset,
		Series:  make([]jsonSeries, 0, len(parsed.DataSet.Series)),
	}

	for _, series := range parsed.DataSet.Series {
		item := jsonSeries{
			SeriesKey:    seriesKeyMap(series),
			Observations: make([]jsonObs, 0, len(series.Obs)),
		}

		for _, obs := range series.Obs {
			item.Observations = append(item.Observations, jsonObs{
				Time:  observationTime(obs),
				Value: obs.ObsValue.Value,
			})
		}

		output.Series = append(output.Series, item)
	}

	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")

	return encoder.Encode(output)
}

func (parsed *genericData) writeCSVOutput(w io.Writer) error {
	writer := csv.NewWriter(w)
	keys := collectKeyIDs(parsed)

	header := append([]string{"series_index", "time", "value"}, keys...)
	if err := writer.Write(header); err != nil {
		return fmt.Errorf("CSV write error: %w", err)
	}

	for i, series := range parsed.DataSet.Series {
		metadata := seriesKeyMap(series)

		if len(series.Obs) == 0 {
			row := []string{strconv.Itoa(i), "", ""}
			for _, key := range keys {
				row = append(row, metadata[key])
			}

			if err := writer.Write(row); err != nil {
				return fmt.Errorf("CSV write error: %w", err)
			}

			continue
		}

		for _, obs := range series.Obs {
			row := []string{
				strconv.Itoa(i),
				observationTime(obs),
				strconv.FormatFloat(obs.ObsValue.Value, 'f', -1, 64),
			}

			for _, key := range keys {
				row = append(row, metadata[key])
			}

			if err := writer.Write(row); err != nil {
				return fmt.Errorf("CSV write error: %w", err)
			}
		}
	}

	writer.Flush()

	if err := writer.Error(); err != nil {
		return fmt.Errorf("CSV flush error: %w", err)
	}

	return nil
}

type dataSet struct {
	Series []series `xml:"Series"`
}

type series struct {
	SeriesKey seriesKey `xml:"SeriesKey"`
	Obs       []obs     `xml:"Obs"`
}

type seriesKey struct {
	Values []keyValue `xml:"Value"`
}

type keyValue struct {
	ID    string `xml:"id,attr"`
	Value string `xml:"value,attr"`
}

type obs struct {
	Time         string `xml:"Time"`
	ObsDimension struct {
		Value string `xml:"value,attr"`
	} `xml:"ObsDimension"`
	ObsValue struct {
		Value float64 `xml:"value,attr"`
	} `xml:"ObsValue"`
}

type jsonOutput struct {
	Dataset string       `json:"dataset"`
	Series  []jsonSeries `json:"series"`
}

type jsonSeries struct {
	SeriesKey    map[string]string `json:"series_key"`
	Observations []jsonObs         `json:"observations"`
}

type jsonObs struct {
	Time  string  `json:"time"`
	Value float64 `json:"value"`
}

func parseGenericData(r io.Reader) (*genericData, error) {
	var parsed genericData
	if err := xml.NewDecoder(r).Decode(&parsed); err != nil {
		return nil, fmt.Errorf("XML decode error: %w", err)
	}

	return &parsed, nil
}

func observationTime(obs obs) string {
	if strings.TrimSpace(obs.Time) != "" {
		return obs.Time
	}

	return obs.ObsDimension.Value
}

func seriesKeyMap(series series) map[string]string {
	values := make(map[string]string, len(series.SeriesKey.Values))
	for _, kv := range series.SeriesKey.Values {
		values[kv.ID] = kv.Value
	}

	return values
}

func collectKeyIDs(parsed *genericData) []string {
	set := map[string]struct{}{}

	for _, series := range parsed.DataSet.Series {
		for _, kv := range series.SeriesKey.Values {
			set[kv.ID] = struct{}{}
		}
	}

	keys := make([]string, 0, len(set))
	for key := range set {
		keys = append(keys, key)
	}

	sort.Strings(keys)

	return keys
}
