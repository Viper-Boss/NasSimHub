package store

import (
	"context"
	"github.com/human-agent65535/modemdeck/internal/auth"
	"testing"
	"time"
)

func TestCommunicationStatisticsAreIsolatedByLine(t *testing.T) {
	repo := newHardwareTestStore(t)
	now := time.Now().UTC()
	for _, line := range []string{"line-a", "line-b"} {
		if _, err := repo.database.Exec(`INSERT INTO sms(line_id,peer,content,type,timestamp,created_at) VALUES (?,'test','test',1,?,?)`, line, now.Format(time.RFC3339), now.Format(time.RFC3339)); err != nil {
			t.Fatal(err)
		}
	}
	stats, err := repo.CommunicationStatsForLine(context.Background(), now, 24, "line-a")
	if err != nil || stats.SMSReceived != 1 {
		t.Fatalf("scoped=%+v err=%v", stats, err)
	}
	ctx := auth.ContextWithPrincipal(context.Background(), auth.Principal{UserID: "member", Role: auth.RoleMember, AllowedLineIDs: []string{"line-b"}})
	stats, err = repo.CommunicationStatsForLine(ctx, now, 24, "line-a")
	if err != nil || stats.SMSReceived != 0 {
		t.Fatalf("access scope widened: %+v %v", stats, err)
	}
}
