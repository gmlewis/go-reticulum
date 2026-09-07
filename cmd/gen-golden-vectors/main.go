// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

// gen-golden-vectors generates deterministic cryptographic golden
// test vectors from the go-reticulum stack for hardware accelerator
// verification (such as the https://github.com/gmlewis/asic-reticulum SpinalHDL project).
//
// It executes the canonical Go Reticulum algorithms for:
//   - Anti-spam hashcash workblock derivation (lxmf.StampWorkblock) across
//     various material strings and expansion round configurations (e.g.
//     512-byte standard LXMF message blocks and 256-byte RNode peering keys).
//   - Incremental multi-block SHA-256 midstate calculation over the workblock
//     prefix, mirroring hardware midstate restore registers.
//   - Proof-of-work candidate exploration and difficulty validation
//     (lxmf.StampValue, lxmf.StampValid), ensuring exact nonce, leading zero
//     bit counts, round counts, and final 256-bit SHA-256 digests match.
//   - Standard FIPS 180-4 multi-block SHA-256 test vectors for validating raw
//     pipeline hashing.
//
// Output:
//
// By default or with `-format scala`, it formats the generated test cases as a
// ready-to-compile Scala object (e.g. `hw/sim/reticulum/parity/GoldenVectors.scala`).
// If `-output` is specified, it writes directly to the destination path;
// otherwise, it prints to stdout.
//
// Usage:
//
//	gen-golden-vectors [-output PATH] [-format FORMAT]
//
// Examples:
//
//	# Print golden vectors to stdout:
//	go run ./cmd/gen-golden-vectors
//
//	# Write directly to asic-reticulum simulation directory:
//	go run ./cmd/gen-golden-vectors -output ../asic-reticulum/hw/sim/reticulum/parity/GoldenVectors.scala
package main

import (
	"bytes"
	"crypto/ecdh"
	"encoding/hex"
	"flag"
	"fmt"
	"log"
	"math/bits"
	"os"
	"path/filepath"

	"github.com/gmlewis/go-reticulum/lxmf"
	"github.com/gmlewis/go-reticulum/rns"
)

type goldenCase struct {
	Name             string
	Material         string
	ExpandRounds     int
	WorkblockLen     int
	MidstateHex      string
	TotalLenBits     uint64
	BaseCandidateHex string
	StartNonce       uint64
	TargetCost       int
	ExpectedNonce    uint64
	ExpectedZeros    int
	ExpectedDigest   string
	ExpectedCandHex  string
	ExpectedRounds   uint64
}

type goldenX25519Case struct {
	Name              string
	ScalarHex         string
	UCoordHex         string
	ExpectedSharedHex string
}

func makeX25519Case(name, scalarHex, uCoordHex string) goldenX25519Case {
	scalarBytes, err := hex.DecodeString(scalarHex)
	if err != nil {
		log.Fatalf("invalid scalar hex %v: %v", scalarHex, err)
	}
	uBytes, err := hex.DecodeString(uCoordHex)
	if err != nil {
		log.Fatalf("invalid u hex %v: %v", uCoordHex, err)
	}
	priv, err := ecdh.X25519().NewPrivateKey(scalarBytes)
	if err != nil {
		log.Fatalf("failed to create X25519 private key for %v: %v", name, err)
	}
	pub, err := ecdh.X25519().NewPublicKey(uBytes)
	if err != nil {
		log.Fatalf("failed to create X25519 public key for %v: %v", name, err)
	}
	shared, err := priv.ECDH(pub)
	if err != nil {
		log.Fatalf("failed to compute ECDH for %v: %v", name, err)
	}
	return goldenX25519Case{
		Name:              name,
		ScalarHex:         scalarHex,
		UCoordHex:         uCoordHex,
		ExpectedSharedHex: hex.EncodeToString(shared),
	}
}

func leadingZeroBits(data []byte) int {
	count := 0
	for _, b := range data {
		if b == 0 {
			count += 8
			continue
		}
		count += bits.LeadingZeros8(uint8(b))
		break
	}
	return count
}

