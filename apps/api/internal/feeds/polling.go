package feeds

import (
	"strconv"
	"strings"
	"time"
)

// Adaptive poll bounds. A feed that keeps producing items is polled every
// pollFloor; a quiet one backs off by doubling up to pollCap.
const (
	pollFloor = 30 * time.Minute
	pollCap   = 24 * time.Hour
)

// nextPollInterval computes when a feed should next be polled. New items reset
// to the floor; an unchanged poll doubles the current interval (capped). A
// server's Cache-Control max-age is a hard lower bound in every case, so we
// never poll faster than the origin asked.
func nextPollInterval(current time.Duration, hadNewItems bool, cacheMaxAge time.Duration) time.Duration {
	var next time.Duration
	if hadNewItems {
		next = pollFloor
	} else {
		next = current * 2
		if next < pollFloor {
			next = pollFloor
		}
		if next > pollCap {
			next = pollCap
		}
	}
	if cacheMaxAge > next {
		next = cacheMaxAge
	}
	return next
}

// parseCacheControlMaxAge extracts max-age (seconds) from a Cache-Control
// header and subtracts the Age header when present, yielding the remaining
// freshness lifetime. Returns 0 when absent, malformed, or already stale.
func parseCacheControlMaxAge(cacheControl, age string) time.Duration {
	var maxAge time.Duration
	found := false
	for _, part := range strings.Split(cacheControl, ",") {
		part = strings.TrimSpace(part)
		if v, ok := strings.CutPrefix(part, "max-age="); ok {
			secs, err := strconv.Atoi(strings.TrimSpace(v))
			if err != nil || secs < 0 {
				return 0
			}
			maxAge = time.Duration(secs) * time.Second
			found = true
		}
	}
	if !found {
		return 0
	}
	if age != "" {
		if a, err := strconv.Atoi(strings.TrimSpace(age)); err == nil && a > 0 {
			maxAge -= time.Duration(a) * time.Second
		}
	}
	if maxAge < 0 {
		return 0
	}
	return maxAge
}
