// Package geoiptest writes tiny MaxMind-format (.mmdb) databases for tests, so
// geo lookups can be exercised without shipping or downloading real data.
//
// It implements just enough of the MaxMind DB format (v2.0): an IPv6 search
// tree with 24-bit records, and a data section of maps, strings and unsigned
// integers. IPv4 networks are stored in the ::/96 subtree, as real databases do.
package geoiptest

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"net/netip"
	"os"
	"sort"
)

// Record is one network's data: nested maps of strings / uints.
type Record = map[string]any

// Write builds a database of the given type containing nets, and writes it
// to path.
func Write(path, dbType string, nets map[string]Record) error {
	b, err := Build(dbType, nets)
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o644)
}

type node struct{ child [2]int } // >=0 node index; -1 empty; <= -2: -(data index)-2

// Build returns the bytes of a database containing nets (CIDR → record).
func Build(dbType string, nets map[string]Record) ([]byte, error) {
	nodes := []node{{child: [2]int{-1, -1}}}

	var data bytes.Buffer
	keys := make([]string, 0, len(nets))
	for k := range nets {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var dataOffsets []int
	for _, cidr := range keys {
		p, err := netip.ParsePrefix(cidr)
		if err != nil {
			return nil, err
		}
		bits := p.Bits()
		addr := p.Addr()
		if addr.Is4() {
			bits += 96
		}
		a16 := addr.As16()
		if p.Addr().Is4() {
			a16 = [16]byte{} // ::a.b.c.d, not ::ffff:a.b.c.d
			copy(a16[12:], addr.AsSlice())
		}
		if bits == 0 {
			return nil, fmt.Errorf("%s: zero-length prefix not supported", cidr)
		}

		dataOffsets = append(dataOffsets, data.Len())
		if err := encode(&data, nets[cidr]); err != nil {
			return nil, err
		}
		leaf := -(len(dataOffsets) - 1) - 2

		cur := 0
		for i := 0; i < bits; i++ {
			bit := (a16[i/8] >> (7 - uint(i%8))) & 1
			if i == bits-1 {
				nodes[cur].child[bit] = leaf
				break
			}
			next := nodes[cur].child[bit]
			if next < 0 {
				nodes = append(nodes, node{child: [2]int{-1, -1}})
				next = len(nodes) - 1
				nodes[cur].child[bit] = next
			}
			cur = next
		}
	}

	n := len(nodes)
	var out bytes.Buffer
	rec := func(v int) uint32 {
		switch {
		case v >= 0:
			return uint32(v)
		case v == -1:
			return uint32(n)
		default:
			return uint32(n + 16 + dataOffsets[-v-2])
		}
	}
	for _, nd := range nodes {
		for _, c := range nd.child {
			v := rec(c)
			out.Write([]byte{byte(v >> 16), byte(v >> 8), byte(v)})
		}
	}
	out.Write(make([]byte, 16))
	out.Write(data.Bytes())
	out.WriteString("\xab\xcd\xefMaxMind.com")
	if err := encode(&out, Record{
		"node_count":                  uint32(n),
		"record_size":                 uint16(24),
		"ip_version":                  uint16(6),
		"database_type":               dbType,
		"binary_format_major_version": uint16(2),
		"binary_format_minor_version": uint16(0),
		"build_epoch":                 uint64(1700000000),
		"languages":                   []string{"en"},
		"description":                 Record{"en": "test database"},
	}); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

func ctrl(w *bytes.Buffer, typ, size int) {
	var sizeBits int
	var extra []byte
	switch {
	case size < 29:
		sizeBits = size
	case size < 29+256:
		sizeBits, extra = 29, []byte{byte(size - 29)}
	default:
		sizeBits = 30
		s := size - 285
		extra = []byte{byte(s >> 8), byte(s)}
	}
	if typ <= 7 {
		w.WriteByte(byte(typ<<5 | sizeBits))
	} else {
		w.WriteByte(byte(sizeBits))
		w.WriteByte(byte(typ - 7))
	}
	w.Write(extra)
}

func uintBytes(v uint64) []byte {
	var b [8]byte
	binary.BigEndian.PutUint64(b[:], v)
	i := 0
	for i < 8 && b[i] == 0 {
		i++
	}
	return b[i:]
}

func encode(w *bytes.Buffer, v any) error {
	switch x := v.(type) {
	case string:
		ctrl(w, 2, len(x))
		w.WriteString(x)
	case uint16:
		b := uintBytes(uint64(x))
		ctrl(w, 5, len(b))
		w.Write(b)
	case uint32:
		b := uintBytes(uint64(x))
		ctrl(w, 6, len(b))
		w.Write(b)
	case int:
		b := uintBytes(uint64(x))
		ctrl(w, 6, len(b))
		w.Write(b)
	case uint64:
		b := uintBytes(x)
		ctrl(w, 9, len(b))
		w.Write(b)
	case []string:
		ctrl(w, 11, len(x))
		for _, s := range x {
			if err := encode(w, s); err != nil {
				return err
			}
		}
	case map[string]any:
		ctrl(w, 7, len(x))
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			if err := encode(w, k); err != nil {
				return err
			}
			if err := encode(w, x[k]); err != nil {
				return err
			}
		}
	default:
		return fmt.Errorf("geoiptest: cannot encode %T", v)
	}
	return nil
}

// CityRecord is a DB-IP City Lite–shaped record.
func CityRecord(city, country string) Record {
	return Record{
		"city":    Record{"names": Record{"en": city}},
		"country": Record{"iso_code": "XX", "names": Record{"en": country}},
	}
}

// ASNRecord is a DB-IP ASN Lite–shaped record.
func ASNRecord(asn int, org string) Record {
	return Record{
		"autonomous_system_number":       asn,
		"autonomous_system_organization": org,
	}
}
