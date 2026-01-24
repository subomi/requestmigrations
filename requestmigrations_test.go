package requestmigrations

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

type NameReader interface {
	Read(string) string
}

type User struct {
	Name NameReader `json:"name"`
}

// v1 - 2023-02-01
type profile struct {
	Name    string `json:"name"`
	Address string `json:"address"`
}

// v2 - 2023-03-01
type profilev2 struct {
	FirstName string `json:"firstName"`
	LastName  string `json:"lastName"`
	Address   *address
}

type address struct {
	StreetName string `json:"streetName"`
	Country    string `json:"country"`
	State      string `json:"state"`
	Postcode   string `json:"postCode"`
}

type addressMigration struct{}

func (m *addressMigration) MigrateForward(ctx context.Context, data any) (any, error) {
	return nil, nil
}

func (m *addressMigration) MigrateBackward(ctx context.Context, data any) (any, error) {
	a, ok := data.(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("bad type")
	}

	addrString := fmt.Sprintf("%s %s %s %s", a["streetName"],
		a["state"], a["country"], a["postCode"])

	return addrString, nil
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

func registerVersions(t *testing.T, rm *RequestMigration) {
	// Register migrations for version 2023-03-01
	err := Register[address](rm, "2023-03-01", &addressMigration{})

	if err != nil {
		t.Fatal(err)
	}
}

func Test_Marshal(t *testing.T) {
	rm := newRequestMigration(t)
	registerVersions(t, rm)

	t.Run("transforms address struct to string for older version", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/profile", strings.NewReader(""))
		req.Header.Add("X-Test-Version", "2023-02-01")

		pStruct := profilev2{
			FirstName: "John",
			LastName:  "Doe",
			Address: &address{
				StreetName: "123 Main St",
				State:      "London",
				Country:    "UK",
				Postcode:   "CR0 1GB",
			},
		}
		migrator, err := rm.For(req)
		require.NoError(t, err)

		bytesW, err := migrator.Marshal(&pStruct)
		require.NoError(t, err)

		var res map[string]interface{}
		err = json.Unmarshal(bytesW, &res)
		require.NoError(t, err)

		// Address should be transformed from struct to string by MigrateBackward
		// Format: "streetName state country postCode"
		expectedAddr := "123 Main St London UK CR0 1GB"
		require.Equal(t, expectedAddr, string(res["Address"].(string)))
		require.Equal(t, "John", res["firstName"])
		require.Equal(t, "Doe", res["lastName"])
	})

	t.Run("no transformation for current version", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/profile", strings.NewReader(""))
		req.Header.Add("X-Test-Version", "2023-03-01")

		pStruct := profilev2{
			FirstName: "Jane",
			LastName:  "Smith",
			Address: &address{
				StreetName: "456 Oak Ave",
				State:      "Manchester",
				Country:    "UK",
				Postcode:   "M1 2AB",
			},
		}
		migrator, err := rm.For(req)
		require.NoError(t, err)

		bytesW, err := migrator.Marshal(&pStruct)
		require.NoError(t, err)

		var res map[string]interface{}
		err = json.Unmarshal(bytesW, &res)
		require.NoError(t, err)

		// Address should remain as struct for current version
		addrMap, ok := res["Address"].(map[string]interface{})
		require.True(t, ok, "Address should be a map for current version")
		require.Equal(t, "456 Oak Ave", addrMap["streetName"])
		require.Equal(t, "Manchester", addrMap["state"])
	})
}

func Test_Unmarshal(t *testing.T) {}

type AddressString string

type addressStringMigration struct{}

func (m *addressStringMigration) MigrateForward(ctx context.Context, data any) (any, error) {
	s, ok := data.(string)
	if !ok {
		return nil, fmt.Errorf("bad type")
	}
	return AddressString("Migrated: " + s), nil
}

func (m *addressStringMigration) MigrateBackward(ctx context.Context, data any) (any, error) {
	s, ok := data.(string)
	if !ok {
		return nil, fmt.Errorf("bad type")
	}
	return "Backward: " + s, nil
}

type CustomUser struct {
	Address AddressString `json:"address"`
}

func Test_CustomPrimitive(t *testing.T) {
	opts := &RequestMigrationOptions{
		VersionHeader:  "X-Test-Version",
		CurrentVersion: "2023-03-01",
		VersionFormat:  DateFormat,
	}
	rm, _ := NewRequestMigration(opts)
	Register[AddressString](rm, "2023-03-01", &addressStringMigration{})

	tests := []struct {
		name              string
		inputAddress      string
		expectedMarshal   string
		expectedUnmarshal AddressString
	}{
		{
			name:              "transforms Main St address",
			inputAddress:      "Main St",
			expectedMarshal:   "Backward: Main St",
			expectedUnmarshal: AddressString("Migrated: Main St"),
		},
		{
			name:              "transforms Oak Ave address",
			inputAddress:      "Oak Ave",
			expectedMarshal:   "Backward: Oak Ave",
			expectedUnmarshal: AddressString("Migrated: Oak Ave"),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.Header.Add("X-Test-Version", "2023-02-01")

			migrator, err := rm.For(req)
			require.NoError(t, err)

			// Test Marshal
			u := CustomUser{Address: AddressString(tc.inputAddress)}
			data, err := migrator.Marshal(&u)
			require.NoError(t, err)

			var res map[string]interface{}
			json.Unmarshal(data, &res)
			require.Equal(t, tc.expectedMarshal, res["address"])

			// Test Unmarshal
			jsonData := fmt.Sprintf(`{"address": "%s"}`, tc.inputAddress)
			var unmarshaledUser CustomUser
			err = migrator.Unmarshal([]byte(jsonData), &unmarshaledUser)
			require.NoError(t, err)
			require.Equal(t, tc.expectedUnmarshal, unmarshaledUser.Address)
		})
	}
}

// dummyMigration is a no-op migration for testing registration validation
type dummyMigration struct{}

func (m *dummyMigration) MigrateForward(ctx context.Context, data any) (any, error) {
	return data, nil
}

