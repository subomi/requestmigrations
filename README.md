# requestmigrations <br /> [![Go Reference](https://pkg.go.dev/badge/github.com/subomi/requestmigrations.svg)](https://pkg.go.dev/github.com/subomi/requestmigrations)
`requestmigrations` is a Golang implementation of [rolling versions](https://stripe.com/blog/api-versioning) for REST APIs. It's a port of the [Ruby implementation](https://github.com/keygen-sh/request_migrations) by [ezekg](https://github.com/ezekg).

## Features
- API Versioning with date and semver versioning support.
- Prometheus Instrumentation to track and optimize slow transformations.
- Support arbitrary data migration. (Coming soon)

## Installation
```bash
 go get github.com/subomi/requestmigrations 
```

## Usage
This package primarily exposes two APIs - `VersionRequest` and `VersionResponse` used in your HTTP handlers to transform the request and response respectively. Here's an short example:

```go
package main 

func createUser(r *http.Request, w http.ResponseWriter) {
  err := rm.VersionRequest(r, "createUser")
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

  resBody, err := rm.VersionResponse(r, body, "createUser")
  if err != nil {
    t.Fatal(err)
  }

  _, _ = w.Write(resBody)
}

```

## License
MIT License
