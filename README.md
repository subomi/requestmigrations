# requestmigrations
`requestmigrations` is a Golang implementation of [rolling versions](https://stripe.com/blog/api-versioning) for REST APIs. It's a port of the [Ruby implementation](https://github.com/keygen-sh/request_migrations) by [ezekg](https://github.com/ezekg).

## Features
- API Versioning with date and semver versioning support.
- Prometheus Instrumentation to track and optimize slow transformations.
- Progressive versioning support - roll out versioning to a subset of your users. (Coming soon)
- Support arbitrary data migration. (Coming soon)

## Installation
```bash
 $ go get github.com/subomi/requestmigrations 
```

## Usage
This package primarily exposes one API - `VersionAPI`. It can be used as an HTTP middleware to intercept and apply transformations on requests and responses to your REST API. See an abridged example below:
```go 
package main

func main() {
    rms := rm.NewRequestMigration(opts)

    r := mux.NewRouter()
    r.use(rms.VersionAPI)

    r.HandleFunc("/users", ListUsers).Methods("GET")
}
```

You can also use it to apply transformations to specific routes only. See example below:
```go
package main

func main() {
    rms := rm.NewRequestMigration(opts)

    r := mux.NewRouter()

    r.HandleFunc("/users", rms.VersionAPI(ListUsers)).Methods("GET")
}
```

You can see a full example in the [example directory](https://github.com/subomi/requestmigrations/tree/main/example) for a detailed example.

## Limitations
This package depends on `httptest.ResponseRecorder`. There are valid concerns as to why this shouldn't be used in the production code, see [here](https://stackoverflow.com/a/52810532). Implementing `ResponseRecorder` that can be used in production requires more knowledge of the http library than I currently know, the plan is to do in this in the nearest future. Be advised. 

## License
MIT License
