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

// Migrations
type combineNamesForUserMigration struct{}

func (c *combineNamesForUserMigration) ShouldMigrateRequest(r *http.Request) bool {
	return false
}

func (c *combineNamesForUserMigration) MigrateRequest(r *http.Request) error {
	fmt.Println("migrating request...")

	return nil
}

func (c *combineNamesForUserMigration) ShouldMigrateResponse(
	req *http.Request,
	res *http.Response) bool {

	isUserPath := req.URL.Path == "/users"
	isGetMethod := req.Method == http.MethodGet

	return isUserPath && isGetMethod
}

func (c *combineNamesForUserMigration) MigrateResponse(r *http.Response) error {
	type oldUser struct {
		UID       string    `json:"uid"`
		Email     string    `json:"email"`
		FullName  string    `json:"full_name"`
		Profile   string    `json:"profile"`
		CreatedAt time.Time `json:"created_at"`
		UpdatedAt time.Time `json:"updated_at"`
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		return err
	}

	var res ServerResponse
	err = json.Unmarshal(body, &res)
	if err != nil {
		return err
	}

	var users []*oldUser20230501
	err = json.Unmarshal(res.Data, &users)
	if err != nil {
		return err
	}

	var newUsers []*oldUser
	for _, u := range users {
		var oldUser oldUser
		oldUser.UID = u.UID
		oldUser.Email = u.Email
		oldUser.FullName = strings.Join([]string{u.FirstName, u.LastName}, " ")
		oldUser.Profile = u.Profile
		oldUser.CreatedAt = u.CreatedAt
		oldUser.UpdatedAt = u.UpdatedAt
		newUsers = append(newUsers, &oldUser)
	}

	body, err = generateSuccessResponse(&newUsers, "users retrieved successfully")
	if err != nil {
		return err
	}

	r.Body = io.NopCloser(bytes.NewReader(body))
	return nil
}

type oldUser20230501 struct {
	UID       string    `json:"uid"`
	Email     string    `json:"email"`
	FirstName string    `json:"first_name"`
	LastName  string    `json:"last_name"`
	Profile   string    `json:"profile"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type expandProfileForUserMigration struct{}

func (e *expandProfileForUserMigration) ShouldMigrateRequest(r *http.Request) bool {
	return false
}

func (e *expandProfileForUserMigration) MigrateRequest(r *http.Request) error {
	return nil
}

func (e *expandProfileForUserMigration) ShouldMigrateResponse(
	req *http.Request,
	res *http.Response) bool {

	isUserPath := req.URL.Path == "/users"
	isGetMethod := req.Method == http.MethodGet

	return isUserPath && isGetMethod
}

func (e *expandProfileForUserMigration) MigrateResponse(r *http.Response) error {
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

	var newUsers []*oldUser20230501
	for _, u := range users {
		var oldUser oldUser20230501
		oldUser.UID = u.UID
		oldUser.Email = u.Email
		oldUser.FirstName = u.FirstName
		oldUser.LastName = u.LastName
		oldUser.Profile = u.Profile.UID
		oldUser.CreatedAt = u.CreatedAt
		oldUser.UpdatedAt = u.UpdatedAt
		newUsers = append(newUsers, &oldUser)
	}

	body, err = generateSuccessResponse(&newUsers, "users retrieved successfully")
	if err != nil {
		return err
	}

	r.Body = io.NopCloser(bytes.NewReader(body))
	return nil
}
