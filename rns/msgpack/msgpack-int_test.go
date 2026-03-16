//go:build integration
// +build integration

package msgpack

import (
	"encoding/json"
	"fmt"
	"log"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
)

func getPythonPath() string {
	if path := os.Getenv("ORIGINAL_RETICULUM_REPO_DIR"); path != "" {
		return path
	}
	log.Fatalf("missing required environment variable: ORIGINAL_RETICULUM_REPO_DIR")
	return "" // unreachable
}

const checkMsgpackParityPy = `import RNS.vendor.umsgpack as umsgpack
import sys
import os
import json

def check_msgpack(path):
    try:
        if not os.path.exists(path):
            print(f"File not found: {path}")
            sys.exit(1)

        with open(path, "rb") as f:
            data = f.read()

        unpacked = umsgpack.unpackb(data)

        # Convert to a stable JSON representation for comparison
        def json_serializable(obj):
            if isinstance(obj, bytes):
                return {"_type": "bytes", "val": obj.hex()}
            if isinstance(obj, dict):
                # Sort keys for stability
                return {str(k): json_serializable(v) for k, v in sorted(obj.items(), key=lambda x: str(x[0]))}
            if isinstance(obj, list) or isinstance(obj, tuple):
                return [json_serializable(x) for x in obj]
            if isinstance(obj, int) and (obj > 2**53 - 1 or obj < -(2**53 - 1)):
                return str(obj)
            if isinstance(obj, float):
                return round(obj, 6)
            return obj

        print(json.dumps(json_serializable(unpacked)))
        sys.exit(0)
    except Exception as e:
        print(f"Error: {e}")
        sys.exit(1)

if __name__ == "__main__":
    if len(sys.argv) != 2:
        print("Usage: check_msgpack_parity.py <path>")
        sys.exit(1)
    check_msgpack(sys.argv[1])
`

const generateMsgpackParityPy = `import RNS.vendor.umsgpack as umsgpack
import sys

def generate_msgpack(path):
    data = {
        "x": 42,
        "y": [1.1, 2.2, {"z": b"bytes data"}],
        "null": None,
        "bool": True
    }
    packed = umsgpack.packb(data)
    with open(path, "wb") as f:
        f.write(packed)

if __name__ == "__main__":
    generate_msgpack(sys.argv[1])
`

func TestMessagePackParity(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "go-reticulum-msgpack-parity-*")
	mustTest(t, err)
	defer os.RemoveAll(tmpDir)

	scriptPath := filepath.Join(tmpDir, "check_msgpack_parity.py")
	if err := os.WriteFile(scriptPath, []byte(checkMsgpackParityPy), 0o644); err != nil {
		t.Fatal(err)
	}

	// Large string to trigger str8/16
	largeStr := ""
	for range 500 {
		largeStr += "x"
	}

	// Large byte slice to trigger bin8/16
	largeBytes := make([]byte, 500)
	for i := range largeBytes {
		largeBytes[i] = byte(i % 256)
	}

	// Complex nested structure with edge cases
	data := map[any]any{
		"a": int64(1),
		"b": []any{
			2.5,
			"c",
			map[any]any{
				"d": []byte{0xde, 0xad, 0xbe, 0xef},
			},
		},
		"neg":     int64(-42),
		"neg_fix": int64(-10),
		"large":   uint64(0x123456789abcdef0),
		"large_i": int64(-0x123456789abcdef0),
		"float32": float32(1.234),
		"float64": float64(1.23456789),
		"nil":     nil,
		"bool_t":  true,
		"bool_f":  false,
		"str8":    largeStr[:40],
		"str16":   largeStr,
		"bin8":    largeBytes[:40],
		"bin16":   largeBytes,
		"empty_s": "",
		"empty_b": []byte{},
		"empty_a": []any{},
		"empty_m": map[any]any{},
		"nested":  map[any]any{"deep": map[any]any{"deeper": []any{map[any]any{"deepest": "yes"}}}},
	}

	packed, err := Pack(data)
	mustTest(t, err)

	packedPath := filepath.Join(tmpDir, "packed.msgpack")
	if err := os.WriteFile(packedPath, packed, 0o644); err != nil {
		t.Fatal(err)
	}

	// Verify with Python
	cmd := exec.Command("python3", scriptPath, packedPath)
	cmd.Env = append(os.Environ(), "PYTHONPATH="+getPythonPath())
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("Python verification failed: %v\nOutput: %v", err, string(out))
	}

	// Compare JSON representations
	var pyJSON any
	if err := json.Unmarshal(out, &pyJSON); err != nil {
		t.Fatalf("Failed to parse Python output as JSON: %v\nOutput: %v", err, string(out))
	}

	// Construct Go JSON representation
	goJSON := jsonSerializable(data)

	goBytes, _ := json.Marshal(goJSON)
	pyBytes, _ := json.Marshal(pyJSON)

	// Since maps are not ordered, we compare the objects after unmarshaling again to avoid key order issues in JSON string
	var goObj, pyObj any
	json.Unmarshal(goBytes, &goObj)
	json.Unmarshal(pyBytes, &pyObj)

	if !reflect.DeepEqual(goObj, pyObj) {
		t.Errorf("MessagePack structure mismatch!\nGo JSON: %v\nPy JSON: %v", string(goBytes), string(pyBytes))
	}
}

