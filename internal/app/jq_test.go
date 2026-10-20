package app

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

func TestJQFiltersAndPreservesIntegers(t *testing.T) {
	for _, tc := range []struct {
		query, input string
		want         []string
	}{
		{`.items[] | select(.active == true) | .name`, `{"items":[{"name":"Floe","active":true},{"name":"Other","active":false}]}`, []string{`"Floe"`}},
		{`map(.n) | sort`, `[{"n":3},{"n":1}]`, []string{`[1,3]`}},
		{`.id`, `{"id":900719925474099312345}`, []string{`900719925474099312345`}},
		{`.[]`, `[null,false,0]`, []string{`null`, `false`, `0`}},
		{`empty`, `{}`, []string{}},
		{`env`, `{}`, []string{`{}`}},
		{`keys`, `{"z":0,"a":1}`, []string{`["a","z"]`}},
	} {
		t.Run(tc.query, func(t *testing.T) {
			body, _ := json.Marshal(map[string]any{"query": tc.query, "input": json.RawMessage(tc.input)})
			response := httptest.NewRecorder()
			(&Server{}).api(response, httptest.NewRequest(http.MethodPost, "/api/v1/tools/jq", bytes.NewReader(body)))
			if response.Code != http.StatusOK {
				t.Fatalf("status = %d: %s", response.Code, response.Body.String())
			}
			var result struct {
				Results []string `json:"results"`
			}
			if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(result.Results, tc.want) {
				t.Fatalf("results = %v, want %v", result.Results, tc.want)
			}
		})
	}
}

func TestJQRejectsInvalidQueriesAndBoundsOutput(t *testing.T) {
	for _, tc := range []struct {
		query string
		code  int
	}{
		{`.items[`, 400}, {`undefined_function`, 400}, {`.a[]`, 400},
		{`import "file" as x; x`, 400}, {`range(0; 6000)`, 413}, {strings.Repeat(".", maxJQQueryLength+1), 400},
	} {
		body, _ := json.Marshal(map[string]any{"query": tc.query, "input": map[string]any{}})
		response := httptest.NewRecorder()
		(&Server{}).runJQ(response, httptest.NewRequest(http.MethodPost, "/api/v1/tools/jq", bytes.NewReader(body)))
		if response.Code != tc.code {
			t.Errorf("query %q: status = %d, want %d: %s", tc.query, response.Code, tc.code, response.Body.String())
		}
	}
	for _, body := range []string{`{"input":`, `{"query":"."}`, `{"input":{},"query":""}`} {
		response := httptest.NewRecorder()
		(&Server{}).runJQ(response, httptest.NewRequest(http.MethodPost, "/api/v1/tools/jq", strings.NewReader(body)))
		if response.Code != 400 {
			t.Errorf("invalid request returned %d", response.Code)
		}
	}
}
