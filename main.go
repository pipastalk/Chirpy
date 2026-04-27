package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/mail"
	"os"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/joho/godotenv"
	_ "github.com/lib/pq"
	"github.com/pipastalk/Chirpy/internal/auth"
	"github.com/pipastalk/Chirpy/internal/database"
)

func main() {
	godotenv.Load()
	jwtSecret := os.Getenv("jwt_secret")
	if jwtSecret == "" {
		fmt.Println("JWT secret is not set in environment variables")
		os.Exit(1)
	}
	dbURL := os.Getenv("DB_URL")
	db, err := sql.Open("postgres", dbURL)
	if err != nil {
		fmt.Printf("Failed to connect to database: %v\n", err)
		os.Exit(1)
	}
	polkaKey := os.Getenv("POLKA_KEY")
	if polkaKey == "" {
		fmt.Println("Polka key is not set in environment variables")
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
		jwtSecret:      jwtSecret,
		polkaKey:       polkaKey,
	}

	fileServer := fileServerHandler()
	//region API routes
	readiness := http.HandlerFunc(readyCheck)
	serveMux.HandleFunc("GET /api/healthz", middlewareAPICalls(readiness))
	serveMux.Handle("/app/", apiMetrics.middlewareMetricsInc(fileServer))
	serveMux.HandleFunc("GET /admin/metrics", middlewareAdminCalls(http.HandlerFunc(apiMetrics.fileServerHitsHandler)))
	serveMux.HandleFunc("POST /admin/reset", middlewareAdminCalls(http.HandlerFunc(apiMetrics.resetHandler)))
	serveMux.HandleFunc("POST /api/chirps", middlewareAPICalls(http.HandlerFunc(apiMetrics.postChirpHandler)))
	serveMux.HandleFunc("POST /api/login", middlewareAPICalls(http.HandlerFunc(apiMetrics.loginUserFromEmailHandler)))
	serveMux.HandleFunc("POST /api/users", middlewareAPICalls(apiMetrics.userHandler()))
	serveMux.HandleFunc("GET /api/users", middlewareAPICalls(apiMetrics.userHandler()))
	serveMux.HandleFunc("PUT /api/users", middlewareAPICalls(http.HandlerFunc(apiMetrics.updateUser)))
	serveMux.HandleFunc("POST /api/polka/webhooks", middlewareAPICalls(http.HandlerFunc(apiMetrics.enableChirpyRed)))
	serveMux.HandleFunc("GET /api/chirps", middlewareAPICalls(http.HandlerFunc(apiMetrics.getChirpsHandler)))
	serveMux.HandleFunc("GET /api/chirps/{chirpID}", middlewareAPICalls(http.HandlerFunc(apiMetrics.getChirpsHandler)))
	serveMux.HandleFunc("DELETE /api/chirps/{chirpID}", middlewareAPICalls(http.HandlerFunc(apiMetrics.deleteChirp)))
	serveMux.HandleFunc("POST /api/refresh", middlewareAPICalls(http.HandlerFunc(apiMetrics.refreshTokenHandler)))
	serveMux.HandleFunc("POST /api/revoke", middlewareAPICalls(http.HandlerFunc(apiMetrics.revokeRefreshToken)))
	//endregion
	httpServer.ListenAndServe()
}

type httpErrors struct {
	httpStatus int
	message    string
}

func (cfg *apiConfig) getUserChirps(r *http.Request) ([]chirpResponse, httpErrors) {
	authorID := r.URL.Query().Get("author_id")
	if authorID == "" {
		return nil, httpErrors{httpStatus: http.StatusBadRequest, message: "Missing author_id parameter"}
	}
	userID, err := uuid.Parse(authorID)
	if err != nil {
		return nil, httpErrors{httpStatus: http.StatusBadRequest, message: "Invalid author_id parameter"}
	}
	chirps, err := cfg.dbQueries.GetChirpByUser(r.Context(), userID)
	if err != nil {
		return nil, httpErrors{httpStatus: http.StatusInternalServerError, message: "Failed to retrieve chirps"}
	}
	if len(chirps) == 0 {
		return nil, httpErrors{httpStatus: http.StatusNotFound, message: "No chirps found"}
	}
	response := make([]chirpResponse, len(chirps))
	for i, chirp := range chirps {
		response[i] = chirpResponse{
			ID:        chirp.ID,
			UserID:    chirp.UserID,
			Body:      chirp.Body,
			CreatedAt: chirp.CreatedAt.Format(time.RFC3339Nano),
			UpdatedAt: chirp.UpdatedAt.Format(time.RFC3339Nano),
		}
	}
	return response, httpErrors{}
}
func (cfg *apiConfig) getChirp(r *http.Request) (chirpResponse, httpErrors) {
	id := r.PathValue("chirpID")
	post_id, err := uuid.Parse(id)
	if err != nil {
		return chirpResponse{}, httpErrors{httpStatus: http.StatusBadRequest, message: "Invalid chirp ID"}
	}
	chirp, err := cfg.dbQueries.GetChirp(r.Context(), post_id)
	if err != nil {
		return chirpResponse{}, httpErrors{httpStatus: http.StatusNotFound, message: "Failed to retrieve chirps"}
	}
	cRes := chirpResponse{
		ID:        chirp.ID,
		UserID:    chirp.UserID,
		Body:      chirp.Body,
		CreatedAt: chirp.CreatedAt.Format(time.RFC3339Nano),
		UpdatedAt: chirp.UpdatedAt.Format(time.RFC3339Nano),
	}
	return cRes, httpErrors{}
}