func TestMessagePackPythonToGoParity(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "go-reticulum-msgpack-parity-*")
	mustTest(t, err)
	defer os.RemoveAll(tmpDir)

	const generateMsgpackParityPyExtended = `import RNS.vendor.umsgpack as umsgpack
import sys

def generate_msgpack(path):
    data = {
        "x": 42,
        "y": [1.1, 2.2, {"z": b"bytes data"}],
        "null": None,
        "bool": True,
        "neg": -123456789,
        "large": 0x123456789abcdef0,
        "float": 3.14159,
        "bytes": b"\x00\xff\x00\xff",
        "nested": {"a": [1, 2, 3]}
    }
    packed = umsgpack.packb(data)
    with open(path, "wb") as f:
        f.write(packed)

if __name__ == "__main__":
    generate_msgpack(sys.argv[1])
`

	scriptPath := filepath.Join(tmpDir, "generate_msgpack_parity.py")
	if err := os.WriteFile(scriptPath, []byte(generateMsgpackParityPyExtended), 0o644); err != nil {
		t.Fatal(err)
	}

	packedPath := filepath.Join(tmpDir, "py_packed.msgpack")

	// Generate with Python
	cmd := exec.Command("python3", scriptPath, packedPath)
	cmd.Env = append(os.Environ(), "PYTHONPATH="+getPythonPath())
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("Python generation failed: %v\nOutput: %v", err, string(out))
	}

	// Load with Go
	packed, err := os.ReadFile(packedPath)
	mustTest(t, err)

	unpacked, err := Unpack(packed)
	if err != nil {
		t.Fatalf("Go Unpack failed: %v", err)
	}

	// Verify structure
	expected := map[string]any{
		"x":      int64(42),
		"y":      []any{1.1, 2.2, map[string]any{"z": map[string]any{"_type": "bytes", "val": "62797465732064617461"}}},
		"null":   nil,
		"bool":   true,
		"neg":    int64(-123456789),
		"large":  fmt.Sprintf("%v", uint64(0x123456789abcdef0)),
		"float":  3.14159,
		"bytes":  map[string]any{"_type": "bytes", "val": "00ff00ff"},
		"nested": map[string]any{"a": []any{int64(1), int64(2), int64(3)}},
	}

	goJSON := jsonSerializable(unpacked)

	goBytes, _ := json.Marshal(goJSON)
	expectedBytes, _ := json.Marshal(expected)

	var goObj, expectedObj any
	json.Unmarshal(goBytes, &goObj)
	json.Unmarshal(expectedBytes, &expectedObj)

	if !reflect.DeepEqual(goObj, expectedObj) {
		t.Errorf("MessagePack structure mismatch!\nGo JSON: %v\nExpected JSON: %v", string(goBytes), string(expectedBytes))
	}
}

func jsonSerializable(obj any) any {
	rv := reflect.ValueOf(obj)
	if !rv.IsValid() {
		return nil
	}
	switch rv.Kind() {
	case reflect.Slice:
		if rv.Type().Elem().Kind() == reflect.Uint8 {
			return map[string]any{"_type": "bytes", "val": fmt.Sprintf("%x", rv.Bytes())}
		}
		s := make([]any, rv.Len())
		for i := range rv.Len() {
			s[i] = jsonSerializable(rv.Index(i).Interface())
		}
		return s
	case reflect.Map:
		m := make(map[string]any)
		for _, k := range rv.MapKeys() {
			m[fmt.Sprintf("%v", k.Interface())] = jsonSerializable(rv.MapIndex(k).Interface())
		}
		return m
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		i := rv.Int()
		if i > (1<<53-1) || i < -(1<<53-1) {
			return fmt.Sprintf("%v", i)
		}
		return i
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		u := rv.Uint()
		if u > (1<<53 - 1) {
			return fmt.Sprintf("%v", u)
		}
		return u
	case reflect.Float32, reflect.Float64:
		f := rv.Float()
		// Round to 6 decimal places for stable comparison
		return math.Round(f*1e6) / 1e6
	case reflect.Interface, reflect.Ptr:
		if rv.IsNil() {
			return nil
		}
		return jsonSerializable(rv.Elem().Interface())
	default:
		return obj
	}
}
