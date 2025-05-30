package integration

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"testing"
	"time"
)

const baseAddress = "http://balancer:8090"

var client = http.Client{
	Timeout: 3 * time.Second,
}

func TestBalancer(t *testing.T) {
	if _, exists := os.LookupEnv("INTEGRATION_TEST"); !exists {
		t.Skip("Integration test is not enabled")
	}

	const totalRequests = 6
	const minSeen = 2
	var today = time.Now().Format(time.DateOnly)
	seen := make(map[string]int)

	for i := 0; i < totalRequests; i++ {
		resp, err := client.Get(fmt.Sprintf("%s/api/v1/some-data?key=%s", baseAddress, "kpi3-test"))
		if err != nil {
			t.Fatalf("Request %d failed: %v", i+1, err)
		}
		from := resp.Header.Get("lb-from")
		if from == "" {
			t.Errorf("Request %d: missing 'lb-from' header", i+1)
		}

		type RespJson struct {
			Value string `json:"value"`
		}

		respJson := RespJson{}
		err = json.NewDecoder(resp.Body).Decode(&respJson)
		if err != nil {
			t.Errorf("Request %d: failed to decode response body: %v", i+1, err)
		}
		if respJson.Value != today {
			t.Errorf("Request %d: expected %s, got %s", i+1, today, respJson.Value)
		}

		seen[from]++
		err = resp.Body.Close()
		if err != nil {
			t.Fatalf("Request %d: failed to close response body: %v", i+1, err)
		}
		t.Logf("Request %d → Response from [%s]", i+1, from)
	}

	if len(seen) < minSeen {
		t.Errorf("Expected responses from multiple backends, got: %v", seen)
	}
}

func BenchmarkBalancer(b *testing.B) {
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			resp, err := client.Get(fmt.Sprintf("%s/api/v1/some-data", baseAddress))
			if err != nil {
				b.Errorf("Request failed: %v", err)
				continue
			}
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
		}
	})
}