func (cfg *apiConfig) getChirpsHandler(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("chirpID")
	authorID := r.URL.Query().Get("author_id")
	var response []chirpResponse
	if id != "" { //Individual chirp request
		chrip, err := cfg.getChirp(r)
		if err != (httpErrors{}) {
			respondWithError(w, err.httpStatus, err.message)
			return
		}
		response = append(response, chrip)
	} else if authorID != "" { //chirps by user request
		res, err := cfg.getUserChirps(r)
		if err != (httpErrors{}) {
			respondWithError(w, err.httpStatus, err.message)
			return
		}
		response = append(response, res...)
	} else { //all chirps
		res, err := cfg.getAllChirps(r)
		if err != (httpErrors{}) {
			respondWithError(w, err.httpStatus, err.message)
			return
		}
		response = append(response, res...)
	}
	sortMethod := r.URL.Query().Get("sort")
	switch sortMethod {
	case "asc":
		sort.Slice(response, func(i, j int) bool {
			timeI, errI := time.Parse(time.RFC3339Nano, response[i].CreatedAt)
			timeJ, errJ := time.Parse(time.RFC3339Nano, response[j].CreatedAt)
			if errI != nil || errJ != nil {
				return false // If parsing fails, maintain original order
			}
			if c := timeI.Compare(timeJ); c != 0 {
				return c < 0
			}
			return false
		})
	case "desc":
		sort.Slice(response, func(i, j int) bool {
			timeI, errI := time.Parse(time.RFC3339Nano, response[i].CreatedAt)
			timeJ, errJ := time.Parse(time.RFC3339Nano, response[j].CreatedAt)
			if errI != nil || errJ != nil {
				return false // If parsing fails, maintain original order
			}
			if c := timeI.Compare(timeJ); c != 0 {
				return c > 0
			}
			return false
		})
	}
	//default to no sorting

	respondWithJSON(w, http.StatusOK, response)
}

