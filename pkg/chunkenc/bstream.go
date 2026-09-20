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
// This walks one bit at a time rather than slicing whole bytes out of u. That
// is measurably slower than the word-at-a-time approach, and it is a deliberate
// choice: the encoder is not the bottleneck (ingest is dominated by the storage
// engine's write path, see BenchmarkXORChunk_Append), and a codec whose
// correctness cannot be read off the page is a liability in a system whose
// entire job is to be trusted about what happened. If profiling ever shows this
// mattering, the fast path is contained to this one function.
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
