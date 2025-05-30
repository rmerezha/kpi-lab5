package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"time"

	"github.com/roman-mazur/architecture-practice-4-template/httptools"
	"github.com/roman-mazur/architecture-practice-4-template/signal"
)

var port = flag.Int("port", 8080, "server port")

const (
	confResponseDelaySec = "CONF_RESPONSE_DELAY_SEC"
	confHealthFailure    = "CONF_HEALTH_FAILURE"
	DB_URL               = "http://db:8080"
	TEAM_NAME            = "kpi3-test"
)

func main() {
	err := load()
	if err != nil {
		log.Fatal(err)
	}
	h := new(http.ServeMux)

	h.HandleFunc("/health", func(rw http.ResponseWriter, r *http.Request) {
		rw.Header().Set("content-type", "text/plain")
		if failConfig := os.Getenv(confHealthFailure); failConfig == "true" {
			rw.WriteHeader(http.StatusInternalServerError)
			_, _ = rw.Write([]byte("FAILURE"))
		} else {
			rw.WriteHeader(http.StatusOK)
			_, _ = rw.Write([]byte("OK"))
		}
	})

	report := make(Report)

	h.HandleFunc("/api/v1/some-data", func(rw http.ResponseWriter, r *http.Request) {
		respDelayString := os.Getenv(confResponseDelaySec)
		if delaySec, parseErr := strconv.Atoi(respDelayString); parseErr == nil && delaySec > 0 && delaySec < 300 {
			time.Sleep(time.Duration(delaySec) * time.Second)
		}
		report.Process(r)
		key := r.URL.Query().Get("key")
		if key == "" {
			rw.WriteHeader(http.StatusBadRequest)
			return
		}
		t := r.URL.Query().Get("type")
		if t == "" {
			t = "string"
		}
		rawUrl, err := url.JoinPath(DB_URL, "db", key)
		if err != nil {
			rw.WriteHeader(http.StatusInternalServerError)
			return
		}
		url, err := url.Parse(rawUrl)
		if err != nil {
			rw.WriteHeader(http.StatusInternalServerError)
			return
		}

		q := url.Query()
		q.Set("type", t)
		url.RawQuery = q.Encode()
		respFromDb, err := http.Get(url.String())
		if err != nil {
			rw.WriteHeader(http.StatusInternalServerError)
			return
		}
		defer respFromDb.Body.Close()
		if respFromDb.StatusCode == http.StatusNotFound {
			rw.WriteHeader(http.StatusNotFound)
			return
		}
		rw.Header().Set("content-type", "application/json")
		rw.WriteHeader(http.StatusOK)
		_, err = io.Copy(rw, respFromDb.Body)
		if err != nil {
			log.Printf("failed to copy response body: %v", err)
		}
	})

	h.Handle("/report", report)

	server := httptools.CreateServer(*port, h)
	server.Start()
	signal.WaitForTerminationSignal()
}

func load() error {
	url := fmt.Sprintf("%s/db/%s", DB_URL, TEAM_NAME)
	today := time.Now().Format(time.DateOnly)
	payload := map[string]string{
		"value": today,
	}
	jsonBytes, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to encode JSON: %w", err)
	}

	resp, err := http.Post(url, "application/json", bytes.NewBuffer(jsonBytes))
	if err != nil {
		return fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("unexpected response status: %s", resp.Status)
	}
	log.Printf("successfully loaded date %s", today)
	return nil
}