func (m *dummyMigration) MigrateBackward(ctx context.Context, data any) (any, error) {
	return data, nil
}

// Named composite types for testing - these should be ALLOWED
type NamedSlice []string
type NamedMap map[string]int
type NamedIntSlice []int

func Test_MigrationTypeValidation(t *testing.T) {
	opts := &RequestMigrationOptions{
		VersionHeader:  "X-Test-Version",
		CurrentVersion: "2023-03-01",
		VersionFormat:  DateFormat,
	}
	rm, err := NewRequestMigration(opts)
	require.NoError(t, err)

	tests := []struct {
		name         string
		registerFunc func() error
		wantErr      bool
	}{
		// Built-in primitives - should be REJECTED
		{
			name: "rejects native string type",
			registerFunc: func() error {
				return Register[string](rm, "2023-03-01", &dummyMigration{})
			},
			wantErr: true,
		},
		{
			name: "rejects native int type",
			registerFunc: func() error {
				return Register[int](rm, "2023-03-01", &dummyMigration{})
			},
			wantErr: true,
		},
		{
			name: "rejects native bool type",
			registerFunc: func() error {
				return Register[bool](rm, "2023-03-01", &dummyMigration{})
			},
			wantErr: true,
		},
		{
			name: "rejects native float64 type",
			registerFunc: func() error {
				return Register[float64](rm, "2023-03-01", &dummyMigration{})
			},
			wantErr: true,
		},
		{
			name: "rejects native int64 type",
			registerFunc: func() error {
				return Register[int64](rm, "2023-03-01", &dummyMigration{})
			},
			wantErr: true,
		},
		{
			name: "rejects native uint type",
			registerFunc: func() error {
				return Register[uint](rm, "2023-03-01", &dummyMigration{})
			},
			wantErr: true,
		},

		// Unnamed composite types - should be REJECTED
		{
			name: "rejects unnamed string slice",
			registerFunc: func() error {
				return Register[[]string](rm, "2023-03-01", &dummyMigration{})
			},
			wantErr: true,
		},
		{
			name: "rejects unnamed int slice",
			registerFunc: func() error {
				return Register[[]int](rm, "2023-03-01", &dummyMigration{})
			},
			wantErr: true,
		},
		{
			name: "rejects unnamed byte slice",
			registerFunc: func() error {
				return Register[[]byte](rm, "2023-03-01", &dummyMigration{})
			},
			wantErr: true,
		},
		{
			name: "rejects unnamed map string to string",
			registerFunc: func() error {
				return Register[map[string]string](rm, "2023-03-01", &dummyMigration{})
			},
			wantErr: true,
		},
		{
			name: "rejects unnamed map string to int",
			registerFunc: func() error {
				return Register[map[string]int](rm, "2023-03-01", &dummyMigration{})
			},
			wantErr: true,
		},
		{
			name: "rejects unnamed interface type",
			registerFunc: func() error {
				return Register[interface{}](rm, "2023-03-01", &dummyMigration{})
			},
			wantErr: true,
		},
		{
			name: "rejects error interface type",
			registerFunc: func() error {
				return Register[error](rm, "2023-03-01", &dummyMigration{})
			},
			wantErr: true,
		},

		// User-defined named types - should be ALLOWED
		{
			name: "allows custom string type alias",
			registerFunc: func() error {
				return Register[AddressString](rm, "2023-02-15", &dummyMigration{})
			},
			wantErr: false,
		},
		{
			name: "allows struct type",
			registerFunc: func() error {
				return Register[profile](rm, "2023-02-15", &dummyMigration{})
			},
			wantErr: false,
		},
		{
			name: "allows named slice type",
			registerFunc: func() error {
				return Register[NamedSlice](rm, "2023-02-15", &dummyMigration{})
			},
			wantErr: false,
		},
		{
			name: "allows named map type",
			registerFunc: func() error {
				return Register[NamedMap](rm, "2023-02-15", &dummyMigration{})
			},
			wantErr: false,
		},
		{
			name: "allows named int slice type",
			registerFunc: func() error {
				return Register[NamedIntSlice](rm, "2023-02-15", &dummyMigration{})
			},
			wantErr: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.registerFunc()
			if tc.wantErr {
				require.Error(t, err)
				require.ErrorIs(t, err, ErrNativeTypeMigration)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

type CyclicUser struct {
	ID        string           `json:"id"`
	Name      string           `json:"name"`
	Workspace *CyclicWorkspace `json:"workspace"`
}

type CyclicWorkspace struct {
	ID    string        `json:"id"`
	Title string        `json:"title"`
	Users []*CyclicUser `json:"users"`
}

type cyclicUserMigration struct{}

func (m *cyclicUserMigration) MigrateForward(ctx context.Context, data any) (any, error) {
	d, ok := data.(map[string]interface{})
	if !ok {
		return data, nil
	}
	// v1 -> v2: "username" becomes "name"
	if username, exists := d["username"]; exists {
		d["name"] = username
		delete(d, "username")
	}
	return d, nil
}

func (m *cyclicUserMigration) MigrateBackward(ctx context.Context, data any) (any, error) {
	d, ok := data.(map[string]interface{})
	if !ok {
		return data, nil
	}
	// v2 -> v1: "name" becomes "username"
	if name, exists := d["name"]; exists {
		d["username"] = name
		delete(d, "name")
	}
	return d, nil
}

type cyclicWorkspaceMigration struct{}

func (m *cyclicWorkspaceMigration) MigrateForward(ctx context.Context, data any) (any, error) {
	d, ok := data.(map[string]interface{})
	if !ok {
		return data, nil
	}
	// v1 -> v2: "name" becomes "title"
	if name, exists := d["name"]; exists {
		d["title"] = name
		delete(d, "name")
	}
	return d, nil
}

func (m *cyclicWorkspaceMigration) MigrateBackward(ctx context.Context, data any) (any, error) {
	d, ok := data.(map[string]interface{})
	if !ok {
		return data, nil
	}
	// v2 -> v1: "title" becomes "name"
	if title, exists := d["title"]; exists {
		d["name"] = title
		delete(d, "title")
	}
	return d, nil
}

func Test_Cycles(t *testing.T) {
	opts := &RequestMigrationOptions{
		VersionHeader:  "X-Test-Version",
		CurrentVersion: "2023-03-01",
		VersionFormat:  DateFormat,
	}

	t.Run("build graph with cycles", func(t *testing.T) {
		rm, _ := NewRequestMigration(opts)
		_, err := rm.graphBuilder.buildFromType(reflect.TypeOf(CyclicUser{}), &Version{Format: DateFormat, Value: "2023-01-01"})
		require.NoError(t, err)
	})

	t.Run("marshal cyclic structure with migrations", func(t *testing.T) {
		rm, _ := NewRequestMigration(opts)
		Register[CyclicUser](rm, "2023-03-01", &cyclicUserMigration{})
		Register[CyclicWorkspace](rm, "2023-03-01", &cyclicWorkspaceMigration{})

		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Add("X-Test-Version", "2023-01-01") // old version

		user := &CyclicUser{
			ID:   "user-1",
			Name: "Alice",
			Workspace: &CyclicWorkspace{
				ID:    "ws-1",
				Title: "Engineering",
				Users: []*CyclicUser{
					{ID: "user-2", Name: "Bob"},
				},
			},
		}

		migrator, err := rm.For(req)
		require.NoError(t, err)

		data, err := migrator.Marshal(user)
		require.NoError(t, err)

		var res map[string]interface{}
		err = json.Unmarshal(data, &res)
		require.NoError(t, err)

		// User should have "username" instead of "name"
		require.Equal(t, "Alice", res["username"])
		require.Nil(t, res["name"])

		// Workspace should have "name" instead of "title"
		ws := res["workspace"].(map[string]interface{})
		require.Equal(t, "Engineering", ws["name"])
		require.Nil(t, ws["title"])

		// Nested users should also be migrated
		users := ws["users"].([]interface{})
		nestedUser := users[0].(map[string]interface{})
		require.Equal(t, "Bob", nestedUser["username"])
		require.Nil(t, nestedUser["name"])
	})

	t.Run("unmarshal cyclic structure with migrations", func(t *testing.T) {
		rm, _ := NewRequestMigration(opts)
		Register[CyclicUser](rm, "2023-03-01", &cyclicUserMigration{})
		Register[CyclicWorkspace](rm, "2023-03-01", &cyclicWorkspaceMigration{})

		req := httptest.NewRequest(http.MethodPost, "/", nil)
		req.Header.Add("X-Test-Version", "2023-01-01") // old version

		// JSON from old API client (using old field names)
		jsonData := `{
			"id": "user-1",
			"username": "Alice",
			"workspace": {
				"id": "ws-1",
				"name": "Engineering",
				"users": [
					{"id": "user-2", "username": "Bob"}
				]
			}
		}`

		migrator, err := rm.For(req)
		require.NoError(t, err)

		var user CyclicUser
		err = migrator.Unmarshal([]byte(jsonData), &user)
		require.NoError(t, err)

		// User should have name populated from "username"
		require.Equal(t, "user-1", user.ID)
		require.Equal(t, "Alice", user.Name)

		// Workspace should have title populated from "name"
		require.NotNil(t, user.Workspace)
		require.Equal(t, "ws-1", user.Workspace.ID)
		require.Equal(t, "Engineering", user.Workspace.Title)

		// Nested users should also be migrated
		require.Len(t, user.Workspace.Users, 1)
		require.Equal(t, "user-2", user.Workspace.Users[0].ID)
		require.Equal(t, "Bob", user.Workspace.Users[0].Name)
	})

	t.Run("deeply nested cycles", func(t *testing.T) {
		rm, _ := NewRequestMigration(opts)
		Register[CyclicUser](rm, "2023-03-01", &cyclicUserMigration{})
		Register[CyclicWorkspace](rm, "2023-03-01", &cyclicWorkspaceMigration{})

		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Add("X-Test-Version", "2023-01-01")

		// Create a structure with multiple levels of nesting
		user := &CyclicUser{
			ID:   "user-1",
			Name: "Alice",
			Workspace: &CyclicWorkspace{
				ID:    "ws-1",
				Title: "Engineering",
				Users: []*CyclicUser{
					{
						ID:   "user-2",
						Name: "Bob",
						Workspace: &CyclicWorkspace{
							ID:    "ws-2",
							Title: "Design",
							Users: []*CyclicUser{
								{ID: "user-3", Name: "Charlie"},
							},
						},
					},
				},
			},
		}

		migrator, err := rm.For(req)
		require.NoError(t, err)

		data, err := migrator.Marshal(user)
		require.NoError(t, err)

		var res map[string]interface{}
		err = json.Unmarshal(data, &res)
		require.NoError(t, err)

		// Verify top-level user
		require.Equal(t, "Alice", res["username"])

		// Verify nested workspace and user
		ws1 := res["workspace"].(map[string]interface{})
		require.Equal(t, "Engineering", ws1["name"])

		users1 := ws1["users"].([]interface{})
		user2 := users1[0].(map[string]interface{})
		require.Equal(t, "Bob", user2["username"])

		// Verify deeply nested workspace and user
		ws2 := user2["workspace"].(map[string]interface{})
		require.Equal(t, "Design", ws2["name"])

		users2 := ws2["users"].([]interface{})
		user3 := users2[0].(map[string]interface{})
		require.Equal(t, "Charlie", user3["username"])
	})
}

func Test_RootSlice(t *testing.T) {
	opts := &RequestMigrationOptions{
		VersionHeader:  "X-Test-Version",
		CurrentVersion: "2023-03-01",
		VersionFormat:  DateFormat,
	}
	rm, _ := NewRequestMigration(opts)
	Register[AddressString](rm, "2023-03-01", &addressStringMigration{})

	tests := []struct {
		name              string
		addresses         []string
		expectedMarshal   []string
		expectedUnmarshal []AddressString
	}{
		{
			name:              "two addresses",
			addresses:         []string{"Main St", "Second St"},
			expectedMarshal:   []string{"Backward: Main St", "Backward: Second St"},
			expectedUnmarshal: []AddressString{"Migrated: Main St", "Migrated: Second St"},
		},
		{
			name:              "single address",
			addresses:         []string{"Oak Ave"},
			expectedMarshal:   []string{"Backward: Oak Ave"},
			expectedUnmarshal: []AddressString{"Migrated: Oak Ave"},
		},
		{
			name:              "empty slice",
			addresses:         []string{},
			expectedMarshal:   []string{},
			expectedUnmarshal: []AddressString{},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.Header.Add("X-Test-Version", "2023-02-01")

			migrator, err := rm.For(req)
			require.NoError(t, err)

			// Build input slice
			users := make([]CustomUser, len(tc.addresses))
			for i, addr := range tc.addresses {
				users[i] = CustomUser{Address: AddressString(addr)}
			}

			// Test Marshal
			data, err := migrator.Marshal(&users)
			require.NoError(t, err)

			var res []map[string]interface{}
			json.Unmarshal(data, &res)
			require.Len(t, res, len(tc.expectedMarshal))
			for i, expected := range tc.expectedMarshal {
				require.Equal(t, expected, res[i]["address"])
			}

			// Test Unmarshal - build JSON array
			var jsonParts []string
			for _, addr := range tc.addresses {
				jsonParts = append(jsonParts, fmt.Sprintf(`{"address": "%s"}`, addr))
			}
			jsonData := "[" + strings.Join(jsonParts, ", ") + "]"

			var unmarshaledUsers []CustomUser
			err = migrator.Unmarshal([]byte(jsonData), &unmarshaledUsers)
			require.NoError(t, err)
			require.Len(t, unmarshaledUsers, len(tc.expectedUnmarshal))
			for i, expected := range tc.expectedUnmarshal {
				require.Equal(t, expected, unmarshaledUsers[i].Address)
			}
		})
	}
}

type chainMigrationV2 struct{}

func (m *chainMigrationV2) MigrateForward(ctx context.Context, data any) (any, error) {
	s := data.(string)
	return s + " -> v2", nil
}
func (m *chainMigrationV2) MigrateBackward(ctx context.Context, data any) (any, error) {
	s := data.(string)
	return s + " -> v1", nil
}

type chainMigrationV3 struct{}

func (m *chainMigrationV3) MigrateForward(ctx context.Context, data any) (any, error) {
	s := data.(string)
	return s + " -> v3", nil
}
func (m *chainMigrationV3) MigrateBackward(ctx context.Context, data any) (any, error) {
	s := data.(string)
	return s + " -> v2", nil
}

func Test_VersionChain(t *testing.T) {
	opts := &RequestMigrationOptions{
		VersionHeader:  "X-Test-Version",
		CurrentVersion: "2023-03-01",
		VersionFormat:  DateFormat,
	}
	rm, _ := NewRequestMigration(opts)

	// v1: 2023-01-01 (initial version, no migrations)
	// v2: 2023-02-01
	Register[AddressString](rm, "2023-02-01", &chainMigrationV2{})
	// v3: 2023-03-01
	Register[AddressString](rm, "2023-03-01", &chainMigrationV3{})

	t.Run("Marshal chain v3 to v1", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Add("X-Test-Version", "2023-01-01")

		type User struct {
			Address AddressString `json:"address"`
		}
		u := User{Address: "start"}
		migrator, err := rm.For(req)
		require.NoError(t, err)

		data, err := migrator.Marshal(&u)
		require.NoError(t, err)

		var res map[string]interface{}
		json.Unmarshal(data, &res)
		require.Equal(t, "start -> v2 -> v1", res["address"])
	})

	t.Run("Unmarshal chain v1 to v3", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/", nil)
		req.Header.Add("X-Test-Version", "2023-01-01")

		type User struct {
			Address AddressString `json:"address"`
		}
		jsonData := `{"address": "start"}`
		var u User
		migrator, err := rm.For(req)
		require.NoError(t, err)

		err = migrator.Unmarshal([]byte(jsonData), &u)
		require.NoError(t, err)
		require.Equal(t, AddressString("start -> v2 -> v3"), u.Address)
	})
}

type EndpointResponse struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

type endpointMigration struct{}

func (m *endpointMigration) MigrateForward(ctx context.Context, data any) (any, error) {
	d, ok := data.(map[string]interface{})
	if !ok {
		return data, nil
	}
	// v1 -> v2: title becomes name
	if title, exists := d["title"]; exists {
		d["name"] = title
		delete(d, "title")
	}
	return d, nil
}

func (m *endpointMigration) MigrateBackward(ctx context.Context, data any) (any, error) {
	d, ok := data.(map[string]interface{})
	if !ok {
		return data, nil
	}
	// v2 -> v1: name becomes title
	if name, exists := d["name"]; exists {
		d["title"] = name
		delete(d, "name")
	}
	return d, nil
}

type PagedResponse struct {
	Content    interface{} `json:"content"`
	Page       int         `json:"page"`
	TotalPages int         `json:"total_pages"`
}

func Test_InterfaceFieldMigration(t *testing.T) {
	opts := &RequestMigrationOptions{
		VersionHeader:  "X-API-Version",
		CurrentVersion: "2024-06-01",
		VersionFormat:  DateFormat,
	}
	rm, err := NewRequestMigration(opts)
	require.NoError(t, err)

	err = Register[EndpointResponse](rm, "2024-01-01", &endpointMigration{})
	require.NoError(t, err)

	t.Run("Marshal single item in interface field", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Add("X-API-Version", "2023-01-01") // older than migration

		wrapper := &PagedResponse{
			Content:    EndpointResponse{Name: "test-endpoint", Description: "A test"},
			Page:       1,
			TotalPages: 1,
		}

		migrator, err := rm.For(req)
		require.NoError(t, err)

		data, err := migrator.Marshal(wrapper)
		require.NoError(t, err)

		var res map[string]interface{}
		err = json.Unmarshal(data, &res)
		require.NoError(t, err)

		content := res["content"].(map[string]interface{})
		// Should have "title" (old field), not "name" (new field)
		require.Equal(t, "test-endpoint", content["title"])
		require.Nil(t, content["name"])
		require.Equal(t, "A test", content["description"])
	})

	t.Run("Marshal slice in interface field", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Add("X-API-Version", "2023-01-01") // older than migration

		wrapper := &PagedResponse{
			Content: []EndpointResponse{
				{Name: "endpoint-1", Description: "First"},
				{Name: "endpoint-2", Description: "Second"},
			},
			Page:       1,
			TotalPages: 2,
		}

		migrator, err := rm.For(req)
		require.NoError(t, err)

		data, err := migrator.Marshal(wrapper)
		require.NoError(t, err)

		var res map[string]interface{}
		err = json.Unmarshal(data, &res)
		require.NoError(t, err)

		content := res["content"].([]interface{})
		require.Len(t, content, 2)

		first := content[0].(map[string]interface{})
		require.Equal(t, "endpoint-1", first["title"])
		require.Nil(t, first["name"])

		second := content[1].(map[string]interface{})
		require.Equal(t, "endpoint-2", second["title"])
		require.Nil(t, second["name"])
	})

	t.Run("Marshal with current version - no migration", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Add("X-API-Version", "2024-06-01") // current version

		wrapper := &PagedResponse{
			Content: EndpointResponse{Name: "test-endpoint", Description: "A test"},
		}

		migrator, err := rm.For(req)
		require.NoError(t, err)

		data, err := migrator.Marshal(wrapper)
		require.NoError(t, err)

		var res map[string]interface{}
		err = json.Unmarshal(data, &res)
		require.NoError(t, err)

		content := res["content"].(map[string]interface{})
		// Should keep "name" (current version field)
		require.Equal(t, "test-endpoint", content["name"])
		require.Nil(t, content["title"])
	})

	t.Run("Marshal nil interface field", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Add("X-API-Version", "2023-01-01")

		wrapper := &PagedResponse{
			Content:    nil,
			Page:       1,
			TotalPages: 0,
		}

		migrator, err := rm.For(req)
		require.NoError(t, err)

		data, err := migrator.Marshal(wrapper)
		require.NoError(t, err)

		var res map[string]interface{}
		err = json.Unmarshal(data, &res)
		require.NoError(t, err)
		require.Nil(t, res["content"])
	})

	t.Run("Marshal unregistered type in interface field", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Add("X-API-Version", "2023-01-01")

		// UnregisteredType has no migrations registered
		type UnregisteredType struct {
			Foo string `json:"foo"`
		}

		wrapper := &PagedResponse{
			Content: UnregisteredType{Foo: "bar"},
		}

		migrator, err := rm.For(req)
		require.NoError(t, err)

		data, err := migrator.Marshal(wrapper)
		require.NoError(t, err)

		var res map[string]interface{}
		err = json.Unmarshal(data, &res)
		require.NoError(t, err)

		content := res["content"].(map[string]interface{})
		// Should pass through unchanged
		require.Equal(t, "bar", content["foo"])
	})
}

