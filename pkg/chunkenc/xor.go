package chunkenc

import (
	"encoding/binary"
	"errors"
	"math"
	"math/bits"
)

// ErrChunkFull is returned by Appender.Append when the chunk has reached
// maxSamplesPerChunk and must be cut.
var ErrChunkFull = errors.New("chunkenc: chunk is full")

// headerLen is the size of the sample-count prefix that precedes the bit stream.
const headerLen = 2

// MaxSamplesPerChunk bounds how many samples a single chunk holds.
//
// 120 is the same bound Prometheus uses, and the reasoning is the same: a chunk
// is the unit of read amplification, so a point query for one sample still has
// to decode the whole chunk. At a 15s scrape interval 120 samples is 30 minutes
// of data, which keeps decode cost small while still amortising the 16-byte
// chunk header and the per-series label set across enough points to matter.
const MaxSamplesPerChunk = 120

// XORChunk stores a sequence of (timestamp, float64) samples using
// delta-of-delta timestamp encoding and XOR value encoding, as described in
// Facebook's Gorilla paper.
//
// The layout is a 2-byte big-endian sample count followed by the bit stream.
type XORChunk struct {
	b bstream
	// readOnly marks chunks rehydrated from bytes. The count of unused bits in
	// the final byte is not part of the encoded form, so a decoded chunk cannot
	// be appended to without corrupting the last sample. This matches how a
	// TSDB behaves in practice: once a chunk is flushed it is immutable, and
	// new samples open a new chunk.
	readOnly bool
}

// NewXORChunk returns an empty chunk ready to append to.
func NewXORChunk() *XORChunk {
	c := &XORChunk{}
	c.b.stream = make([]byte, headerLen, 64)
	c.b.free = 0
	return c
}

// FromBytes wraps an already-encoded chunk for reading. The slice is not copied.
func FromBytes(b []byte) (*XORChunk, error) {
	if len(b) < headerLen {
		return nil, errors.New("chunkenc: buffer too short for chunk header")
	}
	return &XORChunk{b: bstream{stream: b}, readOnly: true}, nil
}

// ErrReadOnly is returned when appending to a chunk obtained from FromBytes.
var ErrReadOnly = errors.New("chunkenc: chunk decoded from bytes is read-only")

// Bytes returns the encoded chunk. The slice aliases the chunk's buffer and is
// only valid until the next Append.
func (c *XORChunk) Bytes() []byte { return c.b.bytes() }

// NumSamples returns how many samples the chunk holds.
func (c *XORChunk) NumSamples() int {
	return int(binary.BigEndian.Uint16(c.b.bytes()))
}

func (c *XORChunk) setNumSamples(n int) {
	binary.BigEndian.PutUint16(c.b.stream, uint16(n))
}

// Appender writes samples into a chunk. Samples must be appended in
// non-decreasing timestamp order.
type Appender struct {
	c *XORChunk

	t      int64
	v      float64
	tDelta int64

	// leading and trailing track the window of meaningful bits used by the
	// previous value, so a run of similar values can skip re-sending it.
	// leading is set to notSet until the first non-zero XOR is written.
	leading  uint8
	trailing uint8
}

const notSet = uint8(255)

// Appender returns an appender positioned at the end of the chunk.
//
// Re-deriving the appender state requires replaying the chunk, because the
// trailing/leading window is not recoverable from the raw bytes alone. Callers
// that append continuously should hold on to the returned Appender.
func (c *XORChunk) Appender() (*Appender, error) {
	if c.readOnly {
		return nil, ErrReadOnly
	}
	it := c.Iterator()
	for it.Next() {
	}
	if err := it.Err(); err != nil {
		return nil, err
	}

	a := &Appender{
		c:        c,
		t:        it.t,
		v:        it.v,
		tDelta:   it.tDelta,
		leading:  it.leading,
		trailing: it.trailing,
	}
	if c.NumSamples() == 0 {
		a.leading = notSet
	}
	return a, nil
}

// Append adds a sample to the chunk.
func (a *Appender) Append(t int64, v float64) error {
	num := a.c.NumSamples()
	if num >= MaxSamplesPerChunk {
		return ErrChunkFull
	}

	switch num {
	case 0:
		// The first sample is stored verbatim. There is nothing to delta
		// against, and paying 128 bits once per chunk is amortised across the
		// following samples.
		a.c.b.writeBits(uint64(t), 64)
		a.c.b.writeBits(math.Float64bits(v), 64)

	default:
		tDelta := t - a.t
		// For the second sample the previous delta is zero, so the
		// delta-of-delta is just the delta. Reusing the same bucket encoder
		// here keeps one code path instead of two.
		dod := tDelta - a.tDelta
		a.writeDOD(dod)
		a.writeVDelta(v)
		a.tDelta = tDelta
	}

	a.t = t
	a.v = v
	a.c.setNumSamples(num + 1)
	return nil
}

// writeDOD encodes a delta-of-delta using variable-width buckets.
//
// The bucket boundaries assume millisecond-resolution timestamps, which is what
// TinyObs stores (see docs/adr/0002-millisecond-timestamps.md). For a scrape
// running on a fixed interval the delta-of-delta is exactly zero, so the common
// case costs a single bit. The wider buckets absorb ordinary scheduler jitter;
// only a genuine interval change falls through to the 64-bit escape.
func (a *Appender) writeDOD(dod int64) {
	switch {
	case dod == 0:
		a.c.b.writeBit(false)
	case bitRange(dod, 14):
		a.c.b.writeBits(0b10, 2)
		a.c.b.writeBits(uint64(dod), 14)
	case bitRange(dod, 17):
		a.c.b.writeBits(0b110, 3)
		a.c.b.writeBits(uint64(dod), 17)
	case bitRange(dod, 20):
		a.c.b.writeBits(0b1110, 4)
		a.c.b.writeBits(uint64(dod), 20)
	default:
		a.c.b.writeBits(0b1111, 4)
		a.c.b.writeBits(uint64(dod), 64)
	}
}

