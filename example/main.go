package main

import (
	"encoding/json"
	"log"
	"math/rand"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"time"

	"github.com/gorilla/mux"
	rms "github.com/subomi/requestmigrations"
)

func main() {
	rm := rms.NewRequestMigration(
		&rms.RequestMigrationOptions{
			VersionHeader:  "X-Example-Version",
			CurrentVersion: "2023-09-02",
			DefaultVersion: "2023-08-01",
			VersionFormat:  rms.DateFormat,
		})

	rm.RegisterMigrations(rms.Migrations{
		"2023-09-02": []rms.Migration{
			&CombineNamesForUserMigration{},
		},
		"2023-08-01": []rms.Migration{},
	})

	api := &API{
		rm: rm,
		db: &userStore,
	}

	backend := http.Server{
		Addr:    ":9000",
		Handler: buildMux(api),
	}

	err := backend.ListenAndServe()
	if err != nil {
		log.Fatal(err)
	}

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, os.Interrupt)
	<-quit
}

func buildMux(api *API) http.Handler {
	m := mux.NewRouter()

	m.HandleFunc("/users", api.ListUser).Methods("GET")
	m.HandleFunc("/users", api.CreateUser).Methods("POST")
	m.HandleFunc("/users/{id}", api.rm.VersionAPI(api.GetUser)).Methods("GET")
	m.HandleFunc("/users/{id}", api.UpdateUser).Methods("PUT")
	m.HandleFunc("/users/{id}", api.DeleteUser).Methods("DELETE")

	return m
}

type User struct {
	UID       string    `json:"uid"`
	Email     string    `json:"email"`
	FirstName string    `json:"first_name"`
	LastName  string    `json:"last_name"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// api models

type UserRequest struct {
	Email     string `json:"email"`
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name"`
}

type ServerResponse struct {
	Status  bool            `json:"status"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

// define api
type API struct {
	db *Store
	rm *rms.RequestMigration
}

func (a *API) ListUser(w http.ResponseWriter, r *http.Request) {
	// generate response
	res, err := json.Marshal(a.db.users)
	if err != nil {
		w.WriteHeader(http.StatusBadGateway)
		return
	}

	w.Write(res)
}

func (a *API) CreateUser(w http.ResponseWriter, r *http.Request) {
	a.db.mu.Lock()
	defer a.db.mu.Unlock()

	// parse request
	var payload UserRequest
	err := json.NewDecoder(r.Body).Decode(&payload)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{ "status": false }`))
		return
	}

	// business logic
	var user User
	user.UID = strconv.Itoa(rand.Intn(999))
	user.Email = payload.Email
	user.FirstName = payload.FirstName
	user.LastName = payload.LastName
	user.CreatedAt = time.Now()
	user.UpdatedAt = time.Now()

	a.db.users = append(a.db.users, user)

	// generate response
	res, err := json.Marshal(user)
	if err != nil {
		w.Write([]byte(`{ "status": false }`))
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	w.Write(res)
}

func (a *API) GetUser(w http.ResponseWriter, r *http.Request) {
	// parse request
	vars := mux.Vars(r)

	// business logic
	user, err := a.db.Get(vars["id"])
	if err != nil {
		w.WriteHeader(http.StatusNotFound)
		return
	}

	// generate response
	res, err := json.Marshal(user)
	if err != nil {
		w.WriteHeader(http.StatusBadGateway)
		return
	}

	w.Write(res)
}

func (a *API) UpdateUser(w http.ResponseWriter, r *http.Request) {
	a.db.mu.Lock()
	defer a.db.mu.Unlock()

	// parse request
	var payload UserRequest
	err := json.NewDecoder(r.Body).Decode(&payload)
	if err != nil {
		w.Write([]byte(`{ "status": false }`))
		w.WriteHeader(http.StatusBadRequest)
	}

	vars := mux.Vars(r)

	// business logic
	var user *User
	for _, u := range a.db.users {
		if u.UID == vars["id"] {
			user = &u
		}
	}

	if user == nil {
		w.WriteHeader(http.StatusNotFound)
		return
	}

	user.FirstName = payload.FirstName
	user.LastName = payload.LastName

	// generate response
	res, err := json.Marshal(user)
	if err != nil {
		w.Write([]byte(`{ "status": false }`))
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	w.Write(res)
}

func (a *API) DeleteUser(w http.ResponseWriter, r *http.Request) {
	a.db.mu.Lock()
	defer a.db.mu.Unlock()

	// parse request
	vars := mux.Vars(r)

	for i, u := range a.db.users {
		if u.UID == vars["id"] {

			// business logic
			a.db.users = append(a.db.users[:i], a.db.users[i+1:]...)

			// generate response
			w.Write([]byte(`{ "status": true }`))
			return
		}
	}

	w.WriteHeader(http.StatusNotFound)
}

// helpers
func generateResponse(payload interface{}) ([]byte, error) {
	return nil, nil
}
