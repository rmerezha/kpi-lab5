package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/roman-mazur/architecture-practice-4-template/httptools"
	"github.com/roman-mazur/architecture-practice-4-template/signal"
)

var (
	port       = flag.Int("port", 8090, "load balancer port")
	timeoutSec = flag.Int("timeout-sec", 3, "request timeout time in seconds")
	https      = flag.Bool("https", false, "whether backends support HTTPs")
	traceEnabled = flag.Bool("trace", false, "whether to include tracing information into responses")
)

var (
	timeout            = time.Duration(*timeoutSec) * time.Second
	serversPoolStrings = []string{
		"server1:8080",
		"server2:8080",
		"server3:8080",
	}
)

type ServerInfo struct {
	URL          string
	Alive        bool
	TrafficBytes int64
	mux          sync.RWMutex
}

func (s *ServerInfo) SetAlive(alive bool) {
	s.mux.Lock()
	s.Alive = alive
	s.mux.Unlock()
}

func (s *ServerInfo) IsAlive() bool {
	s.mux.RLock()
	defer s.mux.RUnlock()
	return s.Alive
}

func (s *ServerInfo) AddTraffic(bytes int64) {
	s.mux.Lock()
	s.TrafficBytes += bytes
	s.mux.Unlock()
}

func (s *ServerInfo) GetTraffic() int64 {
	s.mux.RLock()
	defer s.mux.RUnlock()
	return s.TrafficBytes
}

func (s *ServerInfo) GetURL() string {
	s.mux.RLock()
	defer s.mux.RUnlock()
	return s.URL
}

var servers []*ServerInfo
var serversMux sync.RWMutex

func scheme() string {
	if *https {
		return "https"
	}
	return "http"
}

func health(server *ServerInfo) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	req, _ := http.NewRequestWithContext(ctx, "GET",
		fmt.Sprintf("%s://%s/health", scheme(), server.GetURL()), nil)
	resp, err := http.DefaultClient.Do(req)

	currentStatus := false
	if err == nil && resp.StatusCode == http.StatusOK {
		currentStatus = true
	}
	if resp != nil {
		resp.Body.Close()
	}

	if server.IsAlive() != currentStatus {
		log.Printf("Server %s health status changed: %t -> %t", server.GetURL(), server.IsAlive(), currentStatus)
	}
	server.SetAlive(currentStatus)
}

func forward(server *ServerInfo, rw http.ResponseWriter, r *http.Request) error {
	dst := server.GetURL()
	ctx, cancel := context.WithTimeout(r.Context(), timeout)
	defer cancel()

	fwdRequest := r.Clone(ctx)
	fwdRequest.RequestURI = ""
	fwdRequest.URL.Host = dst
	fwdRequest.URL.Scheme = scheme()
	fwdRequest.Host = dst

	resp, err := http.DefaultClient.Do(fwdRequest)
	if err != nil {
		log.Printf("Failed to get response from %s: %s", dst, err)
		server.SetAlive(false)
		rw.WriteHeader(http.StatusServiceUnavailable)
		return err
	}
	defer resp.Body.Close()

	for k, values := range resp.Header {
		for _, value := range values {
			rw.Header().Add(k, value)
		}
	}
	if *traceEnabled {
		rw.Header().Set("lb-from", dst)
		rw.Header().Set("lb-traffic-before", fmt.Sprintf("%d", server.GetTraffic()))
	}

	rw.WriteHeader(resp.StatusCode)

	bytesWritten, copyErr := io.Copy(rw, resp.Body)
	if copyErr != nil {
		log.Printf("Failed to write response body for %s: %s", dst, copyErr)
		return copyErr
	}

	if bytesWritten > 0 {
		server.AddTraffic(bytesWritten)
		if *traceEnabled {
			rw.Header().Set("lb-traffic-after", fmt.Sprintf("%d", server.GetTraffic()))
		}
		log.Printf("Forwarded to %s, status %d, bytes written: %d, total traffic: %d",
			dst, resp.StatusCode, bytesWritten, server.GetTraffic())
	} else {
		log.Printf("Forwarded to %s, status %d, no bytes written (or HEAD request)", dst, resp.StatusCode)
	}

	return nil
}

func selectServerLeastTraffic() *ServerInfo {
	serversMux.RLock()
	defer serversMux.RUnlock()

	var selectedServer *ServerInfo
	minTraffic := int64(-1)

	availableServers := make([]*ServerInfo, 0)
	for _, server := range servers {
		if server.IsAlive() {
			availableServers = append(availableServers, server)
		}
	}

	if len(availableServers) == 0 {
		return nil
	}

	for _, server := range availableServers {
		currentServerTraffic := server.GetTraffic()
		if selectedServer == nil || currentServerTraffic < minTraffic {
			minTraffic = currentServerTraffic
			selectedServer = server
		}
	}

	return selectedServer
}

func main() {
	flag.Parse()

	servers = make([]*ServerInfo, 0, len(serversPoolStrings))
	for _, serverURL := range serversPoolStrings {
		servers = append(servers, &ServerInfo{
			URL:          serverURL,
			Alive:        true,
			TrafficBytes: 0,
		})
	}

	if len(servers) == 0 {
		log.Fatal("No servers configured in serversPoolStrings.")
	}

	for _, server := range servers {
		s := server
		go func() {
			for {
				health(s)
				time.Sleep(10 * time.Second)
			}
		}()
	}

	frontend := httptools.CreateServer(*port, http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		selectedServer := selectServerLeastTraffic()

		if selectedServer == nil {
			log.Println("No healthy servers available to handle the request.")
			http.Error(rw, "Service unavailable", http.StatusServiceUnavailable)
			return
		}

		log.Printf("Selected server %s with traffic %d bytes", selectedServer.GetURL(), selectedServer.GetTraffic())
		err := forward(selectedServer, rw, r)
		if err != nil {
			log.Printf("Error during forwarding to %s: %v", selectedServer.GetURL(), err)
		}
	}))

	log.Println("Starting load balancer on port", *port)
	log.Printf("Tracing support enabled: %t", *traceEnabled)
	frontend.Start()
	signal.WaitForTerminationSignal()
}