func Test_NestedInterfaceSliceMigration(t *testing.T) {
	opts := &RequestMigrationOptions{
		VersionHeader:  "X-API-Version",
		CurrentVersion: "2024-06-01",
		VersionFormat:  DateFormat,
	}
	rm, err := NewRequestMigration(opts)
	require.NoError(t, err)

	err = Register[EndpointResponse](rm, "2024-01-01", &endpointMigration{})
	require.NoError(t, err)

	t.Run("Marshal pointer slice in interface field", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Add("X-API-Version", "2023-01-01")

		wrapper := &PagedResponse{
			Content: []*EndpointResponse{
				{Name: "endpoint-1", Description: "First"},
				{Name: "endpoint-2", Description: "Second"},
			},
		}

		migrator, err := rm.For(req)
		require.NoError(t, err)

		data, err := migrator.Marshal(wrapper)
		require.NoError(t, err)

		var res map[string]interface{}
		err = json.Unmarshal(data, &res)
		require.NoError(t, err)

		content := res["content"].([]interface{})
		require.Len(t, content, 2)

		first := content[0].(map[string]interface{})
		require.Equal(t, "endpoint-1", first["title"])
		require.Nil(t, first["name"])
	})
}

func Test_NestedPointers(t *testing.T) {
	opts := &RequestMigrationOptions{
		VersionHeader:  "X-Test-Version",
		CurrentVersion: "2023-03-01",
		VersionFormat:  DateFormat,
	}
	rm, _ := NewRequestMigration(opts)
	Register[AddressString](rm, "2023-03-01", &addressStringMigration{})

	t.Run("Marshal with double pointer", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Add("X-Test-Version", "2023-02-01")

		u := &CustomUser{Address: "Main St"}
		doublePtr := &u // **CustomUser

		migrator, err := rm.For(req)
		require.NoError(t, err)

		data, err := migrator.Marshal(doublePtr)
		require.NoError(t, err)

		var res map[string]interface{}
		json.Unmarshal(data, &res)
		require.Equal(t, "Backward: Main St", res["address"])
	})

	t.Run("Marshal with triple pointer", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Add("X-Test-Version", "2023-02-01")

		u := &CustomUser{Address: "Main St"}
		doublePtr := &u
		triplePtr := &doublePtr // ***CustomUser

		migrator, err := rm.For(req)
		require.NoError(t, err)

		data, err := migrator.Marshal(triplePtr)
		require.NoError(t, err)

		var res map[string]interface{}
		json.Unmarshal(data, &res)
		require.Equal(t, "Backward: Main St", res["address"])
	})

	t.Run("Unmarshal with double pointer", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/", nil)
		req.Header.Add("X-Test-Version", "2023-02-01")

		jsonData := `{"address": "Main St"}`
		var u *CustomUser
		doublePtr := &u // **CustomUser

		migrator, err := rm.For(req)
		require.NoError(t, err)

		err = migrator.Unmarshal([]byte(jsonData), doublePtr)
		require.NoError(t, err)
		require.NotNil(t, u)
		require.Equal(t, AddressString("Migrated: Main St"), u.Address)
	})

	t.Run("Unmarshal with triple pointer", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/", nil)
		req.Header.Add("X-Test-Version", "2023-02-01")

		jsonData := `{"address": "Main St"}`
		var u *CustomUser
		doublePtr := &u
		triplePtr := &doublePtr // ***CustomUser

		migrator, err := rm.For(req)
		require.NoError(t, err)

		err = migrator.Unmarshal([]byte(jsonData), triplePtr)
		require.NoError(t, err)
		require.NotNil(t, u)
		require.Equal(t, AddressString("Migrated: Main St"), u.Address)
	})
}

