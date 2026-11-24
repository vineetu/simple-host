package admin

import (
	"bytes"
	"encoding/binary"
	"errors"
	"log"
	"math"
	"net/netip"
	"os"
	"syscall"
)

// countries finds an address's country for the API growth tab's world map,
// from the IP-to-country file the hosted server already keeps (DB-IP's
// country or city file in the MaxMind .mmdb format). The file is mapped
// into memory once at start; a lookup walks its search tree bit by bit and
// reads the record's country.iso_code. No file, no countries.
type countries struct {
	b                    []byte
	nodes, recordBits    uint
	tree, ipv4Start, end int
}

// openCountries maps path; "" or an unreadable file gives nil (no countries).
func openCountries(path string) *countries {
	if path == "" {
		return nil
	}
	c, err := mapCountries(path)
	if err != nil {
		log.Printf("country file %s: %v (no countries on the API growth map)", path, err)
		return nil
	}
	return c
}

func mapCountries(path string) (*countries, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, err
	}
	b, err := syscall.Mmap(int(f.Fd()), 0, int(st.Size()), syscall.PROT_READ, syscall.MAP_SHARED)
	if err != nil {
		return nil, err
	}
	at := bytes.LastIndex(b, []byte("\xab\xcd\xefMaxMind.com"))
	if at < 0 {
		return nil, errors.New("not a MaxMind-format file")
	}
	c := &countries{b: b, end: len(b)}
	meta, _ := c.decode(at + 14)
	m, _ := meta.(map[string]any)
	nodes, _ := m["node_count"].(uint64)
	bits, _ := m["record_size"].(uint64)
	if nodes == 0 || (bits != 24 && bits != 28 && bits != 32) {
		return nil, errors.New("unreadable metadata")
	}
	c.nodes, c.recordBits = uint(nodes), uint(bits)
	c.tree = int(nodes * bits / 4)
	if c.tree+16 > at {
		return nil, errors.New("truncated file")
	}
	for i := 0; i < 96 && uint(c.ipv4Start) < c.nodes; i++ { // IPv4 lives under ::/96
		c.ipv4Start = int(c.child(uint(c.ipv4Start), 0))
	}
	return c, nil
}

// Of is the ISO country code of ip ("" when unknown).
func (c *countries) Of(ip string) string {
	addr, err := netip.ParseAddr(ip)
	if c == nil || err != nil {
		return ""
	}
	addr = addr.Unmap()
	raw, node, bitCount := addr.AsSlice(), uint(0), len(addr.AsSlice())*8
	if addr.Is4() {
		node = uint(c.ipv4Start)
	}
	for i := 0; i < bitCount && node < c.nodes; i++ {
		node = c.child(node, uint(raw[i/8]>>(7-i%8)&1))
	}
	if node <= c.nodes {
		return ""
	}
	rec, _ := c.decode(c.tree + 16 + int(node-c.nodes-16))
	country, _ := rec.(map[string]any)["country"].(map[string]any)
	code, _ := country["iso_code"].(string)
	return code
}

// child is the left (0) or right (1) record of a search tree node.
func (c *countries) child(node, side uint) uint {
	switch n := c.b[node*c.recordBits/4:]; c.recordBits {
	case 24:
		n = n[side*3:]
		return uint(n[0])<<16 | uint(n[1])<<8 | uint(n[2])
	case 28:
		if side == 0 {
			return uint(n[3]&0xf0)<<20 | uint(n[0])<<16 | uint(n[1])<<8 | uint(n[2])
		}
		return uint(n[3]&0x0f)<<24 | uint(n[4])<<16 | uint(n[5])<<8 | uint(n[6])
	default:
		return uint(binary.BigEndian.Uint32(n[side*4:]))
	}
}

// decode reads one value of the data section at off (an absolute offset)
// and returns it with the offset after it. Pointers count from the data
// section's start.
func (c *countries) decode(off int) (any, int) {
	if off < 0 || off >= c.end {
		return nil, c.end
	}
	ctrl := c.b[off]
	off++
	kind := int(ctrl >> 5)
	if kind == 1 { // pointer
		ss, v := int(ctrl>>3&3), int(ctrl&7)
		p := 0
		for i := 0; i <= ss; i++ {
			p = p<<8 | int(c.b[off+i])
		}
		switch ss {
		case 0:
			p |= v << 8
		case 1:
			p = p | v<<16 + 2048
		case 2:
			p = p | v<<24 + 526336
		}
		val, _ := c.decode(c.tree + 16 + p)
		return val, off + ss + 1
	}
	if kind == 0 {
		kind = 7 + int(c.b[off])
		off++
	}
	size := int(ctrl & 0x1f)
	if size >= 29 {
		n := size - 28
		size = 0
		for i := 0; i < n; i++ {
			size = size<<8 | int(c.b[off+i])
		}
		size += []int{29, 285, 65821}[n-1]
		off += n
	}
	number := func() uint64 {
		var v uint64
		for _, x := range c.b[off : off+size] {
			v = v<<8 | uint64(x)
		}
		return v
	}
	switch kind {
	case 2:
		return string(c.b[off : off+size]), off + size
	case 3:
		return math.Float64frombits(binary.BigEndian.Uint64(c.b[off:])), off + 8
	case 15:
		return float64(math.Float32frombits(binary.BigEndian.Uint32(c.b[off:]))), off + 4
	case 5, 6, 8, 9, 10:
		return number(), off + size
	case 7:
		m := map[string]any{}
		for i := 0; i < size; i++ {
			var k, v any
			k, off = c.decode(off)
			v, off = c.decode(off)
			if s, ok := k.(string); ok {
				m[s] = v
			}
		}
		return m, off
	case 11:
		list := make([]any, size)
		for i := range list {
			list[i], off = c.decode(off)
		}
		return list, off
	case 14:
		return size != 0, off
	}
	return nil, off + size // bytes, a data cache container, an end marker
}
