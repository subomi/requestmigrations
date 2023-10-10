package main

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Migrations
type combineNamesForUserMigration struct{}

func (c *combineNamesForUserMigration) ShouldMigrateConstraint(
	url *url.URL,
	method string,
	data []byte,
	isReq bool) bool {

	isUserPath := url.Path == "/users"
	isGetMethod := method == http.MethodGet
	isValidType := isReq == false

	return isUserPath && isGetMethod && isValidType
}

func (c *combineNamesForUserMigration) Migrate(
	body []byte,
	h http.Header) ([]byte, http.Header, error) {
	type oldUser struct {
		UID       string    `json:"uid"`
		Email     string    `json:"email"`
		FullName  string    `json:"full_name"`
		Profile   string    `json:"profile"`
		CreatedAt time.Time `json:"created_at"`
		UpdatedAt time.Time `json:"updated_at"`
	}

	var res ServerResponse
	err := json.Unmarshal(body, &res)
	if err != nil {
		return nil, nil, err
	}

	var users []*oldUser20230501
	err = json.Unmarshal(res.Data, &users)
	if err != nil {
		return nil, nil, err
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
		return nil, nil, err
	}

	return body, h, nil
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

func (e *expandProfileForUserMigration) ShouldMigrateConstraint(
	url *url.URL,
	method string,
	body []byte,
	isReq bool) bool {

	isUserPath := url.Path == "/users"
	isGetMethod := method == http.MethodGet
	isValidType := isReq == false

	return isUserPath && isGetMethod && isValidType
}

func (e *expandProfileForUserMigration) Migrate(
	body []byte,
	h http.Header) ([]byte, http.Header, error) {
	var res ServerResponse
	err := json.Unmarshal(body, &res)
	if err != nil {
		return nil, nil, err
	}

	var users []*User
	err = json.Unmarshal(res.Data, &users)
	if err != nil {
		return nil, nil, err
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
		return nil, nil, err
	}

	return body, h, nil
}
