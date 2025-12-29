package db

import (
	"fmt"
	"reflect"
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
	GFSStepMinutely GFSStep = "minutely"
	GFSStepHourly   GFSStep = "hourly"
	GFSStepDaily    GFSStep = "daily"
	GFSStepWeekly   GFSStep = "weekly"
	GFSStepMonthly  GFSStep = "monthly"
	GFSStepYearly   GFSStep = "yearly"
)

type ReflectRow struct {
	Ptr interface{}
}

func (r ReflectRow) GetID() uint {
	v := reflect.ValueOf(r.Ptr)
	if v.Kind() != reflect.Ptr || v.IsNil() {
		return 0
	}

	idField := v.Elem().FieldByName("ID")
	if !idField.IsValid() {
		return 0
	}

	switch idField.Kind() {
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return uint(idField.Uint())
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return uint(idField.Int())
	default:
		return 0
	}
}

func (r ReflectRow) GetCreatedAt() time.Time {
	v := reflect.ValueOf(r.Ptr)
	if v.Kind() != reflect.Ptr || v.IsNil() {
		return time.Time{}
	}

	createdAt := v.Elem().FieldByName("CreatedAt")
	if !createdAt.IsValid() {
		return time.Time{}
	}

	if t, ok := createdAt.Interface().(time.Time); ok {
		return t
	}

	return time.Time{}
}

func (s GFSStep) Window() (time.Duration, error) {
	switch s {
	case GFSStepMinutely:
		// last hour, full res (no compaction in ApplyGFS)
		return time.Hour, nil

	case GFSStepHourly:
		// last 24 hours
		return 24 * time.Hour, nil

	case GFSStepDaily:
		// last 30 days
		return 30 * day, nil

	case GFSStepWeekly:
		// last 70 days (full retention)
		return 70 * day, nil

	case GFSStepMonthly:
		return 70 * day, nil

	case GFSStepYearly:
		return 70 * day, nil

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

// ApplyGFS keeps data only in the last 70 days, with variable resolution:
//
//   - age <= 1 hour:   keep all points (highest resolution)
//   - 1h–7d:           1 point / 10 minutes
//   - 7d–30d:          1 point / hour
//   - 30d–70d:         1 point / 6 hours
//   - >70d:            deleted
func ApplyGFS[T TimeSeriesRow](now time.Time, rows []T) (keepIDs []uint, deleteIDs []uint) {
	const (
		retention = 70 * day
	)

	type chosen struct {
		id uint
		t  time.Time
	}

	buckets := map[string]map[int64]chosen{
		"day":   {}, // 10m buckets for 1h–7d
		"week":  {}, // 1h buckets for 7d–30d
		"month": {}, // 6h buckets for 30d–70d
	}

	keepSet := make(map[uint]struct{}, len(rows))

	for _, r := range rows {
		id := r.GetID()
		t := r.GetCreatedAt()

		// Clamp future timestamps to 'now'.
		if t.After(now) {
			t = now
		}

		age := now.Sub(t)

		// Hard cap: anything older than 70 days is deleted.
		if age > retention {
			continue
		}

		// Entire last hour: keep everything, no compaction.
		if age <= time.Hour {
			keepSet[id] = struct{}{}
			continue
		}

		var interval time.Duration
		var bucketMap map[int64]chosen

		switch {
		case age <= 7*day:
			// 1h–7d: 1 point per 10 minutes.
			interval = 10 * time.Minute
			bucketMap = buckets["day"]

		case age <= 30*day:
			// 7d–30d: 1 point per hour.
			interval = time.Hour
			bucketMap = buckets["week"]

		default:
			// 30d–70d: 1 point per 6 hours.
			interval = 6 * time.Hour
			bucketMap = buckets["month"]
		}

		// Compute a bucket ID based on the chosen interval.
		bucketID := t.UnixNano() / interval.Nanoseconds()

		cur, exists := bucketMap[bucketID]
		// Keep the newest point in the bucket.
		if !exists || t.After(cur.t) {
			bucketMap[bucketID] = chosen{id: id, t: t}
		}
	}

	// Mark all bucket winners as "keep".
	for _, m := range buckets {
		for _, ch := range m {
			keepSet[ch.id] = struct{}{}
		}
	}

	// Build keep/delete slices preserving original order.
	keepIDs = make([]uint, 0, len(keepSet))
	deleteIDs = make([]uint, 0, len(rows))

	for _, r := range rows {
		id := r.GetID()
		if _, keep := keepSet[id]; keep {
			keepIDs = append(keepIDs, id)
		} else {
			deleteIDs = append(deleteIDs, id)
		}
	}

	return keepIDs, deleteIDs
}
