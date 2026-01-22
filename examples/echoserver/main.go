package main

import (
	"encoding/json"
	"fmt"
	"log"
	"math/rand"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// User represents a user entity
type User struct {
	ID        int       `json:"id"`
	Name      string    `json:"name"`
	Email     string    `json:"email"`
	CreatedAt time.Time `json:"created_at"`
}

// Store is an in-memory data store
type Store struct {
	users  map[int]*User
	nextID int
	mu     sync.RWMutex
}

// Stats tracks request statistics
type Stats struct {
	TotalRequests int64
	GetRequests   int64
	PostRequests  int64
	PutRequests   int64
	DeleteRequests int64
	Errors        int64
	StartTime     time.Time
}

var (
	store = &Store{
		users:  make(map[int]*User),
		nextID: 1,
	}
	stats = &Stats{
		StartTime: time.Now(),
	}
)

func main() {
	// Seed some initial data
	seedData()

	// Setup routes
	http.HandleFunc("/", handleRoot)
	http.HandleFunc("/health", handleHealth)
	http.HandleFunc("/api/users", handleUsers)
	http.HandleFunc("/api/users/", handleUserByID)
	http.HandleFunc("/api/stats", handleStats)
	http.HandleFunc("/api/echo", handleEcho)

	port := ":8080"
	fmt.Println()
	fmt.Println("  ╔═══════════════════════════════════════════╗")
	fmt.Println("  ║     🚀 Echo CRUD Server Started!          ║")
	fmt.Println("  ╠═══════════════════════════════════════════╣")
	fmt.Printf("  ║  Listening on http://localhost%s       ║\n", port)
	fmt.Println("  ╠═══════════════════════════════════════════╣")
	fmt.Println("  ║  Endpoints:                               ║")
	fmt.Println("  ║    GET    /health        Health check     ║")
	fmt.Println("  ║    GET    /api/users     List users       ║")
	fmt.Println("  ║    POST   /api/users     Create user      ║")
	fmt.Println("  ║    GET    /api/users/:id Get user         ║")
	fmt.Println("  ║    PUT    /api/users/:id Update user      ║")
	fmt.Println("  ║    DELETE /api/users/:id Delete user      ║")
	fmt.Println("  ║    POST   /api/echo      Echo request     ║")
	fmt.Println("  ║    GET    /api/stats     View statistics  ║")
	fmt.Println("  ╚═══════════════════════════════════════════╝")
	fmt.Println()

	log.Fatal(http.ListenAndServe(port, nil))
}

func seedData() {
	names := []string{"Alice", "Bob", "Charlie", "Diana", "Eve"}
	for _, name := range names {
		store.mu.Lock()
		store.users[store.nextID] = &User{
			ID:        store.nextID,
			Name:      name,
			Email:     strings.ToLower(name) + "@example.com",
			CreatedAt: time.Now(),
		}
		store.nextID++
		store.mu.Unlock()
	}
}

func handleRoot(w http.ResponseWriter, r *http.Request) {
	atomic.AddInt64(&stats.TotalRequests, 1)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"service": "echo-crud-server",
		"version": "1.0.0",
		"status":  "running",
	})
}

func handleHealth(w http.ResponseWriter, r *http.Request) {
	atomic.AddInt64(&stats.TotalRequests, 1)
	atomic.AddInt64(&stats.GetRequests, 1)

	// Simulate occasional slow responses
	if rand.Float32() < 0.05 {
		time.Sleep(time.Duration(rand.Intn(100)) * time.Millisecond)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":    "healthy",
		"timestamp": time.Now().Format(time.RFC3339),
		"uptime":    time.Since(stats.StartTime).String(),
	})
}

func handleUsers(w http.ResponseWriter, r *http.Request) {
	atomic.AddInt64(&stats.TotalRequests, 1)
	w.Header().Set("Content-Type", "application/json")

	switch r.Method {
	case http.MethodGet:
		atomic.AddInt64(&stats.GetRequests, 1)
		listUsers(w, r)
	case http.MethodPost:
		atomic.AddInt64(&stats.PostRequests, 1)
		createUser(w, r)
	default:
		atomic.AddInt64(&stats.Errors, 1)
		http.Error(w, `{"error": "method not allowed"}`, http.StatusMethodNotAllowed)
	}
}

