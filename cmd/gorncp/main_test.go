package main

import (
	"testing"

	"github.com/gmlewis/go-reticulum/testutils"
)

func tempDir(t *testing.T) (string, func()) {
	return testutils.TempDir(t, "gorncp-test-")
}
