package v20230401

import (
	"basicexample/helper"
	"encoding/json"
	"net/http"
	"strings"
	"time"
)

// Migrations
type ListUserResponseMigration struct{}

func (c *ListUserResponseMigration) Migrate(
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

	var res helper.ServerResponse
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

	body, err = helper.GenerateSuccessResponse(&newUsers, "users retrieved successfully")
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
