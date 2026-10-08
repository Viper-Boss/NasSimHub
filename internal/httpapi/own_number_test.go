package httpapi

import (
	"context"
	"errors"
	"github.com/human-agent65535/modemdeck/internal/auth"
	"github.com/human-agent65535/modemdeck/internal/store"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type numberRepository struct {
	Repository
	writes  int
	failure error
}

func (r *numberRepository) UpdateOwnNumber(_ context.Context, line, sim, number string) (string, error) {
	r.writes++
	if line != "fixture-line" || sim != "fixture-sim" {
		return "", errors.New("incorrect target")
	}
	return number, r.failure
}

func TestOwnNumberAuthorizationAndConflict(t *testing.T) {
	for _, test := range []struct {
		name           string
		role           auth.Role
		allowed        bool
		failure        error
		status, writes int
	}{
		{"admin", auth.RoleAdmin, true, nil, 200, 1},
		{"member", auth.RoleMember, true, nil, 403, 0},
		{"unassigned", auth.RoleAdmin, false, nil, 404, 0},
		{"SIM changed", auth.RoleAdmin, true, store.ErrOwnNumberSIMChanged, 409, 1},
		{"invalid", auth.RoleAdmin, true, store.ErrOwnNumberInvalid, 400, 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			repo := &numberRepository{failure: test.failure}
			api := &API{repository: repo}
			req := httptest.NewRequest(http.MethodPatch, "/api/v1/lines/fixture-line/own-number", strings.NewReader(`{"phone_number":"+8613800138000","expected_iccid":"fixture-sim"}`))
			req.Header.Set("Content-Type", "application/json")
			p := auth.Principal{UserID: "fixture", Role: test.role}
			if test.allowed {
				p.AllowedLineIDs = []string{"fixture-line"}
			}
			req = req.WithContext(auth.ContextWithPrincipal(req.Context(), p))
			response := httptest.NewRecorder()
			api.ownNumberResource(response, req, "fixture-line")
			if response.Code != test.status || repo.writes != test.writes {
				t.Fatalf("status %d writes %d", response.Code, repo.writes)
			}
		})
	}
}

func TestOwnNumberPathIsolation(t *testing.T) {
	for _, path := range []string{"/api/v1/lines/a/b/own-number", "/api/v1/lines/a%2Fb/own-number", "/api/v1/lines//own-number", "/api/v1/lines/a/own-number/extra"} {
		if _, ok := ownNumberResourceID(path); ok {
			t.Fatal("accepted ambiguous path", path)
		}
	}
	if id, ok := ownNumberResourceID("/api/v1/lines/nsh%3Afixture/own-number"); !ok || id != "nsh:fixture" {
		t.Fatal(id, ok)
	}
}