func (cfg *apiConfig) getAllChirps(r *http.Request) ([]chirpResponse, httpErrors) {
	chirps, err := cfg.dbQueries.GetChirps(r.Context())
	if err != nil {
		return nil, httpErrors{httpStatus: http.StatusInternalServerError, message: "Failed to retrieve chirps"}
	}
	if len(chirps) == 0 {
		return nil, httpErrors{httpStatus: http.StatusNotFound, message: "No chirps found"}
	}
	response := make([]chirpResponse, len(chirps))
	for i, chirp := range chirps {
		response[i] = chirpResponse{
			ID:        chirp.ID,
			UserID:    chirp.UserID,
			Body:      chirp.Body,
			CreatedAt: chirp.CreatedAt.Format(time.RFC3339Nano),
			UpdatedAt: chirp.UpdatedAt.Format(time.RFC3339Nano),
		}
	}
	return response, httpErrors{}
}
func (cfg *apiConfig) deleteChirp(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("chirpID")
	post_id, err := uuid.Parse(id)
	token, err := auth.GetBearerToken(r.Header)
	if err != nil {
		respondWithError(w, http.StatusUnauthorized, "Missing or invalid Authorization header")
		return
	}
	userID, err := auth.ValidateJWT(token, cfg.jwtSecret)
	if err != nil {
		respondWithError(w, http.StatusUnauthorized, "Invalid token")
		return
	}
	chirp, err := cfg.dbQueries.GetChirp(r.Context(), post_id)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Invalid chirp ID")
		return
	}
	if chirp.UserID != userID {
		respondWithError(w, http.StatusForbidden, "You do not have permission to delete this chirp")
		return
	}
	err = cfg.dbQueries.DeleteChirp(r.Context(), post_id)
	if err != nil {
		respondWithError(w, http.StatusNotFound, "Failed to find chirp")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (cfg *apiConfig) loginUserFromEmailHandler(w http.ResponseWriter, req *http.Request) {
	userReq, err := validateUserRequest(req)
	failedLoginMsg := "Invalid email or password"
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Invalid Request")
		return
	}
	dbUser, err := cfg.dbQueries.GetUser(req.Context(), userReq.Email)
	if err != nil {
		respondWithError(w, http.StatusUnauthorized, failedLoginMsg)
		return
	}
	authd, err := auth.CheckPasswordHash(userReq.Password, dbUser.Password)
	if err != nil {
		respondWithError(w, http.StatusUnauthorized, failedLoginMsg)
		return
	}
	if authd {
		bearerExpiryTime := time.Now().Add(time.Duration(userReq.ExpiresInSeconds) * time.Second)
		default_bearer_TTL := 3600 * time.Second
		if userReq.ExpiresInSeconds <= 0 || userReq.ExpiresInSeconds > int64(default_bearer_TTL.Seconds()) {
			bearerExpiryTime = time.Now().Add(default_bearer_TTL) // default to 1 hour if not provided or invalid
		}
		refreshExpiryTime := time.Now().AddDate(0, 0, 60) // 60 days
		if userReq.ExpiresInSeconds <= 0 || userReq.ExpiresInSeconds > int64(default_bearer_TTL.Seconds()) {
			bearerExpiryTime = time.Now().Add(default_bearer_TTL) // default to 1 hour if not provided or invalid
		}
		refresh_token, err := auth.MakeRefreshToken()
		if err != nil {
			respondWithError(w, http.StatusInternalServerError, "Failed to generate refresh token")
			return
		}
		cfg.dbQueries.CreateRefreshToken(req.Context(), database.CreateRefreshTokenParams{
			Token:     refresh_token,
			UserID:    dbUser.ID,
			ExpiresAt: refreshExpiryTime,
		})
		bearer_token, err := auth.MakeJWT(dbUser.ID, cfg.jwtSecret, time.Until(bearerExpiryTime))
		if err != nil {
			respondWithError(w, http.StatusInternalServerError, "Failed to generate token")
			return
		}
		userRes, err := createUserResponse(dbUser, bearer_token, refresh_token)
		if err != nil {
			respondWithError(w, http.StatusInternalServerError, "Failed to create response")
			return
		}
		w.WriteHeader(http.StatusOK)
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Write(userRes)
		return
	}
	respondWithError(w, http.StatusUnauthorized, failedLoginMsg)
}

type ChirpyRedRequest struct {
	Event string `json:"event"`
	Data  struct {
		UserID uuid.UUID `json:"user_id"`
	} `json:"data"`
}

