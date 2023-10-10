package requestmigrations

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
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
	_, _ = w.Write([]byte(body))
}

func Test_VersionAPI(t *testing.T) {
	rm := newRequestMigration(t)
	registerBasicMigrations(t, rm)

	tests := map[string]struct {
		assert        require.ErrorAssertionFunc
		addHeader     func(req *http.Request)
		parseResponse func(t *testing.T, data []byte) error
	}{
		"no_transformation": {
			assert: require.NoError,
			addHeader: func(req *http.Request) {
				req.Header.Add("X-Test-Version", "2023-03-01")
			},
			parseResponse: func(t *testing.T, data []byte) error {
				var newUser user
				err := json.Unmarshal(data, &newUser)
				if err != nil {
					return err
				}

				if isStringEmpty(newUser.FirstName) || isStringEmpty(newUser.LastName) {
					return errors.New("Firstname or Lastname is not present")
				}

				return nil
			},
		},
		"should_transform_response_payload": {
			assert: require.NoError,
			parseResponse: func(t *testing.T, data []byte) error {
				var user oldUser
				err := json.Unmarshal(data, &user)
				if err != nil {
					return err
				}

				if isStringEmpty(user.FullName) {
					return errors.New("Fullname is not present")
				}

				return nil
			},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/users", strings.NewReader(""))
			if tc.addHeader != nil {
				tc.addHeader(req)
			}

			rr := httptest.NewRecorder()

			listUserHandler := http.HandlerFunc(listUser)
			rm.VersionAPI(listUserHandler).
				ServeHTTP(rr, req)

			// Inspect response to determine it worked.
			data, err := io.ReadAll(bytes.NewReader(rr.Body.Bytes()))
			if err != nil {
				t.Error(err)
			}

			// Assert.
			tc.assert(t, tc.parseResponse(t, data))
		})
	}

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

func (c *combineNamesMigration) ShouldMigrateConstraint(
	url *url.URL,
	method string,
	body []byte,
	isReq bool) bool {

	isUserPath := url.Path == "/users"
	isGetMethod := method == http.MethodGet
	isValidType := isReq == false

	return isUserPath && isGetMethod && isValidType
}

func (c *combineNamesMigration) Migrate(
	body []byte,
	h http.Header) ([]byte, http.Header, error) {

	var newuser user
	err := json.Unmarshal(body, &newuser)
	if err != nil {
		return nil, nil, err
	}

	var user oldUser
	user.Email = newuser.Email
	user.FullName = strings.Join([]string{newuser.FirstName, newuser.LastName}, " ")

	body, err = json.Marshal(&user)
	if err != nil {
		return nil, nil, err
	}

	return body, h, nil
}
