//go:build integration
// +build integration

package msgpack

import (
	"encoding/json"
	"fmt"
	"log"
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
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	scriptPath := filepath.Join(tmpDir, "check_msgpack_parity.py")
	if err := os.WriteFile(scriptPath, []byte(checkMsgpackParityPy), 0644); err != nil {
		t.Fatal(err)
	}

	// Complex nested structure
	data := map[any]any{
		"a": int64(1),
		"b": []any{
			2.5,
			"c",
			map[any]any{
				"d": []byte{0xde, 0xad, 0xbe, 0xef},
			},
		},
	}

	packed, err := Pack(data)
	if err != nil {
		t.Fatal(err)
	}

	packedPath := filepath.Join(tmpDir, "packed.msgpack")
	if err := os.WriteFile(packedPath, packed, 0644); err != nil {
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

	if string(goBytes) != string(pyBytes) {
		t.Errorf("MessagePack structure mismatch!\nGo JSON: %v\nPy JSON: %v", string(goBytes), string(pyBytes))
	}
}

func TestMessagePackPythonToGoParity(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "go-reticulum-msgpack-parity-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	scriptPath := filepath.Join(tmpDir, "generate_msgpack_parity.py")
	if err := os.WriteFile(scriptPath, []byte(generateMsgpackParityPy), 0644); err != nil {
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
	if err != nil {
		t.Fatal(err)
	}

	unpacked, err := Unpack(packed)
	if err != nil {
		t.Fatalf("Go Unpack failed: %v", err)
	}

	// Verify structure
	// Python data: {"x": 42, "y": [1.1, 2.2, {"z": b"bytes data"}], "null": None, "bool": True}
	expected := map[string]any{
		"x":    int64(42),
		"y":    []any{1.1, 2.2, map[string]any{"z": map[string]any{"_type": "bytes", "val": "62797465732064617461"}}},
		"null": nil,
		"bool": true,
	}

	goJSON := jsonSerializable(unpacked)

	goBytes, _ := json.Marshal(goJSON)
	expectedBytes, _ := json.Marshal(expected)

	if string(goBytes) != string(expectedBytes) {
		t.Errorf("MessagePack structure mismatch!\nGo JSON: %v\nExpected JSON: %v", string(goBytes), string(expectedBytes))
	}
}

func jsonSerializable(obj any) any {
	rv := reflect.ValueOf(obj)
	switch rv.Kind() {
	case reflect.Slice:
		if rv.Type().Elem().Kind() == reflect.Uint8 {
			return map[string]any{"_type": "bytes", "val": fmt.Sprintf("%x", rv.Bytes())}
		}
		s := make([]any, rv.Len())
		for i := 0; i < rv.Len(); i++ {
			s[i] = jsonSerializable(rv.Index(i).Interface())
		}
		return s
	case reflect.Map:
		m := make(map[string]any)
		for _, k := range rv.MapKeys() {
			m[fmt.Sprintf("%v", k.Interface())] = jsonSerializable(rv.MapIndex(k).Interface())
		}
		return m
	default:
		return obj
	}
}
