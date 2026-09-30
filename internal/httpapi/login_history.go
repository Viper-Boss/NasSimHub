package httpapi

import (
	"sync"
	"time"
)

// loginHistoryWindow is how far back the dashboard's security events look.
const loginHistoryWindow = 24 * time.Hour

// loginFailureHistory counts failed password logins per minute over the last
// day, for the dashboard's "failed logins" figure.
//
// It is a separate thing from the limiter's own state on purpose. The limiter
// forgets an account after five quiet minutes and forgets it at once on a
// successful login -- which is right for throttling and useless for a count,
// because "someone tried eight passwords and then got it right" would read as
// nothing at all.
//
// Per-minute buckets rather than a list of instants: the memory is fixed
// whatever an attacker does (1440 counters), and a count over the last day is
// exact to the minute.
//
// It lives in memory, so a restart starts it again from nothing. `since` is
// when it started, and the reader is told how much of the day it actually
// covers rather than being handed a count for a day nobody watched.
type loginFailureHistory struct {
	mu      sync.Mutex
	since   time.Time
	minutes [int(loginHistoryWindow / time.Minute)]loginMinute
}

type loginMinute struct {
	minute int64
	count  int
}

func newLoginFailureHistory(now time.Time) *loginFailureHistory {
	return &loginFailureHistory{since: now}
}

func (history *loginFailureHistory) record(now time.Time) {
	if history == nil {
		return
	}
	minute := now.Unix() / 60
	history.mu.Lock()
	defer history.mu.Unlock()
	slot := &history.minutes[minute%int64(len(history.minutes))]
	if slot.minute != minute {
		*slot = loginMinute{minute: minute}
	}
	slot.count++
}

// recent answers the failures in the last day and how much of that day was
// actually watched. `watched` is never more than a day, and it is less right
// after a restart -- the caller must say so rather than present the count as a
// whole day's.
func (history *loginFailureHistory) recent(now time.Time) (failures int, watched time.Duration) {
	if history == nil {
		return 0, 0
	}
	current := now.Unix() / 60
	oldest := current - int64(len(history.minutes)) + 1
	history.mu.Lock()
	defer history.mu.Unlock()
	for _, slot := range history.minutes {
		if slot.count > 0 && slot.minute >= oldest && slot.minute <= current {
			failures += slot.count
		}
	}
	watched = now.Sub(history.since)
	if watched < 0 {
		watched = 0
	}
	if watched > loginHistoryWindow {
		watched = loginHistoryWindow
	}
	return failures, watched
}
