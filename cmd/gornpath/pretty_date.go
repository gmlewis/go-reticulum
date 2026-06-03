// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import (
	"strconv"
	"time"
)

func prettyDate(value time.Time) string {
	return prettyDateAt(time.Now(), value)
}

func prettyDateAt(now, value time.Time) string {
	diff := now.Sub(value)
	if diff < 0 {
		return ""
	}
	totalSeconds := int(diff.Seconds())
	dayDiff := totalSeconds / 86400
	secondDiff := totalSeconds % 86400
	if dayDiff == 0 {
		if secondDiff < 10 {
			return fmtInt(secondDiff, "seconds")
		}
		if secondDiff < 60 {
			return fmtInt(secondDiff, "seconds")
		}
		if secondDiff < 120 {
			return "1 minute"
		}
		if secondDiff < 3600 {
			return fmtInt(secondDiff/60, "minutes")
		}
		if secondDiff < 7200 {
			return "an hour"
		}
		return fmtInt(secondDiff/3600, "hours")
	}
	if dayDiff == 1 {
		return "1 day"
	}
	if dayDiff < 7 {
		return fmtInt(dayDiff, "days")
	}
	if dayDiff < 31 {
		return fmtInt(dayDiff/7, "weeks")
	}
	if dayDiff < 365 {
		return fmtInt(dayDiff/30, "months")
	}
	return fmtInt(dayDiff/365, "years")
}

func fmtInt(value int, unit string) string {
	return strconv.Itoa(value) + " " + unit
}
