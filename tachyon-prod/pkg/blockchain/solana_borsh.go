package blockchain

import (
	"encoding/binary"

	"github.com/gagliardetto/solana-go"
)

// borshEncoder is a small Borsh writer covering the subset our Anchor
// instruction args need: u8, u64, i64, [u8;32], Pubkey (alias for [u8;32]),
// bool, Vec<u64>, Vec<[u8;32]>. Anchor uses Borsh for instruction args.
//
// Borsh layout (LE, no padding, no tags):
//
//	bool          1 byte  (0 or 1)
//	u8            1 byte
//	u64           8 bytes LE
//	i64           8 bytes LE (two's complement)
//	[u8; N]       N bytes raw
//	Pubkey        32 bytes raw
//	Vec<T>        u32 LE length || N items in T's encoding
type borshEncoder struct {
	buf []byte
}

func newBorshEncoder() *borshEncoder { return &borshEncoder{buf: make([]byte, 0, 64)} }

func (b *borshEncoder) Bytes() []byte { return b.buf }

func (b *borshEncoder) writeU8(v uint8) { b.buf = append(b.buf, v) }

func (b *borshEncoder) writeBool(v bool) {
	if v {
		b.buf = append(b.buf, 1)
	} else {
		b.buf = append(b.buf, 0)
	}
}

func (b *borshEncoder) writeU32(v uint32) {
	var tmp [4]byte
	binary.LittleEndian.PutUint32(tmp[:], v)
	b.buf = append(b.buf, tmp[:]...)
}

func (b *borshEncoder) writeU64(v uint64) {
	var tmp [8]byte
	binary.LittleEndian.PutUint64(tmp[:], v)
	b.buf = append(b.buf, tmp[:]...)
}

func (b *borshEncoder) writeI64(v int64) { b.writeU64(uint64(v)) }

func (b *borshEncoder) writeFixed32(v [32]byte) { b.buf = append(b.buf, v[:]...) }

func (b *borshEncoder) writePubkey(p solana.PublicKey) { b.buf = append(b.buf, p.Bytes()...) }

func (b *borshEncoder) writeVecU64(xs []uint64) {
	b.writeU32(uint32(len(xs)))
	for _, x := range xs {
		b.writeU64(x)
	}
}

func (b *borshEncoder) writeVecPubkey(xs []solana.PublicKey) {
	b.writeU32(uint32(len(xs)))
	for _, x := range xs {
		b.writePubkey(x)
	}
}

// writeVecFixed32 encodes Vec<[u8;32]>.
func (b *borshEncoder) writeVecFixed32(xs [][32]byte) {
	b.writeU32(uint32(len(xs)))
	for _, x := range xs {
		b.writeFixed32(x)
	}
}

// borshDecoder mirrors the writer for parsing Anchor event payloads.
type borshDecoder struct {
	buf []byte
	off int
}

func newBorshDecoder(b []byte) *borshDecoder { return &borshDecoder{buf: b} }

func (d *borshDecoder) remaining() int { return len(d.buf) - d.off }

func (d *borshDecoder) readU8() (uint8, bool) {
	if d.off+1 > len(d.buf) {
		return 0, false
	}
	v := d.buf[d.off]
	d.off++
	return v, true
}

func (d *borshDecoder) readBool() (bool, bool) {
	v, ok := d.readU8()
	return v != 0, ok
}

func (d *borshDecoder) readU32() (uint32, bool) {
	if d.off+4 > len(d.buf) {
		return 0, false
	}
	v := binary.LittleEndian.Uint32(d.buf[d.off : d.off+4])
	d.off += 4
	return v, true
}

func (d *borshDecoder) readU64() (uint64, bool) {
	if d.off+8 > len(d.buf) {
		return 0, false
	}
	v := binary.LittleEndian.Uint64(d.buf[d.off : d.off+8])
	d.off += 8
	return v, true
}

func (d *borshDecoder) readI64() (int64, bool) {
	v, ok := d.readU64()
	return int64(v), ok
}

func (d *borshDecoder) readFixed32() ([32]byte, bool) {
	var out [32]byte
	if d.off+32 > len(d.buf) {
		return out, false
	}
	copy(out[:], d.buf[d.off:d.off+32])
	d.off += 32
	return out, true
}

func (d *borshDecoder) readPubkey() (solana.PublicKey, bool) {
	v, ok := d.readFixed32()
	if !ok {
		return solana.PublicKey{}, false
	}
	return solana.PublicKeyFromBytes(v[:]), true
}