func Test_ForNilRequest(t *testing.T) {
	opts := &RequestMigrationOptions{
		VersionHeader:  "X-Test-Version",
		CurrentVersion: "2023-03-01",
		VersionFormat:  DateFormat,
	}
	rm, err := NewRequestMigration(opts)
	require.NoError(t, err)

	t.Run("For returns error on nil request", func(t *testing.T) {
		migrator, err := rm.For(nil)
		require.Error(t, err)
		require.Nil(t, migrator)
		require.Equal(t, "request cannot be nil", err.Error())
	})
}

func Test_BindAlias(t *testing.T) {
	opts := &RequestMigrationOptions{
		VersionHeader:  "X-Test-Version",
		CurrentVersion: "2023-03-01",
		VersionFormat:  DateFormat,
	}
	rm, err := NewRequestMigration(opts)
	require.NoError(t, err)

	t.Run("Bind is alias for For", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Add("X-Test-Version", "2023-02-01")

		migrator, err := rm.Bind(req)
		require.NoError(t, err)
		require.NotNil(t, migrator)
	})
}

// Test_EagerGraphBuilding tests that graphs are built at registration time
func Test_EagerGraphBuilding(t *testing.T) {
	opts := &RequestMigrationOptions{
		VersionHeader:  "X-Test-Version",
		CurrentVersion: "2023-03-01",
		VersionFormat:  DateFormat,
	}
	rm, err := NewRequestMigration(opts)
	require.NoError(t, err)

	t.Run("graph is cached after registration", func(t *testing.T) {
		// Register a migration
		err := Register[AddressString](rm, "2023-03-01", &addressStringMigration{})
		require.NoError(t, err)

		// Check that the graph was cached for all known versions
		// The versions are: initial (0001-01-01) and 2023-03-01
		key := graphCacheKey{
			t:       reflect.TypeOf(AddressString("")),
			version: "2023-03-01",
		}

		cached, ok := rm.graphCache.Load(key)
		require.True(t, ok, "graph should be cached after registration")
		require.NotNil(t, cached)

		graph := cached.(*typeGraph)
		require.NotNil(t, graph)
	})

	t.Run("immediate use after registration - no Finalize needed", func(t *testing.T) {
		// Create fresh instance
		rm2, err := NewRequestMigration(opts)
		require.NoError(t, err)

		// Register migration
		err = Register[AddressString](rm2, "2023-03-01", &addressStringMigration{})
		require.NoError(t, err)

		// Use immediately - should work without any Finalize() call
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Add("X-Test-Version", "2023-02-01")

		migrator, err := rm2.For(req)
		require.NoError(t, err)

		u := CustomUser{Address: "123 Main St"}
		data, err := migrator.Marshal(&u)
		require.NoError(t, err)

		var res map[string]interface{}
		json.Unmarshal(data, &res)
		require.Equal(t, "Backward: 123 Main St", res["address"])
	})
}

