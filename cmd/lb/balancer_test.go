package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestServerInfo_Methods(t *testing.T) {
	s := &ServerInfo{URL: "test", Alive: true, TrafficBytes: 100}

	if !s.IsAlive() {
		t.Error("Expected IsAlive to be true")
	}
	s.SetAlive(false)
	if s.IsAlive() {
		t.Error("Expected IsAlive to be false after SetAlive(false)")
	}

	if s.GetTraffic() != 100 {
		t.Errorf("Expected GetTraffic to be 100, got %d", s.GetTraffic())
	}
	s.AddTraffic(50)
	if s.GetTraffic() != 150 {
		t.Errorf("Expected GetTraffic to be 150 after AddTraffic(50), got %d", s.GetTraffic())
	}
	if s.GetURL() != "test" {
		t.Errorf("Expected GetURL to be 'test', got '%s'", s.GetURL())
	}
}

func TestHealth(t *testing.T) {
	originalTimeout := timeout
	timeout = 100 * time.Millisecond
	defer func() { timeout = originalTimeout }()

	tests := []struct {
		name           string
		handler        http.HandlerFunc
		expectedAlive  bool
		initialAlive   bool
	}{
		{
			name: "healthy server",
			handler: func(rw http.ResponseWriter, r *http.Request) {
				rw.WriteHeader(http.StatusOK)
			},
			expectedAlive: true,
			initialAlive:  false,
		},
		{
			name: "unhealthy server (500)",
			handler: func(rw http.ResponseWriter, r *http.Request) {
				rw.WriteHeader(http.StatusInternalServerError)
			},
			expectedAlive: false,
			initialAlive:  true,
		},
		{
			name: "server not reachable (timeout)",
			handler: func(rw http.ResponseWriter, r *http.Request) {
				time.Sleep(200 * time.Millisecond)
				rw.WriteHeader(http.StatusOK)
			},
			expectedAlive: false,
			initialAlive:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			testServer := httptest.NewServer(tt.handler)
			defer testServer.Close()
			serverURL := strings.TrimPrefix(testServer.URL, "http://")
			sInfo := &ServerInfo{URL: serverURL, Alive: tt.initialAlive}
			health(sInfo)
			if sInfo.IsAlive() != tt.expectedAlive {
				t.Errorf("Expected server %s alive status to be %t, got %t", sInfo.URL, tt.expectedAlive, sInfo.IsAlive())
			}
		})
	}
}

func TestSelectServerLeastTraffic(t *testing.T) {
	tests := []struct {
		name           string
		setupServers   func() []*ServerInfo
		expectedURL    string
		expectNil      bool
	}{
		{
			name: "no servers",
			setupServers: func() []*ServerInfo {
				return []*ServerInfo{}
			},
			expectNil: true,
		},
		{
			name: "all servers unhealthy",
			setupServers: func() []*ServerInfo {
				return []*ServerInfo{
					{URL: "s1", Alive: false, TrafficBytes: 10},
					{URL: "s2", Alive: false, TrafficBytes: 0},
				}
			},
			expectNil: true,
		},
		{
			name: "one healthy server",
			setupServers: func() []*ServerInfo {
				return []*ServerInfo{
					{URL: "s1", Alive: true, TrafficBytes: 100},
					{URL: "s2", Alive: false, TrafficBytes: 0},
				}
			},
			expectedURL: "s1",
		},
		{
			name: "multiple healthy servers, select least traffic",
			setupServers: func() []*ServerInfo {
				return []*ServerInfo{
					{URL: "s1", Alive: true, TrafficBytes: 100},
					{URL: "s2", Alive: true, TrafficBytes: 50},
					{URL: "s3", Alive: true, TrafficBytes: 200},
					{URL: "s4", Alive: false, TrafficBytes: 10},
				}
			},
			expectedURL: "s2",
		},
		{
			name: "multiple healthy servers, same least traffic (picks first one found typically)",
			setupServers: func() []*ServerInfo {
				return []*ServerInfo{
					{URL: "s1", Alive: true, TrafficBytes: 100},
					{URL: "s2", Alive: true, TrafficBytes: 50},
					{URL: "s3", Alive: true, TrafficBytes: 50},
					{URL: "s4", Alive: true, TrafficBytes: 200},
				}
			},
			expectedURL: "s2",
		},
	}

	originalServers := servers
	defer func() { servers = originalServers }()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			servers = tt.setupServers()
			selected := selectServerLeastTraffic()

			if tt.expectNil {
				if selected != nil {
					t.Errorf("Expected nil server, got %v", selected)
				}
				return
			}

			if selected == nil {
				t.Errorf("Expected server %s, got nil", tt.expectedURL)
				return
			}
			if selected.GetURL() != tt.expectedURL {
				t.Errorf("Expected server %s, got %s", tt.expectedURL, selected.GetURL())
			}
		})
	}
}