func (cfg *apiConfig) enableChirpyRed(w http.ResponseWriter, r *http.Request) {
	decoder := json.NewDecoder(r.Body)
	var crReq ChirpyRedRequest
	if err := decoder.Decode(&crReq); err != nil {
		respondWithError(w, http.StatusBadRequest, "Invalid JSON")
		return
	}
	if crReq.Event != "user.upgraded" {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	apiKey, err := auth.GetAPIKey(r.Header)
	if err != nil || apiKey != cfg.polkaKey {
		respondWithError(w, http.StatusUnauthorized, "Missing or invalid API key")
		return
	}
	if apiKey != cfg.polkaKey {
		respondWithError(w, http.StatusUnauthorized, "Invalid API key")
		return
	}
	_, err = cfg.dbQueries.EnableUserChripyRed(r.Context(), crReq.Data.UserID)
	if err != nil {
		respondWithError(w, http.StatusNotFound, "User not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)

}

func (cfg *apiConfig) updateUser(w http.ResponseWriter, req *http.Request) {
	userReq, err := validateUserRequest(req)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Invalid Request")
		return
	}
	//region checks
	access_token, err := auth.GetBearerToken(req.Header)
	if err != nil {
		respondWithError(w, http.StatusUnauthorized, "Missing or invalid Authorization header")
		return
	}
	userID, err := auth.ValidateJWT(access_token, cfg.jwtSecret)
	if err != nil {
		respondWithError(w, http.StatusUnauthorized, "Invalid token")
		return
	}
	if userID == uuid.Nil {
		respondWithError(w, http.StatusUnauthorized, "Invalid token")
		return
	}
	hashedPassword, err := auth.HashPassword(userReq.Password)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Failed to hash password")
		return
	}
	existingUser, err := cfg.dbQueries.GetUserByID(req.Context(), userID)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Failed to retrieve user")
		return
	}
	if existingUser == (database.User{}) {
		respondWithError(w, http.StatusNotFound, "User not found")
		return
	}
	//endregion
	//region  UPDATES to User
	updates := database.UpdateUserAuthenticationParams{
		ID:       userID,
		Email:    existingUser.Email,
		Password: existingUser.Password,
	}
	// currently can update both email and password together
	if userReq.Email != updates.Email && userReq.Email != "" {
		updates.Email = userReq.Email
	}
	if userReq.Password != "" && userReq.Password != existingUser.Password {
		updates.Password = hashedPassword
	}
	dbUser, err := cfg.dbQueries.UpdateUserAuthentication(req.Context(), updates)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Failed to update user")
		return
	}
	//endregion
	jsonData, err := createUserResponse(dbUser, "", "")
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Failed to create response")
		return
	}
	w.WriteHeader(http.StatusOK)
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Write(jsonData)
}
func (cfg *apiConfig) userHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		registry := map[string]func(*http.Request) ([]byte, error){
			"POST": cfg.createUserFromEmail,
			"GET":  cfg.getUserFromEmail,
			//"PUT": cfg.updateUser,
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
	hashedPassword, err := auth.HashPassword(userReq.Password)
	if err != nil {
		return nil, fmt.Errorf("failed to hash password: %w", err)
	}
	dbUser, err := cfg.dbQueries.CreateUser(req.Context(), database.CreateUserParams{
		Email:    userReq.Email,
		Password: hashedPassword,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create user: %w", err)
	}
	jsonData, err = createUserResponse(dbUser, "", "")
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
	jsonData, err = createUserResponse(dbUser, "", "")
	if err != nil {
		return nil, err
	}
	return jsonData, nil
}

func createUserResponse(dbUser database.User, bearer_token string, refresh_token string) ([]byte, error) {
	response := userResponse{
		ID:           dbUser.ID,
		Email:        dbUser.Email,
		CreatedAt:    dbUser.CreatedAt.Format("2006-01-02T15:04:05Z07:00"),
		UpdatedAt:    dbUser.UpdatedAt.Format("2006-01-02T15:04:05Z07:00"),
		Token:        bearer_token,
		RefreshToken: refresh_token,
		ChripyRed:    dbUser.IsChirpyRed,
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
	jwtSecret      string
	polkaKey       string
}
type userResponse struct {
	ID           uuid.UUID `json:"id"`
	Email        string    `json:"email"`
	CreatedAt    string    `json:"created_at"`
	UpdatedAt    string    `json:"updated_at"`
	Token        string    `json:"token"`
	RefreshToken string    `json:"refresh_token"`
	ChripyRed    bool      `json:"is_chirpy_red"`
}
type userRequest struct {
	Email            string `json:"email"`
	Password         string `json:"password"`
	ExpiresInSeconds int64  `json:"expires_in_seconds,omitempty"`
}

type chirpRequest struct {
	Body string `json:"body"`
	//UserID uuid.UUID `json:"user_id"`
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
	token, err := auth.GetBearerToken(r.Header)
	if err != nil {
		respondWithError(w, http.StatusUnauthorized, "Missing or invalid Authorization header")
		return
	}
	userID, err := auth.ValidateJWT(token, cfg.jwtSecret)
	if err != nil {
		respondWithError(w, http.StatusUnauthorized, "Invalid token")
		return
	}
	if _, err := cfg.dbQueries.GetUserByID(r.Context(), userID); err != nil {
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
		UserID: userID,
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
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(statusCode)
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

func (cfg *apiConfig) refreshTokenHandler(w http.ResponseWriter, r *http.Request) {
	refreshToken, err := auth.GetBearerToken(r.Header)
	if err != nil {
		respondWithError(w, http.StatusUnauthorized, "Invalid or missing token")
		return
	}
	dbRToken, err := cfg.dbQueries.GetRefreshToken(r.Context(), refreshToken)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Failed to get refresh token")
		return
	}
	if dbRToken == (database.RefreshToken{}) {
		respondWithError(w, http.StatusUnauthorized, "Invalid or expired token")
		return
	}
	if dbRToken.RevokedAt.Valid {
		respondWithError(w, http.StatusUnauthorized, "Token has been revoked")
		return
	}
	if dbRToken.ExpiresAt.Before(time.Now()) {
		respondWithError(w, http.StatusUnauthorized, "Token has expired")
		return
	}
	access_token, err := auth.MakeJWT(dbRToken.UserID, cfg.jwtSecret, time.Duration(1*time.Hour))
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Failed to generate access token")
		return
	}
	respondWithJSON(w, http.StatusOK, map[string]string{"token": access_token})
}

func (cfg *apiConfig) revokeRefreshToken(w http.ResponseWriter, r *http.Request) {
	refreshToken, err := auth.GetBearerToken(r.Header)
	if err != nil {
		respondWithError(w, http.StatusUnauthorized, "Invalid or missing token")
		return
	}
	dbRToken, err := cfg.dbQueries.GetRefreshToken(r.Context(), refreshToken)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Failed to get refresh token")
		return
	}
	if dbRToken == (database.RefreshToken{}) {
		respondWithError(w, http.StatusUnauthorized, "Invalid or expired token")
		return
	}
	_, err = cfg.dbQueries.RevokeRefreshToken(r.Context(), dbRToken.Token)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Failed to revoke refresh token")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
