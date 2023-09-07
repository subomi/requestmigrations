package main

import (
	"errors"
	"sync"
	"time"
)

var userStore *Store

func init() {
	userStore = &Store{
		users: []*User{
			&User{
				UID:       "123",
				Email:     "me@subomioluwalana.com",
				FirstName: "Subomi",
				LastName:  "Oluwalana",
				CreatedAt: time.Date(2023, time.March, 10, 7, 0, 0, 0, time.UTC),
				UpdatedAt: time.Date(2023, time.March, 10, 7, 0, 0, 0, time.UTC),
			},
			&User{
				UID:       "456",
				Email:     "me@raymondtukpe.com",
				FirstName: "Raymond",
				LastName:  "Tukpe",
				CreatedAt: time.Date(2023, time.March, 10, 7, 0, 0, 0, time.UTC),
				UpdatedAt: time.Date(2023, time.March, 10, 7, 0, 0, 0, time.UTC),
			},
			&User{
				UID:       "789",
				Email:     "me@danieloluojomu.com",
				FirstName: "Daniel",
				LastName:  "Oluojomu",
				CreatedAt: time.Date(2023, time.March, 10, 7, 0, 0, 0, time.UTC),
				UpdatedAt: time.Date(2023, time.March, 10, 7, 0, 0, 0, time.UTC),
			},
		},
	}
}

// stub database
type User struct {
	UID       string    `json:"uid"`
	Email     string    `json:"email"`
	FirstName string    `json:"first_name"`
	LastName  string    `json:"last_name"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type Store struct {
	mu    sync.Mutex
	users []*User
}

func (s *Store) Get(id string) (*User, error) {
	for _, u := range s.users {
		if u.UID == id {
			return u, nil
		}
	}

	return nil, errors.New("Not Found")
}

func (s *Store) GetAll() ([]*User, error) {
	return s.users, nil
}
