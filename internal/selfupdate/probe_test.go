package selfupdate

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestProbeLatestTag(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", "/xywendz/db-fly/releases/tag/v1.2.3")
		w.WriteHeader(http.StatusFound)
	}))
	defer srv.Close()

	if got := probeLatestTag(context.Background(), srv.URL); got != "v1.2.3" {
		t.Fatalf("probeLatestTag() = %q, want v1.2.3", got)
	}
}

func TestProbeLatestTagDoesNotFollowRedirect(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/releases/latest" {
			w.Header().Set("Location", "/followed")
			w.WriteHeader(http.StatusFound)
			return
		}
		t.Error("redirect was followed")
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	if got := probeLatestTag(context.Background(), srv.URL+"/releases/latest"); got != "" {
		t.Fatalf("probeLatestTag() = %q, want empty", got)
	}
}

func TestProbeSaysUpToDate(t *testing.T) {
	cases := []struct {
		tag, current string
		want         bool
	}{
		{"v1.0.5", "v1.0.5", true},
		{"v1.0.4", "v1.0.5", true},
		{"v1.0.6", "v1.0.5", false},
		{"1.0.5", "v1.0.5", true},
		{"v1.0.5", "dev", false},
		{"", "v1.0.5", false},
		{"v1.2.3", "v1.2.3+4.gdeadbee", true},
	}
	for _, c := range cases {
		if got := probeSaysUpToDate(c.tag, c.current); got != c.want {
			t.Errorf("probeSaysUpToDate(%q, %q) = %v, want %v", c.tag, c.current, got, c.want)
		}
	}
}
