package ecb

import (
	"bytes"
	"encoding/xml"
	"math"
	"strings"
	"testing"
)

const testObservationDate = "2024-07-01"

func TestParseGenericData(t *testing.T) {
	xmlInput := `
<GenericData>
  <DataSet>
    <Series>
      <SeriesKey>
        <Value id="FREQ" value="D"></Value>
        <Value id="CURRENCY" value="USD"></Value>
      </SeriesKey>
      <Obs>
        <Time>2024-07-01</Time>
        <ObsValue value="0.9300"></ObsValue>
      </Obs>
      <Obs>
        <Time>2024-07-02</Time>
        <ObsValue value="0.9287"></ObsValue>
      </Obs>
    </Series>
  </DataSet>
</GenericData>`

	parsed, err := parseGenericData(strings.NewReader(xmlInput))
	if err != nil {
		t.Fatalf("parseGenericData returned error: %v", err)
	}

	if len(parsed.DataSet.Series) != 1 {
		t.Fatalf("expected 1 series, got %d", len(parsed.DataSet.Series))
	}

	series := parsed.DataSet.Series[0]

	if len(series.SeriesKey.Values) != 2 {
		t.Fatalf("expected 2 series key values, got %d", len(series.SeriesKey.Values))
	}

	if series.SeriesKey.Values[0].ID != "FREQ" || series.SeriesKey.Values[0].Value != "D" {
		t.Fatalf("unexpected first series key: %+v", series.SeriesKey.Values[0])
	}

	if len(series.Obs) != 2 {
		t.Fatalf("expected 2 observations, got %d", len(series.Obs))
	}

	if series.Obs[0].Time != testObservationDate {
		t.Fatalf("unexpected observation time: %s", series.Obs[0].Time)
	}

	if math.Abs(series.Obs[0].ObsValue.Value-0.93) > 1e-9 {
		t.Fatalf("unexpected observation value: %f", series.Obs[0].ObsValue.Value)
	}
}

func TestWriteCSVOutput(t *testing.T) {
	parsed := &genericData{
		XMLName: xml.Name{Space: "", Local: ""},
		DataSet: dataSet{
			Series: []series{
				{
					SeriesKey: seriesKey{
						Values: []keyValue{
							{ID: "FREQ", Value: "D"},
							{ID: "CURRENCY", Value: "USD"},
						},
					},
					Obs: []obs{
						{
							Time: testObservationDate,
							ObsDimension: struct {
								Value string `xml:"value,attr"`
							}{Value: ""},
							ObsValue: struct {
								Value float64 `xml:"value,attr"`
							}{Value: 0.93},
						},
					},
				},
			},
		},
	}

	var buf bytes.Buffer
	if err := parsed.writeCSVOutput(&buf); err != nil {
		t.Fatalf("writeCSVOutput returned error: %v", err)
	}

	output := strings.TrimSpace(buf.String())

	if !strings.Contains(output, "series_index,time,value,CURRENCY,FREQ") {
		t.Fatalf("unexpected CSV header: %s", output)
	}

	if !strings.Contains(output, "0,2024-07-01,0.93,USD,D") {
		t.Fatalf("unexpected CSV row: %s", output)
	}
}

func TestParseGenericData_ObsDimension(t *testing.T) {
	xmlInput := `
<GenericData>
  <DataSet>
    <Series>
      <SeriesKey>
        <Value id="FREQ" value="D"></Value>
        <Value id="CURRENCY" value="USD"></Value>
      </SeriesKey>
      <Obs>
        <ObsDimension value="2024-07-01"></ObsDimension>
        <ObsValue value="0.9300"></ObsValue>
      </Obs>
    </Series>
  </DataSet>
</GenericData>`

	parsed, err := parseGenericData(strings.NewReader(xmlInput))
	if err != nil {
		t.Fatalf("parseGenericData returned error: %v", err)
	}

	if got := observationTime(parsed.DataSet.Series[0].Obs[0]); got != "2024-07-01" {
		t.Fatalf("expected observation time 2024-07-01, got %q", got)
	}
}
