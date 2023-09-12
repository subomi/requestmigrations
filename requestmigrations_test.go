package requestmigrations

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type user struct {
	Email     string `json:"email"`
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name"`
}

func listUser(w http.ResponseWriter, r *http.Request) {
	user := &user{
		Email:     "engineering@getconvoy.io",
		FirstName: "Convoy",
		LastName:  "Engineering",
	}

	body, _ := json.Marshal(user)
	w.Write([]byte(body))
}

func Test_VersionAPI(t *testing.T) {
	listUserHandler := http.HandlerFunc(listUser)
	rm := newRequestMigration(t)
	registerBasicMigrations(t, rm)

	req := httptest.NewRequest(http.MethodGet, "/users", strings.NewReader(""))
	rr := httptest.NewRecorder()

	rm.VersionAPI(listUserHandler).
		ServeHTTP(rr, req)

	// Inspect response to determine it worked.
	user := parseResponse(t, rr)

	if isStringEmpty(user.FullName) {
		t.Error("Error failed")
	}
}

func parseResponse(t *testing.T, rr *httptest.ResponseRecorder) *oldUser {
	data, err := io.ReadAll(bytes.NewReader(rr.Body.Bytes()))
	if err != nil {
		t.Error(err)
	}

	var userPayload oldUser
	_ = json.Unmarshal(data, &userPayload)
	return &userPayload
}

func newRequestMigration(t *testing.T) *RequestMigration {
	opts := &RequestMigrationOptions{
		VersionHeader:  "X-Test-Version",
		CurrentVersion: "2023-03-01",
		VersionFormat:  DateFormat,
	}

	rm, err := NewRequestMigration(opts)
	if err != nil {
		t.Fatal(err)
	}

	return rm
}

func registerBasicMigrations(t *testing.T, rm *RequestMigration) {
	migrations := &Migrations{
		"2023-03-01": []Migration{
			&combineNamesMigration{},
		},
	}

	err := rm.RegisterMigrations(*migrations)
	if err != nil {
		t.Error(err)
	}
}

type oldUser struct {
	Email    string `json:"email"`
	FullName string `json:"full_name"`
}
type combineNamesMigration struct{}

func (c *combineNamesMigration) ShouldMigrateRequest(r *http.Request) bool {
	return false
}

func (c *combineNamesMigration) MigrateRequest(r *http.Request) error {
	return nil
}

func (c *combineNamesMigration) ShouldMigrateResponse(
	req *http.Request,
	res *http.Response) bool {
	isUserPath := req.URL.Path == "/users"
	isGetMethod := req.Method == http.MethodGet

	return isUserPath && isGetMethod
}

func (c *combineNamesMigration) MigrateResponse(r *http.Response) error {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		return err
	}

	var newuser user
	err = json.Unmarshal(body, &newuser)
	if err != nil {
		return err
	}

	var user oldUser
	user.Email = newuser.Email
	user.FullName = strings.Join([]string{newuser.FirstName, newuser.LastName}, " ")

	body, err = json.Marshal(&user)
	if err != nil {
		return err
	}

	r.Body = io.NopCloser(bytes.NewReader(body))
	return nil
}
