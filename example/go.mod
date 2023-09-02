module atlexample

go 1.18

require (
	github.com/gorilla/mux v1.8.0
	github.com/subomi/requestmigrations v0.1.0
)

require github.com/Masterminds/semver/v3 v3.2.1 // indirect

replace github.com/subomi/requestmigrations v0.1.0 => ../
