// Package chunk implements the compressed sample encoding described in
// "Gorilla: A Fast, Scalable, In-Memory Time Series Database" (Pelkonen et
// al., VLDB 2015). Timestamps are stored as delta-of-deltas and values as the
// XOR against the previous value, so regular scrapes of slowly changing values
// cost one to two bytes per sample instead of sixteen.
//
// Layout: a 2-byte big-endian sample count followed by the bit stream.
//
//	sample 0: timestamp (varint), value (64 raw bits)
//	sample 1: timestamp delta (uvarint), value (XOR encoded)
//	sample n: timestamp delta-of-delta (prefix coded), value (XOR encoded)
package chunk

import (
	"encoding/binary"
	"errors"
	"io"
	"math"
	"math/bits"
)

// Chunk is a compressed run of (timestamp, value) samples. Timestamps are
// milliseconds and must be strictly increasing.
type Chunk struct {
	s bstream
}

// New returns an empty chunk.
func New() *Chunk {
	c := &Chunk{}
	c.s.b = make([]byte, 2, 128)
	return c
}

// FromBytes wraps previously encoded bytes. The slice is not copied.
func FromBytes(b []byte) (*Chunk, error) {
	if len(b) < 2 {
		return nil, errors.New("chunk: too short")
	}
	return &Chunk{s: bstream{b: b}}, nil
}

// Bytes returns the encoded chunk. The count field is kept up to date, so the
// result is always decodable.
func (c *Chunk) Bytes() []byte { return c.s.b }

// NumSamples returns the number of samples in the chunk.
func (c *Chunk) NumSamples() int { return int(binary.BigEndian.Uint16(c.s.b)) }

// Appender returns an appender that continues the chunk. It replays the
// existing samples to recover the encoder state.
func (c *Chunk) Appender() (*Appender, error) {
	it := c.Iterator()
	for it.Next() {
	}
	if it.Err() != nil {
		return nil, it.Err()
	}
	a := &Appender{c: c, t: it.t, v: it.v, tDelta: it.tDelta, leading: it.leading, trailing: it.trailing}
	// The iterator counts bits exactly; trim the writer to the same position
	// so we append right after the last encoded bit.
	used := it.br.pos - 16
	c.s.b = c.s.b[:2+(used+7)/8]
	c.s.count = uint8((8 - used%8) % 8)
	return a, nil
}

// Iterator returns an iterator over the chunk's samples.
func (c *Chunk) Iterator() *Iterator {
	return &Iterator{br: bitReader{b: c.s.b, pos: 16}, numTotal: uint16(c.NumSamples()), leading: 0xff}
}

// Appender adds samples to a chunk.
type Appender struct {
	c *Chunk

	t      int64
	v      float64
	tDelta uint64

	leading  uint8 // 0xff means "no previous XOR window"
	trailing uint8
}

// Append adds a sample. The caller guarantees t is greater than the previous one.
func (a *Appender) Append(t int64, v float64) {
	num := a.c.NumSamples()
	s := &a.c.s

	switch num {
	case 0:
		var buf [binary.MaxVarintLen64]byte
		for _, b := range buf[:binary.PutVarint(buf[:], t)] {
			s.writeByte(b)
		}
		s.writeBits(math.Float64bits(v), 64)
	case 1:
		tDelta := uint64(t - a.t)
		var buf [binary.MaxVarintLen64]byte
		for _, b := range buf[:binary.PutUvarint(buf[:], tDelta)] {
			s.writeByte(b)
		}
		a.writeValue(v)
		a.tDelta = tDelta
	default:
		tDelta := uint64(t - a.t)
		dod := int64(tDelta - a.tDelta)
		switch {
		case dod == 0:
			s.writeBit(false)
		case fitsIn(dod, 14):
			s.writeBits(0b10, 2)
			s.writeBits(uint64(dod), 14)
		case fitsIn(dod, 17):
			s.writeBits(0b110, 3)
			s.writeBits(uint64(dod), 17)
		case fitsIn(dod, 20):
			s.writeBits(0b1110, 4)
			s.writeBits(uint64(dod), 20)
		default:
			s.writeBits(0b1111, 4)
			s.writeBits(uint64(dod), 64)
		}
		a.writeValue(v)
		a.tDelta = tDelta
	}

	a.t, a.v = t, v
	binary.BigEndian.PutUint16(s.b, uint16(num+1))
}

