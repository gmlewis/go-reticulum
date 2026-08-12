// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import (
	"errors"
)

// b256Alphabet is the 256-rune base-256 alphabet used by RNS.b256rep.
// Each byte value 0x00..0xFF maps to exactly one rune in this string,
// matching Python RNS.b256 verbatim. Byte 0x17 is 'x' (not 'w'); the
// Latin range intentionally skips 'w'. The escapes below were generated
// programmatically from RNS.b256 and are authoritative.
const b256Alphabet = "abcdefghijklmnopqrstuvxyzæø01234ABCDEFGHIJKLMNOPQRSTUWXYZÆØ56789αβγδεζηθικλμνξπρστφχψωΓΔΘΛΞΠΣΦΨΩБДЖЗИЛПЦЧШЩЪЫЭЮЯбджзилпцчшщъыэюяԱԲԳԴԵԶԷԸԹԺԻԽԾԿՀՁՂՃՄՅՆՇՈՉՊՋՎՐՑՒՔՖᚠᚢᚦᚱᚹᚺᚾᛈᛇᛉᛊᛏᛒᛖᛗᛟｲｳｵｶｷｹｻｼｽｾﾀﾁﾃﾄﾅﾇﾈﾋﾌﾍﾎﾏﾐﾑﾒﾓﾔﾗﾘﾙﾚﾜ\U00010450\U00010451\U00010452\U00010454\U00010455\U00010457\U00010459\U00010473\U00010476\U00010478\U00010479\U0001047A\U0001047B\U0001047D\U0001047E\U0001047F᱑᱕᱘᱙ᱚᱝᱟᱣᱦᱨᱬᱭᱰᱳᱶᱷ\U00010333\U00010338\U0001033E\U00010400\U00010401\U00010402\U00010406\U00010407\U00010408\U00010409\U0001040A\U0001040B\U0001040C\U0001040D\U0001040E\U0001040F"

// b256DecodeMap maps each rune of b256Alphabet to its byte value. It is
// built once at package initialization. Range over the rune slice (not the
// string) so the index is the rune position, not the byte offset, which
// matters for the multi-byte runes in the alphabet.
var b256DecodeMap = func() map[rune]byte {
	m := make(map[rune]byte, 256)
	for i, r := range b256Runes {
		m[r] = byte(i)
	}
	return m
}()

// b256Runes is the pre-split rune slice of b256Alphabet for fast encoding.
var b256Runes = []rune(b256Alphabet)

// B256Rep encodes data into RNS base-256 representation, matching Python's
// RNS.b256rep: each byte maps to one rune from b256Alphabet.
func B256Rep(data []byte) string {
	if len(data) == 0 {
		return ""
	}
	out := make([]rune, len(data))
	for i, b := range data {
		out[i] = b256Runes[b]
	}
	return string(out)
}

// B256ToBytes decodes an RNS base-256 string back to bytes, matching
// Python's RNS.b256_to_bytes. An unknown rune produces an error.
func B256ToBytes(s string) ([]byte, error) {
	if s == "" {
		return []byte{}, nil
	}
	out := make([]byte, 0, len(s))
	for _, r := range s {
		b, ok := b256DecodeMap[r]
		if !ok {
			return nil, errors.New("could not decode base256: unknown rune")
		}
		out = append(out, b)
	}
	return out, nil
}
