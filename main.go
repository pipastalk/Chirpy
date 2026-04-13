package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/mail"
	"os"
	"strings"
	"sync/atomic"

	"github.com/google/uuid"
	"github.com/joho/godotenv"
	_ "github.com/lib/pq"
	"github.com/pipastalk/Chirpy/internal/database"
)

func main() {
	godotenv.Load()
	dbURL := os.Getenv("DB_URL")
	db, err := sql.Open("postgres", dbURL)
	if err != nil {
		fmt.Printf("Failed to connect to database: %v\n", err)
		os.Exit(1)
	}
	dbQueries := database.New(db)

	serveMux := http.NewServeMux()
	httpServer := &http.Server{
		Addr:    ":8080",
		Handler: serveMux,
	}
	apiMetrics := &apiConfig{
		fileserverHits: atomic.Int32{},
		dbQueries:      dbQueries,
	}

	fileServer := fileServerHandler()
	readiness := http.HandlerFunc(readyCheck)
	serveMux.HandleFunc("GET /api/healthz", middlewareAPICalls(readiness))
	serveMux.Handle("/app/", apiMetrics.middlewareMetricsInc(fileServer))
	serveMux.HandleFunc("GET /admin/metrics", middlewareAdminCalls(http.HandlerFunc(apiMetrics.fileServerHitsHandler)))
	serveMux.HandleFunc("POST /admin/reset", middlewareAdminCalls(http.HandlerFunc(apiMetrics.resetHandler)))
	serveMux.HandleFunc("POST /api/chirps", middlewareAPICalls(http.HandlerFunc(apiMetrics.postChirpHandler)))
	serveMux.HandleFunc("POST /api/users", middlewareAPICalls(apiMetrics.userHandler()))
	serveMux.HandleFunc("GET /api/users", middlewareAPICalls(apiMetrics.userHandler()))
	httpServer.ListenAndServe()
}

func (cfg *apiConfig) userHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		registry := map[string]func(*http.Request) ([]byte, error){
			"POST": cfg.createUserFromEmail,
			"GET":  cfg.getUserFromEmail,
		}
		handlerFunc, exists := registry[r.Method]
		if !exists {
			respondWithError(w, http.StatusMethodNotAllowed, "Method not allowed")
			return
		}
		jsonData, err := handlerFunc(r)

		if err != nil {
			respondWithError(w, http.StatusBadRequest, err.Error()) //TODO better error handle for server side errors / user not found etc
			return
		}
		switch r.Method {
		case "GET":
			w.WriteHeader(http.StatusOK)
		case "POST":
			w.WriteHeader(http.StatusCreated)
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")

		w.Write(jsonData)
	}
}

func validateUserRequest(req *http.Request) (userRequest, error) {
	expectedReq := userRequest{}
	decoder := json.NewDecoder(req.Body)
	if err := decoder.Decode(&expectedReq); err != nil {
		return userRequest{}, fmt.Errorf("failed to decode request body: %w", err)
	}
	_, err := mail.ParseAddress(expectedReq.Email)
	if err != nil {
		return userRequest{}, fmt.Errorf("invalid email address: %w", err)
	}
	return expectedReq, nil
}

func (cfg *apiConfig) createUserFromEmail(req *http.Request) (jsonData []byte, err error) {
	userReq, err := validateUserRequest(req)
	if err != nil {
		return nil, err
	}
	dbUser, err := cfg.dbQueries.CreateUser(req.Context(), userReq.Email)
	if err != nil {
		return nil, fmt.Errorf("failed to create user: %w", err)
	}
	jsonData, err = createUserResponse(dbUser)
	if err != nil {
		return nil, err
	}
	return jsonData, nil
}

func (cfg *apiConfig) getUserFromEmail(req *http.Request) (jsonData []byte, err error) {
	userReq, err := validateUserRequest(req)
	if err != nil {
		return nil, err
	}
	dbUser, err := cfg.dbQueries.GetUser(req.Context(), userReq.Email)
	if err != nil {
		return nil, fmt.Errorf("failed to retrieve user: %w", err)
	}
	jsonData, err = createUserResponse(dbUser)
	if err != nil {
		return nil, err
	}
	return jsonData, nil
}

func createUserResponse(dbUser database.User) ([]byte, error) {
	response := userResponse{
		ID:        dbUser.ID,
		Email:     dbUser.Email,
		CreatedAt: dbUser.CreatedAt.Format("2006-01-02T15:04:05Z07:00"),
		UpdatedAt: dbUser.UpdatedAt.Format("2006-01-02T15:04:05Z07:00"),
	}
	jsonData, err := json.Marshal(response)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal user data: %w", err)
	}
	return jsonData, nil
}

func fileServerHandler() http.Handler {
	return http.Handler(http.StripPrefix("/app", http.FileServer(http.Dir("."))))
}

