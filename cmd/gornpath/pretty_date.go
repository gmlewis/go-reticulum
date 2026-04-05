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
	seconds := int(diff.Seconds())
	if seconds < 60 {
		return fmtInt(seconds, "seconds")
	}
	if seconds < 120 {
		return "1 minute"
	}
	if seconds < 3600 {
		return fmtInt(seconds/60, "minutes")
	}
	if seconds < 7200 {
		return "an hour"
	}
	if seconds < 86400 {
		return fmtInt(seconds/3600, "hours")
	}
	days := int(diff.Hours() / 24)
	if days == 1 {
		return "1 day"
	}
	if days < 7 {
		return fmtInt(days, "days")
	}
	if days < 31 {
		return fmtInt(days/7, "weeks")
	}
	if days < 365 {
		return fmtInt(days/30, "months")
	}
	return fmtInt(days/365, "years")
}

func fmtInt(value int, unit string) string {
	return strconv.Itoa(value) + " " + unit
}
