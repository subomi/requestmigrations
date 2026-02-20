package main

import (
	"basicexample/helper"
	"log"
	"math/rand"
	"net/http"
	"os"
	"os/signal"
	"time"

	"github.com/gorilla/mux"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	rms "github.com/subomi/requestmigrations/v2"
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

	err = rm.Register(
		rms.Migration[User]("2023-05-01", &UserMigration{}),
		rms.Migration[profile]("2023-05-01", &ProfileMigration{}),
	).Build()
	if err != nil {
		log.Fatal(err)
	}

	api := &API{rm: rm, store: userStore}
	backend := http.Server{
		Addr:    ":9000",
		Handler: buildMux(api),
	}

	go func() {
		log.Println("Starting server on :9000")
		if err := backend.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("listen: %s\n", err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, os.Interrupt)
	<-quit
	log.Println("Shutting down server...")
}

func buildMux(api *API) http.Handler {
	m := mux.NewRouter()

	m.HandleFunc("/users", api.ListUser).Methods("GET")
	m.HandleFunc("/users/{id}", api.GetUser).Methods("GET")

	reg := prometheus.NewRegistry()
	api.rm.RegisterMetrics(reg)

	promHandler := promhttp.HandlerFor(reg, promhttp.HandlerOpts{Registry: reg})
	m.Handle("/metrics", promHandler)

	return m
}

type API struct {
	store *Store
	rm    *rms.RequestMigration
}

func (a *API) ListUser(w http.ResponseWriter, r *http.Request) {
	// Generate a random delay
	randNum := rand.Intn(2) + 1
	time.Sleep(time.Duration(randNum) * time.Second)

	users, err := a.store.GetAll()
	if err != nil {
		w.WriteHeader(http.StatusBadGateway)
		return
	}

	migrator, err := a.rm.For(r)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	data, err := migrator.Marshal(users)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	res, err := helper.GenerateSuccessResponseFromRaw(data, "users retrieved successfully")
	if err != nil {
		w.WriteHeader(http.StatusBadGateway)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Write(res)
}

func (a *API) GetUser(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	user, err := a.store.Get(vars["id"])
	if err != nil {
		w.WriteHeader(http.StatusNotFound)
		return
	}

	migrator, err := a.rm.For(r)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	data, err := migrator.Marshal(user)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	res, err := helper.GenerateSuccessResponseFromRaw(data, "user retrieved successfully")
	if err != nil {
		w.WriteHeader(http.StatusBadGateway)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Write(res)
}
