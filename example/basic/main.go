package main

import (
	"encoding/json"
	"log"
	"math/rand"
	"net/http"
	"os"
	"os/signal"
	"time"

	"github.com/gorilla/mux"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	rms "github.com/subomi/requestmigrations"
)

func main() {
	rm, err := rms.NewRequestMigration(
		&rms.RequestMigrationOptions{
			VersionHeader:  "X-Example-Version",
			CurrentVersion: "2023-05-01",
			VersionFormat:  rms.DateFormat,
		})

	if err != nil {
		log.Fatal(err)
	}

	rm.RegisterMigrations(rms.Migrations{
		"2023-05-01": []rms.Migration{
			&expandProfileForUserMigration{},
		},
		"2023-04-01": []rms.Migration{
			&combineNamesForUserMigration{},
		},
	})

	api := &API{rm: rm, store: userStore}
	backend := http.Server{
		Addr:    ":9000",
		Handler: buildMux(api),
	}

	err = backend.ListenAndServe()
	if err != nil {
		log.Fatal(err)
	}

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, os.Interrupt)
	<-quit
}

func buildMux(api *API) http.Handler {
	m := mux.NewRouter()

	m.Handle("/users",
		api.rm.VersionAPI(http.HandlerFunc(api.ListUser))).Methods("GET")
	m.HandleFunc("/users/{id}", api.GetUser).Methods("GET")

	reg := prometheus.NewRegistry()
	api.rm.RegisterMetrics(reg)

	promHandler := promhttp.HandlerFor(reg, promhttp.HandlerOpts{Registry: reg})
	m.Handle("/metrics", promHandler)

	return m
}

// api models
type ServerResponse struct {
	Status  bool            `json:"status"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

// define api
type API struct {
	store *Store
	rm    *rms.RequestMigration
}

func (a *API) ListUser(w http.ResponseWriter, r *http.Request) {
	// Generate a random Int type number between 1 and 10
	randNum := rand.Intn(3-1+1) + 1
	time.Sleep(time.Duration(randNum) * time.Second)

	users, err := a.store.GetAll()
	if err != nil {
		w.WriteHeader(http.StatusBadGateway)
		return
	}

	res, err := generateSuccessResponse(users, "users retrieved successfully")
	if err != nil {
		w.WriteHeader(http.StatusBadGateway)
		return
	}

	w.Write(res)
}

func (a *API) GetUser(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	user, err := a.store.Get(vars["id"])
	if err != nil {
		w.WriteHeader(http.StatusNotFound)
		return
	}

	res, err := generateSuccessResponse(user, "user retrieved successfully")
	if err != nil {
		w.WriteHeader(http.StatusBadGateway)
		return
	}

	w.Write(res)
}

// helpers
func generateSuccessResponse(payload interface{}, message string) ([]byte, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}

	s := &ServerResponse{
		Status:  true,
		Message: message,
		Data:    data,
	}

	res, err := json.Marshal(s)
	if err != nil {
		return nil, err
	}

	return res, nil
}
