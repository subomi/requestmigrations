package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type OldUser struct {
	UID       string    `json:"uid"`
	Email     string    `json:"email"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Migrations
type CombineNamesForUserMigration struct{}

func (c *CombineNamesForUserMigration) ShouldMigrateRequest(r *http.Request) bool {
	return true
}

func (c *CombineNamesForUserMigration) MigrateRequest(r *http.Request) error {
	fmt.Println("migrating request...")

	return nil
}

func (c *CombineNamesForUserMigration) ShouldMigrateResponse(
	req *http.Request,
	res *http.Response) bool {

	isUserPath := req.URL.Path == "/users"
	isGetMethod := req.Method == http.MethodGet

	return isUserPath && isGetMethod
}

func (c *CombineNamesForUserMigration) MigrateResponse(r *http.Response) error {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		return err
	}

	var res ServerResponse
	err = json.Unmarshal(body, &res)
	if err != nil {
		return err
	}

	var users []*User
	err = json.Unmarshal(res.Data, &users)
	if err != nil {
		return err
	}

	var newUsers []*OldUser
	for _, u := range users {
		var oldUser OldUser
		oldUser.UID = u.UID
		oldUser.Email = u.Email
		oldUser.Name = strings.Join([]string{u.FirstName, u.LastName}, " ")
		oldUser.CreatedAt = u.CreatedAt
		oldUser.UpdatedAt = u.UpdatedAt
		newUsers = append(newUsers, &oldUser)
	}

	body, err = json.Marshal(&newUsers)
	if err != nil {
		return err
	}

	r.Body = io.NopCloser(bytes.NewReader(body))
	return nil
}
