package db

import (
	"fmt"
	"time"
)

type TimeSeriesRow interface {
	GetID() uint
	GetCreatedAt() time.Time
}

const (
	day  = 24 * time.Hour
	week = 7 * day
	year = 365 * day
)

type gfsState struct {
	lastHour  time.Time
	lastDay   time.Time
	lastWeek  time.Time
	lastMonth time.Time
	lastYear  time.Time
}

type GFSStep string

const (
	GFSStepHourly  GFSStep = "hourly"
	GFSStepDaily   GFSStep = "daily"
	GFSStepWeekly  GFSStep = "weekly"
	GFSStepMonthly GFSStep = "monthly"
	GFSStepYearly  GFSStep = "yearly"
)

func (s GFSStep) Window() (time.Duration, error) {
	switch s {
	case GFSStepHourly:
		return time.Hour, nil
	case GFSStepDaily:
		return 24 * time.Hour, nil
	case GFSStepWeekly:
		return 7 * 24 * time.Hour, nil
	case GFSStepMonthly:
		return 30 * 24 * time.Hour, nil
	case GFSStepYearly:
		return 365 * 24 * time.Hour, nil
	default:
		return 0, fmt.Errorf("unknown_gfs_step: %q", s)
	}
}

func shouldKeep(last *time.Time, t time.Time, step time.Duration) bool {
	if last.IsZero() || t.Sub(*last) >= step {
		*last = t
		return true
	}
	return false
}

func ApplyGFS[T TimeSeriesRow](now time.Time, rows []T) (keepIDs []uint, deleteIDs []uint) {
	const (
		day  = 24 * time.Hour
		week = 7 * day
		year = 365 * day
	)

	type chosen struct {
		id uint
		t  time.Time
	}

	buckets := map[string]map[int64]chosen{
		"hour":  {},
		"day":   {},
		"week":  {},
		"month": {},
		"year":  {},
	}

	keepSet := make(map[uint]struct{}, len(rows))

	for _, r := range rows {
		id := r.GetID()
		t := r.GetCreatedAt()

		if t.After(now) {
			t = now
		}

		age := now.Sub(t)

		if age <= time.Minute {
			keepSet[id] = struct{}{}
			continue
		}

		var interval time.Duration
		var bucketMap map[int64]chosen

		switch {
		case age <= time.Hour:
			interval = time.Minute
			bucketMap = buckets["hour"]
		case age <= 24*time.Hour:
			interval = 30 * time.Minute
			bucketMap = buckets["day"]
		case age <= 7*day:
			interval = 3 * time.Hour
			bucketMap = buckets["week"]
		case age <= 30*day:
			interval = 12 * time.Hour
			bucketMap = buckets["month"]
		case age <= year:
			interval = week
			bucketMap = buckets["year"]
		default:
			continue
		}

		bucketID := t.UnixNano() / interval.Nanoseconds()

		cur, exists := bucketMap[bucketID]
		if !exists || t.After(cur.t) {
			bucketMap[bucketID] = chosen{id: id, t: t}
		}
	}

	for _, m := range buckets {
		for _, ch := range m {
			keepSet[ch.id] = struct{}{}
		}
	}

	seenKeep := make(map[uint]struct{}, len(keepSet))
	seenDel := make(map[uint]struct{}, len(rows))

	for _, r := range rows {
		id := r.GetID()
		if _, keep := keepSet[id]; keep {
			if _, s := seenKeep[id]; !s {
				keepIDs = append(keepIDs, id)
				seenKeep[id] = struct{}{}
			}
		} else {
			if _, s := seenDel[id]; !s {
				deleteIDs = append(deleteIDs, id)
				seenDel[id] = struct{}{}
			}
		}
	}

	return keepIDs, deleteIDs
}
