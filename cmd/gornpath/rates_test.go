// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import (
	"bytes"
	"testing"
	"time"
)

type rateTableProviderFunc func() ([]any, error)

func (f rateTableProviderFunc) RateTable() ([]any, error) {
	return f()
}

func TestRenderRateTableTextFormatsRows(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 4, 5, 15, 0, 0, 0, time.UTC)
	rows := []any{
		map[string]any{
			"hash":            []byte{0x02},
			"last":            float64(now.Add(-30 * time.Second).Unix()),
			"rate_violations": 2,
			"blocked_until":   float64(now.Add(25 * time.Minute).Unix()),
			"timestamps":      []any{float64(now.Add(-2 * time.Hour).Unix()), float64(now.Add(-1 * time.Hour).Unix())},
		},
		map[string]any{
			"hash":            []byte{0x01},
			"last":            float64(now.Add(-2 * time.Minute).Unix()),
			"rate_violations": 0,
			"blocked_until":   float64(0),
			"timestamps":      []any{float64(now.Add(-3 * time.Hour).Unix())},
		},
	}

	got, err := renderRateTable(rows, now, nil, false)
	if err != nil {
		t.Fatalf("renderRateTable returned error: %v", err)
	}
	want := "01 last heard 2 minutes ago, 0.333 announces/hour in the last 3 hours\n02 last heard 30 seconds ago, 1 announces/hour in the last 2 hours, 2 active rate violations, new announces allowed in 25 minutes\n"
	if got != want {
		t.Fatalf("rate table text mismatch:\n got: %q\nwant: %q", got, want)
	}
}

func TestRenderRateTableJSONUsesPythonFieldNames(t *testing.T) {
	t.Parallel()

	rows := []any{
		map[string]any{
			"hash":            []byte{0xaa, 0xbb},
			"last":            float64(123),
			"rate_violations": 3,
			"blocked_until":   float64(456),
			"timestamps":      []any{float64(111), float64(222)},
		},
	}

	got, err := renderRateTable(rows, time.Unix(500, 0).UTC(), nil, true)
	if err != nil {
		t.Fatalf("renderRateTable returned error: %v", err)
	}
	want := `[{"hash":"aabb","last":123,"rate_violations":3,"blocked_until":456,"timestamps":[111,222]}]`
	if got != want {
		t.Fatalf("rate table JSON mismatch:\n got: %q\nwant: %q", got, want)
	}
}

func TestDoRatesPrintsNoInformationAvailable(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	err := doRates(&out, rateTableProviderFunc(func() ([]any, error) {
		return nil, nil
	}), nil, false)
	if err != errNoRateInformation {
		t.Fatalf("doRates error = %v, want %v", err, errNoRateInformation)
	}
	if got, want := out.String(), "No information available\n"; got != want {
		t.Fatalf("unexpected no-information output: got %q want %q", got, want)
	}
}
