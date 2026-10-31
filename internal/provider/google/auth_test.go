package google

import (
	"errors"
	"net/http"
	"testing"

	"golang.org/x/oauth2"

	"github.com/sanjayrj/ai-bot/internal/provider"
)

type failingRT struct{ err error }

func (f failingRT) RoundTrip(*http.Request) (*http.Response, error) { return nil, f.err }

func TestReauthTransport(t *testing.T) {
	req, _ := http.NewRequest("GET", "https://www.googleapis.com/x", nil)
	for _, code := range []string{"invalid_grant", "unauthorized_client"} {
		_, err := reauthTransport{base: failingRT{&oauth2.RetrieveError{ErrorCode: code}}}.RoundTrip(req)
		if !errors.Is(err, provider.ErrReauth) {
			t.Fatalf("%s should map to ErrReauth, got %v", code, err)
		}
	}
	other := errors.New("network down")
	if _, err := (reauthTransport{base: failingRT{other}}).RoundTrip(req); !errors.Is(err, other) {
		t.Fatalf("other errors must pass through, got %v", err)
	}
}
