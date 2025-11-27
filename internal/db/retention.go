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
	lastHour  time.Time // 0–1h, 1m
	lastDay   time.Time // 1h–1d, 30m
	lastWeek  time.Time // 1d–7d, 3h
	lastMonth time.Time // 7d–30d, 12h
	lastYear  time.Time // 30d–365d, 7d
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
	state := gfsState{}
	keepSet := make(map[uint]struct{}, len(rows))

	for _, r := range rows {
		id := r.GetID()
		t := r.GetCreatedAt()

		if t.After(now) {
			t = now
		}

		age := now.Sub(t)
		var keep bool

		switch {
		case age <= time.Minute:
			// 0–1min -> keep all
			keep = true

		case age <= time.Hour:
			// 0–1h, 1/min
			keep = shouldKeep(&state.lastHour, t, time.Minute)

		case age <= 24*time.Hour:
			// 1h–1d, 30min
			keep = shouldKeep(&state.lastDay, t, 30*time.Minute)

		case age <= 7*day:
			// 1d–7d, 3h
			keep = shouldKeep(&state.lastWeek, t, 3*time.Hour)

		case age <= 30*day:
			// 7d–30d, 12h
			keep = shouldKeep(&state.lastMonth, t, 12*time.Hour)

		case age <= year:
			// 30d–365d, 7d
			keep = shouldKeep(&state.lastYear, t, week)

		default:
			// > 1 year -> drop
			keep = false
		}

		if keep {
			keepSet[id] = struct{}{}
		}
	}

	for _, r := range rows {
		id := r.GetID()
		if _, ok := keepSet[id]; ok {
			keepIDs = append(keepIDs, id)
		} else {
			deleteIDs = append(deleteIDs, id)
		}
	}

	return keepIDs, deleteIDs
}
