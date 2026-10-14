// Package pb is a minimal reader for the protobuf wire format.
//
// GTFS-Realtime feeds are protobuf messages. Rather than depend on the full
// protobuf library and generated code, transitd reads the handful of fields
// it needs directly. The wire format is simple: a message is a sequence of
// (field number, wire type, value) records.
//
// https://protobuf.dev/programming-guides/encoding/
package pb

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
)

// Wire types.
const (
	Varint  = 0 // int32, int64, uint32, uint64, bool, enum
	Fixed64 = 1 // double, fixed64
	Bytes   = 2 // string, bytes, embedded messages, packed repeated
	Fixed32 = 5 // float, fixed32
)

// ErrTruncated means the message ended in the middle of a field.
var ErrTruncated = errors.New("pb: truncated message")

// Reader walks the fields of one message.
type Reader struct {
	buf []byte
	pos int

	field int
	wire  int
	// value of the current field: n for Varint/Fixed*, b for Bytes
	n uint64
	b []byte

	err error
}

// NewReader reads the message in buf. buf is not copied; Bytes and String
// values alias it.
func NewReader(buf []byte) *Reader { return &Reader{buf: buf} }

// Next advances to the next field. It returns false at the end of the
// message or on error; check Err afterwards.
func (r *Reader) Next() bool {
	if r.err != nil || r.pos >= len(r.buf) {
		return false
	}
	tag, ok := r.varint()
	if !ok {
		return false
	}
	r.field, r.wire = int(tag>>3), int(tag&7)
	if r.field == 0 {
		r.err = errors.New("pb: field number 0")
		return false
	}
	switch r.wire {
	case Varint:
		r.n, ok = r.varint()
	case Fixed64:
		if ok = r.pos+8 <= len(r.buf); ok {
			r.n = binary.LittleEndian.Uint64(r.buf[r.pos:])
			r.pos += 8
		}
	case Fixed32:
		if ok = r.pos+4 <= len(r.buf); ok {
			r.n = uint64(binary.LittleEndian.Uint32(r.buf[r.pos:]))
			r.pos += 4
		}
	case Bytes:
		var l uint64
		if l, ok = r.varint(); ok {
			if ok = l <= uint64(len(r.buf)-r.pos); ok {
				r.b = r.buf[r.pos : r.pos+int(l)]
				r.pos += int(l)
			}
		}
	default:
		// Groups (3, 4) are deprecated and unused by GTFS-Realtime.
		r.err = fmt.Errorf("pb: unsupported wire type %d for field %d", r.wire, r.field)
		return false
	}
	if !ok {
		if r.err == nil {
			r.err = ErrTruncated
		}
		return false
	}
	return true
}

func (r *Reader) varint() (uint64, bool) {
	v, n := binary.Uvarint(r.buf[r.pos:])
	if n <= 0 {
		r.err = ErrTruncated
		if n < 0 {
			r.err = errors.New("pb: varint overflow")
		}
		return 0, false
	}
	r.pos += n
	return v, true
}

// Err returns the first error encountered, if any.
func (r *Reader) Err() error { return r.err }

// Field is the current field number; Wire its wire type.
func (r *Reader) Field() int { return r.field }
func (r *Reader) Wire() int  { return r.wire }

// Typed accessors for the current field. Calling one that doesn't match
// the wire type records an error rather than returning garbage.

func (r *Reader) want(w int) bool {
	if r.wire != w {
		if r.err == nil {
			r.err = fmt.Errorf("pb: field %d has wire type %d, want %d", r.field, r.wire, w)
		}
		return false
	}
	return true
}

func (r *Reader) Uint64() uint64 {
	if !r.want(Varint) {
		return 0
	}
	return r.n
}

func (r *Reader) Uint32() uint32 { return uint32(r.Uint64()) }
func (r *Reader) Bool() bool     { return r.Uint64() != 0 }

// Int64 decodes int64/int32 fields (two's complement, not zigzag).
func (r *Reader) Int64() int64 { return int64(r.Uint64()) }
func (r *Reader) Int32() int32 { return int32(r.Uint64()) }

func (r *Reader) Float32() float32 {
	if !r.want(Fixed32) {
		return 0
	}
	return math.Float32frombits(uint32(r.n))
}

func (r *Reader) Float64() float64 {
	if !r.want(Fixed64) {
		return 0
	}
	return math.Float64frombits(r.n)
}

// Bytes returns the raw value of a length-delimited field (aliases buf).
func (r *Reader) Bytes() []byte {
	if !r.want(Bytes) {
		return nil
	}
	return r.b
}

func (r *Reader) String() string { return string(r.Bytes()) }

// Message returns a reader over an embedded message.
func (r *Reader) Message() *Reader { return NewReader(r.Bytes()) }