func TestForward(t *testing.T) {
	originalTimeout := timeout
	timeout = 200 * time.Millisecond
	defer func() { timeout = originalTimeout }()

	serverBody := "Hello from backend"
	backendServer := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		rw.Header().Set("X-Backend-Header", "BackendValue")
		rw.WriteHeader(http.StatusOK)
		fmt.Fprint(rw, serverBody)
	}))
	defer backendServer.Close()

	backendURL := strings.TrimPrefix(backendServer.URL, "http://")
	sInfo := &ServerInfo{URL: backendURL, Alive: true, TrafficBytes: 0}

	req, err := http.NewRequest("GET", "/testpath", nil)
	if err != nil {
		t.Fatal(err)
	}

	rr := httptest.NewRecorder()
	
	originalTraceEnabled := *traceEnabled
	*traceEnabled = true
	defer func() { *traceEnabled = originalTraceEnabled }()


	err = forward(sInfo, rr, req)
	if err != nil {
		t.Fatalf("forward returned an error: %v", err)
	}

	if status := rr.Code; status != http.StatusOK {
		t.Errorf("handler returned wrong status code: got %v want %v", status, http.StatusOK)
	}

	if rr.Body.String() != serverBody {
		t.Errorf("handler returned unexpected body: got %v want %v", rr.Body.String(), serverBody)
	}

	if rr.Header().Get("X-Backend-Header") != "BackendValue" {
		t.Errorf("X-Backend-Header not copied: got '%s'", rr.Header().Get("X-Backend-Header"))
	}
	
	if rr.Header().Get("lb-from") != backendURL {
		t.Errorf("lb-from header is incorrect: got '%s', want '%s'", rr.Header().Get("lb-from"), backendURL)
	}
	if rr.Header().Get("lb-traffic-before") != "0" {
		t.Errorf("lb-traffic-before header is incorrect: got '%s'", rr.Header().Get("lb-traffic-before"))
	}

	expectedTraffic := int64(len(serverBody))
	if sInfo.GetTraffic() != expectedTraffic {
		t.Errorf("Server traffic not updated correctly: got %d, want %d", sInfo.GetTraffic(), expectedTraffic)
	}
	if rr.Header().Get("lb-traffic-after") != fmt.Sprintf("%d", expectedTraffic) {
			t.Errorf("lb-traffic-after header is incorrect: got '%s', want '%s'", rr.Header().Get("lb-traffic-after"), fmt.Sprintf("%d", expectedTraffic))
	}

	sInfoError := &ServerInfo{URL: "invalid-host-that-will-fail:1234", Alive: true, TrafficBytes: 0}
	rrError := httptest.NewRecorder()
	err = forward(sInfoError, rrError, req)
	if err == nil {
		t.Fatalf("forward should have returned an error for unreachable backend")
	}
	if rrError.Code != http.StatusServiceUnavailable {
		t.Errorf("Expected StatusServiceUnavailable for failed backend, got %d", rrError.Code)
	}
	if sInfoError.IsAlive() {
		t.Error("Server should be marked as not alive after a forwarding error")
	}
}

func TestBalancerHandler(t *testing.T) {
	backendResponses := []string{"Resp1", "Resp22", "Resp333"}
	var testServers []*httptest.Server
	var testServerInfos []*ServerInfo

	for _, respBody := range backendResponses {
		body := respBody
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprint(w, body)
		}))
		testServers = append(testServers, server)
		serverURL := strings.TrimPrefix(server.URL, "http://")
		testServerInfos = append(testServerInfos, &ServerInfo{URL: serverURL, Alive: true, TrafficBytes: 0})
	}
	defer func() {
		for _, ts := range testServers {
			ts.Close()
		}
	}()

	originalGlobalServers := servers
	servers = testServerInfos
	defer func() { servers = originalGlobalServers }()
	
	balancerHandler := http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		selectedServer := selectServerLeastTraffic()
		if selectedServer == nil {
			http.Error(rw, "Service unavailable", http.StatusServiceUnavailable)
			return
		}
		forward(selectedServer, rw, r)
	})

	numRequests := len(testServerInfos) * 2

	for i := 0; i < numRequests; i++ {
		req := httptest.NewRequest("GET", "/", nil)
		rr := httptest.NewRecorder()
		balancerHandler.ServeHTTP(rr, req)

		if rr.Code != http.StatusOK {
			t.Fatalf("Request %d: Expected status 200, got %d", i, rr.Code)
		}
	}
	
	expectedTraffics := []int64{
		int64(2 * len(backendResponses[0])),
		int64(2 * len(backendResponses[1])),
		int64(2 * len(backendResponses[2])),
	}

	for i, sInfo := range testServerInfos {
		if sInfo.GetTraffic() != expectedTraffics[i] {
			t.Errorf("Server %s (%s) traffic: expected %d, got %d after %d requests",
				sInfo.GetURL(), backendResponses[i], expectedTraffics[i], sInfo.GetTraffic(), numRequests)
		}
	}

	for _, sInfo := range testServerInfos {
		sInfo.SetAlive(false)
	}
	req := httptest.NewRequest("GET", "/", nil)
	rr := httptest.NewRecorder()
	balancerHandler.ServeHTTP(rr, req)
	if rr.Code != http.StatusServiceUnavailable {
		t.Errorf("Expected 503 when all servers are unhealthy, got %d", rr.Code)
	}
}