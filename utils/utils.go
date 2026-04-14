// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

// Package utils provides small reusable helpers used across this repository.
// The package collects shared routines that reduce duplication and can also
// be reused by external Go Reticulum tools when appropriate.
package utils

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"strings"
)

// ErrHelp reports that command-line help was requested.
var ErrHelp = errors.New("help requested")

// NewFlagSet constructs a flag set that suppresses default flag package
// output and invokes the supplied usage function when help is requested.
func NewFlagSet(name string, usage func()) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.Usage = usage
	return fs
}

// PrintVersion writes a standard "name version" line to the given writer.
func PrintVersion(w io.Writer, appName, version string) {
	if _, err := fmt.Fprintf(w, "%v %v\n", appName, version); err != nil {
		log.Fatalf("utils.PrintVersion: %v", err)
	}
}

// WriteText writes the provided text to the given writer.
func WriteText(w io.Writer, text string) {
	if _, err := io.WriteString(w, text); err != nil {
		log.Fatalf("utils.WriteText: %v", err)
	}
}

// AsInt converts a numeric value to int.
func AsInt(v any) (int, bool) {
	switch val := v.(type) {
	case int:
		return val, true
	case int64:
		return int(val), true
	case uint64:
		return int(val), true
	case float64:
		return int(val), true
	}
	return 0, false
}

// ShlexSplit splits a string into tokens like a shell would, matching Python shlex.split.
func ShlexSplit(s string) ([]string, error) {
	var tokens []string
	var token strings.Builder
	inQuote := false
	var quoteChar rune

	for i := 0; i < len(s); i++ {
		r := rune(s[i])
		if inQuote {
			if r == quoteChar {
				inQuote = false
			} else {
				token.WriteRune(r)
			}
		} else {
			switch r {
			case ' ', '\t', '\n', '\r':
				if token.Len() > 0 {
					tokens = append(tokens, token.String())
					token.Reset()
				}
			case '"', '\'':
				inQuote = true
				quoteChar = r
			default:
				token.WriteRune(r)
			}
		}
	}
	if token.Len() > 0 {
		tokens = append(tokens, token.String())
	}
	if inQuote {
		return nil, fmt.Errorf("unclosed quote")
	}
	return tokens, nil
}
