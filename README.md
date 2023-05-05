# atl
`atl` is an acronym for API Translation Layer. The goal is to build a library that can be embedded direclty into your Go REST APIs to provide request and response translation for your API, making it backwards-compatible, and giving you the freedom to continue releasing new versions of your API. It this is known as [rolling versions](https://stripe.com/blog/api-versioning) popularised by Stripe.


## Rolling Versions vs. Traditional Versioning
- Rolling Version is Progressive. Traditional Versioning is a big bang.
- Rolling Version has a superior developer experience. Traditional Versioning has a poorer developer experience.

## High Level Algorithm
- For every req, 
    - Identify diff between the most list of differences from the beginning to the end.
    - Apply the differences in the order for the request
    - Apply the differences in reverse for the response.


To achieve rolling versions, we need a layer before our http handlers, that can identify our current API version, perform a diff of changes from our selected version to the most recent version. We will also want to achieve this without refactoring all our handlers as well as deploying a new API Gateway. To this end, we want to build API Gateway like features directly into our binary to enable us transform request and response up and down. We also want to do this and minimize the amount of latency we are adding to every API Call. 

## Design 

### Goals
1. Minimize the added latency to each request.
2. Work with existing APIs without the need to refactor the `HTTP` Handlers.
3. Don't couple with third-party proxies like `Kong`, `Tyk` etc.

### Options
To achieve this there are various options with their pros & cons. Let's iterate over each option and discuss them. 

#### Add a Reverse Proxy
Create a reverse proxy that listens on a separate port, receives requests, transforms them and forwards to the actual server.

1. First we will create a new reverse proxy server on a new port.
2. For every request, 
    - Determine the `diff`.
    - Let `req := transformRequest(diff, req)`.
    - Add Diff Header to the request like `X-ATL-Diffs: abc:edf:ghi`
3. For every response,
    - Retrieve the Diff Header. 
    - Let `res := transformResponse(diff, res)`.
    - Write `res` to `ResponseWriter`.

#### Wrap ResponseWriter Type
3. Wrap `http.ResponseWriter`. This would require deeper knowledge of the `http` package that I don't have. The problem is `ResponseWriter` implements multiple types. See [here](https://github.com/felixge/httpsnoop#why-this-package-exists)

#### Create a new Response Type
Create a new return type. This would mean refactor all our handlers.

#### Deploy a third-paty API Gateway
Excellent option if you don't mind deploying another software. In fact, you can do this at the edge with [Cloudflare Workers](https://workers.cloudflare.com/)
