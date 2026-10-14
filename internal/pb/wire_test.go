// stm_tracker_widget: live STM bus and métro departures for KDE Plasma 6
// https://github.com/TCurrie11/stm_tracker_widget
//
// File:    internal/pb/wire_test.go
// Purpose: Tests for the protobuf reader.
// Author:  TCurrie11 (https://github.com/TCurrie11)
// Version: 0.1.0
// Created: 2026-10-14
// Updated: 2026-10-14
// License: GPL-2.0-or-later

package pb

import (
	"errors"
	"testing"
)

func TestReadFields(t *testing.T) {
	msg := []byte{
		0x08, 0x96, 0x01, // field 1 varint 150
		0x12, 0x03, 'a', 'b', 'c', // field 2 string "abc"
		0x18, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0x01, // field 3 int32 -1
		0x25, 0x00, 0x00, 0x80, 0x3f, // field 4 float 1.0
		0x29, 0, 0, 0, 0, 0, 0, 0xf0, 0x3f, // field 5 double 1.0
	}
	r := NewReader(msg)
	var got []any
	for r.Next() {
		switch r.Field() {
		case 1:
			got = append(got, r.Uint64())
		case 2:
			got = append(got, r.String())
		case 3:
			got = append(got, r.Int32())
		case 4:
			got = append(got, r.Float32())
		case 5:
			got = append(got, r.Float64())
		}
	}
	if r.Err() != nil {
		t.Fatal(r.Err())
	}
	want := []any{uint64(150), "abc", int32(-1), float32(1), float64(1)}
	if len(got) != len(want) {
		t.Fatalf("got %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("field %d: got %v (%T), want %v", i+1, got[i], got[i], want[i])
		}
	}
}

func TestErrors(t *testing.T) {
	cases := map[string][]byte{
		"truncated varint":  {0x08, 0x96},
		"truncated length":  {0x12, 0x05, 'a'},
		"truncated fixed32": {0x25, 0x00},
		"field zero":        {0x00, 0x01},
		"group wire type":   {0x0b},
		"varint overflow":   {0x08, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0x01},
	}
	for name, b := range cases {
		r := NewReader(b)
		for r.Next() {
		}
		if r.Err() == nil {
			t.Errorf("%s: no error", name)
		}
	}
	r := NewReader([]byte{0x12, 0x05, 'a'})
	r.Next()
	if !errors.Is(r.Err(), ErrTruncated) {
		t.Errorf("want ErrTruncated, got %v", r.Err())
	}
}

func TestWrongAccessor(t *testing.T) {
	r := NewReader([]byte{0x08, 0x01})
	r.Next()
	if s := r.String(); s != "" || r.Err() == nil {
		t.Errorf("reading a varint as a string should fail, got %q, %v", s, r.Err())
	}
}
