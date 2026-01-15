# requestmigrations <br /> [![Go Reference](https://pkg.go.dev/badge/github.com/subomi/requestmigrations/v2.svg)](https://pkg.go.dev/github.com/subomi/requestmigrations/v2)
`requestmigrations` is a Golang implementation of [rolling versions](https://stripe.com/blog/api-versioning) for REST APIs. It's a port of the [Ruby implementation](https://github.com/keygen-sh/request_migrations) by [ezekg](https://github.com/ezekg). We use it in production at [Convoy](https://github.com/frain-dev/convoy).

> [!NOTE]
> This README describes **v2** of requestmigrations that is currently **experimental**. For older versions, please check the [release tags](https://github.com/subomi/requestmigrations/tags).

#### Built By
<a href="https://getconvoy.io/?utm_source=requestmigrations">
<img src="https://getconvoy.io/svg/convoy-logo-full-new.svg" alt="Sponsored by Convoy"></a>

## Features
- API Versioning with date and semver versioning support.
- Prometheus Instrumentation to track and optimize slow transformations.
- Type-based migration system.

## Installation
```bash
 go get github.com/subomi/requestmigrations/v2
```

## Usage
RequestMigrations introduces a **type-based migration system**. Instead of defining migrations per API handler, migrations are now defined per **Go type**.

```go
package main

import (
	rms "github.com/subomi/requestmigrations/v2"
)

func main() {
    rm, _ := rms.NewRequestMigration(&rms.RequestMigrationOptions{
        VersionHeader:  "X-API-Version",
        CurrentVersion: "2024-01-01",
        VersionFormat:  rms.DateFormat,
    })

    // Register migrations for a specific type
    rms.Register[User](rm, "2024-01-01", &UserMigration{})
}
```

## Example
Check the [examples](./examples) directory for full examples. 

## License
MIT License
