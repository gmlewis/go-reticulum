// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

// This file holds the embedded wasm test fixtures for the LXMF filter
// plugin-host tests. The modules are minimal hand-assembled bytecode so the
// tests never need an external wasm compiler; the wago compiler validates the
// section sizes the first time each fixture is compiled.

package main

// MinWasmFilterAccept exports wagoplugin_alloc and filter_inbound over one
// memory page: alloc returns the fixed offset 1024 and filter_inbound always
// returns 1 (accept).
var MinWasmFilterAccept = []byte{
	0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00,
	// Type section: 0: (i32) -> (i32), 1: (i32, i32) -> (i32)
	0x01, 0x0c, 0x02,
	0x60, 0x01, 0x7f, 0x01, 0x7f,
	0x60, 0x02, 0x7f, 0x7f, 0x01, 0x7f,
	// Function section: 0: alloc, 1: filter_inbound
	0x03, 0x03, 0x02, 0x00, 0x01,
	// Memory section: 1 page, max 1 page
	0x05, 0x04, 0x01, 0x01, 0x01, 0x01,
	// Export section (size 0x2e = 1 + 9 + 19 + 17)
	0x07, 0x2e, 0x03,
	0x06, 'm', 'e', 'm', 'o', 'r', 'y', 0x02, 0x00,
	0x10, 'w', 'a', 'g', 'o', 'p', 'l', 'u', 'g', 'i', 'n', '_', 'a', 'l', 'l', 'o', 'c', 0x00, 0x00,
	0x0e, 'f', 'i', 'l', 't', 'e', 'r', '_', 'i', 'n', 'b', 'o', 'u', 'n', 'd', 0x00, 0x01,
	// Code section
	0x0a, 0x0c, 0x02,
	// func 0 (alloc): returns the fixed offset 1024 (body: 00 41 80 08 0b)
	0x05, 0x00, 0x41, 0x80, 0x08, 0x0b,
	// func 1 (filter_inbound): returns 1 (accept) (body: 00 41 01 0b)
	0x04, 0x00, 0x41, 0x01, 0x0b,
}

// MinWasmFilterDrop is identical to MinWasmFilterAccept except
// filter_inbound always returns 0 (drop).
var MinWasmFilterDrop = []byte{
	0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00,
	// Type section: 0: (i32) -> (i32), 1: (i32, i32) -> (i32)
	0x01, 0x0c, 0x02,
	0x60, 0x01, 0x7f, 0x01, 0x7f,
	0x60, 0x02, 0x7f, 0x7f, 0x01, 0x7f,
	// Function section: 0: alloc, 1: filter_inbound
	0x03, 0x03, 0x02, 0x00, 0x01,
	// Memory section: 1 page, max 1 page
	0x05, 0x04, 0x01, 0x01, 0x01, 0x01,
	// Export section (size 0x2e = 1 + 9 + 19 + 17)
	0x07, 0x2e, 0x03,
	0x06, 'm', 'e', 'm', 'o', 'r', 'y', 0x02, 0x00,
	0x10, 'w', 'a', 'g', 'o', 'p', 'l', 'u', 'g', 'i', 'n', '_', 'a', 'l', 'l', 'o', 'c', 0x00, 0x00,
	0x0e, 'f', 'i', 'l', 't', 'e', 'r', '_', 'i', 'n', 'b', 'o', 'u', 'n', 'd', 0x00, 0x01,
	// Code section
	0x0a, 0x0c, 0x02,
	// func 0 (alloc): returns the fixed offset 1024 (body: 00 41 80 08 0b)
	0x05, 0x00, 0x41, 0x80, 0x08, 0x0b,
	// func 1 (filter_inbound): returns 0 (drop) (body: 00 41 00 0b)
	0x04, 0x00, 0x41, 0x00, 0x0b,
}

// MinWasmSpin is the infinite-loop module used for timeout and cancellation
// tests:
//
//	(module (func (export "spin") loop br 0 end)).
var MinWasmSpin = []byte{
	0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00,
	0x01, 0x04, 0x01, 0x60, 0x00, 0x00,
	0x03, 0x02, 0x01, 0x00,
	0x07, 0x08, 0x01, 0x04, 's', 'p', 'i', 'n', 0x00, 0x00,
	// Code section: body size 7 (locals 00, loop 03 40, br 0c 00, end 0b, end 0b), section size 9.
	0x0a, 0x09, 0x01, 0x07, 0x00, 0x03, 0x40, 0x0c, 0x00, 0x0b, 0x0b,
}
