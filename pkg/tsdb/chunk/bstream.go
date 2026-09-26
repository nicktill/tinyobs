package chunk

import "io"

// bstream is an append-only bit stream.
type bstream struct {
	b     []byte
	count uint8 // bits still free in the last byte
}

func (s *bstream) writeBit(bit bool) {
	if s.count == 0 {
		s.b = append(s.b, 0)
		s.count = 8
	}
	if bit {
		s.b[len(s.b)-1] |= 1 << (s.count - 1)
	}
	s.count--
}

func (s *bstream) writeByte(v byte) {
	s.writeBits(uint64(v), 8)
}

// writeBits writes the low nbits of u, most significant first.
func (s *bstream) writeBits(u uint64, nbits int) {
	for nbits > 0 {
		if s.count == 0 {
			s.b = append(s.b, 0)
			s.count = 8
		}
		n := nbits
		if n > int(s.count) {
			n = int(s.count)
		}
		// Take the top n of the remaining nbits.
		chunk := byte((u >> uint(nbits-n)) & (1<<uint(n) - 1))
		s.b[len(s.b)-1] |= chunk << (s.count - uint8(n))
		s.count -= uint8(n)
		nbits -= n
	}
}

// bitReader reads a bit stream written by bstream.
type bitReader struct {
	b   []byte
	pos int // absolute bit position
}

func (r *bitReader) readBit() (bool, error) {
	v, err := r.readBits(1)
	return v == 1, err
}

func (r *bitReader) readBits(nbits int) (uint64, error) {
	if r.pos+nbits > len(r.b)*8 {
		return 0, io.EOF
	}
	var u uint64
	for nbits > 0 {
		byteIdx := r.pos / 8
		bitOff := r.pos % 8
		avail := 8 - bitOff
		n := nbits
		if n > avail {
			n = avail
		}
		cur := r.b[byteIdx] >> uint(avail-n) & (1<<uint(n) - 1)
		u = u<<uint(n) | uint64(cur)
		r.pos += n
		nbits -= n
	}
	return u, nil
}

func (r *bitReader) ReadByte() (byte, error) {
	v, err := r.readBits(8)
	return byte(v), err
}
