# requestmigrations <br /> [![Go Reference](https://pkg.go.dev/badge/github.com/subomi/requestmigrations.svg)](https://pkg.go.dev/github.com/subomi/requestmigrations)
`requestmigrations` is a Golang implementation of [rolling versions](https://stripe.com/blog/api-versioning) for REST APIs. It's a port of the [Ruby implementation](https://github.com/keygen-sh/request_migrations) by [ezekg](https://github.com/ezekg). We use in production with [Convoy](https://github.com/frain-dev/convoy).

#### Built By
<a href="https://getconvoy.io/?utm_source=requestmigrations">
<img src="https://getconvoy.io/svg/convoy-logo-full-new.svg" alt="Sponsored by Convoy"></a>

## Features
- API Versioning with date and semver versioning support.
- Prometheus Instrumentation to track and optimize slow transformations.
- Support arbitrary data migration. (Coming soon)

## Installation
```bash
 go get github.com/subomi/requestmigrations 
```

## Usage
This package exposes primarily one API - `Migrate`. It is used to migrate and rollback changes to your request and response respectively. Here's a short example:

```go
package main 

func createUser(r *http.Request, w http.ResponseWriter) {
  // Identify version and transform the request payload.
  err, vw, rollback := rm.Migrate(r, "createUser")
  if err != nil {
     w.Write("Bad Request")
  }

  // Setup response transformation callback.
  defer rollback(w)

  // ...Perform core business logic...
  data, err := createUserObject(body)
  if err != nil {
    return err 
  }

  // Write response
  body, err := json.Marshal(data)
  if err != nil {
    w.Write("Bad Request")
  }

  vw.Write(body)
}

```

### Writing migrations
A migration is a struct that performs a migration on either a request or a response, but not both. Here's an example:

```go
  type createUserRequestSplitNameMigration struct{} 

  func (c *createUserRequestSplitNameMigration) Migrate(body []byte, h http.Header) ([]byte, http.Header, error) {
    var oUser oldUser 
    err := json.Unmarshal(body, &oUser)
    if err != nil {
      return nil, nil, err 
    }

    var nUser user 
    nUser.Email = oUser.Email 

    splitName := strings.Split(oUser.FullName, " ")
    nUser.FirstName = splitName[0]
    nUser.LastName = splitName[1]

    body, err = json.Marshal(&nUser)
    if err != nil {
      return nil, nil, err 
    }

    return body, h, nil 
  }
```

Notice from the above that the migration struct name follows a particular structure. The structure adopted is `{handlerName}{MigrationType}`. The `handlerName` refers to the exact name of your handler. For example, if you have a handler named `LoginUser`, any migration on this handler should start with `LoginUser`. It'll also be what we use in `VersionRequest` and `VersionResponse`. The `MigrationType` can be `Request` or `Response`. We use this field to determine if the migration should run on the request or the response payload. 

This library doesn't support multiple transformations per version as of the time of this writing. For example, no handler can have multiple changes for the same version.

## Example
Check the [example](./example) directory for a full example. Do the following to run the example:

1. Run the server.
```bash 
$ git clone https://github.com/subomi/requestmigrations 

$ cd example/basic 

$ go run *.go
```

2. Open another terminal and call the server
```bash
# Call the API without specifying a version.
$ curl -s localhost:9000/users \
  -H "Content-Type: application/json" | jq

# Call the API with 2023-04-01 version.
$ curl -s localhost:9000/users \ 
  -H "Content-Type: application/json" \
  -H "X-Example-Version: 2023-04-01" | jq
```

## License
MIT License
