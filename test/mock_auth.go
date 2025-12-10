package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
)

type AuthRequest struct {
	Extension string `json:"extension"`
	Secret    string `json:"secret"`
}

type AuthResponse struct {
	MainExtension string   `json:"main_extension"`
	SubExtensions []string `json:"sub_extensions"`
	UserName      string   `json:"user_name"`
}

func main() {
	port := os.Getenv("MOCK_AUTH_PORT")
	if port == "" {
		port = "18081"
	}

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		var req AuthRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "Bad request", http.StatusBadRequest)
			return
		}

		var response []AuthResponse

		// Mock responses based on test.env
		if req.Extension == "201" && req.Secret == "Giacomo,1234" {
			response = []AuthResponse{
				{
					MainExtension: "201",
					SubExtensions: []string{"91201", "92201"},
					UserName:      "giacomo",
				},
			}
		} else if req.Extension == "202" && req.Secret == "Mario,1234" {
			response = []AuthResponse{
				{
					MainExtension: "202",
					SubExtensions: []string{"91202"},
					UserName:      "mario",
				},
			}
		} else {
			// Invalid credentials
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
	})

	fmt.Printf("Mock auth server listening on :%s\n", port)
	log.Fatal(http.ListenAndServe(":"+port, nil))
}