// writeVDelta encodes a float64 by XOR-ing it against the previous value.
//
// Consecutive samples of a slow-moving gauge share most of their exponent and
// high mantissa bits, so the XOR is mostly zeros and only the middle window
// needs sending. An unchanged value costs one bit.
func (a *Appender) writeVDelta(v float64) {
	vDelta := math.Float64bits(v) ^ math.Float64bits(a.v)

	if vDelta == 0 {
		a.c.b.writeBit(false)
		return
	}
	a.c.b.writeBit(true)

	leading := uint8(bits.LeadingZeros64(vDelta))
	trailing := uint8(bits.TrailingZeros64(vDelta))

	// leading is sent in 5 bits, so it cannot exceed 31. Clamping costs a few
	// redundant bits on values with very long runs of leading zeros and keeps
	// the header fixed-width.
	if leading >= 32 {
		leading = 31
	}

	if a.leading != notSet && leading >= a.leading && trailing >= a.trailing {
		// The meaningful bits still fit inside the previous window, so reuse it.
		a.c.b.writeBit(false)
		a.c.b.writeBits(vDelta>>a.trailing, 64-a.leading-a.trailing)
		return
	}

	a.leading, a.trailing = leading, trailing
	a.c.b.writeBit(true)
	a.c.b.writeBits(uint64(leading), 5)

	// sigbits is in 1..64, but 64 does not fit in 6 bits. It is written as 0
	// and mapped back on read: zero significant bits is unreachable here
	// because vDelta == 0 was handled above.
	sigbits := 64 - leading - trailing
	a.c.b.writeBits(uint64(sigbits), 6)
	a.c.b.writeBits(vDelta>>trailing, sigbits)
}

// bitRange reports whether x fits in a signed field of nbits.
func bitRange(x int64, nbits uint8) bool {
	return -((1<<(nbits-1))-1) <= x && x <= 1<<(nbits-1)
}

// Iterator walks the samples in a chunk.
type Iterator struct {
	br  *bstreamReader
	num int
	i   int

	t      int64
	v      float64
	tDelta int64

	leading  uint8
	trailing uint8

	err error
}

// Iterator returns a fresh iterator over the chunk.
func (c *XORChunk) Iterator() *Iterator {
	b := c.b.bytes()
	return &Iterator{
		br:      newBReader(b[headerLen:]),
		num:     int(binary.BigEndian.Uint16(b)),
		leading: notSet,
	}
}

// At returns the sample the iterator is positioned on.
func (it *Iterator) At() (int64, float64) { return it.t, it.v }

// Err returns the first error encountered while iterating.
func (it *Iterator) Err() error { return it.err }

// Next advances the iterator, reporting whether a sample was read.
func (it *Iterator) Next() bool {
	if it.err != nil || it.i >= it.num {
		return false
	}

	if it.i == 0 {
		t, err := it.br.readBits(64)
		if err != nil {
			it.err = err
			return false
		}
		v, err := it.br.readBits(64)
		if err != nil {
			it.err = err
			return false
		}
		it.t = int64(t)
		it.v = math.Float64frombits(v)
		it.i++
		return true
	}

	if !it.readDOD() {
		return false
	}
	if !it.readVDelta() {
		return false
	}
	it.i++
	return true
}

func (it *Iterator) readDOD() bool {
	// The bucket tag is unary: count leading ones, up to four.
	var tag uint8
	for tag < 4 {
		bit, err := it.br.readBit()
		if err != nil {
			it.err = err
			return false
		}
		if !bit {
			break
		}
		tag++
	}

	var width uint8
	switch tag {
	case 0:
		// dod == 0, delta carries over unchanged.
		it.t += it.tDelta
		return true
	case 1:
		width = 14
	case 2:
		width = 17
	case 3:
		width = 20
	default:
		width = 64
	}

	raw, err := it.br.readBits(width)
	if err != nil {
		it.err = err
		return false
	}

	dod := int64(raw)
	// Sign-extend everything narrower than a full word.
	if width < 64 && raw > (1<<(width-1)) {
		dod = int64(raw) - (1 << width)
	}

	it.tDelta += dod
	it.t += it.tDelta
	return true
}

func (it *Iterator) readVDelta() bool {
	changed, err := it.br.readBit()
	if err != nil {
		it.err = err
		return false
	}
	if !changed {
		return true
	}

	newWindow, err := it.br.readBit()
	if err != nil {
		it.err = err
		return false
	}

	if newWindow {
		leading, err := it.br.readBits(5)
		if err != nil {
			it.err = err
			return false
		}
		sigbits, err := it.br.readBits(6)
		if err != nil {
			it.err = err
			return false
		}
		if sigbits == 0 {
			sigbits = 64
		}
		it.leading = uint8(leading)
		it.trailing = 64 - it.leading - uint8(sigbits)
	}

	mbits := 64 - it.leading - it.trailing
	raw, err := it.br.readBits(mbits)
	if err != nil {
		it.err = err
		return false
	}

	vbits := math.Float64bits(it.v)
	vbits ^= raw << it.trailing
	it.v = math.Float64frombits(vbits)
	return true
}
