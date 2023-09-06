package main

import (
	"errors"
	"sync"
	"time"
)

// stub database
type Store struct {
	mu    sync.Mutex
	users []User
}

var userStore = Store{
	users: []User{
		User{
			UID:       "123",
			Email:     "me@subomioluwalana.com",
			FirstName: "Subomi",
			LastName:  "Oluwalana",
			CreatedAt: time.Date(2023, time.March, 10, 7, 0, 0, 0, time.UTC),
			UpdatedAt: time.Date(2023, time.March, 10, 7, 0, 0, 0, time.UTC),
		},
		User{
			UID:       "456",
			Email:     "me@raymondtukpe.com",
			FirstName: "Raymond",
			LastName:  "Tukpe",
			CreatedAt: time.Date(2023, time.March, 10, 7, 0, 0, 0, time.UTC),
			UpdatedAt: time.Date(2023, time.March, 10, 7, 0, 0, 0, time.UTC),
		},
		User{
			UID:       "789",
			Email:     "me@danieloluojomu.com",
			FirstName: "Daniel",
			LastName:  "Oluojomu",
			CreatedAt: time.Date(2023, time.March, 10, 7, 0, 0, 0, time.UTC),
			UpdatedAt: time.Date(2023, time.March, 10, 7, 0, 0, 0, time.UTC),
		},
	},
}

func (s *Store) Get(id string) (*User, error) {
	for _, u := range s.users {
		if u.UID == id {
			return &u, nil
		}
	}

	return nil, errors.New("Not Found")
}
