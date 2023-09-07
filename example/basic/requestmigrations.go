package main

import (
	"fmt"
	"net/http"
)

// Migrations
type CombineNamesForUserMigration struct{}

func (c *CombineNamesForUserMigration) GetName() string {
	return "combine_names_for_user_migration"
}

func (c *CombineNamesForUserMigration) ShouldMigrateRequest() bool {
	return true
}

func (c *CombineNamesForUserMigration) MigrateRequest(r *http.Request) error {
	fmt.Println("migrating request...")
	return nil
}

func (c *CombineNamesForUserMigration) ShouldMigrateResponse() bool {
	return true
}

func (c *CombineNamesForUserMigration) MigrateResponse(r *http.Response) error {
	fmt.Println("migrating response...")
	return nil
}