// Test_LazyFallback tests that unregistered container types work via lazy building
func Test_LazyFallback(t *testing.T) {
	opts := &RequestMigrationOptions{
		VersionHeader:  "X-Test-Version",
		CurrentVersion: "2023-03-01",
		VersionFormat:  DateFormat,
	}
	rm, err := NewRequestMigration(opts)
	require.NoError(t, err)

	// Only register AddressString migration, NOT any container type
	err = Register[AddressString](rm, "2023-03-01", &addressStringMigration{})
	require.NoError(t, err)

	t.Run("unregistered container with registered field - marshal", func(t *testing.T) {
		// UnregisteredContainer was never registered, but contains AddressString
		// which HAS a migration registered. The lazy fallback should still work.
		type UnregisteredContainer struct {
			Address AddressString `json:"address"`
		}

		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Add("X-Test-Version", "2023-02-01")

		migrator, err := rm.For(req)
		require.NoError(t, err)

		container := UnregisteredContainer{Address: "123 Main St"}
		data, err := migrator.Marshal(&container)
		require.NoError(t, err)

		var res map[string]interface{}
		json.Unmarshal(data, &res)
		// Address migration should still be applied via lazy fallback
		require.Equal(t, "Backward: 123 Main St", res["address"])
	})

	t.Run("unregistered container with registered field - unmarshal", func(t *testing.T) {
		type UnregisteredContainer struct {
			Address AddressString `json:"address"`
		}

		req := httptest.NewRequest(http.MethodPost, "/", nil)
		req.Header.Add("X-Test-Version", "2023-02-01")

		migrator, err := rm.For(req)
		require.NoError(t, err)

		jsonData := `{"address": "123 Main St"}`
		var container UnregisteredContainer
		err = migrator.Unmarshal([]byte(jsonData), &container)
		require.NoError(t, err)

		// Address migration should still be applied via lazy fallback
		require.Equal(t, AddressString("Migrated: 123 Main St"), container.Address)
	})

	t.Run("lazy fallback caches the graph", func(t *testing.T) {
		type AnotherUnregistered struct {
			Address AddressString `json:"address"`
		}

		req := httptest.NewRequest(http.MethodPost, "/", nil)
		req.Header.Add("X-Test-Version", "2023-02-01")

		migrator, err := rm.For(req)
		require.NoError(t, err)

		// First unmarshal - should build and cache graph
		jsonData := `{"address": "First St"}`
		var container1 AnotherUnregistered
		err = migrator.Unmarshal([]byte(jsonData), &container1)
		require.NoError(t, err)

		// Check that graph was cached
		// Note: dereferenceToLastPtr keeps the last pointer level, so *AnotherUnregistered
		key := graphCacheKey{
			t:       reflect.TypeOf(&AnotherUnregistered{}), // Use pointer type to match cache key
			version: "2023-02-01",
		}
		cached, ok := rm.graphCache.Load(key)
		require.True(t, ok, "graph should be cached after lazy build")
		require.NotNil(t, cached)

		// Second unmarshal - should use cached graph
		var container2 AnotherUnregistered
		err = migrator.Unmarshal([]byte(jsonData), &container2)
		require.NoError(t, err)
		require.Equal(t, AddressString("Migrated: First St"), container2.Address)
	})
}

