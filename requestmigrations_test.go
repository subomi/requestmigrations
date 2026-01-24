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

// TODO(subomi): For tests should focus on migrating different complex types
// and analyzing why it's not working. E.g. Primitive Types, Structs, Nested Structs,
// interfaces{}, map types, arrays, cycles etc.

// Test Cases
// Primitive Types
//	- Int
// 	- string
// 	- etc.
// Pointer to Primitive Types
// --------------------------
// Structs.
// Pointer to Structs.
// Cyclic Structs.
// Nested Structs.
// interface{}
// interface type
// Generic types
// --------------------------
// 1. Inside each type we test both Marshal & Unmarshal.

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

// TODO(subomi): This test is meaningless. There's a transformation because the request
// is for version 2023-02-01, so the user should see an address string which happens in
// MigrateBackward method.
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

	// TODO(subomi): We can merge these two into one test case.
	// It doesn't need to be separate.
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

// TODO(subomi): This test isn't robust enough.
func Test_Cycles(t *testing.T) {
	opts := &RequestMigrationOptions{
		VersionHeader:  "X-Test-Version",
		CurrentVersion: "2023-03-01",
		VersionFormat:  DateFormat,
	}
	rm, _ := NewRequestMigration(opts)

	t.Run("Build graph with cycles", func(t *testing.T) {
		_, err := rm.graphBuilder.buildFromType(reflect.TypeOf(CyclicUser{}), &Version{Format: DateFormat, Value: "2023-01-01"})
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

	// TODO(subomi): We can merge these two into one test case.
	// It doesn't need to be separate.
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
