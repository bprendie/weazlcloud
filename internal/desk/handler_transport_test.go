package desk

import (
	"net/http"
	"net/http/httptest"
)

// Exercise the real HTTP routes and cookie jar without reserving a TCP port.
// Network/container/browser tests remain separate integration gates.
type handlerTransport struct{ handler http.Handler }

func (transport handlerTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	request = request.Clone(request.Context())
	request.RemoteAddr = "127.0.0.1:12345"
	recorder := httptest.NewRecorder()
	transport.handler.ServeHTTP(recorder, request)
	response := recorder.Result()
	response.Request = request
	return response, nil
}
