package main

import (
	"encoding/json"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"sync"
	"time"
)

// Server represents a Server.
type Server struct {
	// URL of the backend server.
	URL *url.URL
	// ActiveConnections returns the number of active connections.
	ActiveConnections int
	// Mu mutex for safe concurrency.
	//
	// For when ActiveConnections is used concurrently.
	Mu *sync.Mutex
	// Healthy returns true if the server is active.
	Healthy bool
}

// Proxy returns a reverse proxy instance configured to forward requests to the backend server
func (s Server) Proxy() *httputil.ReverseProxy {
	return httputil.NewSingleHostReverseProxy(s.URL)
}

// Config represents the configuration.
type Config struct {
	HealthCheckInterval string `json:"healthCheckInterval"`
	// Servers contains a list of servers.
	Servers    []string `json:"servers"`
	ListenPort string   `json:"listenPort"`
}

// loadConfig loads the configuration file and returns it.
func loadConfig(path string) (Config, error) {
	var config Config

	bytes, err := os.ReadFile(path)
	if err != nil {
		return config, err
	}

	err = json.Unmarshal(bytes, &config)
	if err != nil {
		return config, err
	}

	return config, nil
}

// nextServerLeastActive finds a healthy server with the least active connections
// and returns it.
// It uses a mutex to lock access to Server.ActiveConnections.
func nextServerLeastActive(servers []*Server) *Server {
	leastActiveConnections := servers[0].ActiveConnections
	leastActiveServer := servers[0]

	// Checks if a server is healthy and if it has the least amount of connections.
	for _, server := range servers {
		server.Mu.Lock()
		if server.Healthy {
			if server.ActiveConnections < leastActiveConnections || leastActiveConnections == -1 {
				leastActiveConnections = server.ActiveConnections
				leastActiveServer = server
			}
		}
		server.Mu.Unlock()
	}

	return leastActiveServer
}

func main() {
	config, err := loadConfig("config.jsonc")
	if err != nil {
		log.Fatalf("Error loading configuration: %v", err)
	}

	healthCheckInterval, err := time.ParseDuration(config.HealthCheckInterval)
	if err != nil {
		log.Fatalf("Error parsing healthCheckInterval: %v", err)
	}

	var servers []*Server
	for _, serverUrl := range config.Servers {
		u, err := url.Parse(serverUrl)
		if err != nil {
			log.Fatalf("Error parsing servers (server URLs): %v", err)
		}
		servers = append(servers, &Server{URL: u, Mu: &sync.Mutex{}, Healthy: true})
	}

	// Start goroutines that periodically checks each server health
	// by making an HTTP GET request to it.
	for _, server := range servers {
		go func(s *Server) {
			for range time.Tick(healthCheckInterval) {
				res, err := http.Get(s.URL.String())
				s.Mu.Lock()

				if err := res.Body.Close(); err != nil {
					log.Printf("Error closing request body: %v", err)
				}

				// If no response or status code is 5xx.
				if err != nil || res.StatusCode >= 500 {
					s.Healthy = false
				} else {
					s.Healthy = true
				}

				s.Mu.Unlock()
			}
		}(server)
	}

	// HTTP handler that selects least active.
	http.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		server := nextServerLeastActive(servers)
		server.Mu.Lock()
		defer server.Mu.Unlock()
		server.ActiveConnections++

		server.Proxy().ServeHTTP(w, r)

		server.Mu.Lock()
		defer server.Mu.Unlock()
		server.ActiveConnections--
	})

	log.Println("Starting server on port", config.ListenPort)
	err = http.ListenAndServe(config.ListenPort, nil)
	if err != nil {
		log.Fatalf("Error starting server: %v", err)
	}
}
