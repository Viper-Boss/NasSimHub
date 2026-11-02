package store

import (
	"context"
	"strings"
	"testing"

	"github.com/human-agent65535/modemdeck/internal/auth"
)

// Review P2-12: the viewer's user ID reaches SQL only as a bound parameter.
// An ID full of SQL metacharacters must come back byte-for-byte, and the
// statement's own positional arguments must keep their order behind it.
func TestWithViewerBindsTheUserIDAsAParameter(t *testing.T) {
	t.Parallel()

	_, database := newContactTestStore(t)
	hostile := `x' OR '1'='1'; -- "\`
	ctx := auth.ContextWithPrincipal(context.Background(), auth.Principal{
		UserID: hostile,
		Role:   auth.RoleMember,
	})

	if owner := contactOwnerSQL(ctx, "contacts"); strings.Contains(owner, hostile) || strings.Contains(owner, "'") {
		t.Fatalf("contactOwnerSQL spliced the user ID into SQL: %q", owner)
	}
	statement, arguments := withViewer(ctx, `SELECT ?, `+viewerUserIDSQL+`, ?`, []any{"first", "second"})
	var first, viewer, second string
	if err := database.QueryRowContext(ctx, statement, arguments...).Scan(&first, &viewer, &second); err != nil {
		t.Fatalf("query error = %v", err)
	}
	if first != "first" || viewer != hostile || second != "second" {
		t.Fatalf("got (%q, %q, %q), want (first, %q, second)", first, viewer, second, hostile)
	}

	unscoped, unscopedArguments := withViewer(context.Background(), "SELECT ?", []any{1})
	if unscoped != "SELECT ?" || len(unscopedArguments) != 1 || contactOwnerSQL(context.Background(), "contacts") != "" {
		t.Fatal("a query run without a principal was changed")
	}
}