// Test_ConcurrentAccess tests that concurrent access is safe
func Test_ConcurrentAccess(t *testing.T) {
	opts := &RequestMigrationOptions{
		VersionHeader:  "X-Test-Version",
		CurrentVersion: "2023-03-01",
		VersionFormat:  DateFormat,
	}

	t.Run("concurrent marshal same type", func(t *testing.T) {
		rm, err := NewRequestMigration(opts)
		require.NoError(t, err)

		err = Register[AddressString](rm, "2023-03-01", &addressStringMigration{})
		require.NoError(t, err)

		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Add("X-Test-Version", "2023-02-01")

		migrator, err := rm.For(req)
		require.NoError(t, err)

		// Concurrent marshals should work safely
		var wg sync.WaitGroup
		for i := 0; i < 100; i++ {
			wg.Add(1)
			go func(idx int) {
				defer wg.Done()
				u := CustomUser{Address: AddressString(fmt.Sprintf("Street %d", idx))}
				_, err := migrator.Marshal(&u)
				require.NoError(t, err)
			}(i)
		}
		wg.Wait()
	})

	t.Run("concurrent lazy fallback is safe", func(t *testing.T) {
		rm, err := NewRequestMigration(opts)
		require.NoError(t, err)

		err = Register[AddressString](rm, "2023-03-01", &addressStringMigration{})
		require.NoError(t, err)

		req := httptest.NewRequest(http.MethodPost, "/", nil)
		req.Header.Add("X-Test-Version", "2023-02-01")

		migrator, err := rm.For(req)
		require.NoError(t, err)

		// Type defined inside test — never registered
		type ConcurrentUnregistered struct {
			Address AddressString `json:"address"`
		}

		// Multiple goroutines hitting lazy fallback for same unregistered type
		// Tests that idempotent builds + sync.Map handle races correctly
		var wg sync.WaitGroup
		for i := 0; i < 50; i++ {
			wg.Add(1)
			go func(idx int) {
				defer wg.Done()
				jsonData := fmt.Sprintf(`{"address": "St %d"}`, idx)
				var container ConcurrentUnregistered
				err := migrator.Unmarshal([]byte(jsonData), &container)
				require.NoError(t, err)
				require.Contains(t, string(container.Address), "Migrated:")
			}(i)
		}
		wg.Wait()
	})

	t.Run("concurrent registration and use", func(t *testing.T) {
		rm, err := NewRequestMigration(opts)
		require.NoError(t, err)

		var wg sync.WaitGroup

		// Start registration goroutines
		for i := 0; i < 10; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				// Registration may happen multiple times (idempotent)
				Register[AddressString](rm, "2023-03-01", &addressStringMigration{})
			}()
		}

		// Start usage goroutines concurrently with registration
		for i := 0; i < 50; i++ {
			wg.Add(1)
			go func(idx int) {
				defer wg.Done()
				req := httptest.NewRequest(http.MethodGet, "/", nil)
				req.Header.Add("X-Test-Version", "2023-02-01")

				migrator, err := rm.For(req)
				if err != nil {
					return // Skip if version not ready yet
				}

				u := CustomUser{Address: AddressString(fmt.Sprintf("Street %d", idx))}
				_, _ = migrator.Marshal(&u) // May or may not have migration applied
			}(i)
		}

		wg.Wait()
	})
}