// fitsIn reports whether x fits in an nbits two's-complement integer, using the
// asymmetric range [-(2^(n-1)-1), 2^(n-1)] so that the all-ones pattern stays free.
func fitsIn(x int64, nbits uint8) bool {
	return -((1<<(nbits-1))-1) <= x && x <= 1<<(nbits-1)
}

func (a *Appender) writeValue(v float64) {
	s := &a.c.s
	delta := math.Float64bits(v) ^ math.Float64bits(a.v)
	if delta == 0 {
		s.writeBit(false)
		return
	}
	s.writeBit(true)

	leading := uint8(bits.LeadingZeros64(delta))
	trailing := uint8(bits.TrailingZeros64(delta))
	if leading >= 32 {
		leading = 31 // only 5 bits available
	}

	if a.leading != 0xff && leading >= a.leading && trailing >= a.trailing {
		// Fits inside the previous meaningful-bit window.
		s.writeBit(false)
		s.writeBits(delta>>a.trailing, 64-int(a.leading)-int(a.trailing))
		return
	}
	a.leading, a.trailing = leading, trailing
	s.writeBit(true)
	s.writeBits(uint64(leading), 5)
	sigbits := 64 - leading - trailing
	// 64 significant bits does not fit in 6 bits; it is stored as 0.
	s.writeBits(uint64(sigbits), 6)
	s.writeBits(delta>>trailing, int(sigbits))
}

// Iterator decodes a chunk.
type Iterator struct {
	br       bitReader
	numTotal uint16
	numRead  uint16

	t      int64
	v      float64
	tDelta uint64

	leading, trailing uint8
	err               error
}

// Next advances to the next sample.
func (it *Iterator) Next() bool {
	if it.err != nil || it.numRead == it.numTotal {
		return false
	}
	switch it.numRead {
	case 0:
		t, err := binary.ReadVarint(&it.br)
		if err != nil {
			return it.fail(err)
		}
		v, err := it.br.readBits(64)
		if err != nil {
			return it.fail(err)
		}
		it.t, it.v = t, math.Float64frombits(v)
	case 1:
		tDelta, err := binary.ReadUvarint(&it.br)
		if err != nil {
			return it.fail(err)
		}
		it.tDelta = tDelta
		it.t += int64(tDelta)
		if !it.readValue() {
			return false
		}
	default:
		var prefix uint8
		for i := 0; i < 4; i++ {
			bit, err := it.br.readBit()
			if err != nil {
				return it.fail(err)
			}
			if !bit {
				break
			}
			prefix++
		}
		var sz int
		switch prefix {
		case 1:
			sz = 14
		case 2:
			sz = 17
		case 3:
			sz = 20
		case 4:
			sz = 64
		}
		var dod int64
		if sz > 0 {
			raw, err := it.br.readBits(sz)
			if err != nil {
				return it.fail(err)
			}
			dod = int64(raw)
			if sz < 64 && raw > 1<<(sz-1) {
				dod = int64(raw) - 1<<sz // sign-extend
			}
		}
		it.tDelta = uint64(int64(it.tDelta) + dod)
		it.t += int64(it.tDelta)
		if !it.readValue() {
			return false
		}
	}
	it.numRead++
	return true
}

func (it *Iterator) readValue() bool {
	bit, err := it.br.readBit()
	if err != nil {
		return it.fail(err)
	}
	if !bit {
		return true // same value
	}
	bit, err = it.br.readBit()
	if err != nil {
		return it.fail(err)
	}
	if bit {
		leading, err := it.br.readBits(5)
		if err != nil {
			return it.fail(err)
		}
		sigbits, err := it.br.readBits(6)
		if err != nil {
			return it.fail(err)
		}
		if sigbits == 0 {
			sigbits = 64
		}
		it.leading = uint8(leading)
		it.trailing = 64 - uint8(leading) - uint8(sigbits)
	}
	sigbits := 64 - int(it.leading) - int(it.trailing)
	raw, err := it.br.readBits(sigbits)
	if err != nil {
		return it.fail(err)
	}
	it.v = math.Float64frombits(math.Float64bits(it.v) ^ raw<<it.trailing)
	return true
}

func (it *Iterator) fail(err error) bool {
	if err == io.EOF {
		err = io.ErrUnexpectedEOF
	}
	it.err = err
	return false
}

// At returns the current sample.
func (it *Iterator) At() (int64, float64) { return it.t, it.v }

// Err returns the first decoding error.
func (it *Iterator) Err() error { return it.err }
