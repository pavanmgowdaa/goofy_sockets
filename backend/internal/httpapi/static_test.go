package httpapi

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestFrontendPageEmbeddedAndServed(t *testing.T) {
	server := &Server{}
	for _, path := range []string{"/", "/fairpass.html"} {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest("GET", path, nil)
		server.frontend(recorder, request)
		if recorder.Code != 200 {
			t.Fatalf("GET %s returned %d", path, recorder.Code)
		}
		if !strings.Contains(recorder.Header().Get("Content-Type"), "text/html") {
			t.Fatalf("GET %s returned unexpected content type %q", path, recorder.Header().Get("Content-Type"))
		}
		body := recorder.Body.String()
		if !strings.Contains(body, "FAIRPASS — 360° Stadium Gallery") || !strings.Contains(body, "/v1/events") || !strings.Contains(body, "checkout.razorpay.com") || !strings.Contains(body, "WELCOME BACK") || !strings.Contains(body, "/v1/auth/login") || strings.Contains(body, "Demo only: connect this form") {
			t.Fatalf("GET %s did not serve the embedded FAIRPASS page", path)
		}
	}
}

func TestFrontendRejectsUnknownPaths(t *testing.T) {
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest("GET", "/unknown", nil)
	(&Server{}).frontend(recorder, request)
	if recorder.Code != 404 {
		t.Fatalf("unknown frontend path returned %d", recorder.Code)
	}
}