func handleUserByID(w http.ResponseWriter, r *http.Request) {
	atomic.AddInt64(&stats.TotalRequests, 1)
	w.Header().Set("Content-Type", "application/json")

	// Extract ID from path
	path := strings.TrimPrefix(r.URL.Path, "/api/users/")
	id, err := strconv.Atoi(path)
	if err != nil {
		atomic.AddInt64(&stats.Errors, 1)
		http.Error(w, `{"error": "invalid user id"}`, http.StatusBadRequest)
		return
	}

	switch r.Method {
	case http.MethodGet:
		atomic.AddInt64(&stats.GetRequests, 1)
		getUser(w, r, id)
	case http.MethodPut:
		atomic.AddInt64(&stats.PutRequests, 1)
		updateUser(w, r, id)
	case http.MethodDelete:
		atomic.AddInt64(&stats.DeleteRequests, 1)
		deleteUser(w, r, id)
	default:
		atomic.AddInt64(&stats.Errors, 1)
		http.Error(w, `{"error": "method not allowed"}`, http.StatusMethodNotAllowed)
	}
}

func listUsers(w http.ResponseWriter, r *http.Request) {
	store.mu.RLock()
	defer store.mu.RUnlock()

	users := make([]*User, 0, len(store.users))
	for _, u := range store.users {
		users = append(users, u)
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"users": users,
		"total": len(users),
	})
}

func createUser(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Name  string `json:"name"`
		Email string `json:"email"`
	}

	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		atomic.AddInt64(&stats.Errors, 1)
		http.Error(w, `{"error": "invalid request body"}`, http.StatusBadRequest)
		return
	}

	if input.Name == "" {
		input.Name = fmt.Sprintf("User%d", rand.Intn(1000))
	}
	if input.Email == "" {
		input.Email = fmt.Sprintf("user%d@example.com", rand.Intn(1000))
	}

	store.mu.Lock()
	user := &User{
		ID:        store.nextID,
		Name:      input.Name,
		Email:     input.Email,
		CreatedAt: time.Now(),
	}
	store.users[store.nextID] = user
	store.nextID++
	store.mu.Unlock()

	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(user)
}

func getUser(w http.ResponseWriter, r *http.Request, id int) {
	store.mu.RLock()
	user, exists := store.users[id]
	store.mu.RUnlock()

	if !exists {
		atomic.AddInt64(&stats.Errors, 1)
		http.Error(w, `{"error": "user not found"}`, http.StatusNotFound)
		return
	}

	json.NewEncoder(w).Encode(user)
}

func updateUser(w http.ResponseWriter, r *http.Request, id int) {
	var input struct {
		Name  string `json:"name"`
		Email string `json:"email"`
	}

	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		atomic.AddInt64(&stats.Errors, 1)
		http.Error(w, `{"error": "invalid request body"}`, http.StatusBadRequest)
		return
	}

	store.mu.Lock()
	user, exists := store.users[id]
	if !exists {
		store.mu.Unlock()
		atomic.AddInt64(&stats.Errors, 1)
		http.Error(w, `{"error": "user not found"}`, http.StatusNotFound)
		return
	}

	if input.Name != "" {
		user.Name = input.Name
	}
	if input.Email != "" {
		user.Email = input.Email
	}
	store.mu.Unlock()

	json.NewEncoder(w).Encode(user)
}

func deleteUser(w http.ResponseWriter, r *http.Request, id int) {
	store.mu.Lock()
	_, exists := store.users[id]
	if !exists {
		store.mu.Unlock()
		atomic.AddInt64(&stats.Errors, 1)
		http.Error(w, `{"error": "user not found"}`, http.StatusNotFound)
		return
	}
	delete(store.users, id)
	store.mu.Unlock()

	w.WriteHeader(http.StatusNoContent)
}

func handleEcho(w http.ResponseWriter, r *http.Request) {
	atomic.AddInt64(&stats.TotalRequests, 1)
	atomic.AddInt64(&stats.PostRequests, 1)

	w.Header().Set("Content-Type", "application/json")

	var body interface{}
	json.NewDecoder(r.Body).Decode(&body)

	response := map[string]interface{}{
		"method":     r.Method,
		"path":       r.URL.Path,
		"headers":    r.Header,
		"body":       body,
		"timestamp":  time.Now().Format(time.RFC3339),
		"request_id": fmt.Sprintf("%d", rand.Int63()),
	}

	json.NewEncoder(w).Encode(response)
}

func handleStats(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	uptime := time.Since(stats.StartTime)
	total := atomic.LoadInt64(&stats.TotalRequests)
	rps := float64(total) / uptime.Seconds()

	json.NewEncoder(w).Encode(map[string]interface{}{
		"uptime":          uptime.String(),
		"total_requests":  total,
		"get_requests":    atomic.LoadInt64(&stats.GetRequests),
		"post_requests":   atomic.LoadInt64(&stats.PostRequests),
		"put_requests":    atomic.LoadInt64(&stats.PutRequests),
		"delete_requests": atomic.LoadInt64(&stats.DeleteRequests),
		"errors":          atomic.LoadInt64(&stats.Errors),
		"requests_per_sec": fmt.Sprintf("%.2f", rps),
	})
}
