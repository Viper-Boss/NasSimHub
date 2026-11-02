package store

import (
	"context"
	"strings"

	"github.com/human-agent65535/modemdeck/internal/auth"
)

func principalLineScope(
	ctx context.Context,
	column string,
) (condition string, arguments []any, scoped bool) {
	principal, ok := auth.PrincipalFromContext(ctx)
	if !ok {
		return "", nil, false
	}
	lineIDs := uniqueNonEmptyStrings(principal.AllowedLineIDs)
	if len(lineIDs) == 0 {
		return "1 = 0", nil, true
	}
	condition = column + " IN (" + placeholders(len(lineIDs)) + ")"
	arguments = make([]any, 0, len(lineIDs))
	for _, lineID := range lineIDs {
		arguments = append(arguments, lineID)
	}
	return condition, arguments, true
}

func principalCanAccessLine(ctx context.Context, lineID string) bool {
	principal, ok := auth.PrincipalFromContext(ctx)
	return !ok || principal.CanAccessLine(strings.TrimSpace(lineID))
}

// viewerUserIDSQL names the signed-in account inside a query. The ID is never
// spliced into the SQL text: withViewer binds it as the statement's first
// parameter through a one-row CTE, which every subquery of the statement can
// see. This replaced hand-escaped string literals (review P2-12).
const viewerUserIDSQL = "(SELECT user_id FROM modemdeck_viewer)"

// contactOwnerSQL restricts contacts to the viewer's own address book. A
// statement that uses it must be run through withViewer.
func contactOwnerSQL(ctx context.Context, alias string) string {
	if _, ok := auth.PrincipalFromContext(ctx); !ok {
		return ""
	}
	return " AND " + alias + ".owner_user_id = " + viewerUserIDSQL
}

// withViewer binds the signed-in account for viewerUserIDSQL. statement must
// be a single SELECT; the viewer becomes parameter 1 and the existing
// positional arguments keep their order after it. Without a principal the
// statement is returned unchanged, matching contactOwnerSQL.
func withViewer(ctx context.Context, statement string, arguments []any) (string, []any) {
	principal, ok := auth.PrincipalFromContext(ctx)
	if !ok {
		return statement, arguments
	}
	bound := make([]any, 0, len(arguments)+1)
	bound = append(bound, principal.UserID)
	bound = append(bound, arguments...)
	return "WITH modemdeck_viewer(user_id) AS (SELECT ?) " + statement, bound
}
