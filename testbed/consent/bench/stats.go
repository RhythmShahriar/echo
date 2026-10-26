// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package main

import (
	"math"
	"slices"
	"time"
)

// percentile is the p-th percentile (0 < p <= 100) of xs by the
// nearest-rank method: the smallest value with at least p% of the
// values at or below it. Zero for no values.
func percentile(xs []time.Duration, p float64) time.Duration {
	if len(xs) == 0 {
		return 0
	}
	s := slices.Clone(xs)
	slices.Sort(s)
	rank := int(math.Ceil(p / 100 * float64(len(s))))
	rank = min(max(rank, 1), len(s))
	return s[rank-1]
}

// maxDuration is the largest of xs, zero for none.
func maxDuration(xs []time.Duration) time.Duration {
	if len(xs) == 0 {
		return 0
	}
	return slices.Max(xs)
}

// seconds is d in seconds, rounded to tenths, for results.json.
func seconds(d time.Duration) float64 { return math.Round(d.Seconds()*10) / 10 }
