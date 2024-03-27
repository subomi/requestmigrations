package requestmigrations

import "net/http"

type response struct {
	body       []byte
	header     http.Header
	statusCode int
}

func (r *response) SetBody(body []byte) {
	r.body = body
}

func (r *response) Header(header http.Header) {
	r.header = header
}

func (r *response) SetHeader(statusCode int) {
	r.statusCode = statusCode
}
