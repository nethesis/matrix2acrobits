package main

import (
	"encoding/json"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

type mockAuthServer struct {
	server *http.Server
	port   string
}

type LoginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type LoginResponse struct {
	Code   int    `json:"code"`
	Token  string `json:"token"`
	Expire string `json:"expire"`
}

type ChatMatrixConfig struct {
	BaseURL     string `json:"base_url"`
	AcrobitsURL string `json:"acrobits_url"`
}

type ChatUser struct {
	UserName      string   `json:"user_name"`
	MainExtension string   `json:"main_extension"`
	SubExtensions []string `json:"sub_extensions"`
}

type ChatResponse struct {
	Matrix ChatMatrixConfig `json:"matrix"`
	Users  []ChatUser       `json:"users,omitempty"`
}

type mockAuthUser struct {
	Username      string
	Password      string
	MainExtension string
	SubExtensions []string
}

var mockAuthUsers = []mockAuthUser{
	{
		Username:      "giacomo",
		Password:      "Giacomo,1234",
		MainExtension: "201",
		SubExtensions: []string{"91201", "92201"},
	},
	{
		Username:      "mario",
		Password:      "Mario,1234",
		MainExtension: "202",
		SubExtensions: []string{"91202"},
	},
}

// startMockAuthServer starts a mock authentication server on the specified port.
// It returns a mockAuthServer that can be stopped later.
func startMockAuthServer(port string) (*mockAuthServer, error) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/login", handleMockLogin)
	mux.HandleFunc("/api/chat", handleMockChat)
	mux.HandleFunc("/", handleMockNotFound)

	server := &http.Server{
		Addr:    "127.0.0.1:" + port,
		Handler: mux,
	}

	// Start the server in a goroutine
	go func() {
		_ = server.ListenAndServe()
	}()

	// Wait for server to be ready by checking if the port is listening
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := net.Dial("tcp", "127.0.0.1:"+port)
		if err == nil {
			conn.Close()
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	return &mockAuthServer{
		server: server,
		port:   port,
	}, nil
}

// stopMockAuthServer shuts down the mock auth server.
func stopMockAuthServer(mas *mockAuthServer) error {
	if mas == nil || mas.server == nil {
		return nil
	}
	return mas.server.Close()
}

func handleMockNotFound(w http.ResponseWriter, r *http.Request) {
	http.Error(w, "Not found", http.StatusNotFound)
}

func handleMockLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req LoginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Bad request", http.StatusBadRequest)
		return
	}

	// Check if credentials are valid
	var found *mockAuthUser
	for i := range mockAuthUsers {
		u := &mockAuthUsers[i]
		if u.Username == req.Username && u.Password == req.Password {
			found = u
			break
		}
	}

	if found == nil {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	// Generate JWT token with nethvoice_cti.chat claim
	claims := jwt.MapClaims{
		"nethvoice_cti":      true,
		"nethvoice_cti.chat": true,
		"id":                 req.Username,
		"exp":                time.Now().Add(24 * time.Hour).Unix(),
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	tokenString, err := token.SignedString([]byte("test-secret-key"))
	if err != nil {
		http.Error(w, "Server error", http.StatusInternalServerError)
		return
	}

	response := LoginResponse{
		Code:   200,
		Token:  tokenString,
		Expire: time.Now().Add(24 * time.Hour).Format(time.RFC3339Nano),
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(response)
}

func handleMockChat(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Get the Bearer token from Authorization header
	authHeader := r.Header.Get("Authorization")
	if authHeader == "" {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	// Extract token from "Bearer <token>"
	parts := strings.Split(authHeader, " ")
	if len(parts) != 2 || parts[0] != "Bearer" {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	tokenString := parts[1]

	// Parse the token
	token, err := jwt.ParseWithClaims(tokenString, &jwt.MapClaims{}, func(token *jwt.Token) (interface{}, error) {
		return []byte("test-secret-key"), nil
	})

	if err != nil || !token.Valid {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	// Extract username from claims
	claims, ok := token.Claims.(*jwt.MapClaims)
	if !ok {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	_, ok = (*claims)["id"].(string)
	if !ok {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	// Check if users=1 parameter is present
	includeUsers := r.URL.Query().Get("users") == "1"

	// Build response
	response := ChatResponse{
		Matrix: ChatMatrixConfig{
			BaseURL:     "https://synapse.example.com",
			AcrobitsURL: "https://synapse.example.com/m2a",
		},
	}

	// Include users if requested
	if includeUsers {
		for _, u := range mockAuthUsers {
			response.Users = append(response.Users, ChatUser{
				UserName:      u.Username,
				MainExtension: u.MainExtension,
				SubExtensions: u.SubExtensions,
			})
		}
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(response)
}
