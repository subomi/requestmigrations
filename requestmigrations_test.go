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
			&splitNameMigration{},
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
	isValidType := isReq == false

	return isUserPath && isValidType
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

type splitNameMigration struct{}

func (c *splitNameMigration) ShouldMigrateConstraint(
	url *url.URL,
	method string,
	body []byte,
	isReq bool) bool {

	isUserPath := url.Path == "/users"
	isPostMethod := method == http.MethodPost
	isValidType := isReq == true

	return isUserPath && isPostMethod && isValidType
}

func (c *splitNameMigration) Migrate(
	body []byte,
	h http.Header) ([]byte, http.Header, error) {

	var oUser oldUser
	err := json.Unmarshal(body, &oUser)
	if err != nil {
		return nil, nil, err
	}

	var nUser user
	nUser.Email = oUser.Email

	splitName := strings.Split(oUser.FullName, " ")
	nUser.FirstName = splitName[0]
	nUser.LastName = splitName[1]

	body, err = json.Marshal(&nUser)
	if err != nil {
		return nil, nil, err
	}

	return body, h, nil
}

func createUser(t *testing.T, rm *RequestMigration) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		err := rm.VersionRequest(r)
		if err != nil {
			t.Fatal(err)
		}

		payload, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatal(err)
		}

		var userObject user
		err = json.Unmarshal(payload, &userObject)
		if err != nil {
			t.Fatal(err)
		}

		userObject = user{
			Email:     userObject.Email,
			FirstName: userObject.FirstName,
			LastName:  userObject.LastName,
		}

		body, err := json.Marshal(userObject)
		if err != nil {
			t.Fatal(err)
		}

		resBody, err := rm.VersionResponse(r, body)
		if err != nil {
			t.Fatal(err)
		}

		_, _ = w.Write(resBody)
	})
}

func Test_VersionRequest(t *testing.T) {
	rm := newRequestMigration(t)
	registerBasicMigrations(t, rm)

	tests := map[string]struct {
		assert        require.ErrorAssertionFunc
		body          strings.Reader
		addHeader     func(req *http.Request)
		parseResponse func(t *testing.T, data []byte) error
	}{
		"no_transformation": {
			assert: require.NoError,
			body: *strings.NewReader(`
				{
					"email": "engineering@getconvoy.io",
					"first_name": "Convoy",
					"last_name": "Engineering"
				}
			`),
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
		"should_transform_request_payload": {
			assert: require.NoError,
			body: *strings.NewReader(`
				{
					"email": "engineering@getconvoy.io",
					"full_name": "Convoy Engineering"
				}
			`),
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
			req := httptest.NewRequest(http.MethodPost, "/users", &tc.body)

			if tc.addHeader != nil {
				tc.addHeader(req)
			}

			rr := httptest.NewRecorder()

			createUserHandler := createUser(t, rm)
			createUserHandler.ServeHTTP(rr, req)

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

func getUser(t *testing.T, rm *RequestMigration) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		err := rm.VersionRequest(r)
		if err != nil {
			t.Fatal(err)
		}

		user := &user{
			Email:     "engineering@getconvoy.io",
			FirstName: "Convoy",
			LastName:  "Engineering",
		}

		body, err := json.Marshal(user)
		if err != nil {
			t.Fatal(err)
		}

		resBody, err := rm.VersionResponse(r, body)
		if err != nil {
			t.Fatal(err)
		}

		_, _ = w.Write(resBody)
	})
}

func Test_VersionResponse(t *testing.T) {
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

			getUserHandler := getUser(t, rm)
			getUserHandler.ServeHTTP(rr, req)

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
