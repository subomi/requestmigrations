# requestmigrations <br /> [![Go Reference](https://pkg.go.dev/badge/github.com/subomi/requestmigrations.svg)](https://pkg.go.dev/github.com/subomi/requestmigrations)
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
Please be advised that this package relies on `httptest.ResponseRecorder`, which, initially, was designed for your test environment rather than regular production code. I used the `ResponseRecorder` because wrapping `http.ResponseWriter` is weirdly not easy (you can read more [here](https://github.com/felixge/httpsnoop#why-this-package-exists)), and I was able to prototype the functionality of this package with the response recorder very quickly. However, from Go 1.20+, the go team introduced `http.ResponseController` (see [#54136](https://github.com/golang/go/issues/54136)), making wrapping the response writer type easy. In subsequent releases, I will be wrapping the writer and removing the use of the response recorder type.

## License
MIT License
