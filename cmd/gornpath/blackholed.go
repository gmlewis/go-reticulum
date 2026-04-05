// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/gmlewis/go-reticulum/rns"
)

var errNoBlackholedInformation = errors.New("no blackholed identity data available")

type blackholedIdentityProvider interface {
	BlackholedIdentities() ([]any, error)
}

type blackholedIdentityRow struct {
	IdentityHash []byte
	Source       []byte
	Until        int64
	Reason       string
}

func doBlackholed(out io.Writer, provider blackholedIdentityProvider, filter string, localIdentity []byte) error {
	rows, err := provider.BlackholedIdentities()
	if err != nil {
		return err
	}
	rendered, err := renderBlackholedIdentities(rows, time.Now(), filter, localIdentity)
	if errors.Is(err, errNoBlackholedInformation) {
		if _, writeErr := fmt.Fprintln(out, "No blackholed identity data available"); writeErr != nil {
			return writeErr
		}
		return errNoBlackholedInformation
	}
	if err != nil {
		return err
	}
	_, err = fmt.Fprint(out, rendered)
	return err
}

func renderBlackholedIdentities(rows []any, now time.Time, filter string, localIdentity []byte) (string, error) {
	entries, err := normalizeBlackholedIdentities(rows)
	if err != nil {
		return "", err
	}
	sort.SliceStable(entries, func(i, j int) bool {
		return bytes.Compare(entries[i].IdentityHash, entries[j].IdentityHash) < 0
	})

	var builder strings.Builder
	displayed := 0
	for _, entry := range entries {
		untilStr := "indefinitely"
		if entry.Until > 0 {
			remaining := time.Unix(entry.Until, 0).Sub(now)
			if remaining < 0 {
				remaining = 0
			}
			untilStr = "for " + rns.PrettyTime(remaining.Seconds(), false, true)
		}

		reasonStr := ""
		if entry.Reason != "" {
			reasonStr = " (" + truncateBlackholeReason(entry.Reason) + ")"
		}

		byStr := ""
		if len(entry.Source) > 0 && !bytes.Equal(entry.Source, localIdentity) {
			byStr = " by " + rns.PrettyHex(entry.Source)
		}

		filterStr := fmt.Sprintf("%s %s %s %s", rns.PrettyHex(entry.IdentityHash), untilStr, reasonStr, byStr)
		if filter != "" && !strings.Contains(filterStr, filter) {
			continue
		}

		builder.WriteString(fmt.Sprintf("%s blackholed %s%s%s\n", rns.PrettyHex(entry.IdentityHash), untilStr, reasonStr, byStr))
		displayed++
	}

	if displayed == 0 {
		return "", errNoBlackholedInformation
	}
	return builder.String(), nil
}

func normalizeBlackholedIdentities(rows []any) ([]blackholedIdentityRow, error) {
	entries := make([]blackholedIdentityRow, 0, len(rows))
	for _, row := range rows {
		entryMap, ok := row.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("blackholed entry has unexpected type %T", row)
		}
		entry, err := normalizeBlackholedIdentityEntry(entryMap)
		if err != nil {
			return nil, err
		}
		entries = append(entries, entry)
	}
	return entries, nil
}

func normalizeBlackholedIdentityEntry(row map[string]any) (blackholedIdentityRow, error) {
	entry := blackholedIdentityRow{}
	if value, ok := row["identity_hash"]; ok {
		hash, err := asBytes(value)
		if err != nil {
			return blackholedIdentityRow{}, err
		}
		entry.IdentityHash = hash
	}
	if value, ok := row["source"]; ok {
		source, err := asBytes(value)
		if err != nil {
			return blackholedIdentityRow{}, err
		}
		entry.Source = source
	}
	if value, ok := row["until"]; ok {
		until, err := asInt64(value)
		if err != nil {
			return blackholedIdentityRow{}, err
		}
		entry.Until = until
	}
	if value, ok := row["reason"]; ok {
		reason, ok := value.(string)
		if !ok {
			return blackholedIdentityRow{}, fmt.Errorf("unexpected reason type %T", value)
		}
		entry.Reason = reason
	}
	return entry, nil
}

func truncateBlackholeReason(reason string) string {
	const limit = 64
	if len(reason) <= limit {
		return reason
	}
	return reason[:limit-1] + "…"
}

func asInt64(value any) (int64, error) {
	switch v := value.(type) {
	case int:
		return int64(v), nil
	case int64:
		return v, nil
	case int32:
		return int64(v), nil
	case float64:
		return int64(v), nil
	case float32:
		return int64(v), nil
	default:
		return 0, fmt.Errorf("unexpected integer type %T", value)
	}
}
