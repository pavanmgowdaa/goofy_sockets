package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
)

type Ticket struct {
	ID     string `json:"id"`
	Event  string `json:"event"`
	Status string `json:"status"`
}

var tickets = []Ticket{
	{
		ID:     "FP-001",
		Event:  "Bengaluru Music Festival",
		Status: "Valid",
	},
	{
		ID:     "FP-002",
		Event:  "Tech Concert 2026",
		Status: "Valid",
	},
}

func homeHandler(w http.ResponseWriter, r *http.Request) {
	fmt.Fprintln(w, "FairPass Blockchain Backend is running!")
}

func ticketsHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(tickets)
}

func verifyHandler(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")

	w.Header().Set("Content-Type", "application/json")

	if id == "" {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{
			"message": "Please provide a ticket ID",
		})
		return
	}

	for _, ticket := range tickets {
		if ticket.ID == id {
			json.NewEncoder(w).Encode(map[string]interface{}{
				"ticket":  ticket,
				"valid":   ticket.Status == "Valid",
				"message": "Ticket found",
			})
			return
		}
	}

	w.WriteHeader(http.StatusNotFound)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"valid":   false,
		"message": "Ticket not found",
	})
}

func main() {
	http.HandleFunc("/", homeHandler)
	http.HandleFunc("/api/tickets", ticketsHandler)
	http.HandleFunc("/api/verify", verifyHandler)

	fmt.Println("FairPass server running at http://localhost:8080")

	log.Fatal(http.ListenAndServe(":8080", nil))
}