// Test_MarshalCacheLookup tests that Marshal uses cached graphs for types without interface fields
func Test_MarshalCacheLookup(t *testing.T) {
	opts := &RequestMigrationOptions{
		VersionHeader:  "X-Test-Version",
		CurrentVersion: "2023-03-01",
		VersionFormat:  DateFormat,
	}
	rm, err := NewRequestMigration(opts)
	require.NoError(t, err)

	// Register migration - this builds and caches graph for AddressString
	err = Register[AddressString](rm, "2023-03-01", &addressStringMigration{})
	require.NoError(t, err)

	t.Run("marshal uses cached graph for registered type", func(t *testing.T) {
		// Verify graph was cached at registration time for a known version
		// Known versions after setup: initial ("0001-01-01") and "2023-03-01"
		key := graphCacheKey{
			t:       reflect.TypeOf(AddressString("")),
			version: "2023-03-01", // This is a registered version
		}
		_, ok := rm.graphCache.Load(key)
		require.True(t, ok, "graph should be cached after registration for known versions")

		// Marshal should use the cached graph when user version matches a cached version
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Add("X-Test-Version", "2023-02-01") // Older than migration

		migrator, err := rm.For(req)
		require.NoError(t, err)

		u := CustomUser{Address: "123 Main St"}
		data, err := migrator.Marshal(&u)
		require.NoError(t, err)

		var res map[string]interface{}
		json.Unmarshal(data, &res)
		require.Equal(t, "Backward: 123 Main St", res["address"])
	})

	t.Run("marshal still works for types with interface fields", func(t *testing.T) {
		// PagedResponse has interface{} field, so it needs runtime inspection
		err := Register[EndpointResponse](rm, "2024-01-01", &endpointMigration{})
		require.NoError(t, err)

		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Add("X-Test-Version", "2023-01-01")

		migrator, err := rm.For(req)
		require.NoError(t, err)

		wrapper := &PagedResponse{
			Content: EndpointResponse{Name: "test-endpoint", Description: "A test"},
			Page:    1,
		}

		data, err := migrator.Marshal(wrapper)
		require.NoError(t, err)

		var res map[string]interface{}
		json.Unmarshal(data, &res)
		content := res["content"].(map[string]interface{})
		require.Equal(t, "test-endpoint", content["title"])
	})
}

