package main

import (
	"basicexample/helper"
	v20230401 "basicexample/v20230401"
	v20230501 "basicexample/v20230501"
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

	rm.RegisterMigrations(rms.MigrationStore{
		"2023-05-01": []rms.Migration{
			&v20230501.ListUserResponseMigration{},
		},
		"2023-04-01": []rms.Migration{
			&v20230401.ListUserResponseMigration{},
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

	m.HandleFunc("/users", api.ListUser).Methods("GET")
	m.HandleFunc("/users/{id}", api.GetUser).Methods("GET")

	reg := prometheus.NewRegistry()
	api.rm.RegisterMetrics(reg)

	promHandler := promhttp.HandlerFor(reg, promhttp.HandlerOpts{Registry: reg})
	m.Handle("/metrics", promHandler)

	return m
}

// api models

// define api
type API struct {
	store *Store
	rm    *rms.RequestMigration
}

func (a *API) ListUser(w http.ResponseWriter, r *http.Request) {
	err, vw, rollback := a.rm.Migrate(r, "ListUser")
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	defer rollback(w)

	// Generate a random Int type number between 1 and 10
	randNum := rand.Intn(2-1+1) + 1
	time.Sleep(time.Duration(randNum) * time.Second)

	users, err := a.store.GetAll()
	if err != nil {
		w.WriteHeader(http.StatusBadGateway)
		return
	}

	res, err := helper.GenerateSuccessResponse(users, "users retrieved successfully")
	if err != nil {
		w.WriteHeader(http.StatusBadGateway)
		return
	}

	vw.Write(res)
}

func (a *API) GetUser(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	user, err := a.store.Get(vars["id"])
	if err != nil {
		w.WriteHeader(http.StatusNotFound)
		return
	}

	res, err := helper.GenerateSuccessResponse(user, "user retrieved successfully")
	if err != nil {
		w.WriteHeader(http.StatusBadGateway)
		return
	}

	w.Write(res)
}
