# requestmigrations
`requestmigrations` is a Golang implementation of [rolling versions](https://stripe.com/blog/api-versioning) for REST APIs. It's a port of the [Ruby implementation](https://github.com/keygen-sh/request_migrations) by [ezekg](https://github.com/ezekg)

## Features
- API Versioning with date and semver versioning support.
- Prometheus Instrumentation to track and optimize slow transformations.
- Progressive versioning support - roll out versioning to a subset of your users. (Coming soon)
- Include/Exclude specific routes entirely. (Coming soon)
- Support arbitrary data migration. (Coming soon)

## Installation
```bash
 $ go get github.com/subomi/requestmigrations 
```

## Usage
This package only exposes one API - `VersionAPI` which is an HTTP middleware to intercept and apply transformations on requests and responses to your REST API. You can find a complete example in the [example directory](https://github.com/subomi/requestmigrations/tree/main/example). See an abridged example below:
```go 
package main

func main() {
    rms := rm.NewRequestMigration(opts)

    r := mux.NewRouter()
    r.use(rms.VersionAPI)

    r.HandleFunc("/users", ListUsers).Methods("GET")
}
```
See the [example directory](https://github.com/subomi/requestmigrations/tree/main/example) for a detailed example.

## Benchmarks

## Limitations
This package depends on `httptest.ResponseRecorder`. There are valid concerns as to why this shouldn't be used in the production code, see [here](https://stackoverflow.com/a/52810532). Implementing `ResponseRecorder` that can be used in production requires more knowledge of the http library than I currently know, the plan is to do in this in the nearest future. Be advised. 


## License
MIT License
