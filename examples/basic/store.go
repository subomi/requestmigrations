package main

import (
	"errors"
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
				Profile: &profile{
					UID:        "999",
					GithubURL:  "https://github.com/subomi",
					TwitterURL: "https://twitter.com/subomiOluwalana",
				},
				CreatedAt: time.Date(2023, time.March, 10, 7, 0, 0, 0, time.UTC),
				UpdatedAt: time.Date(2023, time.March, 10, 7, 0, 0, 0, time.UTC),
			},
			&User{
				UID:       "456",
				Email:     "me@raymondtukpe.com",
				FirstName: "Raymond",
				LastName:  "Tukpe",
				Profile: &profile{
					UID:        "888",
					GithubURL:  "https://github.com/jirevwe",
					TwitterURL: "https://twitter.com/rtukpe",
				},
				CreatedAt: time.Date(2023, time.March, 10, 7, 0, 0, 0, time.UTC),
				UpdatedAt: time.Date(2023, time.March, 10, 7, 0, 0, 0, time.UTC),
			},
			&User{
				UID:       "789",
				Email:     "me@danieloluojomu.com",
				FirstName: "Daniel",
				LastName:  "Oluojomu",
				Profile: &profile{
					UID:        "777",
					GithubURL:  "https://github.com/danvixent",
					TwitterURL: "https://twitter.com/danvixent",
				},
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
	Profile   *profile  `json:"profile"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type profile struct {
	UID        string `json:"uid"`
	GithubURL  string `json:"github_url"`
	TwitterURL string `json:"twitter_url"`
}

type Store struct {
	users []*User
}

func (s *Store) Get(id string) (*User, error) {
	for _, u := range s.users {
		if u.UID == id {
			return u, nil
		}
	}

	return nil, errors.New("not found")
}

func (s *Store) GetAll() ([]*User, error) {
	return s.users, nil
}