func readyCheck(w http.ResponseWriter, r *http.Request) {
	headers := w.Header()
	body := "OK"
	headers.Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(body))
}

func middlewareAdminCalls(next http.Handler) http.HandlerFunc {
	stripped := http.StripPrefix("/admin", next)
	return func(w http.ResponseWriter, r *http.Request) {
		stripped.ServeHTTP(w, r)
	}
}
func middlewareAPICalls(next http.Handler) http.HandlerFunc {
	stripped := http.StripPrefix("/api", next)
	return func(w http.ResponseWriter, r *http.Request) {
		stripped.ServeHTTP(w, r)
	}
}
func (cfg *apiConfig) middlewareMetricsInc(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cfg.fileserverHits.Add(1)
		next.ServeHTTP(w, r)
	})
}

func (cfg *apiConfig) fileServerHitsHandler(w http.ResponseWriter, r *http.Request) {
	hits := cfg.fileserverHits.Load()
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	body := fmt.Sprintf(
		`<html>
  <body>
    <h1>Welcome, Chirpy Admin</h1>
    <p>Chirpy has been visited %d times!</p>
  </body>
</html>`, hits)

	w.Write([]byte(body))
}

func (cfg *apiConfig) resetHandler(w http.ResponseWriter, r *http.Request) {
	cfg.dbQueries.ResetUserDB(r.Context())
	cfg.fileserverHits.Store(0)
	w.WriteHeader(http.StatusOK)
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Write([]byte("Hits reset\n"))
}

type apiConfig struct {
	fileserverHits atomic.Int32
	dbQueries      *database.Queries
}
type userResponse struct {
	ID        uuid.UUID `json:"id"`
	Email     string    `json:"email"`
	CreatedAt string    `json:"created_at"`
	UpdatedAt string    `json:"updated_at"`
}
type userRequest struct {
	Email string `json:"email"`
}

type chirpRequest struct {
	Body   string    `json:"body"`
	UserID uuid.UUID `json:"user_id"`
}
type chirpResponse struct {
	ID        uuid.UUID `json:"id"`
	Body      string    `json:"body"`
	CreatedAt string    `json:"created_at"`
	UpdatedAt string    `json:"updated_at"`
	UserID    uuid.UUID `json:"user_id"`
}

func (cfg *apiConfig) postChirpHandler(w http.ResponseWriter, r *http.Request) {
	decoder := json.NewDecoder(r.Body)
	var cReq chirpRequest
	if err := decoder.Decode(&cReq); err != nil {
		respondWithError(w, http.StatusNoContent, "Invalid JSON")
		return
	}
	if _, err := cfg.dbQueries.GetUserByID(r.Context(), cReq.UserID); err != nil {
		respondWithError(w, http.StatusBadRequest, "User not found")
		return
	}
	if len(cReq.Body) == 0 {
		respondWithError(w, http.StatusBadRequest, "Message cannot be empty")
		return
	}
	charLimit := 140
	if len(cReq.Body) > charLimit {
		respondWithError(w, http.StatusBadRequest, "Chirp is too long")
		return
	}
	cReq.Body = sanitizeFilter(cReq.Body)
	dbPost, err := cfg.dbQueries.CreatePost(r.Context(), database.CreatePostParams{
		Body:   cReq.Body,
		UserID: cReq.UserID,
	})
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Failed to create post")
		return
	}
	cRes := chirpResponse{
		ID:        dbPost.ID,
		Body:      dbPost.Body,
		CreatedAt: dbPost.CreatedAt.Format("2006-01-02T15:04:05Z07:00"),
		UpdatedAt: dbPost.UpdatedAt.Format("2006-01-02T15:04:05Z07:00"),
		UserID:    dbPost.UserID,
	}
	respondWithJSON(w, http.StatusCreated, cRes)
}
func respondWithError(w http.ResponseWriter, statusCode int, message string) {
	w.WriteHeader(statusCode)
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	type errorResponse struct {
		Error string `json:"error"`
	}
	e := errorResponse{Error: message}
	jsonData, err := json.Marshal(e)
	if err != nil {
		w.Write([]byte(`{"error": "Failed to encode error response"}`))
		return
	}
	w.Write(jsonData)
}

func respondWithJSON(w http.ResponseWriter, statusCode int, payload interface{}) {
	w.WriteHeader(statusCode)
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	jsonData, err := json.Marshal(payload)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Failed to encode response")
		return
	}
	w.Write(jsonData)
}

func sanitizeFilter(message string) string {
	profaneWords := []string{"kerfuffle", "sharbert", "fornax", "dingleberry", "bumfuzzle"}
	for _, word := range profaneWords {
		if strings.Contains(strings.ToLower(message), word) {
			fields := strings.Fields(message)
			for i, field := range fields {
				if strings.EqualFold(field, word) {
					fields[i] = "****"
				}
			}
			message = strings.Join(fields, " ")
		}
	}
	return message
}
