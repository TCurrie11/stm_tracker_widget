// stm_tracker_widget: live STM bus and métro departures for KDE Plasma 6
// https://github.com/TCurrie11/stm_tracker_widget
//
// File:    internal/pb/wire.go
// Purpose: A minimal, dependency-free protobuf wire-format reader.
// Author:  TCurrie11 (https://github.com/TCurrie11)
// Version: 0.1.0
// Created: 2026-10-14
// Updated: 2026-10-14
// License: GPL-2.0-or-later

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
	buffer []byte
	offset int

	fieldNumber int
	wireType    int
	// Value of the current field: scalar for Varint and Fixed*, bytes for Bytes.
	scalar uint64
	bytes  []byte

	err error
}

// NewReader reads the message in buf. buf is not copied; Bytes and String
// values alias it.
func NewReader(buffer []byte) *Reader { return &Reader{buffer: buffer} }

// Next advances to the next field. It returns false at the end of the
// message or on error; check Err afterwards.
func (r *Reader) Next() bool {
	if r.err != nil || r.offset >= len(r.buffer) {
		return false
	}
	key, ok := r.varint()
	if !ok {
		return false
	}
	r.fieldNumber, r.wireType = int(key>>3), int(key&7)
	if r.fieldNumber == 0 {
		r.err = errors.New("pb: field number 0")
		return false
	}
	switch r.wireType {
	case Varint:
		r.scalar, ok = r.varint()
	case Fixed64:
		if ok = r.offset+8 <= len(r.buffer); ok {
			r.scalar = binary.LittleEndian.Uint64(r.buffer[r.offset:])
			r.offset += 8
		}
	case Fixed32:
		if ok = r.offset+4 <= len(r.buffer); ok {
			r.scalar = uint64(binary.LittleEndian.Uint32(r.buffer[r.offset:]))
			r.offset += 4
		}
	case Bytes:
		var length uint64
		if length, ok = r.varint(); ok {
			if ok = length <= uint64(len(r.buffer)-r.offset); ok {
				r.bytes = r.buffer[r.offset : r.offset+int(length)]
				r.offset += int(length)
			}
		}
	default:
		// Groups (3, 4) are deprecated and unused by GTFS-Realtime.
		r.err = fmt.Errorf("pb: unsupported wire type %d for field %d", r.wireType, r.fieldNumber)
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
	value, size := binary.Uvarint(r.buffer[r.offset:])
	if size <= 0 {
		r.err = ErrTruncated
		if size < 0 {
			r.err = errors.New("pb: varint overflow")
		}
		return 0, false
	}
	r.offset += size
	return value, true
}

// Err returns the first error encountered, if any.
func (r *Reader) Err() error { return r.err }

// Field is the current field number; Wire its wire type.
func (r *Reader) Field() int { return r.fieldNumber }
func (r *Reader) Wire() int  { return r.wireType }

// Typed accessors for the current field. Calling one that doesn't match
// the wire type records an error rather than returning garbage.

func (r *Reader) expect(wireType int) bool {
	if r.wireType != wireType {
		if r.err == nil {
			r.err = fmt.Errorf("pb: field %d has wire type %d, want %d", r.fieldNumber, r.wireType, wireType)
		}
		return false
	}
	return true
}

func (r *Reader) Uint64() uint64 {
	if !r.expect(Varint) {
		return 0
	}
	return r.scalar
}

func (r *Reader) Uint32() uint32 { return uint32(r.Uint64()) }
func (r *Reader) Bool() bool     { return r.Uint64() != 0 }

// Int64 decodes int64/int32 fields (two's complement, not zigzag).
func (r *Reader) Int64() int64 { return int64(r.Uint64()) }
func (r *Reader) Int32() int32 { return int32(r.Uint64()) }

func (r *Reader) Float32() float32 {
	if !r.expect(Fixed32) {
		return 0
	}
	return math.Float32frombits(uint32(r.scalar))
}

func (r *Reader) Float64() float64 {
	if !r.expect(Fixed64) {
		return 0
	}
	return math.Float64frombits(r.scalar)
}

// Bytes returns the raw value of a length-delimited field (aliases buf).
func (r *Reader) Bytes() []byte {
	if !r.expect(Bytes) {
		return nil
	}
	return r.bytes
}

func (r *Reader) String() string { return string(r.Bytes()) }

// Message returns a reader over an embedded message.
func (r *Reader) Message() *Reader { return NewReader(r.Bytes()) }
