package series

import (
	"encoding/binary"
	"sort"
	"strings"

	"github.com/cespare/xxhash/v2"
)

// Label is a single name/value pair.
type Label struct {
	Name  string
	Value string
}

// Labels identifies a series. It is always kept sorted by name, which makes
// equality a linear scan, hashing deterministic, and set operations cheap.
//
// A sorted slice is used rather than a map on purpose. Maps give no iteration
// order, so hashing one requires sorting the keys on every call, and they
// allocate per series. Labels are the hottest small object in an ingest path:
// every sample carries one, and most of them are duplicates of a set already
// seen.
type Labels []Label

// FromMap builds sorted Labels from a map, which is the shape the ingest API
// and the existing SDK hand us.
func FromMap(m map[string]string) Labels {
	if len(m) == 0 {
		return nil
	}
	ls := make(Labels, 0, len(m))
	for k, v := range m {
		ls = append(ls, Label{Name: k, Value: v})
	}
	ls.Sort()
	return ls
}

// Map converts back to a map for callers that still want one.
func (ls Labels) Map() map[string]string {
	if len(ls) == 0 {
		return nil
	}
	m := make(map[string]string, len(ls))
	for _, l := range ls {
		m[l.Name] = l.Value
	}
	return m
}

// Sort orders labels by name in place.
func (ls Labels) Sort() {
	sort.Slice(ls, func(i, j int) bool { return ls[i].Name < ls[j].Name })
}

// Get returns the value for name, and whether it was present.
func (ls Labels) Get(name string) (string, bool) {
	// Sorted, so a binary search would work, but label sets are small enough
	// that a linear scan wins on cache behaviour.
	for _, l := range ls {
		if l.Name == name {
			return l.Value, true
		}
	}
	return "", false
}

// Equal reports whether two label sets are identical. Both must be sorted.
func (ls Labels) Equal(other Labels) bool {
	if len(ls) != len(other) {
		return false
	}
	for i := range ls {
		if ls[i].Name != other[i].Name || ls[i].Value != other[i].Value {
			return false
		}
	}
	return true
}

// String renders labels in the familiar {a="1", b="2"} form, for errors and
// log lines.
func (ls Labels) String() string {
	var b strings.Builder
	b.WriteByte('{')
	for i, l := range ls {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(l.Name)
		b.WriteString(`="`)
		b.WriteString(l.Value)
		b.WriteByte('"')
	}
	b.WriteByte('}')
	return b.String()
}

// Encode serialises labels into a canonical, length-prefixed form.
//
// Every string is written as a uvarint length followed by its bytes. This is
// what makes the encoding injective: naive concatenation with separators lets
// distinct label sets collide, because the separator can appear inside a value.
// {a="1|b", c="2"} and {a="1", b="c", ...} can be made to produce identical
// bytes under a delimiter scheme, and two different series sharing a hash is a
// data-corruption bug that surfaces as inexplicably merged graphs. Lengths
// cannot be forged this way.
func (ls Labels) Encode() []byte {
	size := binary.MaxVarintLen64
	for _, l := range ls {
		size += 2*binary.MaxVarintLen64 + len(l.Name) + len(l.Value)
	}
	buf := make([]byte, 0, size)

	buf = binary.AppendUvarint(buf, uint64(len(ls)))
	for _, l := range ls {
		buf = binary.AppendUvarint(buf, uint64(len(l.Name)))
		buf = append(buf, l.Name...)
		buf = binary.AppendUvarint(buf, uint64(len(l.Value)))
		buf = append(buf, l.Value...)
	}
	return buf
}

// Decode reverses Encode.
func Decode(buf []byte) (Labels, error) {
	n, read := binary.Uvarint(buf)
	if read <= 0 {
		return nil, errCorrupt("label count")
	}
	buf = buf[read:]

	if n == 0 {
		return nil, nil
	}
	ls := make(Labels, 0, n)

	readString := func(what string) (string, error) {
		length, read := binary.Uvarint(buf)
		if read <= 0 {
			return "", errCorrupt(what + " length")
		}
		buf = buf[read:]
		if uint64(len(buf)) < length {
			return "", errCorrupt(what)
		}
		s := string(buf[:length])
		buf = buf[length:]
		return s, nil
	}

	for i := uint64(0); i < n; i++ {
		name, err := readString("label name")
		if err != nil {
			return nil, err
		}
		value, err := readString("label value")
		if err != nil {
			return nil, err
		}
		ls = append(ls, Label{Name: name, Value: value})
	}
	return ls, nil
}

// Hash returns a 64-bit fingerprint of the label set.
//
// This is a fingerprint, not an identity. Callers must still compare the full
// label set before treating two series as the same; see Index.
//
// The digest is fed the same byte sequence Encode produces, but streamed rather
// than materialised. Hash is called once per sample on the ingest path, so an
// allocation here would be an allocation per data point. The Digest is a value
// on the stack and the varint scratch buffer is fixed-size, so this does not
// allocate at all; TestHashMatchesEncode pins the two to the same result.
func (ls Labels) Hash() uint64 {
	var d xxhash.Digest
	d.Reset()

	var scratch [binary.MaxVarintLen64]byte
	writeUvarint := func(v uint64) {
		n := binary.PutUvarint(scratch[:], v)
		_, _ = d.Write(scratch[:n])
	}

	writeUvarint(uint64(len(ls)))
	for _, l := range ls {
		writeUvarint(uint64(len(l.Name)))
		_, _ = d.WriteString(l.Name)
		writeUvarint(uint64(len(l.Value)))
		_, _ = d.WriteString(l.Value)
	}
	return d.Sum64()
}