func compressBlock(hIn [8]uint32, block []byte) [8]uint32 {
	K := [64]uint32{
		0x428a2f98, 0x71374491, 0xb5c0fbcf, 0xe9b5dba5, 0x3956c25b, 0x59f111f1, 0x923f82a4, 0xab1c5ed5,
		0xd807aa98, 0x12835b01, 0x243185be, 0x550c7dc3, 0x72be5d74, 0x80deb1fe, 0x9bdc06a7, 0xc19bf174,
		0xe49b69c1, 0xefbe4786, 0x0fc19dc6, 0x240ca1cc, 0x2de92c6f, 0x4a7484aa, 0x5cb0a9dc, 0x76f988da,
		0x983e5152, 0xa831c66d, 0xb00327c8, 0xbf597fc7, 0xc6e00bf3, 0xd5a79147, 0x06ca6351, 0x14292967,
		0x27b70a85, 0x2e1b2138, 0x4d2c6dfc, 0x53380d13, 0x650a7354, 0x766a0abb, 0x81c2c92e, 0x92722c85,
		0xa2bfe8a1, 0xa81a664b, 0xc24b8b70, 0xc76c51a3, 0xd192e819, 0xd6990624, 0xf40e3585, 0x106aa070,
		0x19a4c116, 0x1e376c08, 0x2748774c, 0x34b0bcb5, 0x391c0cb3, 0x4ed8aa4a, 0x5b9cca4f, 0x682e6ff3,
		0x748f82ee, 0x78a5636f, 0x84c87814, 0x8cc70208, 0x90befffa, 0xa4506ceb, 0xbef9a3f7, 0xc67178f2,
	}
	rotr := func(x uint32, n int) uint32 { return (x >> n) | (x << (32 - n)) }
	s0 := func(x uint32) uint32 { return rotr(x, 7) ^ rotr(x, 18) ^ (x >> 3) }
	s1 := func(x uint32) uint32 { return rotr(x, 17) ^ rotr(x, 19) ^ (x >> 10) }
	sigma0 := func(x uint32) uint32 { return rotr(x, 2) ^ rotr(x, 13) ^ rotr(x, 22) }
	sigma1 := func(x uint32) uint32 { return rotr(x, 6) ^ rotr(x, 11) ^ rotr(x, 25) }
	ch := func(e, f, g uint32) uint32 { return (e & f) ^ (^e & g) }
	maj := func(a, b, c uint32) uint32 { return (a & b) ^ (a & c) ^ (b & c) }

	var W [64]uint32
	for i := range 16 {
		W[i] = uint32(block[i*4])<<24 | uint32(block[i*4+1])<<16 | uint32(block[i*4+2])<<8 | uint32(block[i*4+3])
	}
	for t := 16; t < 64; t++ {
		W[t] = s1(W[t-2]) + W[t-7] + s0(W[t-15]) + W[t-16]
	}

	a, b, c, d, e, f, g, h := hIn[0], hIn[1], hIn[2], hIn[3], hIn[4], hIn[5], hIn[6], hIn[7]
	for t := range 64 {
		t1 := h + sigma1(e) + ch(e, f, g) + K[t] + W[t]
		t2 := sigma0(a) + maj(a, b, c)
		h = g
		g = f
		f = e
		e = d + t1
		d = c
		c = b
		b = a
		a = t1 + t2
	}
	return [8]uint32{hIn[0] + a, hIn[1] + b, hIn[2] + c, hIn[3] + d, hIn[4] + e, hIn[5] + f, hIn[6] + g, hIn[7] + h}
}

func computeMidstate(workblock []byte) []byte {
	H0 := [8]uint32{0x6a09e667, 0xbb67ae85, 0x3c6ef372, 0xa54ff53a, 0x510e527f, 0x9b05688c, 0x1f83d9ab, 0x5be0cd19}
	state := H0
	numBlocks := len(workblock) / 64
	for i := range numBlocks {
		state = compressBlock(state, workblock[i*64:(i+1)*64])
	}
	midstateBytes := make([]byte, 32)
	for i := range 8 {
		midstateBytes[i*4] = byte(state[i] >> 24)
		midstateBytes[i*4+1] = byte(state[i] >> 16)
		midstateBytes[i*4+2] = byte(state[i] >> 8)
		midstateBytes[i*4+3] = byte(state[i])
	}
	return midstateBytes
}

