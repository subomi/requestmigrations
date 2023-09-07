package main

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
	"os/signal"

	"github.com/gorilla/mux"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	rms "github.com/subomi/requestmigrations"
)

func main() {
	rm := rms.NewRequestMigration(
		&rms.RequestMigrationOptions{
			VersionHeader:  "X-Example-Version",
			CurrentVersion: "2023-09-02",
			VersionFormat:  rms.DateFormat,
		})

	rm.RegisterMigrations(rms.Migrations{
		"2023-09-02": []rms.Migration{
			&CombineNamesForUserMigration{},
		},
	})

	api := &API{rm: rm, store: userStore}
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
	m.Use(api.rm.VersionAPI)

	m.HandleFunc("/users", api.ListUser).Methods("GET")
	m.HandleFunc("/users/{id}", api.GetUser).Methods("GET")

	reg := prometheus.NewRegistry()
	reg.MustRegister(api.rm.Metric)

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