// Test_NoMigrationFastPath tests that current version requests skip migrations
func Test_NoMigrationFastPath(t *testing.T) {
	opts := &RequestMigrationOptions{
		VersionHeader:  "X-Test-Version",
		CurrentVersion: "2023-03-01",
		VersionFormat:  DateFormat,
	}
	rm, err := NewRequestMigration(opts)
	require.NoError(t, err)

	err = Register[AddressString](rm, "2023-03-01", &addressStringMigration{})
	require.NoError(t, err)

	t.Run("current version marshal - no transformation", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Add("X-Test-Version", "2023-03-01") // current version

		migrator, err := rm.For(req)
		require.NoError(t, err)

		u := CustomUser{Address: "Main St"}
		data, err := migrator.Marshal(&u)
		require.NoError(t, err)

		var res map[string]interface{}
		json.Unmarshal(data, &res)
		// No transformation should occur
		require.Equal(t, "Main St", res["address"])
	})

	t.Run("current version unmarshal - no transformation", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/", nil)
		req.Header.Add("X-Test-Version", "2023-03-01") // current version

		migrator, err := rm.For(req)
		require.NoError(t, err)

		jsonData := `{"address": "Main St"}`
		var u CustomUser
		err = migrator.Unmarshal([]byte(jsonData), &u)
		require.NoError(t, err)
		// No transformation should occur
		require.Equal(t, AddressString("Main St"), u.Address)
	})
}

// =============================================================================
// Generic Types
// =============================================================================

// Generic container type - each instantiation is a distinct type at runtime
type Response[T any] struct {
	Data    T      `json:"data"`
	Message string `json:"message"`
}

// Type used as generic parameter
type Product struct {
	Name  string `json:"name"`
	Price int    `json:"price"`
}

type productMigration struct{}

func (m *productMigration) MigrateForward(ctx context.Context, data any) (any, error) {
	d, ok := data.(map[string]interface{})
	if !ok {
		return data, nil
	}
	// v1: "price" was in cents, v2: "price" is in dollars (divide by 100)
	if price, ok := d["price"].(float64); ok {
		d["price"] = price / 100
	}
	return d, nil
}

func (m *productMigration) MigrateBackward(ctx context.Context, data any) (any, error) {
	d, ok := data.(map[string]interface{})
	if !ok {
		return data, nil
	}
	// v2 -> v1: convert dollars back to cents
	if price, ok := d["price"].(float64); ok {
		d["price"] = price * 100
	}
	return d, nil
}

func Test_GenericTypes(t *testing.T) {
	opts := &RequestMigrationOptions{
		VersionHeader:  "X-Test-Version",
		CurrentVersion: "2023-03-01",
		VersionFormat:  DateFormat,
	}

	rm, _ := NewRequestMigration(opts)

	// Register migration for the nested Product type
	err := Register[Product](rm, "2023-03-01", &productMigration{})
	require.NoError(t, err)

	t.Run("generic type with migrated nested type - marshal", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Add("X-Test-Version", "2023-01-01") // old version

		migrator, err := rm.For(req)
		require.NoError(t, err)

		// Response[Product] - generic instantiation
		response := Response[Product]{
			Data:    Product{Name: "Widget", Price: 10}, // $10 in current version
			Message: "success",
		}

		data, err := migrator.Marshal(&response)
		require.NoError(t, err)

		var res map[string]interface{}
		json.Unmarshal(data, &res)

		// Product migration should apply: 10 dollars -> 1000 cents
		dataResult := res["data"].(map[string]interface{})
		require.Equal(t, float64(1000), dataResult["price"])
		require.Equal(t, "Widget", dataResult["name"])
		require.Equal(t, "success", res["message"])
	})

	t.Run("generic type with migrated nested type - unmarshal", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/", nil)
		req.Header.Add("X-Test-Version", "2023-01-01") // old version

		migrator, err := rm.For(req)
		require.NoError(t, err)

		// JSON from old API client (price in cents)
		jsonData := `{"data": {"name": "Widget", "price": 1000}, "message": "success"}`

		var response Response[Product]
		err = migrator.Unmarshal([]byte(jsonData), &response)
		require.NoError(t, err)

		// Product migration should apply: 1000 cents -> 10 dollars
		require.Equal(t, 10, response.Data.Price)
		require.Equal(t, "Widget", response.Data.Name)
		require.Equal(t, "success", response.Message)
	})

	t.Run("different generic instantiations are independent types", func(t *testing.T) {
		// Response[Product] and Response[string] are completely different types
		// Migrations registered for Product apply when nested in Response[Product]
		// but Response[string] won't have Product migrations

		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Add("X-Test-Version", "2023-01-01")

		migrator, err := rm.For(req)
		require.NoError(t, err)

		// Response[string] - different instantiation, no migrations for string
		stringResponse := Response[string]{
			Data:    "hello",
			Message: "success",
		}

		data, err := migrator.Marshal(&stringResponse)
		require.NoError(t, err)

		var res map[string]interface{}
		json.Unmarshal(data, &res)

		// No transformation - string has no registered migrations
		require.Equal(t, "hello", res["data"])
		require.Equal(t, "success", res["message"])
	})
}