func addNonce(base []byte, nonce uint64) []byte {
	res := make([]byte, len(base))
	copy(res, base)
	carry := nonce
	for i := range 8 {
		sum := uint64(res[i]) + (carry & 0xFF)
		res[i] = byte(sum)
		carry = (carry >> 8) + (sum >> 8)
	}
	return res
}

func searchGolden(name, material string, expandRounds int, baseCand []byte, startNonce uint64, target int) goldenCase {
	wb, err := lxmf.StampWorkblock([]byte(material), expandRounds)
	if err != nil {
		log.Fatalf("failed to build workblock for %v: %v", material, err)
	}
	midBytes := computeMidstate(wb)
	totalLenBits := uint64(len(wb)+32) * 8

	for nonce := startNonce; ; nonce++ {
		cand := addNonce(baseCand, nonce)
		h := rns.FullHash(append(wb, cand...))
		zeros := leadingZeroBits(h)
		if zeros >= target {
			if !lxmf.StampValid(cand, target, wb) {
				log.Fatalf("stamp failed StampValid for %v, target %v", name, target)
			}
			rounds := (nonce - startNonce) + 1
			return goldenCase{
				Name:             name,
				Material:         material,
				ExpandRounds:     expandRounds,
				WorkblockLen:     len(wb),
				MidstateHex:      hex.EncodeToString(midBytes),
				TotalLenBits:     totalLenBits,
				BaseCandidateHex: hex.EncodeToString(baseCand),
				StartNonce:       startNonce,
				TargetCost:       target,
				ExpectedNonce:    nonce,
				ExpectedZeros:    zeros,
				ExpectedDigest:   hex.EncodeToString(h),
				ExpectedCandHex:  hex.EncodeToString(cand),
				ExpectedRounds:   rounds,
			}
		}
	}
}

