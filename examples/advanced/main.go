package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"

	rms "github.com/subomi/requestmigrations/v2"
)

// --- Models (Current Version) ---

type Workspace struct {
	ID       string              `json:"id"`
	Name     string              `json:"name"`
	Users    []*User             `json:"users"`
	Projects map[string]*Project `json:"projects"` // Changed from slice in v2024-01-01
}

type User struct {
	ID       string `json:"id"`
	Username string `json:"username"` // Renamed from email in v2023-06-01
}

type Project struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Lead *User  `json:"lead"`
}

// --- Migrations ---

// UserMigration handles email -> username renaming
type UserMigrationV20230601 struct{}

func (m *UserMigrationV20230601) MigrateForward(data any) (any, error) {
	d := data.(map[string]any)
	if email, ok := d["email"].(string); ok {
		d["username"] = email
		delete(d, "email")
	}
	return d, nil
}

func (m *UserMigrationV20230601) MigrateBackward(data any) (any, error) {
	d := data.(map[string]any)
	if username, ok := d["username"].(string); ok {
		d["email"] = username
		delete(d, "username")
	}
	return d, nil
}

// WorkspaceMigration handles projects slice -> map conversion
type WorkspaceMigrationV20240101 struct{}

func (m *WorkspaceMigrationV20240101) MigrateForward(data any) (any, error) {
	d := data.(map[string]any)
	if projects, ok := d["projects"].([]any); ok {
		projectMap := make(map[string]any)
		for _, p := range projects {
			pm := p.(map[string]any)
			if id, ok := pm["id"].(string); ok {
				projectMap[id] = pm
			}
		}
		d["projects"] = projectMap
	}
	return d, nil
}

func (m *WorkspaceMigrationV20240101) MigrateBackward(data any) (any, error) {
	d := data.(map[string]any)
	if projectMap, ok := d["projects"].(map[string]any); ok {
		projects := make([]any, 0, len(projectMap))
		for _, p := range projectMap {
			projects = append(projects, p)
		}
		d["projects"] = projects
	}
	return d, nil
}

func main() {
	// Initialize RequestMigration
	rm, err := rms.NewRequestMigration(&rms.RequestMigrationOptions{
		VersionHeader:  "X-API-Version",
		CurrentVersion: "2024-01-01",
		VersionFormat:  rms.DateFormat,
	})
	if err != nil {
		log.Fatal(err)
	}

	// Register migrations across versions
	rms.Register[User](rm, "2023-06-01", &UserMigrationV20230601{})
	rms.Register[Workspace](rm, "2024-01-01", &WorkspaceMigrationV20240101{})

	// --- Scenario: Backward Migration (Marshal) ---
	// Current data structure
	w := &Workspace{
		ID:   "w1",
		Name: "Main Workspace",
		Projects: map[string]*Project{
			"p1": {ID: "p1", Name: "Project 1"},
		},
	}
	u1 := &User{ID: "u1", Username: "subomi@example.com"}
	w.Users = []*User{u1}
	w.Projects["p1"].Lead = u1

	fmt.Println("--- Advanced Example: Multi-version Migration ---")

	// Client requesting v2023-01-01 (oldest version)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-API-Version", "2023-01-01")

	data, err := rm.WithUserVersion(req).Marshal(w)
	if err != nil {
		log.Fatal(err)
	}

	var prettyJSON any
	_ = json.Unmarshal(data, &prettyJSON)
	indentedData, _ := json.MarshalIndent(prettyJSON, "", "  ")
	fmt.Printf("Marshaled for v2023-01-01:\n%s\n\n", string(indentedData))

	// --- Scenario: Forward Migration (Unmarshal) ---
	// Client sending data in v2023-01-01 format (email instead of username, projects as slice)
	oldJSON := `{
		"id": "w1",
		"name": "Old Format Workspace",
		"users": [{"id": "u1", "email": "old@example.com"}],
		"projects": [{"id": "p1", "name": "Old Project"}]
	}`

	var incoming Workspace
	err = rm.WithUserVersion(req).Unmarshal([]byte(oldJSON), &incoming)
	if err != nil {
		log.Fatal(err)
	}

	fmt.Printf("Unmarshaled from v2023-01-01 to Current:\n")
	fmt.Printf("User Username: %s\n", incoming.Users[0].Username)
	fmt.Printf("Project Count: %d\n", len(incoming.Projects))
	fmt.Printf("Project 'p1' Name: %s\n", incoming.Projects["p1"].Name)
}
