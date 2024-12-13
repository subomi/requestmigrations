package v20230501

import (
	"basicexample/helper"
	"encoding/json"
	"net/http"
	"time"
)

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

// ListUserResponseMigration handles the response migration for the list users endpoint
type ListUserResponseMigration struct{}

func (e *ListUserResponseMigration) Migrate(
	body []byte, h http.Header) ([]byte, http.Header, error) {
	var res helper.ServerResponse
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

	body, err = helper.GenerateSuccessResponse(&newUsers, "users retrieved successfully")
	if err != nil {
		return nil, nil, err
	}

	return body, h, nil
}

func (e *ListUserResponseMigration) ChangeDescription() string {
	return "Expanded profile field to include GitHub and Twitter URLs"
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