func main() {
	log.SetFlags(0)

	outputPath := flag.String("output", "", "Path to write generated GoldenVectors.scala (default: stdout)")
	format := flag.String("format", "scala", "Output format (scala)")
	flag.Parse()

	if *format != "scala" {
		log.Fatalf("unsupported format %q, currently only 'scala' is supported", *format)
	}

	baseCand := make([]byte, 32)
	for i := range baseCand {
		baseCand[i] = byte(i + 1)
	}

	cases := []goldenCase{
		// Standard LXMF message (512B workblock)
		searchGolden("LXMF Message - Target 1", "lxmf-msg-id-8a3b4c5d6e7f0123", 2, baseCand, 0, 1),
		searchGolden("LXMF Message - Target 2", "lxmf-msg-id-8a3b4c5d6e7f0123", 2, baseCand, 0, 2),
		searchGolden("LXMF Message - Target 3", "lxmf-msg-id-8a3b4c5d6e7f0123", 2, baseCand, 0, 3),
		searchGolden("LXMF Message - Target 4", "lxmf-msg-id-8a3b4c5d6e7f0123", 2, baseCand, 0, 4),
		searchGolden("LXMF Message - Target 6", "lxmf-msg-id-8a3b4c5d6e7f0123", 2, baseCand, 0, 6),
		searchGolden("LXMF Message - Target 8", "lxmf-msg-id-8a3b4c5d6e7f0123", 2, baseCand, 0, 8),

		// Peering key (256B workblock)
		searchGolden("RNode Peering - Target 1", "rnode-peering-session-alpha", 1, baseCand, 0, 1),
		searchGolden("RNode Peering - Target 2", "rnode-peering-session-alpha", 1, baseCand, 0, 2),
		searchGolden("RNode Peering - Target 4", "rnode-peering-session-alpha", 1, baseCand, 0, 4),
		searchGolden("RNode Peering - Target 7", "rnode-peering-session-alpha", 1, baseCand, 0, 7),

		// Non-zero start nonce (offset search / multi-core partition)
		searchGolden("Offset Search - Target 5", "lxmf-msg-id-8a3b4c5d6e7f0123", 2, baseCand, 50, 5),
	}

	x25519Cases := []goldenX25519Case{
		makeX25519Case(
			"RFC 7748 Vector 1",
			"a546e36bf0527c9d3b16154b82465edd62144c0ac1fc5a18506a2244ba449ac4",
			"e6db6867583030db3594c1a424b15f7c726624ec26b3353b10a903a6d0ab1c4c",
		),
		makeX25519Case(
			"RFC 7748 Vector 2",
			"4b66e9d4d1b4673c5ad22691957d6af5c11b6421e0ea01d42ca4169e7918ba0d",
			"e5210f12786811d3f4b7959d0538ae2c31dbe7106fc03c3efc4cd549c715a493",
		),
		makeX25519Case(
			"Base Point u=9 with Scalar 1",
			"0100000000000000000000000000000000000000000000000000000000000000",
			"0900000000000000000000000000000000000000000000000000000000000000",
		),
		makeX25519Case(
			"Reticulum Handshake Key Exchange A",
			"c8079d38767314f11b2a40701026702636e29618201d4fb3a6049c692a947fa5",
			"504602762c4b84965378ac4790be450123963286f14dd16b270733a4e3b0d595",
		),
		makeX25519Case(
			"Reticulum Handshake Key Exchange B",
			"4a5d9d5ba4ce2de1728e3bf480350f25e07e21c947d19e3376f09b3c1e161742",
			"438a6800f489f1a7d0394b507c7510a976ce5439f481ac60bdab3429b59e564e",
		),
	}

	var buf bytes.Buffer
	buf.WriteString("package reticulum.parity\n\n")
	buf.WriteString("/**\n")
	buf.WriteString(" * Golden test vector definitions generated directly from go-reticulum/lxmf.\n")
	buf.WriteString(" * Generated by cmd/gen-golden-vectors in go-reticulum - DO NOT EDIT MANUALLY.\n")
	buf.WriteString(" */\n")
	buf.WriteString("case class GoldenCase(\n")
	buf.WriteString("  name: String,\n")
	buf.WriteString("  material: String,\n")
	buf.WriteString("  expandRounds: Int,\n")
	buf.WriteString("  workblockLen: Int,\n")
	buf.WriteString("  midstateHex: String,\n")
	buf.WriteString("  totalLenBits: Long,\n")
	buf.WriteString("  baseCandidateHex: String,\n")
	buf.WriteString("  startNonce: Long,\n")
	buf.WriteString("  targetCost: Int,\n")
	buf.WriteString("  expectedNonce: Long,\n")
	buf.WriteString("  expectedZeros: Int,\n")
	buf.WriteString("  expectedDigestHex: String,\n")
	buf.WriteString("  expectedCandidateHex: String,\n")
	buf.WriteString("  expectedRounds: Long\n")
	buf.WriteString(")\n\n")

	buf.WriteString("case class GoldenSha256Case(\n")
	buf.WriteString("  name: String,\n")
	buf.WriteString("  messageHex: String,\n")
	buf.WriteString("  expectedDigestHex: String\n")
	buf.WriteString(")\n\n")

	buf.WriteString("case class GoldenX25519Case(\n")
	buf.WriteString("  name: String,\n")
	buf.WriteString("  scalarHex: String,\n")
	buf.WriteString("  uCoordHex: String,\n")
	buf.WriteString("  expectedSharedHex: String\n")
	buf.WriteString(")\n\n")

	buf.WriteString("object GoldenVectors {\n")
	buf.WriteString("  val cases: Seq[GoldenCase] = Seq(\n")
	for _, c := range cases {
		buf.WriteString("    GoldenCase(\n")
		buf.WriteString(fmt.Sprintf("      name = %q,\n", c.Name))
		buf.WriteString(fmt.Sprintf("      material = %q,\n", c.Material))
		buf.WriteString(fmt.Sprintf("      expandRounds = %d,\n", c.ExpandRounds))
		buf.WriteString(fmt.Sprintf("      workblockLen = %d,\n", c.WorkblockLen))
		buf.WriteString(fmt.Sprintf("      midstateHex = %q,\n", c.MidstateHex))
		buf.WriteString(fmt.Sprintf("      totalLenBits = %dL,\n", c.TotalLenBits))
		buf.WriteString(fmt.Sprintf("      baseCandidateHex = %q,\n", c.BaseCandidateHex))
		buf.WriteString(fmt.Sprintf("      startNonce = %dL,\n", c.StartNonce))
		buf.WriteString(fmt.Sprintf("      targetCost = %d,\n", c.TargetCost))
		buf.WriteString(fmt.Sprintf("      expectedNonce = %dL,\n", c.ExpectedNonce))
		buf.WriteString(fmt.Sprintf("      expectedZeros = %d,\n", c.ExpectedZeros))
		buf.WriteString(fmt.Sprintf("      expectedDigestHex = %q,\n", c.ExpectedDigest))
		buf.WriteString(fmt.Sprintf("      expectedCandidateHex = %q,\n", c.ExpectedCandHex))
		buf.WriteString(fmt.Sprintf("      expectedRounds = %dL\n", c.ExpectedRounds))
		buf.WriteString("    ),\n")
	}
	buf.WriteString("  )\n\n")

	buf.WriteString("  val sha256Cases: Seq[GoldenSha256Case] = Seq(\n")
	buf.WriteString("    GoldenSha256Case(\n")
	buf.WriteString("      name = \"FIPS 180-4: Single Block 'abc'\",\n")
	buf.WriteString("      messageHex = \"616263\",\n")
	buf.WriteString("      expectedDigestHex = \"ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad\"\n")
	buf.WriteString("    ),\n")
	buf.WriteString("    GoldenSha256Case(\n")
	buf.WriteString("      name = \"FIPS 180-4: 2-Block 'abcdbcdecdefdefgefghfghighijhijkijkljklmklmnlmnomnopnopq'\",\n")
	buf.WriteString("      messageHex = \"6162636462636465636465666465666765666768666768696768696a68696a6b696a6b6c6a6b6c6d6b6c6d6e6c6d6e6f6d6e6f706e6f7071\",\n")
	buf.WriteString("      expectedDigestHex = \"248d6a61d20638b8e5c026930c3e6039a33ce45964ff2167f6ecedd419db06c1\"\n")
	buf.WriteString("    ),\n")
	buf.WriteString("    GoldenSha256Case(\n")
	buf.WriteString("      name = \"FIPS 180-4: Empty String\",\n")
	buf.WriteString("      messageHex = \"\",\n")
	buf.WriteString("      expectedDigestHex = \"e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855\"\n")
	buf.WriteString("    )\n")
	buf.WriteString("  )\n\n")

	buf.WriteString("  val x25519Cases: Seq[GoldenX25519Case] = Seq(\n")
	for _, c := range x25519Cases {
		buf.WriteString("    GoldenX25519Case(\n")
		buf.WriteString(fmt.Sprintf("      name = %q,\n", c.Name))
		buf.WriteString(fmt.Sprintf("      scalarHex = %q,\n", c.ScalarHex))
		buf.WriteString(fmt.Sprintf("      uCoordHex = %q,\n", c.UCoordHex))
		buf.WriteString(fmt.Sprintf("      expectedSharedHex = %q\n", c.ExpectedSharedHex))
		buf.WriteString("    ),\n")
	}
	buf.WriteString("  )\n")
	buf.WriteString("}\n")

	if *outputPath == "" {
		fmt.Print(buf.String())
		return
	}

	if err := os.MkdirAll(filepath.Dir(*outputPath), 0o755); err != nil {
		log.Fatalf("failed to create directory for %v: %v", *outputPath, err)
	}
	if err := os.WriteFile(*outputPath, buf.Bytes(), 0o644); err != nil {
		log.Fatalf("failed to write %v: %v", *outputPath, err)
	}
	log.Printf("Successfully wrote %d golden cases to %v", len(cases), *outputPath)
}
