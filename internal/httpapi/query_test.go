package httpapi_test

import (
	"testing"
)

func TestStrictQueryRejectsAmbiguousAndInvalidParameters(t *testing.T) {
	v := setup(t)
	for _, query := range []string{"page=-1", "page_size=101", "page=1&page=2", "surprise=1", "category=administrator", "search=%FF"} {
		v.call(t, "GET", "/api/tickets?"+query, "/api/tickets", nil, v.cookie, 400)
	}
	for _, query := range []string{"include_archived=maybe", "include_archived=TRUE", "include_archived=true&include_archived=false"} {
		v.call(t, "GET", "/api/knowledge?"+query, "/api/knowledge", nil, v.cookie, 400)
	}
	for _, path := range []string{"/api/tickets", "/api/jobs", "/api/knowledge"} {
		v.call(t, "GET", path+"?page=9223372036854775807", path, nil, v.cookie, 400)
	}
	v.call(t, "GET", "/api/tickets?page=1&page_size=20&category=technical", "/api/tickets", nil, v.cookie, 200)
}
