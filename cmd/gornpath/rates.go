// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gmlewis/go-reticulum/rns"
)

var errNoRateInformation = errors.New("no rate information available")

type rateTableProvider interface {
	RateTable() ([]any, error)
}

type rateTableEntry struct {
	Hash           []byte
	Last           float64
	RateViolations int
	BlockedUntil   float64
	Timestamps     []float64
}

func doRates(out io.Writer, rt rateTableProvider, destinationHash []byte, jsonOut bool) error {
	rows, err := rt.RateTable()
	if err != nil {
		return err
	}
	rendered, err := renderRateTable(rows, time.Now(), destinationHash, jsonOut)
	if errors.Is(err, errNoRateInformation) {
		if _, writeErr := fmt.Fprintln(out, "No information available"); writeErr != nil {
			return writeErr
		}
		return errNoRateInformation
	}
	if err != nil {
		return err
	}
	_, err = fmt.Fprint(out, rendered)
	return err
}

func renderRateTable(rows []any, now time.Time, destinationHash []byte, jsonOut bool) (string, error) {
	entries, err := normalizeRateTable(rows)
	if err != nil {
		return "", err
	}
	entries = filterAndSortRateTable(entries, destinationHash)
	if jsonOut {
		return renderRateTableJSON(entries)
	}
	if len(entries) == 0 {
		return "", errNoRateInformation
	}
	return renderRateTableText(entries, now), nil
}

func normalizeRateTable(rows []any) ([]rateTableEntry, error) {
	entries := make([]rateTableEntry, 0, len(rows))
	for _, row := range rows {
		entryMap, ok := row.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("rate table entry has unexpected type %T", row)
		}
		entry, err := normalizeRateTableEntry(entryMap)
		if err != nil {
			return nil, err
		}
		entries = append(entries, entry)
	}
	return entries, nil
}

func normalizeRateTableEntry(row map[string]any) (rateTableEntry, error) {
	entry := rateTableEntry{}
	if value, ok := row["hash"]; ok {
		hash, err := asBytes(value)
		if err != nil {
			return rateTableEntry{}, err
		}
		entry.Hash = hash
	}
	if value, ok := row["last"]; ok {
		last, err := asFloat64(value)
		if err != nil {
			return rateTableEntry{}, err
		}
		entry.Last = last
	}
	if value, ok := row["rate_violations"]; ok {
		violations, err := asInt(value)
		if err != nil {
			return rateTableEntry{}, err
		}
		entry.RateViolations = violations
	}
	if value, ok := row["blocked_until"]; ok {
		blockedUntil, err := asFloat64(value)
		if err != nil {
			return rateTableEntry{}, err
		}
		entry.BlockedUntil = blockedUntil
	}
	if value, ok := row["timestamps"]; ok {
		timestamps, err := asFloat64Slice(value)
		if err != nil {
			return rateTableEntry{}, err
		}
		entry.Timestamps = timestamps
	}
	return entry, nil
}

func filterAndSortRateTable(entries []rateTableEntry, destinationHash []byte) []rateTableEntry {
	filtered := make([]rateTableEntry, 0, len(entries))
	for _, entry := range entries {
		if len(destinationHash) > 0 && !bytes.Equal(entry.Hash, destinationHash) {
			continue
		}
		filtered = append(filtered, entry)
	}
	sort.SliceStable(filtered, func(i, j int) bool {
		return filtered[i].Last < filtered[j].Last
	})
	return filtered
}

func renderRateTableText(entries []rateTableEntry, now time.Time) string {
	var builder strings.Builder
	for _, entry := range entries {
		if len(entry.Timestamps) == 0 {
			continue
		}
		lastTime := time.Unix(0, int64(entry.Last*1e9))
		startTime := time.Unix(0, int64(entry.Timestamps[0]*1e9))
		span := math.Max(now.Sub(startTime).Seconds(), 3600.0)
		spanHours := span / 3600.0
		hourRate := math.Round((float64(len(entry.Timestamps))/spanHours)*1000) / 1000
		hourRateStr := strconv.FormatFloat(hourRate, 'f', -1, 64)

		rvStr := ""
		if entry.RateViolations > 0 {
			plural := "s"
			if entry.RateViolations == 1 {
				plural = ""
			}
			rvStr = fmt.Sprintf(", %v active rate violation%v", entry.RateViolations, plural)
		}

		blStr := ""
		if entry.BlockedUntil > float64(now.Unix()) {
			remaining := time.Duration((entry.BlockedUntil - float64(now.Unix())) * float64(time.Second))
			blStr = ", new announces allowed in " + prettyDateAt(now, now.Add(-remaining))
		}

		builder.WriteString(fmt.Sprintf("%x last heard %v ago, %v announces/hour in the last %v%v%v\n",
			entry.Hash, prettyDateAt(now, lastTime), hourRateStr, prettyDateAt(now, startTime), rvStr, blStr))
	}
	return builder.String()
}

type rateTableJSONEntry struct {
	Hash           string    `json:"hash"`
	Last           float64   `json:"last"`
	RateViolations int       `json:"rate_violations"`
	BlockedUntil   float64   `json:"blocked_until"`
	Timestamps     []float64 `json:"timestamps"`
}

func renderRateTableJSON(entries []rateTableEntry) (string, error) {
	jsonEntries := make([]rateTableJSONEntry, 0, len(entries))
	for _, entry := range entries {
		jsonEntries = append(jsonEntries, rateTableJSONEntry{
			Hash:           fmt.Sprintf("%x", entry.Hash),
			Last:           entry.Last,
			RateViolations: entry.RateViolations,
			BlockedUntil:   entry.BlockedUntil,
			Timestamps:     entry.Timestamps,
		})
	}
	data, err := json.Marshal(jsonEntries)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func asBytes(value any) ([]byte, error) {
	if value == nil {
		return nil, nil
	}
	switch v := value.(type) {
	case []byte:
		return append([]byte(nil), v...), nil
	case string:
		return []byte(v), nil
	default:
		return nil, fmt.Errorf("unexpected hash type %T", value)
	}
}

func asFloat64(value any) (float64, error) {
	switch v := value.(type) {
	case float64:
		return v, nil
	case float32:
		return float64(v), nil
	case int:
		return float64(v), nil
	case int64:
		return float64(v), nil
	case int32:
		return float64(v), nil
	case json.Number:
		return v.Float64()
	default:
		return 0, fmt.Errorf("unexpected numeric type %T", value)
	}
}

func asInt(value any) (int, error) {
	switch v := value.(type) {
	case int:
		return v, nil
	case int64:
		return int(v), nil
	case int32:
		return int(v), nil
	case float64:
		return int(v), nil
	case float32:
		return int(v), nil
	case json.Number:
		ival, err := v.Int64()
		return int(ival), err
	default:
		return 0, fmt.Errorf("unexpected integer type %T", value)
	}
}

func asFloat64Slice(value any) ([]float64, error) {
	switch v := value.(type) {
	case []float64:
		return append([]float64(nil), v...), nil
	case []any:
		values := make([]float64, 0, len(v))
		for _, item := range v {
			number, err := asFloat64(item)
			if err != nil {
				return nil, err
			}
			values = append(values, number)
		}
		return values, nil
	default:
		return nil, fmt.Errorf("unexpected timestamps type %T", value)
	}
}

func _rateRowsFromTransport(ts *rns.TransportSystem) []any {
	rows := ts.GetRateTable()
	out := make([]any, 0, len(rows))
	for _, row := range rows {
		out = append(out, row)
	}
	return out
}
