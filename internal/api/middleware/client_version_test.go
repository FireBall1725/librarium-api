// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725 (Adaléa)

package middleware

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// serve runs a request through the gate and reports whether it reached the
// handler, plus the response for inspection.
func serve(t *testing.T, minimums map[string]string, req *http.Request) (bool, *httptest.ResponseRecorder) {
	t.Helper()
	reached := false
	h := RequireClientVersion(minimums)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached = true
		w.WriteHeader(http.StatusOK)
	}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return reached, rec
}

func request(client, clientVersion string, claims *UserClaims) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "/api/v1/books", nil)
	if client != "" {
		r.Header.Set(ClientHeader, client)
	}
	if clientVersion != "" {
		r.Header.Set(ClientVersionHeader, clientVersion)
	}
	if claims != nil {
		r = r.WithContext(context.WithValue(r.Context(), claimsKey, claims))
	}
	return r
}

var session = &UserClaims{FromToken: false}
var pat = &UserClaims{FromToken: true}

func TestGateIsInertWithoutMinimums(t *testing.T) {
	// The state this ships in. Nothing may be rejected, including the clients
	// that do not send the headers yet, or landing the gate breaks everyone
	// before a single response shape has changed.
	for _, r := range []*http.Request{
		request("", "", session),
		request("web", "1.0.0", session),
		request("", "", nil),
	} {
		if reached, rec := serve(t, map[string]string{}, r); !reached {
			t.Errorf("request was rejected with %d while no minimums are set", rec.Code)
		}
	}
}

func TestKnownClientTooOldIsRejected(t *testing.T) {
	min := map[string]string{"web": "26.9.0"}
	reached, rec := serve(t, min, request("web", "26.8.1", session))
	if reached {
		t.Fatal("an out-of-date web client reached the handler")
	}
	if rec.Code != http.StatusUpgradeRequired {
		t.Errorf("status = %d, want 426; 401 in particular would surface as a spurious logout in the web client", rec.Code)
	}
	if got := rec.Header().Get(MinClientHeader); got != "26.9.0" {
		t.Errorf("%s = %q, want %q", MinClientHeader, got, "26.9.0")
	}

	var body struct {
		Error string `json:"error"`
		Code  string `json:"code"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decoding body: %v", err)
	}
	if body.Code != CodeClientTooOld {
		t.Errorf("code = %q, want %q", body.Code, CodeClientTooOld)
	}
	// iOS surfaces the raw body to the user, so the message has to stand alone.
	if body.Error == "" {
		t.Error("no message; the iOS client would show an empty error")
	}
	for _, want := range []string{"26.9.0", "26.8.1"} {
		if !strings.Contains(body.Error, want) {
			t.Errorf("message %q does not mention %q", body.Error, want)
		}
	}
}

func TestCurrentAndNewerClientsPass(t *testing.T) {
	min := map[string]string{"web": "26.9.0"}
	for _, v := range []string{"26.9.0", "26.9.1", "26.10.0", "27.1.0"} {
		if reached, rec := serve(t, min, request("web", v, session)); !reached {
			t.Errorf("web %s was rejected with %d, want allowed", v, rec.Code)
		}
	}
}

func TestReleaseCandidateOfTheRequiredVersionPasses(t *testing.T) {
	// An rc carries the shapes of the release it is a candidate for, and the
	// testers running it are the people the minimum is being raised for.
	min := map[string]string{"web": "26.9.0"}
	for _, v := range []string{"26.9.0-rc.1", "26.9.0-rc.3", "26.9.0-nightly.202608240719"} {
		if reached, rec := serve(t, min, request("web", v, session)); !reached {
			t.Errorf("web %s was rejected with %d, want allowed", v, rec.Code)
		}
	}
}

func TestDevBuildsAreExempt(t *testing.T) {
	min := map[string]string{"web": "26.9.0", "mcp": "26.9.0"}
	for _, v := range []string{"0.0.0-dev", "0.0.0-dev 2026-08-24 07:19 EDT"} {
		if reached, _ := serve(t, min, request("mcp", v, pat)); !reached {
			t.Errorf("dev build %q was rejected; this locks a developer out of their own stack", v)
		}
	}
}

func TestKnownClientWithoutAVersionIsRejected(t *testing.T) {
	// It claimed to be one of ours and would not say which version. Fail closed.
	min := map[string]string{"web": "26.9.0"}
	reached, rec := serve(t, min, request("web", "", session))
	if reached {
		t.Fatal("a web client that sent no version reached the handler")
	}
	if rec.Code != http.StatusUpgradeRequired {
		t.Errorf("status = %d, want 426", rec.Code)
	}
}

func TestUnknownClientIsNotGated(t *testing.T) {
	// This release has no opinion about it, so it is not ours to reject.
	min := map[string]string{"web": "26.9.0"}
	if reached, rec := serve(t, min, request("some-third-party-tool", "0.1.0", pat)); !reached {
		t.Errorf("an unrecognised client was rejected with %d", rec.Code)
	}
}

func TestNoHeaderIsTokenDependent(t *testing.T) {
	min := map[string]string{"web": "26.9.0"}

	// A personal access token is a deliberate raw-API integration with no UI to
	// break. Someone's cron job must not stop working because the web client
	// changed shape.
	if reached, rec := serve(t, min, request("", "", pat)); !reached {
		t.Errorf("a PAT-authenticated caller sending no client header was rejected with %d", rec.Code)
	}

	// An interactive session with no client header is a first-party app built
	// before the headers existed, which is the whole population this gate is
	// for. Allowing it would make the gate miss exactly what it is aimed at.
	reached, rec := serve(t, min, request("", "", session))
	if reached {
		t.Error("a session with no client header reached the handler; old clients would slip straight through")
	}
	if rec.Code != http.StatusUpgradeRequired {
		t.Errorf("status = %d, want 426", rec.Code)
	}
}

func TestClientNameIsCaseInsensitive(t *testing.T) {
	// classifyClient in the logger lowercases before comparing, so this has to
	// agree or the two disagree about what "web" means.
	min := map[string]string{"web": "26.9.0"}
	if reached, _ := serve(t, min, request("WEB", "26.8.1", session)); reached {
		t.Error("client name \"WEB\" bypassed the gate")
	}
}
