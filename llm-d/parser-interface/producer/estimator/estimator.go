package estimator

import (
	"crypto/sha256"
	"encoding/binary"

	"prototype/kv"
	"prototype/parser"
)

var PrefixHashKey = kv.NewKey[uint64]("prefix_hash")

// Estimate writes a stable per-request prefix hash into the K/V store. When
// the vendor implements parser.PromptStructured, blocks are hashed with kind
// discrimination (text vs image ref vs ...); otherwise Prompt() is hashed
// as one undifferentiated chunk. The estimator stays vendor-neutral.
func Estimate(in parser.InferenceInput) {
	h := sha256.New()
	var k [4]byte
	if ps, ok := in.(parser.PromptStructured); ok {
		for _, b := range ps.PromptBlocks() {
			binary.LittleEndian.PutUint32(k[:], uint32(b.Kind))
			h.Write(k[:])
			h.Write([]byte(b.Text))
			h.Write(b.Ref)
		}
	} else {
		h.Write(in.Prompt())
	}
	sum := h.Sum(nil)
	kv.Update(in, PrefixHashKey, binary.LittleEndian.Uint64(sum[:8]))
}
