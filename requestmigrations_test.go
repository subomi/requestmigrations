package requestmigrations

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

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

	return []byte(addrString), nil
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

	tests := map[string]struct {
		assert require.ErrorAssertionFunc
	}{
		"no_transformation": {
			assert: require.NoError,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/profile", strings.NewReader(""))
			req.Header.Add("X-Test-Version", "2023-02-01")

			pStruct := profilev2{
				Address: &address{
					State:    "London",
					Postcode: "CR0 1GB",
				},
			}
			migrator, err := rm.For(req)
			require.NoError(t, err)

			bytesW, err := migrator.Marshal(&pStruct)

			_ = bytesW
			tc.assert(t, err)
		})
	}
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

	t.Run("Marshal custom primitive", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Add("X-Test-Version", "2023-02-01")

		u := CustomUser{Address: "Main St"}
		migrator, err := rm.For(req)
		require.NoError(t, err)

		data, err := migrator.Marshal(&u)
		require.NoError(t, err)

		var res map[string]interface{}
		json.Unmarshal(data, &res)
		require.Equal(t, "Backward: Main St", res["address"])
	})

	t.Run("Unmarshal custom primitive", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/", nil)
		req.Header.Add("X-Test-Version", "2023-02-01")

		jsonData := `{"address": "Main St"}`
		var u CustomUser
		migrator, err := rm.For(req)
		require.NoError(t, err)

		err = migrator.Unmarshal([]byte(jsonData), &u)
		require.NoError(t, err)
		require.Equal(t, AddressString("Migrated: Main St"), u.Address)
	})
}

type CyclicUser struct {
	ID        string           `json:"id"`
	Workspace *CyclicWorkspace `json:"workspace"`
}

type CyclicWorkspace struct {
	ID    string        `json:"id"`
	Users []*CyclicUser `json:"users"`
}

func Test_Cycles(t *testing.T) {
	opts := &RequestMigrationOptions{
		VersionHeader:  "X-Test-Version",
		CurrentVersion: "2023-03-01",
		VersionFormat:  DateFormat,
	}
	rm, _ := NewRequestMigration(opts)

	t.Run("Build graph with cycles", func(t *testing.T) {
		_, err := rm.graphBuilder.Build(reflect.TypeOf(CyclicUser{}), &Version{Format: DateFormat, Value: "2023-01-01"})
		require.NoError(t, err)
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

	t.Run("Marshal root slice", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Add("X-Test-Version", "2023-02-01")

		users := []CustomUser{
			{Address: "Main St"},
			{Address: "Second St"},
		}
		migrator, err := rm.For(req)
		require.NoError(t, err)

		data, err := migrator.Marshal(&users)
		require.NoError(t, err)

		var res []map[string]interface{}
		json.Unmarshal(data, &res)
		require.Len(t, res, 2)
		require.Equal(t, "Backward: Main St", res[0]["address"])
		require.Equal(t, "Backward: Second St", res[1]["address"])
	})

	t.Run("Unmarshal root slice", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/", nil)
		req.Header.Add("X-Test-Version", "2023-02-01")

		jsonData := `[{"address": "Main St"}, {"address": "Second St"}]`
		var users []CustomUser
		migrator, err := rm.For(req)
		require.NoError(t, err)

		err = migrator.Unmarshal([]byte(jsonData), &users)
		require.NoError(t, err)
		require.Len(t, users, 2)
		require.Equal(t, AddressString("Migrated: Main St"), users[0].Address)
		require.Equal(t, AddressString("Migrated: Second St"), users[1].Address)
	})
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

	t.Run("Marshal chain v1 to v3", func(t *testing.T) {
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
