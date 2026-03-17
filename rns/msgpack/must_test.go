package msgpack

import "testing"

func mustTest(t *testing.T, err error) {
	if err != nil {
		t.Fatal(err)
	}
}
