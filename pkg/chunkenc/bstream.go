package chunkenc

import "io"

// bstream is an append-only bit stream.
//
// Bits are packed most-significant-first within each byte, which keeps the
// encoded form stable regardless of host endianness and makes the hex dumps in
// the tests readable left to right.
type bstream struct {
	stream []byte
	// free counts the unused low-order bits in the final byte of stream.
	// It is 0 when the stream is empty or the final byte is full.
	free uint8
}

func (b *bstream) bytes() []byte { return b.stream }

// len returns the number of bits written so far.
func (b *bstream) len() int { return len(b.stream)*8 - int(b.free) }

func (b *bstream) writeBit(bit bool) {
	if b.free == 0 {
		b.stream = append(b.stream, 0)
		b.free = 8
	}
	if bit {
		b.stream[len(b.stream)-1] |= 1 << (b.free - 1)
	}
	b.free--
}

// writeBits writes the low nbits of u, most significant bit first.
//
// One bit at a time rather than slicing bytes out of u: slower than the
// word-at-a-time form, but easier to check against the spec. Not a bottleneck
// (BenchmarkXORChunk_Append), and a fast path would live only in this function.
func (b *bstream) writeBits(u uint64, nbits uint8) {
	for nbits > 0 {
		nbits--
		b.writeBit((u>>nbits)&1 == 1)
	}
}

// bstreamReader reads back a bstream written by the functions above.
type bstreamReader struct {
	stream []byte
	// pos is the index of the byte currently being read.
	pos int
	// consumed counts bits already read out of stream[pos].
	consumed uint8
}

func newBReader(b []byte) *bstreamReader {
	return &bstreamReader{stream: b}
}

func (b *bstreamReader) readBit() (bool, error) {
	if b.pos >= len(b.stream) {
		return false, io.EOF
	}
	bit := (b.stream[b.pos] >> (7 - b.consumed)) & 1
	b.consumed++
	if b.consumed == 8 {
		b.consumed = 0
		b.pos++
	}
	return bit == 1, nil
}

func (b *bstreamReader) readBits(nbits uint8) (uint64, error) {
	var u uint64
	for i := uint8(0); i < nbits; i++ {
		bit, err := b.readBit()
		if err != nil {
			return 0, err
		}
		u <<= 1
		if bit {
			u |= 1
		}
	}
	return u, nil
}
